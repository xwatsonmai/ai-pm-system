package gitrepo_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/gitrepo"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatal(string(b), e)
	}
	return strings.TrimSpace(string(b))
}
func TestRealRepositorySyncAndVersionReads(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	git(t, source, "init", "-b", "main")
	os.WriteFile(filepath.Join(source, "README.md"), []byte("# Order service\nrefund limit: paid amount\n"), 0600)
	git(t, source, "add", "README.md")
	git(t, source, "commit", "-m", "initial")
	first := git(t, source, "rev-parse", "HEAD")
	db, e := sqlite.Open(filepath.Join(dir, "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := &app.Service{Store: db}
	p, e := s.SaveProject(context.Background(), domain.Project{Name: "orders", Repository: source, Branch: "main"})
	if e != nil {
		t.Fatal(e)
	}
	r := &gitrepo.Repositories{Root: filepath.Join(dir, "repos"), App: s}
	if e = r.Sync(context.Background(), p.ID); e != nil {
		t.Fatal(e)
	}
	read, e := r.Read(context.Background(), p.ID, gitrepo.Query{Action: "file", Path: "README.md"})
	if e != nil || read["sha"] != first || !strings.Contains(read["text"].(string), "refund") {
		t.Fatal(read, e)
	}
	os.WriteFile(filepath.Join(source, "README.md"), []byte("# Order service\nrefund limit: paid amount\npartial refund enabled\n"), 0600)
	git(t, source, "add", "README.md")
	git(t, source, "commit", "-m", "support partial refunds")
	if e = r.Sync(context.Background(), p.ID); e != nil {
		t.Fatal(e)
	}
	diff, e := r.Read(context.Background(), p.ID, gitrepo.Query{Action: "diff", Base: first})
	if e != nil || !strings.Contains(diff["text"].(string), "+partial refund enabled") {
		t.Fatal(diff, e)
	}
	_, e = r.Read(context.Background(), p.ID, gitrepo.Query{Action: "file", Ref: "--help", Path: "README.md"})
	if e == nil {
		t.Fatal("invalid ref accepted")
	}
	if _, e = r.Read(context.Background(), p.ID, gitrepo.Query{Action: "file", Path: "../../etc/passwd"}); e == nil {
		t.Fatal("escaped git tree")
	}
	// Changes on an explicitly linked feature branch must trigger progress
	// analysis even while the default branch stays at the same commit.
	git(t, source, "checkout", "-b", "feature/refund")
	os.WriteFile(filepath.Join(source, "notes.md"), []byte("Feature branch evidence\n"), 0600)
	git(t, source, "add", "notes.md")
	git(t, source, "commit", "-m", "feature work")
	c := context.Background()
	m, e := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "Feature", Description: "Observe branch", Acceptance: []string{"Observe changes"}, RequestID: "create-feature"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Act(c, app.Human(), m.Task.ID, app.Action{Action: "ready", RequestID: "ready-feature"}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Act(c, app.Human(), m.Task.ID, app.Action{Action: "claim", RequestID: "claim-feature", Branch: "feature/refund"}); e != nil {
		t.Fatal(e)
	}
	changes := 0
	s.OnChange = func(_, _, _ string) { changes++ }
	if e = r.Sync(c, p.ID); e != nil {
		t.Fatal(e)
	}
	if changes != 1 {
		t.Fatal("feature branch did not trigger analysis")
	}
	if e = r.Sync(c, p.ID); e != nil {
		t.Fatal(e)
	}
	if changes != 1 {
		t.Fatal("unchanged repository triggered another analysis")
	}
}
