package credential_test

import (
	"ai-pm-system/internal/credential"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretsEncryptedBoundAndPersistent(t *testing.T) {
	dir := t.TempDir()
	db, e := sqlite.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	key := filepath.Join(dir, "repository.key")
	v, e := credential.Open(key, db)
	if e != nil {
		t.Fatal(e)
	}
	id, e := v.Save(ctx, "owner", credential.Secret{Mode: "token", Token: "private-token-sentinel"})
	if e != nil {
		t.Fatal(e)
	}
	row, e := db.GetRepositoryCredential(ctx, id)
	if e != nil || strings.Contains(string(row.Ciphertext), "private-token-sentinel") {
		t.Fatal("unencrypted secret", e)
	}
	reopened, e := credential.Open(key, db)
	if e != nil {
		t.Fatal(e)
	}
	secret, owner, e := reopened.Load(ctx, id)
	if e != nil || secret.Token != "private-token-sentinel" || owner != "owner" {
		t.Fatal("round trip failed", e)
	}
	row.ID = "copied-record"
	if e = db.PutRepositoryCredential(ctx, row); e != nil {
		t.Fatal(e)
	}
	if _, _, e = v.Load(ctx, row.ID); e == nil {
		t.Fatal("ciphertext not bound to record")
	}
	info, _ := os.Stat(key)
	if info.Mode().Perm() != 0600 {
		t.Fatal("key permissions")
	}
	os.Remove(key)
	if _, e = credential.Open(key, db); e == nil {
		t.Fatal("lost key silently regenerated")
	}
}
