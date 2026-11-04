package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPageGrantsProtectDirectAPIAndLegacySettingsPUT(t *testing.T) {
	c := context.Background()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	srv := &Server{App: svc}
	h := srv.Handler()
	owner := domain.Actor{ID: "owner", Kind: "human"}
	viewer := domain.Actor{ID: "viewer", Kind: "human"}
	editor := domain.Actor{ID: "editor", Kind: "human"}
	for _, a := range []domain.Actor{owner, viewer, editor} {
		_ = db.PutUser(c, domain.User{ID: a.ID, Name: a.ID})
	}
	p, e := svc.SaveProjectAs(c, owner, domain.Project{Name: "Restricted", Repository: "https://example.com/private.git", Branch: "main", AuthMode: "none"})
	if e != nil {
		t.Fatal(e)
	}
	task, e := svc.CreateTask(c, owner, p.ID, app.TaskInput{Title: "SECRET_REQUIREMENT", RequestID: "create"})
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range []struct {
		a          domain.Actor
		name, page string
	}{{viewer, "观察者", "overview"}, {editor, "项目配置", "settings"}} {
		p, e = svc.SaveProjectRole(c, owner, p.ID, "", app.RoleInput{Name: entry.name, Pages: []string{entry.page}, Version: p.AccessVersion})
		if e != nil {
			t.Fatal(e)
		}
		p, e = svc.SetProjectMemberAccess(c, owner, p.ID, entry.a.ID, []string{p.ProjectRoles[len(p.ProjectRoles)-1].ID}, []string{}, false, p.AccessVersion)
		if e != nil {
			t.Fatal(e)
		}
	}
	call := func(a domain.Actor, method, path, body string, status int) string {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), actorKey{}, a))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s as %s: got%d want%d %s", method, path, a.ID, w.Code, status, w.Body.String())
		}
		return w.Body.String()
	}
	base := "/api/projects/" + p.ID
	body := call(viewer, "GET", base+"/context", "", 200)
	for _, secret := range []string{"SECRET_REQUIREMENT", "private.git"} {
		if strings.Contains(body, secret) {
			t.Fatal("aggregate leaked forbidden page", body)
		}
	}
	for _, path := range []string{base + "/roles", base + "/members", base + "/code", base + "/versions", base + "/milestone", base + "/tasks", "/api/tasks/" + task.Task.ID} {
		call(viewer, "GET", path, "", 403)
	}
	call(viewer, "PUT", base, `{"name":"malicious"}`, 403)
	call(editor, "PATCH", base+"/settings", `{"name":"Renamed"}`, 200)
	// The legacy combined endpoint is field-authorized and preserves missing Git fields.
	call(editor, "PUT", base, `{"description":"Changed only base settings"}`, 200)
	stored, _ := db.GetProject(c, p.ID)
	if stored.Repository != p.Repository || stored.Branch != "main" {
		t.Fatal("base settings erased Git settings", stored)
	}
	call(editor, "PUT", base, `{"branch":"hijack"}`, 403)
	call(editor, "PUT", base, `{"repository_auth":{"mode":"none"}}`, 403)
	call(editor, "PATCH", base+"/git", `{"branch":"hijack"}`, 403)
	if _, e = srv.developerCall(c, domain.Actor{ID: viewer.ID, UserID: viewer.ID, Kind: "developer", ProjectID: p.ID}, "task_list", map[string]string{}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("MCP bypassed revoked page", e)
	}
	access := call(viewer, "GET", base+"/access", "", 200)
	if !strings.Contains(access, `"pages":["overview"]`) {
		t.Fatal(access)
	}
	roles := call(owner, "GET", base+"/roles?page=2&page_size=2", "", 200)
	var page struct {
		Items []domain.ProjectRole `json:"items"`
		Total int                  `json:"total"`
	}
	if e = json.Unmarshal([]byte(roles), &page); e != nil || len(page.Items) != 2 || page.Total != 6 {
		t.Fatal(roles, e)
	}
	// A non-member platform administrator has recovery management, not business data.
	recovery := domain.Actor{ID: "recovery", Kind: "human", PlatformAdmin: true}
	call(recovery, "GET", base+"/roles", "", 200)
	call(recovery, "GET", base+"/context", "", 403)
}
