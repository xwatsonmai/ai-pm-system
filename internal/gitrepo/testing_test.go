package gitrepo_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/gitrepo"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTestingBaselineAndFixAncestry(t *testing.T) {
	c := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "repo")
	if e := os.MkdirAll(source, 0755); e != nil {
		t.Fatal(e)
	}
	git(t, source, "init", "-b", "main")
	git(t, source, "config", "user.name", "test")
	git(t, source, "config", "user.email", "test@example.invalid")
	write := func(body string) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(source, "behavior.txt"), []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
		git(t, source, "add", "behavior.txt")
		git(t, source, "commit", "-m", body)
	}
	write("main behavior")
	main := strings.TrimSpace(git(t, source, "rev-parse", "HEAD"))
	git(t, source, "checkout", "-b", "feature/refund")
	write("test behavior")
	submitted := strings.TrimSpace(git(t, source, "rev-parse", "HEAD"))
	write("fixed behavior")
	fixed := strings.TrimSpace(git(t, source, "rev-parse", "HEAD"))
	db, e := sqlite.Open(filepath.Join(dir, "pm.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	service := &app.Service{Store: db}
	p, e := service.SaveProject(c, domain.Project{Name: "test", Repository: source, Branch: "main"})
	if e != nil {
		t.Fatal(e)
	}
	repos := &gitrepo.Repositories{App: service, Root: filepath.Join(dir, "mirrors")}
	if e = repos.Sync(c, p.ID); e != nil {
		t.Fatal(e)
	}
	if e = repos.ValidateTestingCode(c, p.ID, "feature/refund", submitted, nil); e != nil {
		t.Fatal(e)
	}
	if e = repos.ValidateTestingCode(c, p.ID, "main", submitted, nil); e == nil {
		t.Fatal("main accepted a feature-only version")
	}
	if e = repos.ValidateTestingCode(c, p.ID, "missing", submitted, nil); e == nil {
		t.Fatal("missing branch silently fell back")
	}
	if e = repos.ValidateTestingCode(c, p.ID, "feature/refund", submitted, []string{fixed}); e == nil {
		t.Fatal("retest without verified fix")
	}
	if e = repos.ValidateTestingCode(c, p.ID, "feature/refund", fixed, []string{fixed}); e != nil {
		t.Fatal(e)
	}
	snapshot, release, e := repos.AcquireSnapshot(c, p.ID, submitted)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	if snapshot.SHA != submitted || snapshot.SHA == main || snapshot.SHA == fixed {
		t.Fatal("baseline moved", snapshot.SHA)
	}
}
