package auth

import (
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLoadRejectsInvalidFilesAndUnknownProviders(t *testing.T) {
	p := filepath.Join(t.TempDir(), "auth.json")
	if f, e := Load(p, false); e != nil || f != nil {
		t.Fatal(f, e)
	}
	if _, e := Load(p, true); e == nil {
		t.Fatal("explicit missing config ignored")
	}
	for data, want := range map[string]string{
		`not json`: "有效配置",
		`{"provider":"local","public_url":"http://127.0.0.1:8090"} {}`: "一个 JSON 对象",
		`{"public_url":"http://127.0.0.1:8090"}`:                       "需要填写 provider",
		`{"provider":"oa","public_url":"http://127.0.0.1:8090"}`:       "不可用",
		`{"provider":"local","public_url":"http://127.0.0.1:8090/ai"}`: "根地址",
		`{"provider":"local","public_url":"https://u:p@example.com"}`:  "public_url",
	} {
		if e := os.WriteFile(p, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := Load(p, false); e == nil || !strings.Contains(e.Error(), want) {
			t.Errorf("%s: want %q, got %v", data, want, e)
		}
	}
}

func TestLocalProviderIsStrictAndOffersSetupOnce(t *testing.T) {
	ctx := context.Background()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "auth.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	p := filepath.Join(t.TempDir(), "auth.json")
	if e = os.WriteFile(p, []byte(`{"provider":"local","public_url":"http://127.0.0.1:8090","issuer":"typo"}`), 0600); e != nil {
		t.Fatal(e)
	}
	f, e := Load(p, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Open(ctx, f, db); e == nil {
		t.Fatal("unknown local field accepted")
	}
	if e = os.WriteFile(p, []byte(`{"provider":"local","public_url":"http://127.0.0.1:8090/","platform_admin_login_ids":["boot"]}`), 0600); e != nil {
		t.Fatal(e)
	}
	if f, e = Load(p, true); e != nil {
		t.Fatal(e)
	}
	m, e := Open(ctx, f, db)
	if e != nil {
		t.Fatal(e)
	}
	if m.Accounts == nil || m.Redirect != nil || m.Directory == nil || m.PublicURL() != "http://127.0.0.1:8090" || !m.BootstrapAdmin("boot") || m.BootstrapAdmin("") {
		t.Fatal("local provider wiring")
	}
	token := m.Accounts.SetupToken()
	if len(token) != 43 {
		t.Fatal("setup token missing")
	}
	if _, e = m.Accounts.Setup(ctx, "wrong", "admin", "", "correct horse"); e == nil {
		t.Fatal("wrong setup token accepted")
	}
	id, e := m.Accounts.Setup(ctx, token, " Admin ", "", "correct horse")
	if e != nil || id.User.LoginID != "admin" || id.User.Name != "admin" || id.MustChangePassword {
		t.Fatal(id, e)
	}
	if _, e = m.Accounts.Setup(ctx, token, "second", "", "correct horse"); e == nil || m.Accounts.SetupToken() != "" {
		t.Fatal("setup token reused")
	}
	admins, _ := db.ListPlatformAdmins(ctx)
	if len(admins) != 1 || admins[0] != id.User.ID {
		t.Fatal("first account is not platform admin", admins)
	}
	// A restart with an existing account offers no setup token.
	if again, e := Open(ctx, f, db); e != nil || again.Accounts.SetupToken() != "" {
		t.Fatal("setup offered after initialization", e)
	}
}

func TestPasswordHashesVerifyAndRejectTampering(t *testing.T) {
	h := hashPassword("correct horse")
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") || h == hashPassword("correct horse") {
		t.Fatal("hash format or salt", h)
	}
	if !checkPassword(h, "correct horse") || checkPassword(h, "correct horsE") {
		t.Fatal("password check")
	}
	for _, bad := range []string{"", "plain", strings.Replace(h, "m=65536", "m=4194304", 1), strings.Replace(h, "argon2id", "argon2i", 1), h[:len(h)-4]} {
		if checkPassword(bad, "correct horse") {
			t.Fatal("tampered hash accepted", bad)
		}
	}
}

func openLocal(t *testing.T) (*Manager, *sqlite.DB) {
	t.Helper()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "auth.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	p := filepath.Join(t.TempDir(), "auth.json")
	if e = os.WriteFile(p, []byte(`{"provider":"local","public_url":"http://127.0.0.1:8090"}`), 0600); e != nil {
		t.Fatal(e)
	}
	f, e := Load(p, true)
	if e != nil {
		t.Fatal(e)
	}
	m, e := Open(context.Background(), f, db)
	if e != nil {
		t.Fatal(e)
	}
	return m, db
}

func TestParallelGuessesCannotOvertakeLockout(t *testing.T) {
	ctx := context.Background()
	m, _ := openLocal(t)
	if _, e := m.Accounts.Setup(ctx, m.Accounts.SetupToken(), "admin", "", "correct horse"); e != nil {
		t.Fatal(e)
	}
	var wrong, locked atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			_, e := m.Accounts.Authenticate(ctx, "admin", "wrong guess")
			switch {
			case errors.Is(e, errBadLogin):
				wrong.Add(1)
			case e != nil && strings.Contains(e.Error(), "次数过多"):
				locked.Add(1)
			}
		})
	}
	wg.Wait()
	if wrong.Load() != maxFailures || locked.Load() != 12-maxFailures {
		t.Fatal("guesses checked", wrong.Load(), "locked", locked.Load())
	}
	if _, e := m.Accounts.Authenticate(ctx, "admin", "correct horse"); e == nil {
		t.Fatal("locked account accepted a password")
	}
}

