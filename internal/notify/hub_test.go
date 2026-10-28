package notify_test

import (
	"ai-pm-system/internal/credential"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/notify"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeChannel struct {
	mu      sync.Mutex
	sent    map[string]notify.Card // by external ID
	updated map[string]notify.Card // by ref
	fail    error
}

func (f *fakeChannel) Send(_ context.Context, to string, c notify.Card) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return "", f.fail
	}
	f.sent[to] = c
	return "ref:" + to + ":" + c.ID, nil
}
func (f *fakeChannel) Update(_ context.Context, ref string, c notify.Card) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updated[ref] = c
	return nil
}
func (f *fakeChannel) Callback(func(context.Context, notify.Interaction) notify.Card) http.Handler {
	return http.NotFoundHandler()
}
func (f *fakeChannel) AuthorizeURL(*http.Request, string, string) string { return "" }
func (f *fakeChannel) Identify(context.Context, string) (string, error)  { return "", nil }

var fake = &fakeChannel{sent: map[string]notify.Card{}, updated: map[string]notify.Card{}}

func init() {
	notify.Register("fake", notify.Spec{Label: "测试渠道", Fields: []notify.FieldSpec{{Key: "app", Label: "应用", Required: true}, {Key: "secret", Label: "密钥", Secret: true, Required: true}}, Open: func(c notify.Config) (notify.Channel, error) {
		if c.Values["app"] == "" || c.Values["secret"] == "" {
			return nil, errors.New("缺少应用或密钥")
		}
		return fake, nil
	}})
}

type acted struct{ user, todo, action, request string }

func setup(t *testing.T) (*sqlite.DB, *notify.Hub, *[]acted) {
	t.Helper()
	fake.mu.Lock()
	fake.sent, fake.updated, fake.fail = map[string]notify.Card{}, map[string]notify.Card{}, nil
	fake.mu.Unlock()
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "ai-pm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	vault, err := credential.Open(filepath.Join(dir, "repository.key"), db)
	if err != nil {
		t.Fatal(err)
	}
	actions := &[]acted{}
	hub := &notify.Hub{Store: db, Data: db, Vault: vault, PublicURL: "https://pm.example.com/"}
	hub.Act = func(c context.Context, user string, todo notify.Todo, request string) error {
		*actions = append(*actions, acted{user, todo.ID, todo.Action, request})
		task, err := db.GetTask(c, todo.TaskID)
		if err != nil {
			return err
		}
		task.Status = "pending_release"
		task.Version++
		return db.CommitTask(c, task, task.Version-1, domain.Event{ProjectID: task.ProjectID, TaskID: task.ID, Kind: "test_pass", Actor: "陈晨", At: domain.Now()}, nil)
	}
	return db, hub, actions
}

