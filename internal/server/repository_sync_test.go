package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/gitrepo"
	"ai-pm-system/internal/storage/sqlite"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectSaveAutomaticallySyncsRepository(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = source
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatal(string(out), e)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	if e := os.WriteFile(filepath.Join(source, "README.md"), []byte("automatic sync fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	git("add", ".")
	git("commit", "-m", "initial")
	sha := git("rev-parse", "HEAD")
	for _, scenario := range []string{"create with repository", "connect repository later", "sync fails without losing project"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			db, e := sqlite.Open(filepath.Join(dir, "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			svc := &app.Service{Store: db}
			repos := &gitrepo.Repositories{Root: filepath.Join(dir, "repos"), App: svc}
			handler := (&Server{App: svc, Git: repos}).Handler()
			call := func(method, path string, body any) *httptest.ResponseRecorder {
				t.Helper()
				b, e := json.Marshal(body)
				if e != nil {
					t.Fatal(e)
				}
				req := httptest.NewRequest(method, "http://localhost/api"+path, strings.NewReader(string(b)))
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				return rec
			}
			decode := func(rec *httptest.ResponseRecorder) repositoryProject {
				t.Helper()
				if rec.Code != http.StatusOK {
					t.Fatalf("%d: %s", rec.Code, rec.Body.String())
				}
				var p repositoryProject
				if e := json.Unmarshal(rec.Body.Bytes(), &p); e != nil {
					t.Fatal(e)
				}
				return p
			}
			body := map[string]any{"name": scenario, "repository": source, "branch": "main", "repository_auth": map[string]string{"mode": "server"}}
			if scenario == "sync fails without losing project" {
				body["repository"] = filepath.Join(dir, "missing")
			}
			var p repositoryProject
			if scenario == "connect repository later" {
				p = decode(call("POST", "/projects", map[string]string{"name": scenario}))
				if p.Syncing || p.SHA != "" {
					t.Fatal("project without a repository started syncing", p)
				}
				p = decode(call("PUT", "/projects/"+p.ID, body))
			} else {
				p = decode(call("POST", "/projects", body))
			}
			// Finish all background work before closing the store, even on failure.
			defer func() {
				deadline := time.Now().Add(5 * time.Second)
				for repos.Syncing(p.ID) && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
			}()
			deadline := time.Now().Add(5 * time.Second)
			for p.Syncing && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
				p = decode(call("GET", "/projects/"+p.ID+"/repository-status", nil))
			}
			if p.Syncing {
				t.Fatal("automatic sync did not finish")
			}
			if scenario == "sync fails without losing project" {
				if p.SyncError == "" || p.SHA != "" || p.SyncedAt != 0 {
					t.Fatal("failed sync lost its error or claimed success", p)
				}
				code := call("GET", "/projects/"+p.ID+"/code?action=tree", nil)
				if code.Code != 400 || !strings.Contains(code.Body.String(), "仓库尚未同步") {
					t.Fatal("unsynced repository misreported", code.Body.String())
				}
				return
			}
			if p.SHA != sha || p.SyncedAt == 0 || p.SyncError != "" {
				t.Fatal("missing automatic sync receipt", p)
			}
			code := call("GET", "/projects/"+p.ID+"/code?action=file&path=README.md", nil)
			if code.Code != 200 || !strings.Contains(code.Body.String(), "automatic sync fixture") {
				t.Fatal("code unavailable after automatic sync", code.Body.String())
			}
		})
	}
}
