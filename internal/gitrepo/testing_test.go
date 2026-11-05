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
	if e = repos.ValidateTestingCode(c, p.ID, "feature/refund", submitted, []string{fixed}); e == nil || !strings.Contains(e.Error(), "请更新复测 SHA") || strings.Contains(e.Error(), "请先合入") {
		t.Fatal("old selection was misdiagnosed as an unmerged fix", e)
	}
	if e = repos.ValidateTestingCode(c, p.ID, "feature/refund", fixed, []string{fixed}); e != nil {
		t.Fatal(e)
	}
	if e = repos.ValidateTestingCode(c, p.ID, "main", main, []string{fixed}); e == nil || !strings.Contains(e.Error(), "请先合入") {
		t.Fatal("unmerged fix was not distinguished", e)
	}
	if sha, err := repos.TestingCandidate(c, p.ID, "feature/refund", []string{fixed}); err != nil || sha != fixed {
		t.Fatal("candidate retained old submission or default branch", sha, err)
	}
	for _, branch := range []string{"missing", "", "main"} {
		if sha, err := repos.TestingCandidate(c, p.ID, branch, []string{fixed}); err == nil || sha != "" {
			t.Fatal("unsafe candidate fallback", branch, sha, err)
		}
	}
	if sha, err := repos.TestingCandidate(c, p.ID, "feature/refund", []string{fixed, strings.Repeat("f", 40)}); err == nil || sha != "" {
		t.Fatal("candidate ignored an unreadable fix", sha, err)
	}
	// Another verified fix must also be present; checking only one is unsafe.
	write("second fix")
	second := strings.TrimSpace(git(t, source, "rev-parse", "HEAD"))
	if e = repos.Sync(c, p.ID); e != nil {
		t.Fatal(e)
	}
	if e = repos.ValidateTestingCode(c, p.ID, "feature/refund", fixed, []string{fixed, second}); e == nil {
		t.Fatal("a build missing the second fix was accepted")
	}
	if sha, err := repos.TestingCandidate(c, p.ID, "feature/refund", []string{fixed, second}); err != nil || sha != second {
		t.Fatal("candidate does not contain every fix", sha, err)
	}
	git(t, source, "checkout", "-b", "feature/unmerged", submitted)
	write("separate fix")
	unmerged := strings.TrimSpace(git(t, source, "rev-parse", "HEAD"))
	git(t, source, "checkout", "feature/refund")
	if e = repos.Sync(c, p.ID); e != nil {
		t.Fatal(e)
	}
	if e = repos.ValidateTestingCode(c, p.ID, "feature/refund", submitted, []string{fixed, unmerged}); e == nil || !strings.Contains(e.Error(), "请先合入") {
		t.Fatal("one merged fix hid another unmerged fix", e)
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
