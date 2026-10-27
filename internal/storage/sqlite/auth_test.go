package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestOAuthStateConsumedOnceAndExpires(t *testing.T) {
	db, e := Open(filepath.Join(t.TempDir(), "auth.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	a := domain.LoginAttempt{Hash: "state", BrowserHash: "browser", Verifier: "private", ExpiresAt: domain.Now() + 60000}
	if e = db.PutLoginAttempt(ctx, a); e != nil {
		t.Fatal(e)
	}
	if _, e = db.ConsumeLoginAttempt(ctx, "state", "different-browser", domain.Now()); e == nil {
		t.Fatal("wrong browser accepted")
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if got, e := db.ConsumeLoginAttempt(ctx, "state", "browser", domain.Now()); e == nil && got.Verifier == "private" {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("state consumed more than once", wins.Load())
	}
	a.Hash = "expired"
	a.ExpiresAt = domain.Now() - 1
	if e = db.PutLoginAttempt(ctx, a); e != nil {
		t.Fatal(e)
	}
	if _, e = db.ConsumeLoginAttempt(ctx, a.Hash, a.BrowserHash, domain.Now()); e == nil {
		t.Fatal("expired state accepted")
	}
}
