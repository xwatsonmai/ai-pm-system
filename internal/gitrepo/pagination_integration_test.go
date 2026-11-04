package gitrepo_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/gitrepo"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryPagesReachBeyondLegacyCaps(t *testing.T) {
	c := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	git(t, source, "init", "-b", "main")
	for i := 0; i < 625; i++ {
		if e := os.WriteFile(filepath.Join(source, fmt.Sprintf("file-%04d.txt", i)), []byte("PAGE_NEEDLE\n"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	git(t, source, "add", ".")
	git(t, source, "commit", "-m", "files")
	for i := 0; i < 24; i++ {
		git(t, source, "commit", "--allow-empty", "-m", fmt.Sprintf("change %02d", i))
	}
	db, e := sqlite.Open(filepath.Join(root, "pm.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	project, e := svc.SaveProject(c, domain.Project{Name: "Paged repository", Repository: source, Branch: "main"})
	if e != nil {
		t.Fatal(e)
	}
	repos := &gitrepo.Repositories{Root: filepath.Join(root, "repos"), App: svc}
	if e = repos.Sync(c, project.ID); e != nil {
		t.Fatal(e)
	}
	for _, q := range []gitrepo.Query{{Action: "tree", Page: 32, PageSize: 20}, {Action: "search", Query: "PAGE_NEEDLE", Page: 32, PageSize: 20}} {
		v, e := repos.Read(c, project.ID, q)
		if e != nil || v["total"] != 625 || len(v["items"].([]string)) != 5 || v["truncated"] != false {
			t.Fatal(q, v, e)
		}
	}
	v, e := repos.Read(c, project.ID, gitrepo.Query{Action: "log", Page: 2, PageSize: 20})
	if e != nil || v["total"] != 25 || len(v["items"].([]string)) != 5 {
		t.Fatal(v, e)
	}
	// An explicit SHA keeps the second page independent from a later branch sync.
	sha := v["sha"].(string)
	git(t, source, "commit", "--allow-empty", "-m", "later")
	if e = repos.Sync(c, project.ID); e != nil {
		t.Fatal(e)
	}
	v, e = repos.Read(c, project.ID, gitrepo.Query{Action: "log", Ref: sha, Page: 2, PageSize: 20})
	if e != nil || v["total"] != 25 {
		t.Fatal("paging SHA drifted", v, e)
	}
}
