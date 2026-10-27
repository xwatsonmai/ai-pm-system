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
	"testing"
	"time"
)

func TestGitTimelinePinnedHeadsAndPagination(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	os.Mkdir(source, 0700)
	git(t, source, "init", "-b", "main")
	commit := func(title, at string) string {
		t.Helper()
		cmd := exec.Command("git", "commit", "--allow-empty", "-m", title)
		cmd.Dir = source
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Product", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Product", "GIT_COMMITTER_EMAIL=test@example.invalid", "GIT_AUTHOR_DATE="+at, "GIT_COMMITTER_DATE="+at)
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatal(string(b), e)
		}
		return git(t, source, "rev-parse", "HEAD")
	}
	first := commit("首个提交 <script>", "2026-10-18T10:00:00Z")
	second := commit("第二次修改", "2026-10-19T10:00:00Z")
	db, e := sqlite.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := &app.Service{Store: db}
	c := context.Background()
	p, e := s.SaveProject(c, domain.Project{Name: "timeline", Repository: source, Branch: "main"})
	if e != nil {
		t.Fatal(e)
	}
	r := &gitrepo.Repositories{App: s, Root: filepath.Join(dir, "repos")}
	if e = r.Sync(c, p.ID); e != nil {
		t.Fatal(e)
	}
	from, _ := time.Parse(time.RFC3339, "2026-10-17T00:00:00Z")
	until, _ := time.Parse(time.RFC3339, "2026-10-22T00:00:00Z")
	page, e := r.Timeline(c, p.ID, from.UnixMilli(), until.UnixMilli(), 0, 1, "")
	if e != nil || len(page.Commits) != 1 || page.Commits[0].SHA != second || !page.HasMore {
		t.Fatal(page, e)
	}
	third := commit("新同步提交", "2026-10-20T10:00:00Z")
	if e = r.Sync(c, p.ID); e != nil {
		t.Fatal(e)
	}
	next, e := r.Timeline(c, p.ID, from.UnixMilli(), until.UnixMilli(), page.NextOffset, 1, page.Anchor)
	if e != nil || len(next.Commits) != 1 || next.Commits[0].SHA != first || next.HasMore {
		t.Fatal("new commits shifted old pagination", next, e)
	}
	fresh, e := r.Timeline(c, p.ID, from.UnixMilli(), until.UnixMilli(), 0, 10, "")
	if e != nil || len(fresh.Commits) != 3 || fresh.Commits[0].SHA != third {
		t.Fatal(fresh, e)
	}
	if _, e = r.Timeline(c, p.ID, from.UnixMilli(), until.UnixMilli(), 0, 10, "--all"); e == nil {
		t.Fatal("option injection accepted")
	}
}
