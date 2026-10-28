package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlatformAndProjectRoleBoundaries(t *testing.T) {
	c := context.Background()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	api := &Server{App: svc}
	h := api.Handler()
	owner := domain.Actor{ID: "alice", Name: "Alice", Kind: "human"}
	admin := domain.Actor{ID: "platform", Name: "Platform", Kind: "human", PlatformAdmin: true}
	developer := domain.Actor{ID: "bob", Name: "Bob", Kind: "human"}
	product := domain.Actor{ID: "carol", Name: "Carol", Kind: "human"}
	outsider := domain.Actor{ID: "eve", Name: "Eve", Kind: "human"}
	for _, a := range []domain.Actor{owner, admin, developer, product, outsider} {
		if e = db.PutUser(c, domain.User{ID: a.ID, Name: a.Name}); e != nil {
			t.Fatal(e)
		}
	}
	p, e := svc.SaveProjectAs(c, owner, domain.Project{Name: "Private"})
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range []struct {
		a    domain.Actor
		role string
	}{{developer, "developer"}, {product, "product"}} {
		if _, e = svc.SetProjectMember(c, owner, p.ID, entry.a.ID, []string{entry.role}, false); e != nil {
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
			t.Fatalf("%s %s as %s: %d want %d: %s", method, path, a.ID, w.Code, status, w.Body.String())
		}
		return w.Body.String()
	}
	for _, a := range []domain.Actor{developer, product, owner} {
		call(a, "GET", "/api/projects/"+p.ID+"/context", "", 200)
		call(a, "PUT", "/api/projects/"+p.ID, `{"name":"Private","description":"all roles edit settings"}`, 200)
		call(a, "POST", "/api/projects/"+p.ID+"/tasks", `{"title":"Role task","description":"shared capability","acceptance":["works"],"request_id":"`+a.ID+`"}`, 200)
		for _, route := range []string{"/api/platform/settings", "/api/platform/projects", "/api/platform/admins", "/api/platform/directory", "/api/profiles"} {
			call(a, "GET", route, "", 403)
		}
	}
	for _, a := range []domain.Actor{developer, product} {
		call(a, "PUT", "/api/projects/"+p.ID+"/members/eve", `{"role":"admin"}`, 403)
		call(a, "GET", "/api/projects/"+p.ID+"/directory", "", 403)
	}
	call(outsider, "GET", "/api/projects/"+p.ID+"/context", "", 403)
	// Platform administrators manage assignment without gaining business access until assigned.
	call(admin, "GET", "/api/platform/projects", "", 200)
	call(admin, "GET", "/api/projects/"+p.ID+"/context", "", 403)
	call(admin, "PUT", "/api/projects/"+p.ID+"/members/eve", `{"role":"product"}`, 200)
	call(outsider, "GET", "/api/projects/"+p.ID+"/context", "", 200)
	// A member can hold several roles; unknown roles or an empty list are rejected.
	call(admin, "PUT", "/api/projects/"+p.ID+"/members/eve", `{"roles":["tester","product","tester"]}`, 200)
	for _, body := range []string{`{"roles":[]}`, `{"roles":["owner","product"]}`} {
		call(admin, "PUT", "/api/projects/"+p.ID+"/members/eve", body, 400)
	}
	if members := call(admin, "GET", "/api/projects/"+p.ID+"/members", "", 200); !strings.Contains(members, `"roles":["product","tester"]`) {
		t.Fatal("roles not kept in display order", members)
	}
	call(owner, "PUT", "/api/projects/"+p.ID+"/members/bob", `{"role":"admin"}`, 200)
	call(developer, "DELETE", "/api/projects/"+p.ID+"/members/alice", "", 200)
	call(owner, "GET", "/api/projects/"+p.ID+"/context", "", 403)
	call(developer, "DELETE", "/api/projects/"+p.ID+"/members/bob", "", 400)
	// A project settings payload cannot bypass the membership manager or restore the creator.
	call(product, "PUT", "/api/projects/"+p.ID, `{"name":"Private","access_members":["alice"]}`, 400)
	call(product, "PUT", "/api/projects/"+p.ID, `{"name":"Private","memberships":[{"user_id":"carol","role":"admin"}]}`, 400)
	call(admin, "POST", "/api/profiles", `{"name":"Global","provider":"fixture","model":"one","base_url":"http://127.0.0.1:1","key":"DO_NOT_ECHO"}`, 200)
	profiles, e := db.ListProfiles(c)
	if e != nil || len(profiles) != 1 {
		t.Fatal(e)
	}
	if body := call(admin, "PUT", "/api/platform/settings", `{"profile_id":"`+profiles[0].ID+`"}`, 200); strings.Contains(body, "DO_NOT_ECHO") {
		t.Fatal("key leaked")
	}
	status := call(product, "GET", "/api/agent/status", "", 200)
	if !strings.Contains(status, `"configured":true`) || strings.Contains(status, "127.0.0.1") || strings.Contains(status, "DO_NOT_ECHO") {
		t.Fatal("global status boundary", status)
	}
	call(admin, "PUT", "/api/platform/admins/bob", `{"enabled":true}`, 200)
	if e = db.SetPlatformAdmin(c, "bob", false); e == nil {
		t.Fatal("last admin removed")
	}
}
