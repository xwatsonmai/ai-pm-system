package notify

import (
	"ai-pm-system/internal/credential"
	"ai-pm-system/internal/domain"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// Data is what the hub reads from the platform's own records.
type Data interface {
	GetTask(context.Context, string) (domain.Task, error)
	ListTasks(context.Context, string) ([]domain.Task, error)
	GetProject(context.Context, string) (domain.Project, error)
	ListProjects(context.Context) ([]domain.Project, error)
	GetRequirementReview(context.Context, string) (domain.RequirementReview, error)
	Events(context.Context, string, string, int64) ([]domain.Event, error)
}

type Vault interface {
	Save(context.Context, string, credential.Secret) (string, error)
	Load(context.Context, string) (credential.Secret, string, error)
}

// Act performs a to-do's one-step action as the person who clicked. It must
// apply the same permission and version checks as the workspace; requestID
// makes a repeated click harmless.
type Act func(ctx context.Context, userID string, todo Todo, requestID string) error

// Hub keeps to-dos in step with tasks and delivers them to channels.
type Hub struct {
	Store Store
	Data  Data
	Vault Vault
	Act   Act
	// PublicURL is the platform's own public URL, the default for channels.
	PublicURL string

	mu      sync.RWMutex
	running map[string]*active
	queue   chan string
	refresh chan struct{}
	wake    chan struct{}
	now     func() time.Time
}

type active struct {
	channel  Channel
	settings Settings
	err      string
}

const (
	// backfill limits which open to-dos a newly bound person still receives.
	backfill    = 7 * 24 * time.Hour
	maxAttempts = 6
)

var retryAfter = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 3 * time.Hour}

func (h *Hub) init() {
	if h.running == nil {
		h.running = map[string]*active{}
		h.queue = make(chan string, 1024)
		h.refresh = make(chan struct{}, 1)
		h.wake = make(chan struct{}, 1)
	}
	if h.now == nil {
		h.now = time.Now
	}
}

// Start opens the configured channels and keeps to-dos and deliveries moving
// until ctx ends.
func (h *Hub) Start(ctx context.Context) {
	h.init()
	for _, name := range Names() {
		h.activate(ctx, name)
	}
	go h.reconcileLoop(ctx)
	go h.dispatchLoop(ctx)
}

// Changed reports tasks whose state may have changed. It never blocks.
func (h *Hub) Changed(ids ...string) {
	h.init()
	for _, id := range ids {
		if id == "" {
			continue
		}
		select {
		case h.queue <- id:
		default:
			h.refreshAll() // a full pass catches up
		}
	}
}

func (h *Hub) refreshAll() {
	select {
	case h.refresh <- struct{}{}:
	default:
	}
}

func (h *Hub) poke() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *Hub) reconcileLoop(ctx context.Context) {
	h.ReconcileAll(ctx)
	full := time.NewTicker(10 * time.Minute)
	defer full.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-h.queue:
			if err := h.Reconcile(ctx, id); err != nil && ctx.Err() == nil {
				log.Printf("待办更新失败 %s：%v", id, err)
			}
		case <-full.C:
			h.ReconcileAll(ctx)
		case <-h.refresh:
			h.ReconcileAll(ctx)
		}
	}
}

func (h *Hub) dispatchLoop(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		h.Dispatch(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-h.wake:
		}
	}
}

// ReconcileAll rederives every active task and every task with open to-dos.
func (h *Hub) ReconcileAll(ctx context.Context) {
	ids := []string{}
	if open, err := h.Store.OpenTodos(ctx, ""); err == nil {
		for _, t := range open {
			ids = append(ids, t.TaskID)
		}
	}
	projects, err := h.Data.ListProjects(ctx)
	if err != nil {
		log.Printf("待办全量核对失败：%v", err)
		return
	}
	for _, p := range projects {
		tasks, err := h.Data.ListTasks(ctx, p.ID)
		if err != nil {
			continue
		}
		for _, t := range tasks {
			if domain.TaskActive(t.Status) {
				ids = append(ids, t.ID)
			}
		}
	}
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if ctx.Err() != nil {
			return
		}
		if err := h.Reconcile(ctx, id); err != nil {
			log.Printf("待办更新失败 %s：%v", id, err)
		}
	}
}

func (h *Hub) derive(ctx context.Context, taskID string) (domain.Task, []Todo, error) {
	t, err := h.Data.GetTask(ctx, taskID)
	if err != nil {
		return t, nil, err
	}
	p, err := h.Data.GetProject(ctx, t.ProjectID)
	if err != nil {
		return t, nil, err
	}
	var review *domain.RequirementReview
	if v, e := h.Data.GetRequirementReview(ctx, t.ID); e == nil {
		review = &v
	} else if !errors.Is(e, domain.ErrNotFound) {
		return t, nil, e
	}
	return t, Derive(t, review, p), nil
}

