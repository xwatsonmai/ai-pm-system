package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestSchema16MaterializesOnlyLegacyActionsAndPreservesOtherData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actions.db")
	db, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	raw := `{"id":"legacy","name":"Legacy","access_version":7,"project_roles":[{"id":"tasks","name":"Tasks","pages":["tasks"],"version":3,"extension":{"kept":true}},{"id":"review-chat","name":"Review and chat","pages":["reviews","chat"],"actions":null,"version":4},{"id":"none","name":"Revoked","pages":["tasks","chat"],"actions":[],"version":5}],"memberships":[{"user_id":"alice","roles":["tasks"],"responsibilities":[]},{"user_id":"bob","roles":["review-chat"],"responsibilities":["tester"]}],"extension":{"retain":"unknown project metadata"}}`
	if _, e = db.db.Exec("INSERT INTO documents(kind,id,project_id,payload) VALUES('project','legacy','legacy',?)", raw); e != nil {
		t.Fatal(e)
	}
	if _, e = db.db.Exec("DELETE FROM schema_migrations WHERE version=16"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	db, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	c := context.Background()
	p, e := db.GetProject(c, "legacy")
	if e != nil {
		t.Fatal(e)
	}
	if p.AccessVersion != 8 || !slices.Contains(p.ProjectRoles[0].Actions, domain.ActionCreateRequirement) || !slices.Contains(p.ProjectRoles[0].Actions, domain.ActionCreateBug) {
		t.Fatal("tasks actions lost or revision not advanced", p)
	}
	if slices.Contains(p.ProjectRoles[1].Actions, domain.ActionCreateBug) || !slices.Contains(p.ProjectRoles[1].Actions, domain.ActionCreateLinkedBug) || !slices.Contains(p.ProjectRoles[1].Actions, domain.ActionAskAgent) {
		t.Fatal("reviews+chat widened independent Bug capability", p.ProjectRoles[1])
	}
	if p.ProjectRoles[2].Actions == nil || len(p.ProjectRoles[2].Actions) != 0 || p.Memberships[0].Responsibilities == nil || len(p.Memberships[0].Responsibilities) != 0 || !slices.Equal(p.Memberships[1].Responsibilities, []string{"tester"}) {
		t.Fatal("explicit actions/duties or membership changed", p)
	}
	var current string
	if e = db.db.QueryRow("SELECT payload FROM documents WHERE kind='project' AND id='legacy'").Scan(&current); e != nil {
		t.Fatal(e)
	}
	var before, after map[string]any
	if e = json.Unmarshal([]byte(raw), &before); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal([]byte(current), &after); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before["memberships"], after["memberships"]) || !reflect.DeepEqual(before["extension"], after["extension"]) || !reflect.DeepEqual(before["project_roles"].([]any)[0].(map[string]any)["extension"], after["project_roles"].([]any)[0].(map[string]any)["extension"]) {
		t.Fatal("migration rewrote unrelated JSON")
	}
	if e = db.CommitProjectAccess(c, p, 7); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("pre-migration form passed CAS", e)
	}
	db.Close()
	db, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	p, e = db.GetProject(c, "legacy")
	if e != nil || p.AccessVersion != 8 || p.ProjectRoles[2].Actions == nil || len(p.ProjectRoles[2].Actions) != 0 {
		t.Fatal("restart reapplied migration or filled explicit revoke", e)
	}
}
