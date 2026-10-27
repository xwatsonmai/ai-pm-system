package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestReleaseMigrationPreservesLegacySubmissionsAndRunsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"review", "done", "doing"} {
		task := domain.Task{ID: state, ProjectID: "p", Status: state, Version: 7, Revision: 3, Description: "用户原文", Claimant: "dev", Submission: &domain.Submission{Revision: 3, SHA: "v3", Summary: "开发完成", Tests: "本地通过"}}
		payload, _ := json.Marshal(task)
		if _, err := db.db.Exec(`INSERT INTO tasks(id,project_id,version,claim_hash,payload) VALUES(?,?,?,'claim',?)`, task.ID, task.ProjectID, task.Version, string(payload)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.db.Exec(`DELETE FROM schema_migrations WHERE version=9`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for i := 0; i < 2; i++ {
		db, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		current, err := db.GetTask(context.Background(), "review")
		if err != nil || current.Status != "pending_release" || current.Version != 8 || current.Revision != 3 || current.Description != "用户原文" || current.Submission.SHA != "v3" {
			t.Fatal(current, err)
		}
		for _, state := range []string{"done", "doing"} {
			task, _ := db.GetTask(context.Background(), state)
			if task.Status != state || task.Version != 7 {
				t.Fatal(task)
			}
		}
		events, _ := db.Events(context.Background(), "p", "review", 0)
		if len(events) != 1 || events[0].Kind != "release_confirmation_required" {
			t.Fatal(events)
		}
		db.Close()
	}
}
