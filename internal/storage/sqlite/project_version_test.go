package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestVersionMigrationPreservesExistingRowsAndAtomicCAS(t *testing.T) {
	c := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	task := domain.Task{ID: "old-task", ProjectID: "p", Status: "doing", Version: 7, Revision: 3, Description: "图片和文档原文", Claimant: "dev"}
	b, _ := json.Marshal(task)
	if _, e = db.db.Exec(`INSERT INTO tasks(id,project_id,version,claim_hash,payload) VALUES(?,?,?,'keep-claim',?)`, task.ID, task.ProjectID, task.Version, string(b)); e != nil {
		t.Fatal(e)
	}
	db.db.Exec(`DROP TABLE project_versions`)
	db.db.Exec(`DELETE FROM schema_migrations WHERE version=10`)
	db.Close()
	db, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	var after string
	db.db.QueryRow(`SELECT payload FROM tasks WHERE id=?`, task.ID).Scan(&after)
	if after != string(b) {
		t.Fatal("migration rewrote legacy data")
	}
	got, _ := db.GetTask(c, task.ID)
	if got.ClaimHash != "keep-claim" {
		t.Fatal("migration lost claim")
	}
	version := domain.ProjectVersion{ID: "v", ProjectID: "p", Name: "v1", Version: 1, Status: "planned", Scope: []domain.VersionScopeItem{{Task: task}}}
	if e = db.CommitVersionPlan(c, []domain.VersionWrite{{Value: version}}, nil, nil, nil); e != nil {
		t.Fatal(e)
	}
	version.Version = 2
	task.Version = 9
	task.ProjectVersionID = "v"
	receipt := &domain.Receipt{Key: "atomic", Fingerprint: "fp", Response: []byte(`{}`)}
	e = db.CommitVersionPlan(c, []domain.VersionWrite{{Value: version, Expected: 1}}, []domain.VersionTaskWrite{{Value: task, Expected: 8}}, []domain.Event{{ProjectID: "p", TaskID: task.ID, Kind: "must-rollback"}}, receipt)
	if !errors.Is(e, domain.ErrConflict) {
		t.Fatal(e)
	}
	unchanged, _ := db.GetProjectVersion(c, "v")
	if unchanged.Version != 1 {
		t.Fatal("task CAS failure partially changed version")
	}
	if _, e = db.GetReceipt(c, "atomic"); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("failed transaction retained receipt", e)
	}
	events, _ := db.Events(c, "p", task.ID, 0)
	if len(events) != 0 {
		t.Fatal("failed transaction retained history")
	}
	db.Close()
	db, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	unchanged, _ = db.GetProjectVersion(c, "v")
	if unchanged.Scope[0].Task.Description != "图片和文档原文" {
		t.Fatal("version snapshot not persisted")
	}
}

func TestDatabaseReopenPreservesParallelVersionsAndDefault(t *testing.T) {
	c := context.Background()
	path := filepath.Join(t.TempDir(), "parallel.db")
	db, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"A", "B"} {
		v := domain.ProjectVersion{ID: id, ProjectID: "p", Name: id, Status: "active", Version: 1, Scope: []domain.VersionScopeItem{}, Releases: []domain.VersionRelease{}, History: []domain.VersionHistoryEntry{}}
		if e = db.CommitVersionPlan(c, []domain.VersionWrite{{Value: v, Expected: 0}}, nil, nil, nil); e != nil {
			t.Fatal(e)
		}
	}
	if e = db.CommitVersionPlan(c, nil, nil, nil, nil, domain.MilestoneBatch{State: &domain.CurrentMilestone{ProjectID: "p", VersionID: "A", Version: 1}}); e != nil {
		t.Fatal(e)
	}
	db.Close()
	db, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	versions, e := db.ListProjectVersions(c, "p")
	if e != nil || len(versions) != 2 {
		t.Fatal(versions, e)
	}
	for _, v := range versions {
		if v.Status != "active" || v.Version != 1 {
			t.Fatal("reopen reran old single-current migration", v)
		}
	}
	state, e := db.GetCurrentMilestone(c, "p")
	if e != nil || state.VersionID != "A" || state.Version != 1 {
		t.Fatal(state, e)
	}
}
