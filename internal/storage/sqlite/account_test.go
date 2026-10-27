package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestAccountsAreUniqueVersionedAndDisablingRevokesAccess(t *testing.T) {
	c := context.Background()
	db, e := Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	first := domain.Account{UserID: "usr_a", Username: "alice", Name: "Alice", PasswordHash: "h1", Version: 1}
	if e = db.CreateAccount(c, first, true); e != nil {
		t.Fatal(e)
	}
	if e = db.CreateAccount(c, domain.Account{UserID: "usr_x", Username: "xavier", PasswordHash: "h", Version: 1}, true); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("second first-admin accepted", e)
	}
	if e = db.CreateAccount(c, domain.Account{UserID: "usr_b", Username: "alice", PasswordHash: "h", Version: 1}, false); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("duplicate username accepted", e)
	}
	bob := domain.Account{UserID: "usr_b", Username: "bob", Name: "Bob", PasswordHash: "h2", Version: 1}
	if e = db.CreateAccount(c, bob, false); e != nil {
		t.Fatal(e)
	}
	if admins, _ := db.ListPlatformAdmins(c); len(admins) != 1 || admins[0] != "usr_a" {
		t.Fatal("platform admins", admins)
	}
	if users, _ := db.ListUsers(c); len(users) != 2 {
		t.Fatal("account users not recorded", users)
	}
	for _, s := range []domain.WebSession{{Hash: "s-bob", User: domain.User{ID: "usr_b"}, ExpiresAt: domain.Now() + 60000}, {Hash: "s-alice", User: domain.User{ID: "usr_a"}, ExpiresAt: domain.Now() + 60000}} {
		if e = db.PutWebSession(c, s); e != nil {
			t.Fatal(e)
		}
	}
	if e = db.PutMCPAccount(c, domain.MCPAccount{UserID: "usr_b", Enabled: true}); e != nil {
		t.Fatal(e)
	}
	if e = db.PutOAuthGrant(c, domain.OAuthGrant{ID: "grant", UserID: "usr_b", AccessHash: "a", RefreshHash: "r", AccountGeneration: 1}, ""); e != nil {
		t.Fatal(e)
	}
	renamed := bob
	renamed.Name, renamed.Version = "Bob Lee", 2
	if e = db.UpdateAccount(c, renamed, 1); e != nil {
		t.Fatal(e)
	}
	if _, e = db.GetWebSession(c, "s-bob"); e != nil {
		t.Fatal("rename revoked sessions")
	}
	stale := renamed
	stale.Disabled, stale.Version = true, 3
	if e = db.UpdateAccount(c, stale, 1); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("stale update accepted", e)
	}
	if e = db.UpdateAccount(c, stale, 2); e != nil {
		t.Fatal(e)
	}
	if _, e = db.GetWebSession(c, "s-bob"); e == nil {
		t.Fatal("disabled account kept its session")
	}
	if _, e = db.GetWebSession(c, "s-alice"); e != nil {
		t.Fatal("other sessions revoked")
	}
	if _, e = db.FindOAuthGrant(c, "a", false); e == nil {
		t.Fatal("disabled account kept MCP grant")
	}
	got, e := db.FindAccount(c, "bob")
	if e != nil || !got.Disabled || got.Name != "Bob Lee" || got.Version != 3 || got.PasswordHash != "h2" {
		t.Fatal(got, e)
	}
	if list, _ := db.ListAccounts(c); len(list) != 2 || list[0].Username != "alice" {
		t.Fatal(list)
	}
	// Choosing one's own password keeps MCP clients; an administrator's reset does not.
	if e = db.PutMCPAccount(c, domain.MCPAccount{UserID: "usr_a", Enabled: true}); e != nil {
		t.Fatal(e)
	}
	grant := domain.OAuthGrant{ID: "alice-grant", UserID: "usr_a", AccessHash: "aa", RefreshHash: "ar", AccountGeneration: 1}
	if e = db.PutOAuthGrant(c, grant, ""); e != nil {
		t.Fatal(e)
	}
	alice := first
	alice.PasswordHash, alice.Version = "h1-own", 2
	if e = db.UpdateAccount(c, alice, 1); e != nil {
		t.Fatal(e)
	}
	if _, e = db.FindOAuthGrant(c, "aa", false); e != nil {
		t.Fatal("own password change revoked MCP grant")
	}
	alice.PasswordHash, alice.MustChangePassword, alice.Version = "h1-reset", true, 3
	if e = db.UpdateAccount(c, alice, 2); e != nil {
		t.Fatal(e)
	}
	if _, e = db.FindOAuthGrant(c, "aa", false); e == nil {
		t.Fatal("reset kept MCP grant")
	}
}
