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
	"strings"
	"testing"
	"time"
)

func TestSnapshotCompletePinnedTreeAndBoundaries(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.MkdirAll(filepath.Join(source, "server/services/photo-compositor"), 0755); err != nil {
		t.Fatal(err)
	}
	git(t, source, "init", "-b", "main")
	write := func(p, v string) {
		t.Helper()
		if e := os.MkdirAll(filepath.Dir(filepath.Join(source, p)), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(source, p), []byte(v), 0644); e != nil {
			t.Fatal(e)
		}
	}
	write("server/services/photo-compositor/service.py", strings.Repeat("# padding\n", 4000)+"canvas.alpha_composite(background)\ncanvas.alpha_composite(photo)\ncanvas.alpha_composite(frame)\n")
	write(".gitmodules", "[submodule \"mobile\"]\n path = mobile\n url = example.invalid/mobile.git\n")
	write(".gitattributes", "must-include.txt export-ignore\n")
	write("must-include.txt", "tracked even when archive would omit it")
	write("asset.png", "version https://git-lfs.github.com/spec/v1\noid sha256:000000\nsize 10\n")
	for i := 0; i < 900; i++ {
		write(fmt.Sprintf("a-prefix/file-%04d-with-long-name.txt", i), "padding")
	}
	if e := os.Symlink("server/services/photo-compositor/service.py", filepath.Join(source, "internal-link")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("../../outside", filepath.Join(source, "external-link")); e != nil {
		t.Fatal(e)
	}
	write("d/keep.txt", "symlink fixture")
	if e := os.Symlink("..", filepath.Join(source, "d/up")); e != nil {
		t.Fatal(e)
	}
	// Cleaning the text yields d/manifest.json, but following d/up first escapes.
	if e := os.Symlink("d/up/../manifest.json", filepath.Join(source, "escape-chain")); e != nil {
		t.Fatal(e)
	}
	git(t, source, "add", ".")
	git(t, source, "commit", "-m", "fixture")
	parent := git(t, source, "rev-parse", "HEAD")
	git(t, source, "update-index", "--add", "--cacheinfo", "160000,"+parent+",mobile")
	git(t, source, "commit", "-m", "real submodule")
	sha := git(t, source, "rev-parse", "HEAD")
	db, e := sqlite.Open(filepath.Join(dir, "data", "pm.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a := &app.Service{Store: db}
	p, e := a.SaveProject(ctx, domain.Project{Name: "snapshot", Repository: source, Branch: "main"})
	if e != nil {
		t.Fatal(e)
	}
	r := &gitrepo.Repositories{Root: filepath.Join(dir, "data", "repos"), App: a}
	if e = r.Sync(ctx, p.ID); e != nil {
		t.Fatal(e)
	}
	tree, e := r.Read(ctx, p.ID, gitrepo.Query{Action: "tree", Path: "server/services/photo-compositor"})
	if e != nil || tree["text"] != "server/services/photo-compositor/service.py\n" || tree["truncated"] != false {
		t.Fatalf("legacy directory query ignored its path: %+v %v", tree, e)
	}
	found, e := r.Read(ctx, p.ID, gitrepo.Query{Action: "search", Path: "a-prefix/file-0000-with-long-name.txt", Query: "padding"})
	if e != nil || strings.Count(strings.TrimSpace(fmt.Sprint(found["text"])), "\n") != 0 || !strings.Contains(fmt.Sprint(found["text"]), "a-prefix/file-0000-with-long-name.txt") {
		t.Fatalf("legacy search ignored its path: %+v %v", found, e)
	}
	if _, e = r.Read(ctx, p.ID, gitrepo.Query{Action: "file", Path: "services/photo-compositor/.gitmodules"}); e == nil || !strings.Contains(e.Error(), "PATH_NOT_FOUND") {
		t.Fatalf("missing path did not explain the cause: %v", e)
	}
	s, release, e := r.AcquireSnapshot(ctx, p.ID, sha)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	if s.SHA != sha || s.Files < 900 || len(s.Submodules) != 1 || s.Submodules[0].Path != "mobile" || len(s.Warnings) != 3 {
		t.Fatalf("bad manifest: %+v", s)
	}
	root := filepath.Join(dir, "data", "snapshots", p.ID, sha, "source")
	for _, f := range []string{"server/services/photo-compositor/service.py", "must-include.txt", "internal-link"} {
		if b, e := os.ReadFile(filepath.Join(root, f)); e != nil || len(b) == 0 {
			t.Fatalf("missing %s: %v", f, e)
		}
	}
	if _, e = os.Lstat(filepath.Join(root, "external-link")); !os.IsNotExist(e) {
		t.Fatal("escaped symlink materialized")
	}
	if _, e = os.Lstat(filepath.Join(root, "escape-chain")); !os.IsNotExist(e) {
		t.Fatal("transitive symlink escape materialized")
	}
	if _, e = os.Stat(filepath.Join(root, ".git")); !os.IsNotExist(e) {
		t.Fatal("git metadata exposed")
	}
	write("server/services/photo-compositor/service.py", "changed")
	git(t, source, "add", ".")
	git(t, source, "commit", "-m", "changed")
	if e = r.Sync(ctx, p.ID); e != nil {
		t.Fatal(e)
	}
	newSnapshot, newRelease, e := r.AcquireSnapshot(ctx, p.ID, "")
	if e != nil {
		t.Fatal(e)
	}
	defer newRelease()
	if newSnapshot.SHA == sha {
		t.Fatal("new run did not see new SHA")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "server/services/photo-compositor/service.py")); !strings.Contains(string(b), "canvas.alpha_composite") {
		t.Fatal("running snapshot changed")
	}
	marker := filepath.Join(filepath.Dir(root), ".last-used")
	past := time.Now().Add(-8 * 24 * time.Hour)
	os.Chtimes(marker, past, past)
	if e = r.ExpireSnapshots(time.Now().Add(-7 * 24 * time.Hour)); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(root); e != nil {
		t.Fatal("deleted in-use snapshot")
	}
	release()
	if e = r.ExpireSnapshots(time.Now().Add(-7 * 24 * time.Hour)); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(root); !os.IsNotExist(e) {
		t.Fatal("expired snapshot retained")
	}
	if _, _, e = r.AcquireSnapshot(ctx, p.ID, "missing-branch"); e == nil {
		t.Fatal("missing version silently substituted")
	}
}
