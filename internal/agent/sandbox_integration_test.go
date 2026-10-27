package agent

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/gitrepo"
	"ai-pm-system/internal/sandbox"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Run explicitly with AI_PM_SANDBOX_IMAGE after building Worker.Dockerfile.
// Uses a local model fixture, actual Pi, actual Docker, actual Git and SQLite.
func TestDockerSandboxEndToEnd(t *testing.T) {
	image := os.Getenv("AI_PM_SANDBOX_IMAGE")
	if image == "" {
		t.Skip("set AI_PM_SANDBOX_IMAGE to run real container acceptance")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "data")
	os.MkdirAll(data, 0755)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	socketDir, err := os.MkdirTemp("/tmp", "pm-runner-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketDir)
	socket := filepath.Join(socketDir, "r.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	uid, gid := os.Getuid(), os.Getgid()
	if uid == 0 {
		uid = 1000
	}
	if gid == 0 {
		gid = 1000
	}
	runner, err := sandbox.New(sandbox.Config{DataRoot: data, Image: image, UID: uid, GID: gid})
	if err != nil {
		t.Fatal(err)
	}
	runnerDone := make(chan error, 1)
	go func() { runnerDone <- runner.Serve(ctx, listener) }()
	defer func() {
		cancel()
		if e := <-runnerDone; e != nil {
			t.Error(e)
		}
	}()
	var hold atomic.Bool
	var noRepository atomic.Bool
	var seq atomic.Int64
	started := make(chan struct{}, 1)
	model := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if json.NewDecoder(req.Body).Decode(&body) != nil {
			w.WriteHeader(400)
			return
		}
		if hold.Load() && len(body.Tools) > 0 {
			select {
			case started <- struct{}{}:
			default:
			}
			<-req.Context().Done()
			return
		}
		names := map[string]bool{}
		for _, tool := range body.Tools {
			names[tool.Function.Name] = true
		}
		if len(body.Tools) > 0 && !noRepository.Load() {
			for _, name := range []string{"read", "grep", "find", "ls", "project_context", "save_analysis", "repository_info", "repository_history"} {
				if !names[name] {
					t.Errorf("missing tool %s", name)
				}
			}
			for _, name := range []string{"bash", "powershell", "edit", "write", "repository_read", "create_bug"} {
				if names[name] {
					t.Errorf("unexpected tool %s", name)
				}
			}
		}
		if len(body.Tools) > 0 && noRepository.Load() {
			for _, name := range []string{"read", "grep", "find", "ls"} {
				if names[name] {
					t.Errorf("source tool enabled without repository: %s", name)
				}
			}
			if !names["project_context"] || !names["save_analysis"] {
				t.Error("business tools unavailable without repository")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(delta any, stop any) {
			b, _ := json.Marshal(map[string]any{"id": "fixture", "object": "chat.completion.chunk", "created": 1, "model": "fixture", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": stop}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		if len(body.Tools) == 0 {
			send(map[string]string{"role": "assistant", "content": "源码版本核对"}, nil)
			send(map[string]any{}, "stop")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		if noRepository.Load() {
			send(map[string]string{"role": "assistant", "content": "当前项目尚未配置仓库，可以继续讨论需求资料。"}, nil)
			send(map[string]any{}, "stop")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		lastUser := 0
		for i, m := range body.Messages {
			if m.Role == "user" {
				lastUser = i
			}
		}
		var results []json.RawMessage
		for _, m := range body.Messages[lastUser:] {
			if m.Role == "tool" {
				results = append(results, m.Content)
			}
		}
		steps := []struct{ name, args string }{
			{"find", `{"pattern":"**/service.py"}`},
			{"read", `{"path":"server/services/photo-compositor/service.py","offset":316,"limit":12}`},
			{"grep", `{"path":"server/services/photo-compositor","pattern":"alpha_composite"}`},
			{"ls", `{"path":"server/services"}`},
			{"read", `{"path":"/state/sessions"}`},
			{"project_context", `{}`},
			{"save_analysis", `{"kind":"analysis","title":"sandbox fixture","body":"已读取三层合成源码","sha":"forged"}`},
		}
		if len(results) < len(steps) {
			step := steps[len(results)]
			send(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("native_%d", seq.Add(1)), "type": "function", "function": map[string]string{"name": step.name, "arguments": step.args}}}}, nil)
			send(map[string]any{}, "tool_calls")
		} else {
			if !strings.Contains(string(results[0]), "server/services/photo-compositor/service.py") {
				t.Error("find did not see full source tree", string(results[0]))
			}
			if !strings.Contains(string(results[1]), "alpha_composite") {
				t.Error("read missed target range", string(results[1]))
			}
			if !strings.Contains(string(results[4]), "PATH_OUTSIDE_WORKSPACE") {
				t.Error("private state was not denied", string(results[4]))
			}
			send(map[string]string{"role": "assistant", "content": "已读取相框合成源码，并记录本轮版本。"}, nil)
			send(map[string]any{}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	model.Listener = l
	model.Start()
	defer model.Close()
	host := os.Getenv("AI_PM_DOCKER_HOST")
	if host == "" {
		host = "host.docker.internal"
	}
	modelURL := fmt.Sprintf("http://%s:%d/v1", host, l.Addr().(*net.TCPAddr).Port)
	db, err := sqlite.Open(filepath.Join(data, "pm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := &app.Service{Store: db}
	profile, err := a.SaveProfile(ctx, domain.Profile{Name: "sandbox fixture", Provider: "fixture", Model: "fixture", BaseURL: modelURL, API: "openai-completions"}, "fixture-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.PutPlatformSettings(ctx, domain.PlatformSettings{ProfileID: profile.ID}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "repo")
	os.MkdirAll(filepath.Join(source, "server/services/photo-compositor"), 0755)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = source
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatal(string(b), e)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "-b", "main")
	file := filepath.Join(source, "server/services/photo-compositor/service.py")
	os.WriteFile(file, []byte(strings.Repeat("# padding\n", 315)+"canvas.alpha_composite(background)\ncanvas.alpha_composite(photo)\ncanvas.alpha_composite(frame)\n"), 0644)
	git("add", ".")
	git("commit", "-m", "fixture")
	first := git("rev-parse", "HEAD")
	p, err := a.SaveProject(ctx, domain.Project{Name: "sandbox", Repository: source, Branch: "main", AgentReadMode: "sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	repos := &gitrepo.Repositories{Root: filepath.Join(data, "repos"), App: a}
	if err = repos.Sync(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	worker, _ := filepath.Abs("../../runtime/worker.mjs")
	m := New(ctx, a, repos, worker, data)
	m.SandboxSocket = socket
	defer m.Close()
	wait := func(id string) domain.Run {
		t.Helper()
		deadline := time.Now().Add(40 * time.Second)
		for time.Now().Before(deadline) {
			r, e := db.GetRun(ctx, id)
			if e != nil {
				t.Fatal(e)
			}
			if r.Status != "queued" && r.Status != "running" {
				return r
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("run timed out", id)
		return domain.Run{}
	}
	r, err := m.Start(ctx, p.ID, "", "question", "读取合成源码", "")
	if err != nil {
		t.Fatal(err)
	}
	r = wait(r.ID)
	if r.Status != "succeeded" || r.CodeSnapshot.SHA != first || len(r.Steps) != 7 {
		t.Fatalf("sandbox run: %+v", r)
	}
	artifacts, _ := db.ListArtifacts(ctx, p.ID)
	analysisCount := 0
	for _, v := range artifacts {
		if v.Kind == "analysis" {
			analysisCount++
			if v.SHA != first {
				t.Fatal("model overrode pinned SHA", v)
			}
		}
	}
	if analysisCount != 1 {
		t.Fatalf("business analysis was not saved: %d", analysisCount)
	}
	os.WriteFile(file, []byte(strings.Repeat("# padding\n", 315)+"canvas.alpha_composite(background_v2)\ncanvas.alpha_composite(photo)\ncanvas.alpha_composite(frame)\n"), 0644)
	git("add", ".")
	git("commit", "-m", "new version")
	second := git("rev-parse", "HEAD")
	if err = repos.Sync(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	followup, err := m.Start(ctx, p.ID, "", "question", "读取新版本", r.ID)
	if err != nil {
		t.Fatal(err)
	}
	followup = wait(followup.ID)
	if followup.Status != "succeeded" || followup.CodeSnapshot.SHA != second || followup.ConversationID != r.ConversationID {
		t.Fatalf("follow-up failed: %+v", followup)
	}
	hold.Store(true)
	blocked, err := m.StartAsAtRef(ctx, app.Human(), p.ID, "", "question", "固定旧版本", followup.ID, first)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("model did not start")
	}
	name := "ai-pm-run-" + blocked.ID
	probe := `const fs=require('fs');try{fs.writeFileSync('/workspace/server/services/photo-compositor/service.py','bad');process.exit(4)}catch(e){if(e.code!=='EROFS'&&e.code!=='EACCES')throw e};if(fs.existsSync('/app/data/pm.db')||fs.existsSync('/var/run/docker.sock'))process.exit(5);console.log('readonly-and-private-ok')`
	b, e := exec.Command("docker", "exec", name, "node", "-e", probe).CombinedOutput()
	if e != nil || !strings.Contains(string(b), "readonly-and-private-ok") {
		t.Fatal("container isolation probe", string(b), e)
	}
	if !m.Stop(blocked.ID) {
		t.Fatal("could not cancel")
	}
	stopped := wait(blocked.ID)
	if stopped.Status != "stopped" || stopped.CodeSnapshot.SHA != first {
		t.Fatal("cancel/version pin", stopped.Status, stopped.Error)
	}
	deadline := time.Now().Add(10 * time.Second)
	cleaned := false
	for time.Now().Before(deadline) {
		if e := exec.Command("docker", "inspect", name).Run(); e != nil {
			cleaned = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !cleaned {
		t.Fatal("canceled container leaked")
	}
	noRepository.Store(true)
	hold.Store(false)
	business, err := a.SaveProject(ctx, domain.Project{Name: "business only", AgentReadMode: "sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	businessRun, err := m.Start(ctx, business.ID, "", "question", "讨论需求资料", "")
	if err != nil {
		t.Fatal(err)
	}
	businessRun = wait(businessRun.ID)
	if businessRun.Status != "succeeded" || businessRun.CodeSnapshot == nil || businessRun.CodeSnapshot.Status != "no_repository" || len(businessRun.Steps) != 0 {
		t.Fatalf("business-only sandbox failed: %+v", businessRun)
	}
}
