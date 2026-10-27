package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/credential"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/gitrepo"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectMembershipAndCredentialBoundaries(t *testing.T) {
	c := context.Background()
	dir := t.TempDir()
	db, e := sqlite.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	vault, e := credential.Open(filepath.Join(dir, "key"), db)
	if e != nil {
		t.Fatal(e)
	}
	api := &Server{App: svc, Git: &gitrepo.Repositories{Root: filepath.Join(dir, "repos"), App: svc, Vault: vault}}
	owner := domain.Actor{ID: "alice", Name: "负责人", Kind: "human"}
	member := domain.Actor{ID: "bob", Name: "成员", Kind: "human"}
	outsider := domain.Actor{ID: "eve", Name: "外部", Kind: "human"}
	for _, a := range []domain.Actor{owner, member, outsider} {
		db.PutUser(c, domain.User{ID: a.ID, Name: a.Name})
	}
	handler := api.Handler()
	call := func(a domain.Actor, method, path, body string, status int) string {
		t.Helper()
		req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), actorKey{}, a))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s %s actor %s: %d want %d %s", method, path, a.ID, rec.Code, status, rec.Body.String())
		}
		return rec.Body.String()
	}
	raw := call(owner, "POST", "/api/projects", `{"name":"Private","repository":"https://git.example.test/team/private.git","branch":"main","repository_auth":{"mode":"token","username":"alice","token":"SECRET_SENTINEL"}}`, 200)
	if strings.Contains(raw, "SECRET_SENTINEL") {
		t.Fatal("secret echoed")
	}
	var p domain.Project
	json.Unmarshal([]byte(raw), &p)
	if p.OwnerID != "alice" || p.CredentialID == "" {
		t.Fatal("owner/credential not bound")
	}
	p, e = svc.SetProjectMember(c, owner, p.ID, "bob", "developer", false)
	if e != nil {
		t.Fatal(e)
	}
	task, e := svc.CreateTask(c, owner, p.ID, app.TaskInput{Title: "private task", Description: "secret", Acceptance: []string{"ok"}, RequestID: "private-create"})
	if e != nil {
		t.Fatal(e)
	}
	db.PutRun(c, domain.Run{ID: "run-private", ProjectID: p.ID, Output: "private answer"})
	attachment, e := svc.SaveAttachment(c, owner, p.ID, "note.txt", []byte("private attachment"))
	if e != nil {
		t.Fatal(e)
	}
	for _, route := range []struct{ method, path, body string }{
		{"GET", "/api/projects/" + p.ID + "/context", ""}, {"GET", "/api/projects/" + p.ID + "/tasks", ""}, {"GET", "/api/projects/" + p.ID + "/code?action=tree", ""}, {"POST", "/api/projects/" + p.ID + "/sync", "{}"}, {"GET", "/api/projects/" + p.ID + "/runs", ""}, {"POST", "/api/projects/" + p.ID + "/runs", `{"kind":"question","prompt":"leak"}`}, {"GET", "/api/runs/run-private", ""}, {"POST", "/api/runs/run-private/stop", "{}"}, {"GET", "/api/tasks/" + task.Task.ID, ""}, {"PUT", "/api/tasks/" + task.Task.ID, "{}"}, {"POST", "/api/tasks/" + task.Task.ID + "/actions", "{}"}, {"GET", "/api/projects/" + p.ID + "/attachments/" + attachment.ID, ""}, {"POST", "/api/projects/" + p.ID + "/attachments", ""}, {"POST", "/api/projects/" + p.ID + "/artifacts", "{}"},
	} {
		call(outsider, route.method, route.path, route.body, 403)
	}
	if strings.Contains(call(outsider, "GET", "/api/projects", "", 200), p.ID) {
		t.Fatal("project list leaked")
	}
	call(member, "GET", "/api/projects/"+p.ID+"/context", "", 200)
	call(member, "GET", "/api/projects/"+p.ID+"/attachments/"+attachment.ID, "", 200)
	// All project roles can edit project settings and inspect repository metadata.
	call(member, "GET", "/api/projects/"+p.ID+"/repository-auth", "", 200)
	call(member, "PUT", "/api/projects/"+p.ID, `{"name":"Private","repository":"https://git.example.test/team/private.git","branch":"main","repository_auth":{"mode":"token"}}`, 200)
	for _, method := range []string{"GET", "POST"} {
		call(member, method, "/api/projects/"+p.ID+"/tokens", `{"name":"steal"}`, 403)
	}
	call(outsider, "GET", "/api/projects/"+p.ID+"/repository-auth", "", 403)
	call(outsider, "PUT", "/api/projects/"+p.ID, `{"name":"takeover"}`, 403)
	call(outsider, "POST", "/api/repository/test", `{"project_id":"`+p.ID+`","repository":"https://git.example.test/team/private.git","repository_auth":{"mode":"token"}}`, 403)
	meta := call(owner, "GET", "/api/projects/"+p.ID+"/repository-auth", "", 200)
	if strings.Contains(meta, "SECRET_SENTINEL") {
		t.Fatal("metadata secret leak")
	}
	call(outsider, "POST", "/api/projects", `{"name":"stolen","repository":"https://git.example.test/team/private.git","generated_credential_id":"`+p.CredentialID+`","repository_auth":{"mode":"token"}}`, 403)
	call(outsider, "POST", "/api/projects", `{"name":"machine","repository":"https://git.example.test/team/private.git","repository_auth":{"mode":"server"}}`, 400)
	call(outsider, "POST", "/api/repository/test", `{"repository":"/etc","repository_auth":{"mode":"none"}}`, 400)
	// An edit with blank secret retains the credential; owner cannot be forged.
	updated := call(owner, "PUT", "/api/projects/"+p.ID, `{"name":"Private","repository":"https://git.example.test/team/private.git","owner_id":"eve","branch":"main","repository_auth":{"mode":"token"}}`, 200)
	var after domain.Project
	json.Unmarshal([]byte(updated), &after)
	if after.OwnerID != "alice" || after.CredentialID != p.CredentialID {
		t.Fatal("ownership or retained credential changed")
	}
	call(owner, "DELETE", "/api/projects/"+p.ID+"/members/bob", "", 200)
	call(member, "GET", "/api/projects/"+p.ID+"/context", "", 403)
	if _, e = svc.Context(c, member, p.ID, ""); e == nil {
		t.Fatal("service context bypass")
	}
	if _, e = svc.EditTask(c, member, task.Task.ID, app.TaskInput{Title: "leak", Description: "leak", Acceptance: []string{"ok"}, RequestID: "revoked"}); e == nil {
		t.Fatal("service write bypass")
	}
	// MCP/platform contexts expose project facts but no credential reference or secret.
	result, e := svc.Context(c, domain.Actor{ID: "dev", Kind: "developer", ProjectID: p.ID}, p.ID, "")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(result)
	if strings.Contains(string(b), p.CredentialID) || strings.Contains(string(b), "SECRET_SENTINEL") {
		t.Fatal("agent credential leak")
	}
	// Unrelated users cannot alter the model endpoint used by a private project.
	platform := owner
	platform.PlatformAdmin = true
	profile := call(platform, "POST", "/api/profiles", `{"name":"model","provider":"custom","model":"test","base_url":"https://models.example.test/v1","key":"model-secret"}`, 200)
	var model domain.Profile
	json.Unmarshal([]byte(profile), &model)
	call(outsider, "POST", "/api/profiles", `{"id":"`+model.ID+`","name":"attack","provider":"custom","model":"test","base_url":"https://attacker.example.test"}`, 403)
	call(outsider, "POST", "/api/projects", `{"name":"borrow","profile_id":"`+model.ID+`"}`, 400)
}

