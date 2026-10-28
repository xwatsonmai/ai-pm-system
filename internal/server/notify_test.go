package server_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/auth"
	"ai-pm-system/internal/credential"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/notify"
	"ai-pm-system/internal/server"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// imChannel stands in for an IM: its authorization page hands back the code
// it was given, and the code is the person's channel account.
type imChannel struct {
	mu   sync.Mutex
	sent map[string]notify.Card
}

func (c *imChannel) Send(_ context.Context, to string, card notify.Card) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent[to] = card
	return to, nil
}
func (c *imChannel) Update(context.Context, string, notify.Card) error { return notify.ErrNotUpdatable }
func (c *imChannel) Callback(on func(context.Context, notify.Interaction) notify.Card) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		_, _ = io.WriteString(w, on(r.Context(), notify.Interaction{CardID: q.Get("card"), Key: q.Get("key"), ExternalID: q.Get("user")}).Done)
	})
}
func (c *imChannel) AuthorizeURL(_ *http.Request, redirect, state string) string {
	return "https://im.example/authorize?" + url.Values{"redirect_uri": {redirect}, "state": {state}}.Encode()
}
func (c *imChannel) Identify(_ context.Context, code string) (string, error) {
	if code == "bad" {
		return "", errors.New("授权码无效")
	}
	return code, nil
}

var im = &imChannel{sent: map[string]notify.Card{}}

func init() {
	notify.Register("fakeim", notify.Spec{Label: "测试 IM", Fields: []notify.FieldSpec{{Key: "secret", Label: "密钥", Secret: true}}, Open: func(notify.Config) (notify.Channel, error) { return im, nil }})
}

