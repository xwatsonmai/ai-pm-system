package server_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/auth"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/server"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type browser struct {
	t      *testing.T
	site   string
	client *http.Client
}

func newBrowser(t *testing.T, site string) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t, site, &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (b *browser) do(method, path string, body any, want int) map[string]any {
	b.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = strings.NewReader(string(raw))
	}
	req, _ := http.NewRequest(method, b.site+path, reader)
	req.Header.Set("Content-Type", "application/json")
	resp, e := b.client.Do(req)
	if e != nil {
		b.t.Fatal(e)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		b.t.Fatalf("%s %s: want %d got %d: %s", method, path, want, resp.StatusCode, raw)
	}
	if strings.Contains(string(raw), "argon2") {
		b.t.Fatalf("%s %s leaked a password hash", method, path)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	if len(raw) > 0 && raw[0] == '[' {
		var list []any
		_ = json.Unmarshal(raw, &list)
		out["items"] = list
	}
	return out
}

func TestBuiltInAccountsSetupLoginPasswordAndAdministration(t *testing.T) {
	ctx := context.Background()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "auth.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	site := httptest.NewUnstartedServer(nil)
	base := "http://" + site.Listener.Addr().String()
	config := filepath.Join(t.TempDir(), "auth.json")
	if e = os.WriteFile(config, []byte(`{"provider":"local","public_url":"`+base+`","platform_admin_login_ids":["boot"]}`), 0600); e != nil {
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
	site.Config.Handler = (&server.Server{App: svc, Auth: login}).Handler()
	site.Start()
	defer site.Close()

	admin := newBrowser(t, site.URL)
	if s := admin.do("GET", "/api/auth/session", nil, 200); s["login"] != "password" || s["setup_required"] != true || s["authenticated"] != false {
		t.Fatal(s)
	}
	admin.do("GET", "/api/projects", nil, 401)
	admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "whatever-123"}, 400)
	admin.do("POST", "/api/auth/setup", map[string]string{"token": "guess", "username": "admin", "password": "admin-pass-1"}, 400)
	admin.do("POST", "/api/auth/setup", map[string]string{"token": login.Accounts.SetupToken(), "username": "Admin", "name": "管理员", "password": "admin-pass-1"}, 200)
	s := admin.do("GET", "/api/auth/session", nil, 200)
	if s["authenticated"] != true || s["platform_admin"] != true || s["setup_required"] != false || s["directory"].(map[string]any)["label"] != "平台账号" {
		t.Fatal(s)
	}
	adminID := s["user"].(map[string]any)["id"].(string)
	for _, c := range admin.client.Jar.Cookies(must(url.Parse(site.URL))) {
		if c.Name == login.SessionCookie && len(c.Value) != 43 {
			t.Fatal("session cookie")
		}
	}
	admin.do("POST", "/api/auth/setup", map[string]string{"token": "", "username": "late", "password": "late-pass-1"}, 400)

	// Accounts an administrator creates must choose their own password first.
	bob := admin.do("POST", "/api/platform/accounts", map[string]string{"username": "bob", "name": "Bob", "password": "temporary-1"}, 200)
	bobID := bob["user_id"].(string)
	if bob["must_change_password"] != true {
		t.Fatal(bob)
	}
	admin.do("POST", "/api/platform/accounts", map[string]string{"username": "BOB", "password": "temporary-1"}, 400)
	admin.do("POST", "/api/platform/accounts", map[string]string{"username": "x", "password": "temporary-1"}, 400)
	admin.do("POST", "/api/platform/accounts", map[string]string{"username": "carol", "password": "short"}, 400)
	if list := admin.do("GET", "/api/platform/accounts", nil, 200)["items"].([]any); len(list) != 2 {
		t.Fatal(list)
	}
	if d := admin.do("GET", "/api/platform/directory?q=bob", nil, 200); d["total"] != float64(1) || d["label"] != "平台账号" {
		t.Fatal(d)
	}

	user := newBrowser(t, site.URL)
	user.do("POST", "/api/auth/login", map[string]string{"username": "nobody", "password": "temporary-1"}, 400)
	if r := user.do("POST", "/api/auth/login", map[string]string{"username": " BOB ", "password": "temporary-1", "return_to": "https://evil.example/"}, 200); r["must_change_password"] != true || r["redirect"] != "/" {
		t.Fatal(r)
	}
	if s := user.do("GET", "/api/auth/session", nil, 200); s["must_change_password"] != true || s["platform_admin"] != nil {
		t.Fatal("password change not required", s)
	}
	user.do("GET", "/api/projects", nil, 401)
	// Signing out is still possible before choosing a password.
	user.do("POST", "/api/auth/logout", map[string]string{}, 200)
	if s := user.do("GET", "/api/auth/session", nil, 200); s["authenticated"] != false {
		t.Fatal("logout kept the password-change session", s)
	}
	user.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": "temporary-1"}, 200)
	user.do("POST", "/api/auth/password", map[string]string{"current_password": "wrong-pass", "new_password": "bob-own-pass"}, 400)
	user.do("POST", "/api/auth/password", map[string]string{"current_password": "temporary-1", "new_password": "temporary-1"}, 400)
	user.do("POST", "/api/auth/password", map[string]string{"current_password": "temporary-1", "new_password": "bob-own-pass", "return_to": "/oauth/authorize?client_id=x"}, 200)
	user.do("GET", "/api/projects", nil, 200)
	user.do("GET", "/api/platform/accounts", nil, 403)
	user.do("PATCH", "/api/platform/accounts/"+adminID, map[string]bool{"disabled": true}, 403)

	// Administrators manage membership from the same accounts.
	project, e := svc.SaveProjectAs(ctx, domain.Actor{ID: adminID, Name: "管理员", Kind: "human"}, domain.Project{Name: "Accounts project"})
	if e != nil {
		t.Fatal(e)
	}
	admin.do("PUT", "/api/projects/"+project.ID+"/members/"+bobID, map[string]string{"role": "developer"}, 200)

	// Disabling ends sessions at once and blocks sign-in and new membership.
	admin.do("PATCH", "/api/platform/accounts/"+adminID, map[string]bool{"disabled": true}, 400)
	admin.do("PATCH", "/api/platform/accounts/"+bobID, map[string]bool{"disabled": true}, 200)
	user.do("GET", "/api/projects", nil, 401)
	if r := user.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": "bob-own-pass"}, 400); !strings.Contains(r["error"].(string), "停用") {
		t.Fatal(r)
	}
	if r := admin.do("PUT", "/api/projects/"+project.ID+"/members/"+bobID, map[string]string{"role": "product"}, 400); !strings.Contains(r["error"].(string), "平台账号中没有该成员") {
		t.Fatal(r)
	}
	if d := admin.do("GET", "/api/platform/directory?q=bob", nil, 200); d["total"] != float64(0) {
		t.Fatal("disabled account still listed", d)
	}

	// A reset password is temporary again; repeated failures pause the account.
	admin.do("PATCH", "/api/platform/accounts/"+bobID, map[string]any{"disabled": false, "password": "reset-pass-1", "name": "Bob Lee"}, 200)
	if r := user.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": "reset-pass-1"}, 200); r["must_change_password"] != true {
		t.Fatal(r)
	}
	for range 5 {
		user.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": "wrong-pass"}, 400)
	}
	if r := user.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": "reset-pass-1"}, 400); !strings.Contains(r["error"].(string), "次数过多") {
		t.Fatal("lockout", r)
	}

	// platform_admin_login_ids also applies to built-in usernames.
	admin.do("POST", "/api/platform/accounts", map[string]string{"username": "boot", "password": "boot-temp-1"}, 200)
	boot := newBrowser(t, site.URL)
	boot.do("POST", "/api/auth/login", map[string]string{"username": "boot", "password": "boot-temp-1"}, 200)
	boot.do("POST", "/api/auth/password", map[string]string{"current_password": "boot-temp-1", "new_password": "boot-own-pass"}, 200)
	if s := boot.do("GET", "/api/auth/session", nil, 200); s["platform_admin"] != true {
		t.Fatal("bootstrap admin", s)
	}
	// A rename sticks even while the renamed person keeps using an older session.
	bootID := admin.do("GET", "/api/platform/accounts", nil, 200)["items"].([]any)[2].(map[string]any)["user_id"].(string)
	admin.do("PATCH", "/api/platform/accounts/"+bootID, map[string]string{"name": "引导管理员"}, 200)
	boot.do("GET", "/api/auth/session", nil, 200)
	if users, _ := db.ListUsers(ctx); !slices.ContainsFunc(users, func(u domain.User) bool { return u.ID == bootID && u.Name == "引导管理员" }) {
		t.Fatal("rename reverted by an older session", users)
	}

	// Sign-in pages exist only for this provider, and cross-site sign-in is refused.
	start, e := admin.client.Get(site.URL + "/auth/login?return_to=" + url.QueryEscape("/oauth/authorize?client_id=x"))
	if e != nil {
		t.Fatal(e)
	}
	start.Body.Close()
	if start.StatusCode != 303 || start.Header.Get("Location") != "/?return_to=%2Foauth%2Fauthorize%3Fclient_id%3Dx" {
		t.Fatal("login redirect", start.StatusCode, start.Header.Get("Location"))
	}
	callback, e := admin.client.Get(site.URL + "/auth/callback?code=x&state=y")
	if e != nil {
		t.Fatal(e)
	}
	callback.Body.Close()
	if callback.StatusCode != 404 {
		t.Fatal("callback served without a redirect provider")
	}
	csrf, _ := http.NewRequest("POST", site.URL+"/api/auth/login", strings.NewReader(`{"username":"admin","password":"admin-pass-1"}`))
	csrf.Header.Set("Content-Type", "application/json")
	csrf.Header.Set("Origin", "https://untrusted.example")
	csrfResponse, e := user.client.Do(csrf)
	if e != nil {
		t.Fatal(e)
	}
	csrfResponse.Body.Close()
	if csrfResponse.StatusCode != 403 {
		t.Fatal("cross-site login accepted")
	}
	admin.do("POST", "/api/auth/logout", map[string]string{}, 200)
	admin.do("GET", "/api/projects", nil, 401)
}

func TestAccountEndpointsAbsentWithoutBuiltInAccounts(t *testing.T) {
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	site := httptest.NewServer((&server.Server{App: &app.Service{Store: db}}).Handler())
	defer site.Close()
	b := newBrowser(t, site.URL)
	b.do("POST", "/api/auth/login", map[string]string{"username": "a", "password": "b"}, 404)
	b.do("GET", "/api/platform/accounts", nil, 404)
}

func must[T any](v T, e error) T {
	if e != nil {
		panic(e)
	}
	return v
}
