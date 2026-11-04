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

func TestPrivateAnalysisArtifactsAreFilteredBeforePagingAndAggregation(t *testing.T) {
	c := context.Background()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	owner := domain.Actor{ID: "owner", Kind: "human"}
	p, e := svc.SaveProjectAs(c, owner, domain.Project{Name: "Private analysis"})
	if e != nil {
		t.Fatal(e)
	}
	created, e := svc.CreateTask(c, owner, p.ID, app.TaskInput{Title: "Task", RequestID: "task"})
	if e != nil {
		t.Fatal(e)
	}
	p, _ = db.GetProject(c, p.ID)
	p.ProjectRoles = append(p.ProjectRoles, domain.ProjectRole{ID: "reviews", Name: "Reviews", Pages: []string{"reviews"}, Version: 1}, domain.ProjectRole{ID: "overview", Name: "Overview", Pages: []string{"overview"}, Version: 1})
	p.Memberships = append(p.Memberships, domain.ProjectMember{UserID: "reviewer", Roles: []string{"reviews"}}, domain.ProjectMember{UserID: "observer", Roles: []string{"overview"}})
	p.AccessVersion++
	if e = db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	system := domain.Actor{ID: "platform", Kind: "platform", ProjectID: p.ID}
	for _, r := range []domain.Run{
		{ID: "manual", Kind: "progress", OwnerID: owner.ID, OwnerKind: "human", TaskID: created.Task.ID},
		{ID: "question", Kind: "question", OwnerID: owner.ID, OwnerKind: "human"},
		{ID: "legacy-personal", Kind: "question", OwnerID: owner.ID, OwnerKind: "developer"},
		{ID: "background", Kind: "requirement_review", TaskID: created.Task.ID},
	} {
		r.ProjectID, r.ConversationID = p.ID, r.ID
		if e = db.PutRun(c, r); e != nil {
			t.Fatal(e)
		}
		body := "SECRET_" + r.ID
		if r.ID == "background" {
			body = "PUBLIC_BACKGROUND"
		}
		v := domain.Artifact{ID: "artifact-" + r.ID, ProjectID: p.ID, TaskID: r.TaskID, OriginRunID: r.ID, Kind: "analysis", Body: body}
		if r.ID == "legacy-personal" {
			// Exercise a real pre-upgrade generated ID without any origin fields.
			v.ID, v.OriginRunID = "analysis_legacy-personal_0123456789ab", ""
			e = db.PutArtifact(c, v)
		} else {
			_, e = svc.Artifact(c, system, v)
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = db.PutArtifact(c, domain.Artifact{ID: "analysis_missing-origin_0123456789ab", ProjectID: p.ID, Kind: "analysis", Body: "SECRET_ORPHAN"}); e != nil {
		t.Fatal(e)
	}
	srv := &Server{App: svc}
	h := srv.Handler()
	call := func(user, path string) string {
		t.Helper()
		r := httptest.NewRequest("GET", "http://localhost"+path, nil)
		r = r.WithContext(context.WithValue(r.Context(), actorKey{}, domain.Actor{ID: user, Kind: "human"}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	base := "/api/projects/" + p.ID
	for _, path := range []string{base + "/context", "/api/tasks/" + created.Task.ID, "/api/tasks/" + created.Task.ID + "?compact=true", base + "/artifacts?page=1&page_size=1", base + "/artifacts?page=2&page_size=1"} {
		raw := call("reviewer", path)
		if strings.Contains(raw, "SECRET_") || !strings.Contains(raw, "PUBLIC_BACKGROUND") {
			t.Fatal("review API crossed private-analysis origin", path, raw)
		}
		if strings.Contains(path, "/artifacts?") && !strings.Contains(raw, `"total":1`) {
			t.Fatal("private origins affected COUNT/LIMIT", raw)
		}
	}
	if raw := call("observer", base+"/artifacts?page=1"); strings.Contains(raw, "SECRET_") || !strings.Contains(raw, `"total":0`) {
		t.Fatal("overview saw another employee's saved question", raw)
	}
	if raw := call(owner.ID, base+"/artifacts?page=1&page_size=100"); !strings.Contains(raw, `"total":4`) || !strings.Contains(raw, "SECRET_legacy-personal") || strings.Contains(raw, "SECRET_ORPHAN") {
		t.Fatal("owner lost current/legacy personal analyses", raw)
	}
	personal := domain.Actor{ID: owner.ID, UserID: owner.ID, Kind: "developer", ProjectID: p.ID}
	if out, e := srv.developerCall(c, personal, "analysis_get", map[string]string{"run_id": "question"}); e != nil || out.(domain.Run).OwnerID != owner.ID {
		t.Fatal("personal MCP could not read same employee's Web run", e)
	}
	if _, e := srv.developerCall(c, domain.Actor{ID: "reviewer", UserID: "reviewer", Kind: "developer", ProjectID: p.ID}, "analysis_get", map[string]string{"run_id": "question"}); e == nil {
		t.Fatal("another employee's MCP read a private Web run")
	}
	if out, e := svc.RequirementDetail(c, domain.Actor{ID: "reviewer", Kind: "human"}, p.ID, app.RequirementDetailQuery{TaskID: created.Task.ID}); e != nil {
		t.Fatal(e)
	} else if raw, _ := json.Marshal(out); strings.Contains(string(raw), "SECRET_") {
		t.Fatal("Agent requirement_detail exposed a private analysis", string(raw))
	}
	if e = db.PutConversation(c, domain.Conversation{ID: "question", ProjectID: p.ID, DeletedAt: domain.Now()}); e != nil {
		t.Fatal(e)
	}
	if raw := call(owner.ID, base+"/artifacts?page=1&page_size=100"); strings.Contains(raw, "SECRET_question") || !strings.Contains(raw, `"total":3`) {
		t.Fatal("deleted private question remained in saved analyses", raw)
	}
	p, _ = db.GetProject(c, p.ID)
	if _, e = svc.SaveProjectRole(c, owner, p.ID, "admin", app.RoleInput{Name: "Access and review", Pages: []string{"access", "reviews"}, Version: p.AccessVersion}); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{base + "/context", base + "/artifacts?page=1&page_size=100"} {
		if raw := call(owner.ID, path); strings.Contains(raw, "SECRET_") || !strings.Contains(raw, "PUBLIC_BACKGROUND") {
			t.Fatal("origin owner retained saved runs after task/chat pages were revoked", raw)
		}
	}
}