func TestSessionIsNotIssuedForAnAccountChangedDuringSignIn(t *testing.T) {
	ctx := context.Background()
	m, _ := openLocal(t)
	admin, e := m.Accounts.Setup(ctx, m.Accounts.SetupToken(), "admin", "", "correct horse")
	if e != nil {
		t.Fatal(e)
	}
	bob, e := m.Accounts.Create(ctx, "bob", "", "temporary-1")
	if e != nil {
		t.Fatal(e)
	}
	for _, change := range []AccountChange{{Disabled: ptr(true)}, {Password: ptr("reset-pass-1")}} {
		id, e := m.Accounts.Authenticate(ctx, "bob", "temporary-1")
		if e != nil {
			t.Fatal(e)
		}
		// The administrator acts after the password was verified but before the session is written.
		if _, e = m.Accounts.Update(ctx, admin.User.ID, bob.UserID, change); e != nil {
			t.Fatal(e)
		}
		w := httptest.NewRecorder()
		if e = m.Issue(ctx, w, httptest.NewRequest("POST", "/api/auth/login", nil), id); e == nil || len(w.Result().Cookies()) != 0 {
			t.Fatal("session issued for a changed account", change)
		}
		if _, e = m.Accounts.Update(ctx, admin.User.ID, bob.UserID, AccountChange{Disabled: ptr(false), Password: ptr("temporary-1")}); e != nil {
			t.Fatal(e)
		}
	}
	id, _ := m.Accounts.Authenticate(ctx, "bob", "temporary-1")
	w := httptest.NewRecorder()
	if e = m.Issue(ctx, w, httptest.NewRequest("POST", "/api/auth/login", nil), id); e != nil || len(w.Result().Cookies()) != 1 {
		t.Fatal("unchanged account could not sign in", e)
	}
	req := httptest.NewRequest("GET", "/api/auth/session", nil)
	req.AddCookie(w.Result().Cookies()[0])
	if s, e := m.Session(req); e != nil || !s.MustChangePassword {
		t.Fatal(s, e)
	}
	if _, e = m.User(req); e == nil {
		t.Fatal("password-change session used as a full session")
	}
}

func ptr[T any](v T) *T { return &v }
