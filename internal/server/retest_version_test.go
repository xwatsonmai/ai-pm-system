package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetestCandidateIsReadOnlyVersionBoundAndPermissionScoped(t *testing.T) {
	c := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	owner := domain.Actor{ID: "owner", Kind: "human"}
	viewer := domain.Actor{ID: "viewer", Kind: "human"}
	for _, a := range []domain.Actor{owner, viewer} {
		if err = db.PutUser(c, domain.User{ID: a.ID, Name: a.ID}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := svc.SaveProjectAs(c, owner, domain.Project{Name: "Retest"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.CreateTask(c, owner, p.ID, app.TaskInput{Title: "Requirement", Description: "Keep history", RequestID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	task := created.Task
	task.Status = "retest"
	task.Branch = "feature/retest"
	task.Submission = &domain.Submission{SHA: strings.Repeat("a", 40), Environment: "test"}
	if err = db.CommitTask(c, task, task.Version, domain.Event{ProjectID: p.ID, TaskID: task.ID, Kind: "fixture", At: domain.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	task, _ = db.GetTask(c, task.ID)
	p, err = svc.SaveProjectRole(c, owner, p.ID, "", app.RoleInput{Name: "Tasks only", Pages: []string{"tasks"}, Actions: []string{}, Version: p.AccessVersion})
	if err != nil {
		t.Fatal(err)
	}
	role := p.ProjectRoles[len(p.ProjectRoles)-1]
	p, err = svc.SetProjectMemberAccess(c, owner, p.ID, viewer.ID, []string{role.ID}, []string{}, false, p.AccessVersion)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc.ResolveTestingCode = func(_ context.Context, project, branch string, fixes []string) (string, error) {
		calls++
		if project != p.ID || branch != task.Branch || len(fixes) != 0 {
			t.Fatal("wrong candidate context", project, branch, fixes)
		}
		return strings.Repeat("b", 40), nil
	}
	h := (&Server{App: svc}).Handler()
	call := func(a domain.Actor, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "http://localhost/api/tasks/"+task.ID+"/retest-version", nil)
		r = r.WithContext(context.WithValue(c, actorKey{}, a))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("got %d want %d: %s", w.Code, want, w.Body.String())
		}
		return w
	}
	call(viewer, 403)
	call(domain.Actor{ID: "outsider", Kind: "human"}, 403)
	if calls != 0 {
		t.Fatal("unauthorized Git read")
	}
	var candidate app.RetestVersion
	if err = json.Unmarshal(call(owner, 200).Body.Bytes(), &candidate); err != nil {
		t.Fatal(err)
	}
	if candidate.SHA != strings.Repeat("b", 40) || candidate.Version != task.Version || candidate.Revision != task.Revision {
		t.Fatal(candidate)
	}
	after, err := db.GetTask(c, task.ID)
	if err != nil || after.Submission.SHA != task.Submission.SHA || after.Status != "retest" || after.Version != task.Version {
		t.Fatal("candidate changed evidence", after, err)
	}
	// A stale/opened form cannot ask for candidates after a new Bug blocks retest.
	after.Status = "fixing"
	if err = db.CommitTask(c, after, after.Version, domain.Event{ProjectID: p.ID, TaskID: task.ID, Kind: "fixture", At: domain.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	call(owner, 400)
	if calls != 1 {
		t.Fatal("invalid stage reached Git", calls)
	}
}