// get follows nothing and returns the redirect target.
func (b *browser) get(path string) (int, string) {
	b.t.Helper()
	resp, e := b.client.Get(b.site + path)
	if e != nil {
		b.t.Fatal(e)
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Location")
}

// authorizeAs completes the fake channel's authorization as account.
func (b *browser) authorizeAs(path, account string) string {
	b.t.Helper()
	code, to := b.get(path)
	if code != 303 || !strings.HasPrefix(to, "https://im.example/authorize?") {
		b.t.Fatalf("%s: %d %s", path, code, to)
	}
	q := must(url.Parse(to)).Query()
	callback := must(url.Parse(q.Get("redirect_uri")))
	code, next := b.get(strings.TrimPrefix(callback.Path, must(url.Parse(b.site)).Path) + "?" + url.Values{"code": {account}, "state": {q.Get("state")}}.Encode())
	if code != 303 {
		b.t.Fatalf("oauth: %d %s", code, next)
	}
	return next
}

func TestIMBindingEntrySignInAndCardActions(t *testing.T) {
	for _, mount := range []string{"", "/zhiqu"} {
		t.Run(mount, func(t *testing.T) { testIMBindingEntrySignInAndCardActions(t, mount) })
	}
}

func testIMBindingEntrySignInAndCardActions(t *testing.T, mount string) {
	ctx := context.Background()
	dir := t.TempDir()
	db, e := sqlite.Open(filepath.Join(dir, "ai-pm.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	vault, e := credential.Open(filepath.Join(dir, "repository.key"), db)
	if e != nil {
		t.Fatal(e)
	}
	site := httptest.NewUnstartedServer(nil)
	base := "http://" + site.Listener.Addr().String() + mount
	config := filepath.Join(dir, "auth.json")
	if e = os.WriteFile(config, []byte(`{"provider":"local","public_url":"`+base+`"}`), 0600); e != nil {
		t.Fatal(e)
	}
	file, e := auth.Load(config, true)
	if e != nil {
		t.Fatal(e)
	}
	login, e := auth.Open(ctx, file, db)
	if e != nil {
		t.Fatal(e)
	}
	hub := &notify.Hub{Store: db, Data: db, Vault: vault, PublicURL: base}
	api := &server.Server{App: &app.Service{Store: notify.Observed{Store: db, Hub: hub}}, Auth: login, Notify: hub}
	hub.Act = api.IMAct
	site.Config.Handler = api.Handler()
	site.Start()
	defer site.Close()

	admin := newBrowser(t, base)
	admin.do("POST", "/api/auth/setup", map[string]string{"token": login.Accounts.SetupToken(), "username": "admin", "name": "管理员", "password": "admin-pass-1"}, 200)
	lin := admin.do("POST", "/api/platform/accounts", map[string]string{"username": "lin", "name": "陈晨", "password": "temporary-1"}, 200)["user_id"].(string)
	// Static entry pages keep all assets under the configured mount, including detail pages.
	for _, path := range []string{"/todo", "/todo/td_abc"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(body), `src="`+mount+`/todo.mjs"`) || !strings.Contains(string(body), `href="`+mount+`/style.css"`) {
			t.Fatal(resp.StatusCode, string(body))
		}
	}
	admin.do("PUT", "/api/platform/channels/fakeim", map[string]any{"enabled": true, "sign_in": true, "values": map[string]string{"secret": "s3cret"}}, 200)
	if v := admin.do("GET", "/api/platform/channels", nil, 200); strings.Contains(must(jsonText(v)), "s3cret") {
		t.Fatal("secret echoed", v)
	}
	newBrowser(t, base).do("GET", "/api/platform/channels", nil, 401)

	p := domain.Project{ID: "prj_im", Name: "星河商城", Memberships: []domain.ProjectMember{{UserID: lin, Roles: []string{"product", "tester", "admin"}}}}
	if e = db.PutProject(ctx, p); e != nil {
		t.Fatal(e)
	}
	task := domain.Task{ID: "tsk_im", ProjectID: p.ID, Kind: "requirement", Title: "积分兑换", Status: "testing", Version: 1, Revision: 1, Acceptance: []string{"可兑换"}, Submission: &domain.Submission{SHA: "abc1234567", Environment: "staging", Revision: 1}}
	if e = db.CommitTask(ctx, task, 0, domain.Event{ProjectID: p.ID, TaskID: task.ID, Kind: "created", Actor: "陈晨", At: domain.Now()}, nil); e != nil {
		t.Fatal(e)
	}
	if e = hub.Reconcile(ctx, task.ID); e != nil {
		t.Fatal(e)
	}
	todos, _ := db.OpenTodos(ctx, task.ID)
	if len(todos) != 1 {
		t.Fatal(todos)
	}
	page := "/todo/" + todos[0].ID

	// From the IM, an unbound account signs in once and is bound to that member.
	phone := newBrowser(t, base)
	if next := phone.authorizeAs("/integrations/fakeim/entry?to="+url.QueryEscape(page), "wx_lin"); next != mount+"/?return_to=%2Fintegrations%2Ffakeim%2Fcomplete" {
		t.Fatal(next)
	}
	if r := phone.do("POST", "/api/auth/login", map[string]string{"username": "lin", "password": "temporary-1", "return_to": "/integrations/fakeim/complete"}, 200); r["redirect"] != mount+"/integrations/fakeim/complete" {
		t.Fatal(r)
	}
	phone.do("POST", "/api/auth/password", map[string]string{"current_password": "temporary-1", "new_password": "lin-pass-12"}, 200)
	if code, to := phone.get("/integrations/fakeim/complete"); code != 303 || to != mount+page {
		t.Fatal(code, to)
	}
	if b, e := db.FindBinding(ctx, "fakeim", "wx_lin"); e != nil || b.UserID != lin {
		t.Fatal(b, e)
	}
	if code, _ := phone.get("/integrations/fakeim/complete"); code != 303 {
		t.Fatal("a pending binding is used once")
	}
	if items := phone.do("GET", "/api/todos", nil, 200)["items"].([]any); len(items) != 1 {
		t.Fatal(items)
	}
	phone.do("GET", "/api/todos/"+todos[0].ID, nil, 200)
	newBrowser(t, base).do("GET", "/api/todos/"+todos[0].ID, nil, 401)

	// A bound account opens the platform without signing in again.
	desktop := newBrowser(t, base)
	if next := desktop.authorizeAs("/integrations/fakeim/entry?to=https://evil.example/", "wx_lin"); next != mount+"/todo" {
		t.Fatal("only to-do pages are entry targets", next)
	}
	if s := desktop.do("GET", "/api/auth/session", nil, 200); s["authenticated"] != true || s["user"].(map[string]any)["id"] != lin {
		t.Fatal(s)
	}
	// Someone else's channel account cannot be bound to an account already bound.
	other := newBrowser(t, base)
	if next := other.authorizeAs("/integrations/fakeim/entry", "bad"); !strings.HasPrefix(next, mount+"/todo?error=") {
		t.Fatal(next)
	}
	// The authorization state belongs to the browser that started it.
	code, to := other.get("/integrations/fakeim/entry")
	state := must(url.Parse(to)).Query().Get("state")
	if code, next := newBrowser(t, base).get("/integrations/fakeim/oauth?code=wx_lin&state=" + url.QueryEscape(state)); code != 303 || !strings.HasPrefix(next, mount+"/todo?error=") {
		t.Fatal(code, next)
	}
	_ = code
	// Binding from the workspace replaces the member's own account.
	if next := admin.authorizeAs("/integrations/fakeim/bind", "wx_admin"); next != mount+"/todo?bound=fakeim" {
		t.Fatal(next)
	}
	if next := admin.authorizeAs("/integrations/fakeim/bind", "wx_lin"); !strings.Contains(next, "error=") {
		t.Fatal("an account bound to someone else is refused", next)
	}

	// Card buttons act through the workspace service as the bound member.
	hub.Dispatch(ctx)
	card := im.sent["wx_lin"]
	if card.Buttons[0].Key != "test_pass" || !strings.HasPrefix(card.URL, base+"/integrations/fakeim/entry?to=%2Ftodo%2Ftd_") {
		t.Fatalf("%+v", card)
	}
	click := func(user string) string {
		resp, e := http.Get(base + "/integrations/fakeim/callback?" + url.Values{"card": {card.ID}, "key": {"test_pass"}, "user": {user}}.Encode())
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if got := click("wx_admin"); got != "无法识别这张卡片" {
		t.Fatal(got)
	}
	if got := click("wx_lin"); got != "已确认测试通过" {
		t.Fatal(got)
	}
	after, _ := db.GetTask(ctx, task.ID)
	if after.Status != "pending_release" || after.TestVerification == nil || after.TestVerification.SHA != "abc1234567" || after.TestVerification.Actor != "陈晨" {
		t.Fatalf("%+v %+v", after.Status, after.TestVerification)
	}
	if open, _ := db.OpenTodos(ctx, task.ID); len(open) != 0 {
		t.Fatal("the release to-do has no developer to go to", open)
	}

	// Unbinding stops cards; the channel's public host is accepted.
	phone.do("DELETE", "/api/me/channels/fakeim", nil, 200)
	if items := phone.do("GET", "/api/me/channels", nil, 200)["items"].([]any); items[0].(map[string]any)["bound"] != false {
		t.Fatal(items)
	}
	admin.do("PUT", "/api/platform/channels/fakeim", map[string]any{"enabled": true, "public_url": "https://im.test" + mount, "values": map[string]string{}}, 200)
	for host, want := range map[string]int{"im.test": 200, "elsewhere.test": 403} {
		req, _ := http.NewRequest("GET", base+"/todo", nil)
		req.Host = host
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatal(host, resp.StatusCode)
		}
	}
	if code, to := phone.get("/integrations/fakeim/bind"); code != 303 || !strings.HasPrefix(to, "https://im.test"+mount+"/integrations/fakeim/bind") {
		t.Fatal("channel flows move to the public address first", code, to)
	}
}

func jsonText(v any) (string, error) {
	b, e := json.Marshal(v)
	return string(b), e
}
