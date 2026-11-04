package server

import (
	"ai-pm-system/internal/agent"
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTTPManualAnalysisKeepsOwnerPagesAndPrivateResults(t *testing.T) {
	if _, e := exec.LookPath("node"); e != nil {
		t.Skip("Node required for worker protocol regression")
	}
	c := context.Background()
	dir := t.TempDir()
	db, e := sqlite.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	owner := domain.Actor{ID: "owner", Kind: "human"}
	p, e := svc.SaveProjectAs(c, owner, domain.Project{Name: "Manual runs", Repository: "https://example.com/SECRET_SOURCE.git", AgentReadMode: "sandbox"})
	if e != nil {
		t.Fatal(e)
	}
	p.ProjectRoles = append(p.ProjectRoles, domain.ProjectRole{ID: "limited", Name: "Limited", Pages: []string{"tasks", "chat"}, Version: 1})
	p.Memberships = append(p.Memberships, domain.ProjectMember{UserID: "limited", Roles: []string{"limited"}}, domain.ProjectMember{UserID: "other", Roles: []string{"developer"}})
	p.AccessVersion++
	if e = db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	if e = db.PutArtifact(c, domain.Artifact{ID: "review", ProjectID: p.ID, Kind: "deployment", Body: "SECRET_REVIEW"}); e != nil {
		t.Fatal(e)
	}
	if e = db.PutProfile(c, domain.Profile{ID: "profile", Name: "Fixture", Model: "fixture", BaseURL: "http://unused"}); e != nil {
		t.Fatal(e)
	}
	if e = db.PutPlatformSettings(c, domain.PlatformSettings{ProfileID: "profile"}); e != nil {
		t.Fatal(e)
	}
	worker := filepath.Join(dir, "worker.mjs")
	js := `import readline from 'node:readline';
const rl=readline.createInterface({input:process.stdin});
const calls=[['project_context',{}],['repository_read',{action:'file',path:'secret.txt'}],['project_version_list',{}],['project_timeline',{}],['save_analysis',{kind:'deployment',title:'forged',body:'not allowed',sha:'forged'}]];
let records=[],index=0;
function next(){if(index===calls.length){process.stdout.write(JSON.stringify({type:'done',text:JSON.stringify(records)})+'\n');process.exit(0);}const [name,args]=calls[index++];process.stdout.write(JSON.stringify({type:'tool_call',id:String(index),name,args})+'\n');}
rl.on('line',line=>{const m=JSON.parse(line);if(m.type==='start'){records.push(m.prompt);next();}if(m.type==='tool_result'){records.push(m);next();}});`
	if e = os.WriteFile(worker, []byte(js), 0600); e != nil {
		t.Fatal(e)
	}
	m := agent.New(c, svc, nil, worker, dir)
	defer m.Close()
	h := (&Server{App: svc, Agent: m}).Handler()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := domain.Actor{ID: r.Header.Get("X-Fixture-User"), Kind: "human"}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, a)))
	}))
	defer site.Close()
	call := func(user, method, path, body string, status int) string {
		t.Helper()
		req, _ := http.NewRequest(method, site.URL+path, strings.NewReader(body))
		req.Header.Set("X-Fixture-User", user)
		resp, err := site.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != status {
			t.Fatalf("%s %s as %s: %d want %d %s", method, path, user, resp.StatusCode, status, raw)
		}
		return string(raw)
	}
	base := "/api/projects/" + p.ID
	for _, kind := range []string{"progress", "knowledge", "requirements", "triage"} {
		raw := call("limited", "POST", base+"/runs", `{"kind":"`+kind+`","prompt":"Read source and private evidence"}`, 200)
		var r domain.Run
		if e = json.Unmarshal([]byte(raw), &r); e != nil {
			t.Fatal(e)
		}
		if r.OwnerID != "limited" || r.OwnerKind != "human" || r.CodeSnapshot != nil {
			t.Fatal("manual run borrowed a sandbox or lost owner", raw)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			r, e = db.GetRun(c, r.ID)
			if e != nil {
				t.Fatal(e)
			}
			if r.Status != "queued" && r.Status != "running" {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if r.Status != "succeeded" || strings.Contains(r.Output, "SECRET_") || strings.Count(r.Output, "当前身份不能执行此操作") != 4 {
			t.Fatalf("%s escaped current owner pages: %+v", kind, r)
		}
		call("limited", "GET", "/api/runs/"+r.ID, "", 200)
		call("other", "GET", "/api/runs/"+r.ID, "", 403)
		call("other", "GET", "/api/runs/"+r.ID+"/events", "", 403)
		call("other", "POST", "/api/runs/"+r.ID+"/stop", "{}", 403)
		for _, suffix := range []string{"", "?page=1&page_size=100"} {
			if strings.Contains(call("other", "GET", base+"/runs"+suffix, "", 200), r.ID) {
				t.Fatal("another member found a private manual run in listing")
			}
		}
	}
	// The HTTP API cannot manufacture a background review identity.
	call("limited", "POST", base+"/runs", `{"kind":"requirement_review","prompt":"pretend to be internal"}`, 403)
	// Revoking tasks also removes owned manual runs from both detail and paging.
	p, _ = db.GetProject(c, p.ID)
	p.ProjectRoles[len(p.ProjectRoles)-1].Pages = []string{"reviews", "chat"}
	p.AccessVersion++
	if e = db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	for _, suffix := range []string{"", "?page=1&page_size=100"} {
		raw := call("limited", "GET", base+"/runs"+suffix, "", 200)
		if strings.Contains(raw, `"kind":"progress"`) || strings.Contains(raw, `"kind":"knowledge"`) {
			t.Fatal("revoked owner retained manual analyses via listing", raw)
		}
	}
}