func TestLegacyProjectAdoption(t *testing.T) {
	c := context.Background()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	p, e := svc.SaveProject(c, domain.Project{Name: "legacy"})
	if e != nil {
		t.Fatal(e)
	}
	a := domain.Actor{ID: "alice", Name: "Alice", Kind: "human"}
	b := domain.Actor{ID: "bob", Name: "Bob", Kind: "human"}
	if app.CanRead(b, p) {
		t.Fatal("unassigned legacy project is public")
	}
	a.PlatformAdmin = true
	if e = db.PutUser(c, domain.User{ID: a.ID, Name: a.Name}); e != nil {
		t.Fatal(e)
	}
	p, e = svc.SetProjectMember(c, a, p.ID, a.ID, "admin", false)
	if e != nil {
		t.Fatal(e)
	}
	p, e = svc.SaveProjectAs(c, a, p)
	if e != nil {
		t.Fatal(e)
	}
	if app.CanRead(b, p) || p.OwnerID != "alice" {
		t.Fatal("adoption did not enforce membership")
	}
	if _, e = svc.SaveProjectAs(c, b, p); e == nil {
		t.Fatal("ownership takeover")
	}
	p.OwnerID = "bob"
	if _, e = svc.SaveProject(c, p); e == nil {
		t.Fatal("concurrent ownership overwrite")
	}
}