func project(t *testing.T, db *sqlite.DB, members ...domain.ProjectMember) domain.Project {
	p := domain.Project{ID: "prj_1", Name: "星河商城", Memberships: members}
	if err := db.PutProject(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func task(t *testing.T, db *sqlite.DB, status string) domain.Task {
	v := domain.Task{ID: "tsk_1", ProjectID: "prj_1", Kind: "requirement", Title: "积分兑换", Status: status, Version: 1, Revision: 1, Creator: &domain.ActorRef{ID: "pm", Name: "产品", Kind: "human"}, Submission: &domain.Submission{SHA: "0123456789abcdef", Environment: "staging", Revision: 1}}
	if err := db.CommitTask(context.Background(), v, 0, domain.Event{ProjectID: v.ProjectID, TaskID: v.ID, Kind: "created", Actor: "产品", At: domain.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	return v
}

func configure(t *testing.T, hub *notify.Hub) {
	t.Helper()
	if err := hub.Configure(context.Background(), "fake", notify.Input{Enabled: true, SignIn: true, Values: map[string]string{"app": "1", "secret": "s3cret"}}, "admin"); err != nil {
		t.Fatal(err)
	}
}

func TestTestingFallsBackToProductAndActsFromCard(t *testing.T) {
	c := context.Background()
	db, hub, actions := setup(t)
	configure(t, hub)
	project(t, db, domain.ProjectMember{UserID: "pm", Roles: []string{"product", "admin"}}, domain.ProjectMember{UserID: "pm2", Roles: []string{"product"}}, domain.ProjectMember{UserID: "dev", Roles: []string{"developer"}})
	task(t, db, "testing")
	for user, external := range map[string]string{"pm": "wx_pm", "pm2": "wx_pm2", "dev": "wx_dev"} {
		if err := hub.Bind(c, "fake", user, external); err != nil {
			t.Fatal(err)
		}
	}
	if err := hub.Reconcile(c, "tsk_1"); err != nil {
		t.Fatal(err)
	}
	todos, _ := db.OpenTodos(c, "tsk_1")
	if len(todos) != 1 || todos[0].Kind != "test" || todos[0].Action != "test_pass" || strings.Join(todos[0].Recipients, ",") != "pm,pm2" {
		t.Fatalf("no tester means product tests: %+v", todos)
	}
	hub.Dispatch(c)
	card, ok := fake.sent["wx_pm"]
	if !ok || fake.sent["wx_dev"].ID != "" || card.Buttons[0].Key != "test_pass" || !strings.HasPrefix(card.URL, "https://pm.example.com/integrations/fake/entry?to=%2Ftodo%2Ftd_") {
		t.Fatalf("cards: %+v", fake.sent)
	}
	// A click from someone else's card is refused; the recipient's click acts once.
	if got := hub.Interact(c, "fake", notify.Interaction{CardID: card.ID, Key: "test_pass", ExternalID: "wx_dev"}); got.Done != "无法识别这张卡片" {
		t.Fatal(got)
	}
	if got := hub.Interact(c, "fake", notify.Interaction{CardID: card.ID, Key: "test_pass", ExternalID: "wx_pm"}); got.Done != "已确认测试通过" {
		t.Fatal(got)
	}
	if len(*actions) != 1 || (*actions)[0].user != "pm" || (*actions)[0].request != "im:"+card.ID+":test_pass" {
		t.Fatal(*actions)
	}
	if got := hub.Interact(c, "fake", notify.Interaction{CardID: card.ID, Key: "test_pass", ExternalID: "wx_pm"}); !strings.HasPrefix(got.Done, "已由 陈晨") || len(*actions) != 1 {
		t.Fatal("a repeated click must not act again", got)
	}
	hub.Dispatch(c)
	other := fake.sent["wx_pm2"]
	if fake.updated["ref:wx_pm2:"+other.ID].Done != "已由 陈晨 处理" {
		t.Fatalf("other recipients' cards are closed: %+v", fake.updated)
	}
	if _, clicked := fake.updated["ref:wx_pm:"+card.ID]; clicked {
		t.Fatal("the clicked card already shows its result")
	}
	// The developer now has the release to-do.
	todos, _ = db.OpenTodos(c, "tsk_1")
	if len(todos) != 1 || todos[0].Kind != "release" || todos[0].Recipients[0] != "dev" {
		t.Fatalf("%+v", todos)
	}
}

func TestTesterRoleBindingBackfillAndRetry(t *testing.T) {
	c := context.Background()
	db, hub, _ := setup(t)
	configure(t, hub)
	project(t, db, domain.ProjectMember{UserID: "pm", Roles: []string{"product", "admin"}}, domain.ProjectMember{UserID: "dev", Roles: []string{"developer", "tester"}})
	task(t, db, "testing")
	if err := hub.Reconcile(c, "tsk_1"); err != nil {
		t.Fatal(err)
	}
	todos, _ := db.OpenTodos(c, "tsk_1")
	if len(todos) != 1 || strings.Join(todos[0].Recipients, ",") != "dev" {
		t.Fatalf("testers receive testing: %+v", todos)
	}
	if mine, _ := db.UserTodos(c, "dev", true, 10); len(mine) != 1 {
		t.Fatal("to-dos exist without any binding", mine)
	}
	hub.Dispatch(c)
	if len(fake.sent) != 0 {
		t.Fatal("nothing is sent before binding")
	}
	fake.fail = errors.New("企业微信接口错误 60020")
	if err := hub.Bind(c, "fake", "dev", "wx_dev"); err != nil {
		t.Fatal(err)
	}
	hub.Dispatch(c)
	deliveries, _ := db.TodoDeliveries(c, todos[0].ID)
	if len(deliveries) != 1 || deliveries[0].State != "pending" || deliveries[0].Attempts != 1 || !strings.Contains(deliveries[0].Error, "60020") {
		t.Fatalf("failed sends retry later: %+v", deliveries)
	}
	if err := hub.Bind(c, "fake", "pm", "wx_dev"); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("one channel account binds one member", err)
	}
	if err := hub.Unbind(c, "fake", "dev"); err != nil {
		t.Fatal(err)
	}
	fake.fail = nil
	d := deliveries[0]
	d.NextAt = 0
	_ = db.PutDelivery(c, d)
	hub.Dispatch(c)
	if deliveries, _ = db.TodoDeliveries(c, todos[0].ID); deliveries[0].State != "dropped" || len(fake.sent) != 0 {
		t.Fatalf("unbinding drops unsent cards: %+v", deliveries)
	}
	// Binding again sends the card again.
	if err := hub.Bind(c, "fake", "dev", "wx_dev"); err != nil {
		t.Fatal(err)
	}
	hub.Dispatch(c)
	if _, ok := fake.sent["wx_dev"]; !ok {
		t.Fatal("rebinding resends open to-dos")
	}
}

func TestWorkWaitingAgainIsANewTodo(t *testing.T) {
	c := context.Background()
	db, hub, _ := setup(t)
	project(t, db, domain.ProjectMember{UserID: "pm", Roles: []string{"product", "admin"}}, domain.ProjectMember{UserID: "dev", Roles: []string{"developer"}})
	v := task(t, db, "ready")
	_ = hub.Reconcile(c, v.ID)
	first, _ := db.OpenTodos(c, v.ID)
	for i, status := range []string{"doing", "ready"} {
		v.Status, v.Claimant = status, map[bool]string{true: "dev"}[status == "doing"]
		v.Version++
		if err := db.CommitTask(c, v, v.Version-1, domain.Event{ProjectID: v.ProjectID, TaskID: v.ID, Kind: "claim", Actor: "开发 · MCP", At: domain.Now()}, nil); err != nil {
			t.Fatal(i, err)
		}
		_ = hub.Reconcile(c, v.ID)
	}
	again, _ := db.OpenTodos(c, v.ID)
	closed, _ := db.GetTodo(c, first[0].ID)
	if len(first) != 1 || len(again) != 1 || again[0].ID != first[0].ID+"_2" || closed.Open || closed.Done != "已由 开发 处理" {
		t.Fatalf("first %+v again %+v closed %+v", first, again, closed)
	}
}

func TestConfigureKeepsAndHidesSecrets(t *testing.T) {
	c := context.Background()
	_, hub, _ := setup(t)
	if err := hub.Configure(c, "fake", notify.Input{Enabled: true, Values: map[string]string{"app": "1"}}, "admin"); err == nil {
		t.Fatal("enabling validates the settings")
	}
	configure(t, hub)
	if err := hub.Configure(c, "fake", notify.Input{Enabled: true, PublicURL: "https://im.example.com/", Values: map[string]string{"app": "2"}}, "admin"); err != nil {
		t.Fatal("an empty secret keeps the saved one", err)
	}
	if err := hub.Configure(c, "fake", notify.Input{Enabled: true, PublicURL: "https://im.example.com/path", Values: map[string]string{"app": "2"}}, "admin"); err == nil {
		t.Fatal("public URL must be a site root")
	}
	views, err := hub.Views(c)
	if err != nil {
		t.Fatal(err)
	}
	var v notify.View
	for _, x := range views {
		if x.Name == "fake" {
			v = x
		}
	}
	if !v.Running || v.Values["app"] != "2" || !v.Secrets["secret"] || v.PublicURL != "https://im.example.com" || strings.Contains(strings.Join(mapValues(v.Values), ","), "s3cret") {
		t.Fatalf("%+v", v)
	}
	if got := hub.Hosts(); len(got) != 1 || got[0] != "im.example.com" {
		t.Fatal(got)
	}
}

func mapValues(m map[string]string) []string {
	out := []string{}
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func TestChannelPublicURLKeepsWorkspaceMount(t *testing.T) {
	_, hub, _ := setup(t)
	hub.PublicURL = "https://pm.example.com/workspace"
	for _, public := range []string{"https://im.example.com", "https://im.example.com/other", "https://im.example.com/a/../workspace", "https://im.example.com/workspace?x=1"} {
		if err := hub.Configure(context.Background(), "fake", notify.Input{PublicURL: public}, "admin"); err == nil {
			t.Fatal("accepted mismatched mount", public)
		}
	}
	if err := hub.Configure(context.Background(), "fake", notify.Input{PublicURL: "https://im.example.com/workspace"}, "admin"); err != nil {
		t.Fatal(err)
	}
}
