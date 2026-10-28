package server_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/auth"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/server"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopAttachmentOAuthIsReadOnlyProjectScopedAndRevocable(t *testing.T) {
	for _, base := range []string{"", "/syai/zhiqu"} {
		t.Run(base, func(t *testing.T) {
			ctx := context.Background()
			db, err := sqlite.Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			svc := &app.Service{Store: db}
			project, err := svc.SaveProject(ctx, domain.Project{Name: "desktop"})
			if err != nil {
				t.Fatal(err)
			}
			other, err := svc.SaveProject(ctx, domain.Project{Name: "other"})
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range []*domain.Project{&project, &other} {
				p.Memberships = []domain.ProjectMember{{UserID: "local-owner", Roles: []string{"admin"}}, {UserID: "employee", Roles: []string{"developer"}}}
				if err = db.PutProject(ctx, *p); err != nil {
					t.Fatal(err)
				}
			}
			file, err := svc.SaveAttachment(ctx, app.Human(), project.ID, "说明.md", []byte("original bytes\n"))
			if err != nil {
				t.Fatal(err)
			}
			foreign, err := svc.SaveAttachment(ctx, app.Human(), other.ID, "foreign.md", []byte("private"))
			if err != nil {
				t.Fatal(err)
			}
			if err = db.PutMCPAccount(ctx, domain.MCPAccount{UserID: "employee", Enabled: true}); err != nil {
				t.Fatal(err)
			}
			site := httptest.NewUnstartedServer(nil)
			public := "http://" + site.Listener.Addr().String() + base
			config := filepath.Join(t.TempDir(), "auth.json")
			if err = os.WriteFile(config, []byte(`{"provider":"local","public_url":"`+public+`"}`), 0600); err != nil {
				t.Fatal(err)
			}
			settings, err := auth.Load(config, true)
			if err != nil {
				t.Fatal(err)
			}
			manager, err := auth.Open(ctx, settings, db)
			if err != nil {
				t.Fatal(err)
			}
			site.Config.Handler = (&server.Server{App: svc, Auth: manager}).Handler()
			site.Start()
			defer site.Close()
			grant := domain.OAuthGrant{ID: "valid", ClientID: "desktop", UserID: "employee", UserName: "Developer", ProjectID: project.ID, Resource: public + "/mcp", AccessHash: app.Hash("valid"), RefreshHash: app.Hash("refresh"), AccessExpiresAt: domain.Now() + 60000, RefreshExpiresAt: domain.Now() + 120000, AccountGeneration: 1}
			if err = db.PutOAuthGrant(ctx, grant, ""); err != nil {
				t.Fatal(err)
			}
			expired := grant
			expired.ID, expired.AccessHash, expired.RefreshHash, expired.AccessExpiresAt = "expired", app.Hash("expired"), app.Hash("expired-refresh"), domain.Now()-1
			if err = db.PutOAuthGrant(ctx, expired, ""); err != nil {
				t.Fatal(err)
			}
			wrong := grant
			wrong.ID, wrong.AccessHash, wrong.RefreshHash, wrong.Resource = "wrong-resource", app.Hash("wrong-resource"), app.Hash("wrong-refresh"), "https://other.invalid/mcp"
			if err = db.PutOAuthGrant(ctx, wrong, ""); err != nil {
				t.Fatal(err)
			}
			path := "/api/projects/" + project.ID + "/attachments/" + file.ID
			request := func(method, path, token string, status int) string {
				t.Helper()
				req, _ := http.NewRequest(method, public+path, nil)
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				if resp.StatusCode != status {
					t.Fatalf("%s %s: %d, want %d: %s", method, path, resp.StatusCode, status, body)
				}
				return string(body)
			}
			if got := request("GET", path, "valid", 200); got != "original bytes\n" {
				t.Fatal(got)
			}
			request("GET", path, "", 401)
			request("GET", path, "bad", 401)
			request("GET", path, "expired", 401)
			request("GET", path, "wrong-resource", 401)
			request("GET", "/api/projects/"+project.ID+"/attachments/"+foreign.ID, "valid", 403)
			request("GET", "/api/projects/"+other.ID+"/attachments/"+foreign.ID, "valid", 403)
			request("GET", "/api/projects/"+project.ID, "valid", 401)
			request("POST", "/api/projects/"+project.ID+"/attachments", "valid", 401)
			project.Memberships = project.Memberships[:1]
			if err = db.PutProject(ctx, project); err != nil {
				t.Fatal(err)
			}
			request("GET", path, "valid", 401)
			project.Memberships = append(project.Memberships, domain.ProjectMember{UserID: "employee", Roles: []string{"developer"}})
			if err = db.PutProject(ctx, project); err != nil {
				t.Fatal(err)
			}
			if err = db.PutMCPAccount(ctx, domain.MCPAccount{UserID: "employee", Enabled: false}); err != nil {
				t.Fatal(err)
			}
			request("GET", path, "valid", 401)
		})
	}
}
