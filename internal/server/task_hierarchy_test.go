package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestTaskTreeHTTPContractAndProjectBoundary(t *testing.T) {
	c := context.Background()
	file := filepath.Join(t.TempDir(), "db")
	db, e := sqlite.Open(file)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	owner := domain.Actor{ID: "owner", Kind: "human"}
	if e = db.PutUser(c, domain.User{ID: owner.ID, Name: "Owner"}); e != nil {
		t.Fatal(e)
	}
	if e = db.PutUser(c, domain.User{ID: "questioner", Name: "Questioner"}); e != nil {
		t.Fatal(e)
	}
	p, e := svc.SaveProjectAs(c, owner, domain.Project{Name: "Tree"})
	if e != nil {
		t.Fatal(e)
	}
	other, e := svc.SaveProjectAs(c, owner, domain.Project{Name: "Other"})
	if e != nil {
		t.Fatal(e)
	}
	raw, e := sql.Open("sqlite", file)
	if e != nil {
		t.Fatal(e)
	}
	defer raw.Close()
	for _, task := range []domain.Task{
		{ID: "root", ProjectID: p.ID, Kind: "requirement", Title: "Parent", Status: "done", Description: "PRIVATE_BODY"},
		{ID: "child", ProjectID: p.ID, Kind: "bug", Title: "Needle", Status: "ready", ParentRequirementID: "root"},
		{ID: "foreign", ProjectID: other.ID, Kind: "requirement", Title: "Other project secret"},
	} {
		task.Version = 1
		task.Revision = 1
		b, _ := json.Marshal(task)
		if _, e = raw.Exec("INSERT INTO tasks(id,project_id,version,claim_hash,payload) VALUES(?,?,1,'',?)", task.ID, task.ProjectID, string(b)); e != nil {
			t.Fatal(e)
		}
	}
	handler := (&Server{App: svc}).Handler()
	get := func(path string, actor domain.Actor, status int) []byte {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "http://localhost/api/projects/"+p.ID+path, nil)
		req = req.WithContext(context.WithValue(req.Context(), actorKey{}, actor))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var roots domain.TaskTreePage
	if e = json.Unmarshal(get("/tasks?view=tree&q=Needle", owner, 200), &roots); e != nil {
		t.Fatal(e)
	}
	if roots.Total != 1 || roots.MatchingTotal != 1 || len(roots.Items) != 1 || roots.Items[0].ID != "root" || roots.Items[0].MatchesFilter || roots.Items[0].Description != "" {
		t.Fatal(roots)
	}
	var children domain.TaskTreePage
	if e = json.Unmarshal(get("/tasks?view=tree&tree_parent_id=root&q=Needle&page_size=1", owner, 200), &children); e != nil {
		t.Fatal(e)
	}
	if children.Total != 1 || children.Items[0].ID != "child" || children.Items[0].Relation != "bug" || !children.Items[0].MatchesFilter {
		t.Fatal(children)
	}
	get("/tasks?view=tree&tree_parent_id=foreign", owner, 404)
	get("/tasks?view=tree&page=0", owner, 400)
	var legacy []domain.Task
	if e = json.Unmarshal(get("/tasks", owner, 200), &legacy); e != nil || len(legacy) != 2 {
		t.Fatal("legacy shape changed", e)
	}
	// Tree endpoints use exactly the same page authorization as the flat pool.
	p.ProjectRoles = append(p.ProjectRoles, domain.ProjectRole{ID: "qa", Name: "Question only", Pages: []string{"chat"}, Version: 1})
	p.Memberships = append(p.Memberships, domain.ProjectMember{UserID: "questioner", Roles: []string{"qa"}})
	p.AccessVersion++
	if e = db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	get("/tasks?view=tree", domain.Actor{ID: "questioner", Kind: "human"}, 403)
	get("/tasks?view=tree&tree_parent_id=root", domain.Actor{ID: "questioner", Kind: "human"}, 403)
}