// Reconcile brings one task's to-dos in line with its state: work that no
// longer waits closes, and new work is queued for its recipients.
func (h *Hub) Reconcile(ctx context.Context, taskID string) error {
	h.init()
	t, desired, err := h.derive(ctx, taskID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	open, err := h.Store.OpenTodos(ctx, taskID)
	if err != nil {
		return err
	}
	now := h.now().UnixMilli()
	for _, o := range open {
		if slices.ContainsFunc(desired, func(d Todo) bool { return d.Base == o.Base }) {
			continue
		}
		if err = h.Store.CloseTodo(ctx, o.ID, h.doneText(ctx, t), now); err != nil {
			return err
		}
	}
	for _, d := range desired {
		if i := slices.IndexFunc(open, func(o Todo) bool { return o.Base == d.Base }); i >= 0 {
			d.ID, d.CreatedAt = open[i].ID, open[i].CreatedAt
		} else if d.ID, err = h.freeID(ctx, d.Base); err != nil {
			return err
		} else {
			d.CreatedAt = now
		}
		deliveries := []Delivery{}
		if h.now().Sub(time.UnixMilli(d.CreatedAt)) < backfill {
			deliveries = h.deliveries(ctx, d, d.Recipients)
		}
		if err = h.Store.SaveTodo(ctx, d, deliveries); err != nil {
			return err
		}
	}
	h.poke()
	return nil
}

func (h *Hub) freeID(ctx context.Context, base string) (string, error) {
	for n := 1; ; n++ {
		id := base
		if n > 1 {
			id = fmt.Sprintf("%s_%d", base, n)
		}
		if _, err := h.Store.GetTodo(ctx, id); errors.Is(err, domain.ErrNotFound) {
			return id, nil
		} else if err != nil {
			return "", err
		}
	}
}

// doneText says who moved the work on, from the task's latest event.
func (h *Hub) doneText(ctx context.Context, t domain.Task) string {
	if t.ID == "" {
		return "已删除"
	}
	if domain.TaskTerminated(t.Status) {
		return "已结束"
	}
	if events, err := h.Data.Events(ctx, t.ProjectID, t.ID, -1); err == nil && len(events) > 0 {
		if actor := strings.TrimSuffix(events[len(events)-1].Actor, " · MCP"); actor != "" {
			return "已由 " + actor + " 处理"
		}
	}
	return "已处理"
}

// deliveries queues a card for each recipient on each enabled channel they bound.
func (h *Hub) deliveries(ctx context.Context, t Todo, users []string) []Delivery {
	out := []Delivery{}
	now := h.now().UnixMilli()
	for _, name := range h.enabled() {
		for _, u := range users {
			b, err := h.Store.GetBinding(ctx, name, u)
			if err != nil {
				continue
			}
			// A new binding is a new delivery, even for the same account.
			sum := sha256.Sum256([]byte(fmt.Sprint(t.ID, "\x00", u, "\x00", name, "\x00", b.ExternalID, "\x00", b.At)))
			out = append(out, Delivery{ID: "dl_" + hex.EncodeToString(sum[:12]), TodoID: t.ID, UserID: u, Channel: name, ExternalID: b.ExternalID, State: "pending", NextAt: now, UpdatedAt: now})
		}
	}
	return out
}

func (h *Hub) enabled() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := []string{}
	for name, a := range h.running {
		if a.channel != nil {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func (h *Hub) channel(name string) (*active, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	a, ok := h.running[name]
	return a, ok && a.channel != nil
}

// Dispatch sends due cards and rewrites cards whose to-do closed.
func (h *Hub) Dispatch(ctx context.Context) {
	due, err := h.Store.DueDeliveries(ctx, h.now().UnixMilli(), 50)
	if err != nil {
		log.Printf("读取待发送消息失败：%v", err)
		return
	}
	for _, d := range due {
		if ctx.Err() != nil {
			return
		}
		h.deliver(ctx, d)
	}
}

func (h *Hub) deliver(ctx context.Context, d Delivery) {
	now := h.now()
	d.UpdatedAt = now.UnixMilli()
	a, ok := h.channel(d.Channel)
	todo, err := h.Store.GetTodo(ctx, d.TodoID)
	if err != nil {
		d.State, d.Error = "failed", err.Error()
		_ = h.Store.PutDelivery(ctx, d)
		return
	}
	if !ok {
		// The channel was turned off; keep the card until it is back.
		d.NextAt = now.Add(10 * time.Minute).UnixMilli()
		_ = h.Store.PutDelivery(ctx, d)
		return
	}
	card := h.card(a, todo, d)
	send, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	switch d.State {
	case "pending":
		if b, e := h.Store.GetBinding(ctx, d.Channel, d.UserID); e != nil || b.ExternalID != d.ExternalID {
			d.State, d.Error = "dropped", "成员已解除绑定"
			break
		}
		ref, e := a.channel.Send(send, d.ExternalID, card)
		if e == nil {
			d.State, d.Ref, d.SentAt, d.Error = "sent", ref, d.UpdatedAt, ""
			break
		}
		d.Attempts++
		d.Error = e.Error()
		if d.Attempts >= maxAttempts {
			d.State = "failed"
			log.Printf("消息发送失败 %s：%v", d.ID, e)
		} else {
			d.NextAt = now.Add(retryAfter[min(d.Attempts-1, len(retryAfter)-1)]).UnixMilli()
		}
	case "closing":
		card.Done = todo.Done
		e := a.channel.Update(send, d.Ref, card)
		if e == nil || errors.Is(e, ErrNotUpdatable) {
			d.State, d.Error = "closed", ""
			break
		}
		d.Attempts++
		d.Error = e.Error()
		if d.Attempts >= 3 {
			d.State = "closed"
		} else {
			d.NextAt = now.Add(retryAfter[d.Attempts-1]).UnixMilli()
		}
	}
	if err = h.Store.PutDelivery(ctx, d); err != nil {
		log.Printf("保存消息状态失败 %s：%v", d.ID, err)
	}
}

// EntryURL opens a platform path through the channel's sign-in entry.
func (h *Hub) EntryURL(name, path string) string {
	return h.BaseURL(name) + "/integrations/" + name + "/entry?to=" + url.QueryEscape(path)
}

func (h *Hub) card(a *active, t Todo, d Delivery) Card {
	link := h.EntryURL(a.settings.Channel, "/todo/"+t.ID)
	c := Card{ID: d.ID, Title: t.Title, Subject: t.Subject, Project: t.Project, Fields: t.Fields, URL: link}
	if t.Action != "" {
		c.Buttons = append(c.Buttons, Button{Key: t.Action, Text: ActionLabels[t.Action], Primary: true}, Button{Text: "查看详情", URL: link})
	} else {
		c.Buttons = append(c.Buttons, Button{Text: "去处理", URL: link, Primary: true})
	}
	if !t.Open {
		c.Done = t.Done
	}
	return c
}

var doneLabels = map[string]string{
	"confirm":           "已确认需求稿",
	"confirm_rejection": "已同意驳回",
	"test_pass":         "已确认测试通过",
	"verify_bug":        "已确认验证通过",
	"accept":            "已确认验收通过",
}

// Interact handles a card button. Whatever happens, the person sees either
// a final line on the card or, when nothing changed, the card as it was.
func (h *Hub) Interact(ctx context.Context, name string, in Interaction) Card {
	d, err := h.Store.GetDelivery(ctx, in.CardID)
	if err != nil || d.Channel != name || d.ExternalID != in.ExternalID {
		return Card{Done: "无法识别这张卡片"}
	}
	todo, err := h.Store.GetTodo(ctx, d.TodoID)
	if err != nil {
		return Card{}
	}
	if !todo.Open {
		return Card{Done: todo.Done}
	}
	// Act on the work as it waits now, not as it was when the card was sent.
	_, desired, err := h.derive(ctx, todo.TaskID)
	if err != nil {
		return Card{}
	}
	i := slices.IndexFunc(desired, func(t Todo) bool { return t.Base == todo.Base })
	if i < 0 || desired[i].Action == "" || desired[i].Action != in.Key || !slices.Contains(desired[i].Recipients, d.UserID) {
		h.Changed(todo.TaskID)
		return Card{Done: "内容已更新，请打开详情处理"}
	}
	current := desired[i]
	current.ID, current.CreatedAt = todo.ID, todo.CreatedAt
	if err = h.Act(ctx, d.UserID, current, "im:"+d.ID+":"+in.Key); err != nil {
		log.Printf("卡片操作未完成 %s：%v", d.ID, err)
		if errors.Is(err, domain.ErrConflict) {
			h.Changed(todo.TaskID)
			return Card{Done: "内容已更新，请打开详情处理"}
		}
		return Card{}
	}
	// The clicked card gets its final line from the click itself.
	d.State, d.UpdatedAt = "closed", h.now().UnixMilli()
	_ = h.Store.PutDelivery(ctx, d)
	if err = h.Reconcile(ctx, todo.TaskID); err != nil {
		log.Printf("待办更新失败 %s：%v", todo.TaskID, err)
	}
	return Card{Done: doneLabels[in.Key]}
}

// Bind links a person to their channel account and sends the open to-dos
// they would have received.
func (h *Hub) Bind(ctx context.Context, name, userID, externalID string) error {
	if strings.TrimSpace(externalID) == "" {
		return errors.New("没有拿到成员 ID")
	}
	if b, err := h.Store.FindBinding(ctx, name, externalID); err == nil && b.UserID != userID {
		return fmt.Errorf("%w: 这个账号已绑定其他平台成员，请先在原成员处解除绑定", domain.ErrConflict)
	}
	if err := h.Store.PutBinding(ctx, Binding{Channel: name, UserID: userID, ExternalID: externalID, At: h.now().UnixMilli()}); err != nil {
		return err
	}
	todos, err := h.Store.UserTodos(ctx, userID, true, 50)
	if err != nil {
		return err
	}
	for _, t := range todos {
		if h.now().Sub(time.UnixMilli(t.CreatedAt)) >= backfill {
			continue
		}
		if err = h.Store.SaveTodo(ctx, t, h.deliveries(ctx, t, []string{userID})); err != nil {
			return err
		}
	}
	h.poke()
	return nil
}

func (h *Hub) Unbind(ctx context.Context, name, userID string) error {
	return h.Store.DeleteBinding(ctx, name, userID)
}

// Test sends a sample card to a person who bound the channel.
func (h *Hub) Test(ctx context.Context, name, userID string) error {
	a, ok := h.channel(name)
	if !ok {
		return errors.New("请先保存并启用渠道")
	}
	b, err := h.Store.GetBinding(ctx, name, userID)
	if err != nil {
		return errors.New("请先在「我的待办」页面绑定你自己的账号，再发送测试消息")
	}
	link := h.EntryURL(name, "/todo")
	_, err = a.channel.Send(ctx, b.ExternalID, Card{ID: "test_" + strings.ReplaceAll(domain.ID(""), "_", ""), Title: "测试消息", Subject: "智驱已经可以给你发送待办", Project: "智驱", URL: link, Buttons: []Button{{Text: "打开我的待办", URL: link, Primary: true}}})
	return err
}

// Channel returns a running channel for its routes.
func (h *Hub) Channel(name string) (Channel, Settings, bool) {
	a, ok := h.channel(name)
	if !ok {
		return nil, Settings{}, false
	}
	return a.channel, a.settings, true
}

// BaseURL is the address a channel's links and callbacks use.
func (h *Hub) BaseURL(name string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if a, ok := h.running[name]; ok && a.settings.PublicURL != "" {
		return a.settings.PublicURL
	}
	return strings.TrimSuffix(h.PublicURL, "/")
}

// Hosts lists the public hosts of running channels, which the server accepts
// in addition to the platform's own.
func (h *Hub) Hosts() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := []string{}
	for _, a := range h.running {
		if a.channel != nil && a.settings.PublicURL != "" {
			if u, err := url.Parse(a.settings.PublicURL); err == nil {
				out = append(out, u.Host)
			}
		}
	}
	return out
}

// activate (re)opens a channel from its saved settings.
func (h *Hub) activate(ctx context.Context, name string) {
	s, err := h.Store.GetChannelSettings(ctx, name)
	a := &active{settings: s}
	if err == nil && s.Enabled {
		var values map[string]string
		if values, err = h.values(ctx, s); err == nil {
			spec, _ := lookup(name)
			a.channel, err = spec.Open(Config{Values: values, PublicURL: h.publicURLOf(s)})
		}
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		a.err = err.Error()
		log.Printf("渠道 %s 未启用：%v", name, err)
	}
	a.settings.Channel = name
	h.mu.Lock()
	h.running[name] = a
	h.mu.Unlock()
}

func (h *Hub) publicURLOf(s Settings) string {
	if s.PublicURL != "" {
		return s.PublicURL
	}
	return strings.TrimSuffix(h.PublicURL, "/")
}

func (h *Hub) values(ctx context.Context, s Settings) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range s.Values {
		out[k] = v
	}
	if s.SecretID != "" {
		secret, _, err := h.Vault.Load(ctx, s.SecretID)
		if err != nil {
			return nil, err
		}
		values := map[string]string{}
		if err = json.Unmarshal([]byte(secret.Token), &values); err != nil {
			return nil, errors.New("渠道密钥已损坏，请重新填写")
		}
		for k, v := range values {
			out[k] = v
		}
	}
	return out, nil
}

// View is a channel as the settings page shows it; secrets only say whether
// they are set.
type View struct {
	Name       string            `json:"name"`
	Label      string            `json:"label"`
	Fields     []FieldSpec       `json:"fields"`
	Values     map[string]string `json:"values"`
	Secrets    map[string]bool   `json:"secrets"`
	Enabled    bool              `json:"enabled"`
	SignIn     bool              `json:"sign_in"`
	PublicURL  string            `json:"public_url"`
	DefaultURL string            `json:"default_url"`
	Setup      []Field           `json:"setup"`
	Running    bool              `json:"running"`
	Error      string            `json:"error,omitempty"`
}

func (h *Hub) Views(ctx context.Context) ([]View, error) {
	out := []View{}
	for _, name := range Names() {
		spec, _ := lookup(name)
		s, err := h.Store.GetChannelSettings(ctx, name)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		if errors.Is(err, domain.ErrNotFound) {
			s = Settings{SignIn: true}
		}
		v := View{Name: name, Label: spec.Label, Fields: spec.Fields, Values: map[string]string{}, Secrets: map[string]bool{}, Enabled: s.Enabled, SignIn: s.SignIn, PublicURL: s.PublicURL, DefaultURL: strings.TrimSuffix(h.PublicURL, "/")}
		secrets := map[string]string{}
		if s.SecretID != "" {
			if all, e := h.values(ctx, Settings{SecretID: s.SecretID}); e == nil {
				secrets = all
			}
		}
		for _, f := range spec.Fields {
			if f.Secret {
				v.Secrets[f.Key] = secrets[f.Key] != ""
			} else {
				v.Values[f.Key] = s.Values[f.Key]
			}
		}
		if spec.Setup != nil {
			v.Setup = spec.Setup(h.publicURLOf(s))
		}
		h.mu.RLock()
		if a, ok := h.running[name]; ok {
			v.Running, v.Error = a.channel != nil, a.err
		}
		h.mu.RUnlock()
		out = append(out, v)
	}
	return out, nil
}

// Input changes a channel's settings; an empty secret keeps the saved one.
type Input struct {
	Enabled   bool              `json:"enabled"`
	SignIn    bool              `json:"sign_in"`
	PublicURL string            `json:"public_url"`
	Values    map[string]string `json:"values"`
}

// Configure validates and saves a channel's settings, then restarts it.
func (h *Hub) Configure(ctx context.Context, name string, in Input, actor string) error {
	h.init()
	spec, ok := lookup(name)
	if !ok {
		return domain.ErrNotFound
	}
	old, err := h.Store.GetChannelSettings(ctx, name)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	public := strings.TrimSuffix(strings.TrimSpace(in.PublicURL), "/")
	if public != "" {
		u, e := url.Parse(public)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return errors.New("公网地址必须是有效的 http(s) 地址")
		}

		platform, e := url.Parse(strings.TrimSuffix(h.PublicURL, "/"))
		if e != nil || u.Path != platform.Path || u.RawPath != "" {
			return errors.New("公网地址的路径必须与平台访问地址相同，请保留工作台挂载的子路径")
		}
	}
	saved := map[string]string{}
	if old.SecretID != "" {
		if saved, err = h.values(ctx, Settings{SecretID: old.SecretID}); err != nil {
			return err
		}
	}
	plain, secrets := map[string]string{}, map[string]string{}
	for _, f := range spec.Fields {
		v := strings.TrimSpace(in.Values[f.Key])
		if f.Secret {
			if v == "" {
				v = saved[f.Key]
			}
			if v != "" {
				secrets[f.Key] = v
			}
		} else if v != "" {
			plain[f.Key] = v
		}
	}
	s := Settings{Channel: name, Enabled: in.Enabled, SignIn: in.SignIn, PublicURL: public, Values: plain, SecretID: old.SecretID, UpdatedBy: actor, UpdatedAt: h.now().UnixMilli()}
	if in.Enabled {
		all := map[string]string{}
		for k, v := range plain {
			all[k] = v
		}
		for k, v := range secrets {
			all[k] = v
		}
		if _, err = spec.Open(Config{Values: all, PublicURL: h.publicURLOf(s)}); err != nil {
			return err
		}
	}
	if !sameValues(secrets, saved) {
		b, _ := json.Marshal(secrets)
		if s.SecretID, err = h.Vault.Save(ctx, "channel:"+name, credential.Secret{Mode: "channel", Token: string(b)}); err != nil {
			return err
		}
	}
	if err = h.Store.PutChannelSettings(ctx, s); err != nil {
		return err
	}
	h.activate(ctx, name)
	h.refreshAll()
	return nil
}

func sameValues(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
