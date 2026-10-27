package gitrepo

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotPreparationIsProjectScopedAndCancellable(t *testing.T) {
	root := t.TempDir()
	db, err := sqlite.Open(filepath.Join(root, "pm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := &app.Service{Store: db}
	p, err := a.SaveProject(context.Background(), domain.Project{Name: "business-only"})
	if err != nil {
		t.Fatal(err)
	}
	r := &Repositories{Root: filepath.Join(root, "repos"), App: a}
	// Simulate a long preparation for another project; it must not block this one.
	gate := r.snapshotGate("prj_busy")
	gate <- struct{}{}
	defer func() { <-gate }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s, release, err := r.AcquireSnapshot(ctx, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if s.Status != "no_repository" || s.SHA != "" || s.Key != "none" {
		t.Fatalf("bad business-only snapshot: %+v", s)
	}
	if _, _, err = r.AcquireSnapshot(ctx, p.ID, "main"); err == nil {
		t.Fatal("accepted a code version for a project without a repository")
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, _, err = r.AcquireSnapshot(cancelled, "prj_busy", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting preparation ignored cancellation: %v", err)
	}
}
