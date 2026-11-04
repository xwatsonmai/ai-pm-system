package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

func TestProjectAccessMigrationRetainsGrantsAndIndependentDuties(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = raw.Exec(`CREATE TABLE documents(kind TEXT,id TEXT,project_id TEXT,payload TEXT,PRIMARY KEY(kind,id)); INSERT INTO documents VALUES('project','legacy','legacy','{"id":"legacy","name":"Legacy","owner_id":"alice","memberships":[{"user_id":"alice","roles":["admin"]},{"user_id":"bob","roles":["tester","developer"]}]}');`)
	if e != nil {
		t.Fatal(e)
	}
	raw.Close()
	db, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	c := context.Background()
	p, e := db.GetProject(c, "legacy")
	if e != nil {
		t.Fatal(e)
	}
	if p.AccessVersion != 1 || len(p.ProjectRoles) != 4 || !slices.Equal(p.Memberships[1].Responsibilities, []string{"developer", "tester"}) {
		t.Fatal("legacy migration lost membership/duties", p)
	}
	for _, role := range p.ProjectRoles {
		if !slices.Contains(role.Pages, "settings") || !slices.Contains(role.Pages, "git") {
			t.Fatal("legacy settings capability lost", role)
		}
	}
	p.Memberships[1].Responsibilities = []string{}
	if e = db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	db.Close()
	db, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	p, e = db.GetProject(c, "legacy")
	if e != nil || p.Memberships[1].Responsibilities == nil || len(p.Memberships[1].Responsibilities) != 0 {
		t.Fatal("migration reapplied role-derived duties", p, e)
	}
	// Role pages are paginated inside the SQLite adapter, including empty pages.
	items, total, e := db.ListProjectRolesPage(c, p.ID, 2, 2, "")
	if e != nil || total != 4 || len(items) != 2 || items[0].ID != "tester" {
		t.Fatal(items, total, e)
	}
	items, total, e = db.ListProjectRolesPage(c, p.ID, 100, 2, "开发")
	if e != nil || total != 1 || len(items) != 0 {
		t.Fatal(items, total, e)
	}
	next := p
	next.AccessVersion++
	if e = db.CommitProjectAccess(c, next, p.AccessVersion); e != nil {
		t.Fatal(e)
	}
	if e = db.CommitProjectAccess(c, next, p.AccessVersion); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("stale access CAS accepted", e)
	}
}
func TestExplicitEmptyResponsibilitiesSurviveJSON(t *testing.T) {
	m := domain.ProjectMember{UserID: "u", Roles: []string{"product"}, Responsibilities: []string{}}
	raw, _ := json.Marshal(m)
	var out domain.ProjectMember
	if e := json.Unmarshal(raw, &out); e != nil {
		t.Fatal(e)
	}
	if out.Responsibilities == nil || out.ResponsibleFor("product") {
		t.Fatal(string(raw), out)
	}
}
