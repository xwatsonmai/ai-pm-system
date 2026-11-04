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
	"strings"
	"testing"
	"time"
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
				p.Memberships = []domain.ProjectMember{{UserID: "local-owner", Roles: []string{"admin"}}, {UserID: "employee", Roles: []string{"developer"}}, {UserID: "colleague", Roles: []string{"developer"}}}
				if err = db.PutProject(ctx, *p); err != nil {
					t.Fatal(err)
				}
			}
			file, err := svc.SaveAttachment(ctx, app.Human(), project.ID, "说明.md", []byte("original bytes\n"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = svc.CreateTask(ctx, app.Human(), project.ID, app.TaskInput{Title: "Desktop attachment", AttachmentIDs: []string{file.ID}, RequestID: "bind-file"}); err != nil {
				t.Fatal(err)
			}
			foreign, err := svc.SaveAttachment(ctx, app.Human(), other.ID, "foreign.md", []byte("private"))
			if err != nil {
				t.Fatal(err)
			}
			if err = db.PutMCPAccount(ctx, domain.MCPAccount{UserID: "employee", Enabled: true}); err != nil {
				t.Fatal(err)
			}
			web := domain.Actor{ID: "employee", Kind: "human"}
			personal := domain.Actor{ID: "employee", UserID: "employee", Kind: "developer", ProjectID: project.ID}
			webFile, err := svc.SaveAttachment(ctx, web, project.ID, "web-private.txt", []byte("Web private bytes"))
			if err != nil {
				t.Fatal(err)
			}
			mcpFile, err := svc.UploadProductAttachment(ctx, personal, "mcp-private.txt", "private-upload", []byte("MCP private bytes"), app.ProductAuthorization{UserConfirmed: true, UserInstruction: "上传这份文件"})
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range []struct {
				id, kind string
				file     domain.Attachment
			}{{"web-private", "human", webFile}, {"mcp-private", "developer", mcpFile}} {
				if err = db.PutRun(ctx, domain.Run{ID: item.id, ProjectID: project.ID, ConversationID: item.id, Kind: "question", OwnerID: "employee", OwnerKind: item.kind, Attachments: []domain.Attachment{item.file}, Status: "succeeded"}); err != nil {
					t.Fatal(err)
				}
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
			cookieRecorder := httptest.NewRecorder()
			if err = manager.Issue(ctx, cookieRecorder, httptest.NewRequest("GET", public+"/", nil), auth.Identity{User: domain.User{ID: "employee", Name: "Developer"}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			webCookie := cookieRecorder.Result().Cookies()[0]
			site.Config.Handler = (&server.Server{App: svc, Auth: manager}).Handler()
			site.Start()
			defer site.Close()
			grant := domain.OAuthGrant{ID: "valid", ClientID: "desktop", UserID: "employee", UserName: "Developer", ProjectID: project.ID, Resource: public + "/mcp", AccessHash: app.Hash("valid"), RefreshHash: app.Hash("refresh"), AccessExpiresAt: domain.Now() + 60000, RefreshExpiresAt: domain.Now() + 120000, AccountGeneration: 1}
			if err = db.PutOAuthGrant(ctx, grant, ""); err != nil {
				t.Fatal(err)
			}
			if err = db.PutMCPAccount(ctx, domain.MCPAccount{UserID: "colleague", Enabled: true}); err != nil {
				t.Fatal(err)
			}
			colleague := grant
			colleague.ID, colleague.UserID, colleague.AccessHash, colleague.RefreshHash = "colleague", "colleague", app.Hash("colleague"), app.Hash("colleague-refresh")
			if err = db.PutOAuthGrant(ctx, colleague, ""); err != nil {
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
			webRequest := func(path string, status int) string {
				t.Helper()
				req, _ := http.NewRequest("GET", public+path, nil)
				req.AddCookie(webCookie)
				resp, e := site.Client().Do(req)
				if e != nil {
					t.Fatal(e)
				}
				defer resp.Body.Close()
				raw, _ := io.ReadAll(resp.Body)
				if resp.StatusCode != status {
					t.Fatalf("Web GET %s: %d want %d %s", path, resp.StatusCode, status, raw)
				}
				return string(raw)
			}
			for _, item := range []domain.Attachment{webFile, mcpFile} {
				privatePath := "/api/projects/" + project.ID + "/attachments/" + item.ID
				request("GET", privatePath, "valid", 200)
				request("GET", privatePath, "colleague", 403)
				webRequest(privatePath, 200)
			}
			webRequest("/api/runs/mcp-private", 200)
			conversations := webRequest("/api/projects/"+project.ID+"/conversations?page=1", 200)
			if !strings.Contains(conversations, "web-private") || !strings.Contains(conversations, "mcp-private") {
				t.Fatal("Web and personal MCP split one employee's private runs", conversations)
			}
			if _, err = svc.CreateRequirementFromMCP(ctx, domain.Actor{ID: "colleague", UserID: "colleague", Kind: "developer", ProjectID: project.ID}, app.TaskInput{Title: "Copy private file", AttachmentIDs: []string{webFile.ID}, RequestID: "copy-private"}, app.ProductAuthorization{UserConfirmed: true, UserInstruction: "创建需求"}); err == nil {
				t.Fatal("MCP reused another employee's private attachment ID")
			}
			// A current role change revokes both Web and existing OAuth downloads.
			project.ProjectRoles = append(project.ProjectRoles, domain.ProjectRole{ID: "tasks-only", Name: "Tasks only", Pages: []string{"tasks"}, Version: 1})
			project.Memberships[1].Roles = []string{"tasks-only"}
			project.AccessVersion++
			if err = db.PutProject(ctx, project); err != nil {
				t.Fatal(err)
			}
			for _, item := range []domain.Attachment{webFile, mcpFile} {
				privatePath := "/api/projects/" + project.ID + "/attachments/" + item.ID
				request("GET", privatePath, "valid", 403)
				webRequest(privatePath, 403)
			}
			webRequest("/api/runs/mcp-private", 403)
			request("GET", path, "valid", 200)
			project.Memberships[1].Roles = []string{"developer"}
			project.AccessVersion++
			if err = db.PutProject(ctx, project); err != nil {
				t.Fatal(err)
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
