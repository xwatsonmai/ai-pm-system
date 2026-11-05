package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type lineageWrappedStore struct{ storage.Store }

func (s lineageWrappedStore) UnderlyingStore() storage.Store { return s.Store }

func TestLineageHasIndependentReadOnlyPageGrant(t *testing.T) {
	c := context.Background()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	owner := domain.Actor{ID: "owner", Kind: "human"}
	viewer := domain.Actor{ID: "viewer", Kind: "human"}
	for _, a := range []domain.Actor{owner, viewer} {
		_ = db.PutUser(c, domain.User{ID: a.ID, Name: a.ID})
	}
	p, e := svc.SaveProjectAs(c, owner, domain.Project{Name: "Graph"})
	if e != nil {
		t.Fatal(e)
	}
	task, e := svc.CreateTask(c, owner, p.ID, app.TaskInput{Title: "Visible metadata", Description: "PRIVATE_DOCUMENT", RequestID: "create"})
	if e != nil {
		t.Fatal(e)
	}
	p, e = svc.SaveProjectRole(c, owner, p.ID, "", app.RoleInput{Name: "Graph viewer", Pages: []string{"lineage"}, Actions: []string{}, Version: p.AccessVersion})
	if e != nil {
		t.Fatal(e)
	}
	role := p.ProjectRoles[len(p.ProjectRoles)-1]
	p, e = svc.SetProjectMemberAccess(c, owner, p.ID, viewer.ID, []string{role.ID}, []string{}, false, p.AccessVersion)
	if e != nil {
		t.Fatal(e)
	}
	svc.Store = lineageWrappedStore{Store: db}
	h := (&Server{App: svc}).Handler()
	stored, err := db.GetProject(c, p.ID)
	if err != nil || !app.CanAccessPage(viewer, stored, "lineage") || !app.CanRead(viewer, stored) {
		t.Fatalf("fixture grant: %+v %v", stored, err)
	}
	call := func(path string, want int) string {
		t.Helper()
		r := httptest.NewRequest("GET", "http://localhost"+path, nil)
		r = r.WithContext(context.WithValue(c, actorKey{}, viewer))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d want %d %s", path, w.Code, want, w.Body.String())
		}
		return w.Body.String()
	}
	base := "/api/projects/" + p.ID
	call(base+"/lineage/versions?page=1", 200)
	body := call(base+"/lineage?version_id="+task.Task.ProjectVersionID, 200)
	if !strings.Contains(body, "Visible metadata") || strings.Contains(body, "PRIVATE_DOCUMENT") {
		t.Fatal("projection", body)
	}
	call("/api/tasks/"+task.Task.ID+"?compact=true", 403)
	call(base+"/tasks?page=1", 403)
	call(base+"/versions?page=1", 403)
	call(base+"/lineage?page=0&version_id="+task.Task.ProjectVersionID, 400)
	call(base+"/lineage?version_id=missing", 404)
	p, e = svc.SaveProjectRole(c, owner, p.ID, role.ID, app.RoleInput{Name: role.Name, Pages: []string{}, Actions: []string{}, Version: p.AccessVersion})
	if e != nil {
		t.Fatal(e)
	}
	call(base+"/lineage/versions", 403)
	call(base+"/lineage?version_id="+task.Task.ProjectVersionID, 403)
}
