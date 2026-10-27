package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPlatformMigrationIsOneTimeAndCodesAreConsumedOnce(t *testing.T) {
	c := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = raw.Exec(`CREATE TABLE documents(kind TEXT,id TEXT,project_id TEXT,payload TEXT,PRIMARY KEY(kind,id)); INSERT INTO documents VALUES('project','legacy','legacy','{"id":"legacy","name":"Legacy","profile_id":"old-model","owner_id":"alice","access_members":["bob"]}');`)
	if e != nil {
		t.Fatal(e)
	}
	raw.Close()
	db, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	settings, e := db.GetPlatformSettings(c)
	if e != nil || settings.ProfileID != "old-model" {
		t.Fatal("legacy model migration", settings, e)
	}
	if e = db.PutPlatformSettings(c, domain.PlatformSettings{}); e != nil {
		t.Fatal(e)
	}
	db.Close()
	db, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	settings, e = db.GetPlatformSettings(c)
	if e != nil || settings.ProfileID != "" {
		t.Fatal("restart resurrected old project model")
	}
	if e = db.PutMCPAccount(c, domain.MCPAccount{UserID: "alice", Enabled: true}); e != nil {
		t.Fatal(e)
	}
	code := domain.OAuthCode{AccountGeneration: 1, Hash: "hashed-code", ClientID: "client", RedirectURI: "http://localhost/callback", Challenge: "challenge", Resource: "http://localhost/mcp", UserID: "alice", ProjectID: "legacy", ExpiresAt: time.Now().Add(time.Minute).UnixMilli()}
	if e = db.PutOAuthCode(c, code); e != nil {
		t.Fatal(e)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if _, e := db.ConsumeOAuthCode(c, code); e == nil {
				successes.Add(1)
			}
		})
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("code consumed multiple times", successes.Load())
	}
}

func TestClosedAccountCannotRegainDelayedGrant(t *testing.T) {
	c := context.Background()
	db, e := Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = db.PutMCPAccount(c, domain.MCPAccount{UserID: "alice", Enabled: true}); e != nil {
		t.Fatal(e)
	}
	grant := domain.OAuthGrant{ID: "delayed", UserID: "alice", AccountGeneration: 1, AccessHash: "access", RefreshHash: "refresh"}
	if e = db.PutMCPAccount(c, domain.MCPAccount{UserID: "alice", Enabled: false}); e != nil {
		t.Fatal(e)
	}
	if e = db.PutMCPAccount(c, domain.MCPAccount{UserID: "alice", Enabled: true}); e != nil {
		t.Fatal(e)
	}
	if e = db.PutOAuthGrant(c, grant, ""); e == nil {
		t.Fatal("delayed pre-close grant revived")
	}
	account, e := db.GetMCPAccount(c, "alice")
	if e != nil || account.Generation != 3 {
		t.Fatal(account, e)
	}
	if e = db.PutMCPAccount(c, domain.MCPAccount{UserID: "alice", Enabled: true}); e != nil {
		t.Fatal(e)
	}
	account, e = db.GetMCPAccount(c, "alice")
	if e != nil || account.Generation != 3 {
		t.Fatal("idempotent enable invalidated current connections")
	}
}
