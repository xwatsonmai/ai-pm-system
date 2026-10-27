package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestMilestoneMigrationPreservesPlansAndBackfillsOnce(t *testing.T) {
	c := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	first := domain.ProjectVersion{ID: "old", ProjectID: "p", Name: "旧计划", Status: "active", Version: 1, Scope: []domain.VersionScopeItem{}, History: []domain.VersionHistoryEntry{}, Releases: []domain.VersionRelease{{ID: "release", Body: "原上线证据", Tasks: []domain.Task{{ID: "published", Description: "原发布快照"}}}}}
	next := domain.ProjectVersion{ID: "new", ProjectID: "p", Name: "新计划", Status: "active", Version: 1, Scope: []domain.VersionScopeItem{}, History: []domain.VersionHistoryEntry{}, Releases: []domain.VersionRelease{}}
	if e = db.CommitVersionPlan(c, []domain.VersionWrite{{Value: first}, {Value: next}}, nil, nil, nil); e != nil {
		t.Fatal(e)
	}
	for _, state := range []string{"doing", "done"} {
		task := domain.Task{ID: state, ProjectID: "p", Title: state, Description: "完整正文", Acceptance: []string{"原条件"}, Status: state, Revision: 4, Version: 8, Claimant: "developer"}
		b, _ := json.Marshal(task)
		if _, e = db.db.Exec("INSERT INTO tasks(id,project_id,version,claim_hash,payload) VALUES(?,?,?,'keep-claim',?)", task.ID, task.ProjectID, task.Version, string(b)); e != nil {
			t.Fatal(e)
		}
	}
	db.db.Exec("DELETE FROM schema_migrations WHERE version=11")
	db.Close()
	for n := 0; n < 2; n++ {
		db, e = Open(path)
		if e != nil {
			t.Fatal(e)
		}
		state, e := db.GetCurrentMilestone(c, "p")
		if e != nil || state.VersionID != "new" {
			t.Fatal(state, e)
		}
		versions, _ := db.ListProjectVersions(c, "p")
		active := 0
		for _, v := range versions {
			if v.Status == "active" {
				active++
			}
			if v.ID == "old" && (v.Status != "planned" || v.Releases[0].Tasks[0].Description != "原发布快照") {
				t.Fatal("old plan lost", v)
			}
		}
		if active != 1 {
			t.Fatal("parallel active after upgrade")
		}
		work, _ := db.GetTask(c, "doing")
		finished, _ := db.GetTask(c, "done")
		if work.ProjectVersionID != "new" || finished.ProjectVersionID != "new" || work.Revision != 4 || work.Description != "完整正文" || work.Version != 9 || work.ClaimHash != "keep-claim" {
			t.Fatal("content or claim lost", work, finished)
		}
		events, _ := db.Events(c, "p", "doing", 0)
		if len(events) != 1 {
			t.Fatal("migration repeated", events)
		}
		db.Close()
	}
}

func TestMilestoneMigrationUsesOneDefaultVersionForAllExistingRequirements(t *testing.T) {
	for _, states := range [][]string{{"clarify", "review", "done", "closed", "rejected", "canceled"}, {"done", "closed"}} {
		t.Run(states[0], func(t *testing.T) {
			c := context.Background()
			path := filepath.Join(t.TempDir(), "legacy.db")
			db, e := Open(path)
			if e != nil {
				t.Fatal(e)
			}
			for _, status := range states {
				task := domain.Task{ID: status, ProjectID: "p", Title: status, Status: status, Description: "原图文内容", Acceptance: []string{"原验收条件"}, Revision: 3, Version: 8}
				b, _ := json.Marshal(task)
				if _, e = db.db.Exec("INSERT INTO tasks(id,project_id,version,claim_hash,payload) VALUES(?,?,?,'preserved',?)", task.ID, task.ProjectID, task.Version, string(b)); e != nil {
					t.Fatal(e)
				}
			}
			db.db.Exec("DELETE FROM schema_migrations WHERE version=11")
			db.Close()
			for n := 0; n < 2; n++ {
				db, e = Open(path)
				if e != nil {
					t.Fatal(e)
				}
				versions, e := db.ListProjectVersions(c, "p")
				if e != nil || len(versions) != 1 || versions[0].Name != "默认版本" || versions[0].Status != "active" || len(versions[0].Scope) != len(states) {
					t.Fatal("existing requirements split into multiple versions", versions, e)
				}
				current, e := db.GetCurrentMilestone(c, "p")
				if e != nil || current.VersionID != versions[0].ID {
					t.Fatal(current, e)
				}
				for _, status := range states {
					task, e := db.GetTask(c, status)
					if e != nil || task.ProjectVersionID != current.VersionID || task.Status != status || task.Revision != 3 || task.Version != 9 || task.Description != "原图文内容" || task.ClaimHash != "preserved" {
						t.Fatal("original requirement changed or migration repeated", task, e)
					}
				}
				db.Close()
			}
		})
	}
}
