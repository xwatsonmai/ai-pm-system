package server_test

import (
	"ai-pm-system/internal/agent"
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/gitrepo"
	"ai-pm-system/internal/server"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}
func TestIndependentMCPClientAndProjectIsolation(t *testing.T) {
	c, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	db, e := sqlite.Open(filepath.Join(dir, "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := &app.Service{Store: db}
	git := &gitrepo.Repositories{Root: filepath.Join(dir, "repos"), App: s}
	mgr := agent.New(c, s, git, "missing-worker", dir)
	defer mgr.Close()
	p, e := s.SaveProject(c, domain.Project{Name: "one"})
	if e != nil {
		t.Fatal(e)
	}
	other, e := s.SaveProject(c, domain.Project{Name: "two"})
	if e != nil {
		t.Fatal(e)
	}
	create := func(project string) domain.Task {
		m, e := s.CreateTask(c, app.Human(), project, app.TaskInput{Title: "测试需求", Description: "验证业务链路", Acceptance: []string{"结果正确"}, RequestID: domain.ID("r_")})
		if e != nil {
			t.Fatal(e)
		}
		m, e = s.Act(c, app.Human(), m.Task.ID, app.Action{Action: "ready", RequestID: domain.ID("r_")})
		if e != nil {
			t.Fatal(e)
		}
		return m.Task
	}
	task := create(p.ID)
	foreign := create(other.ID)
	_, raw, e := s.CreateToken(c, p.ID, "独立 MCP 客户端")
	if e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer((&server.Server{App: s, Git: git, Agent: mgr}).Handler())
	defer srv.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance-client", Version: "1"}, nil)
	session, e := client.Connect(c, &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{raw}}, DisableStandaloneSSE: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	tools, e := session.ListTools(c, nil)
	if e != nil || len(tools.Tools) != 41 {
		t.Fatalf("list tools: %v %#v", e, tools)
	}
	call := func(name string, args map[string]string) *mcp.CallToolResult {
		result, e := session.CallTool(c, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil {
			t.Fatal(e)
		}
		return result
	}
	listed := call("task_list", map[string]string{"status": "ready"})
	if listed.IsError {
		t.Fatal(listed)
	}
	body := listed.Content[0].(*mcp.TextContent).Text
	if strings.Contains(body, foreign.ID) || !strings.Contains(body, task.ID) {
		t.Fatal("project isolation failed")
	}
	rejected := call("task_get_context", map[string]string{"task_id": foreign.ID})
	if !rejected.IsError {
		t.Fatal("foreign task accessible")
	}
	claimed := call("task_claim", map[string]string{"task_id": task.ID, "request_id": "mcp-claim"})
	if claimed.IsError {
		t.Fatal(claimed.Content)
	}
	var mutation domain.Mutation
	if e = json.Unmarshal([]byte(claimed.Content[0].(*mcp.TextContent).Text), &mutation); e != nil {
		t.Fatal(e)
	}
	submitted := call("task_submit_result", map[string]string{"task_id": task.ID, "request_id": "mcp-submit", "claim_id": mutation.ClaimID, "revision": "1", "summary": "已实现", "sha": strings.Repeat("a", 40), "environment": "local", "tests": "专项测试通过"})
	if submitted.IsError {
		t.Fatal(submitted.Content)
	}
	current, e := db.GetTask(c, task.ID)
	if e != nil || current.Status != "testing" {
		t.Fatal(current, e)
	}
	withdraw := map[string]string{"task_id": task.ID, "request_id": "mcp-withdraw", "claim_id": mutation.ClaimID, "version": fmt.Sprint(current.Version), "revision": "1", "sha": current.Submission.SHA, "body": "提前提测", "user_confirmed": "true", "user_instruction": "撤回提前提测"}
	for _, field := range []string{"version", "revision", "sha", "user_confirmed", "user_instruction", "claim_id"} {
		bad := map[string]string{}
		for k, v := range withdraw {
			bad[k] = v
		}
		bad[field] = ""
		if r := call("task_withdraw_submission", bad); !r.IsError {
			t.Fatal("invalid withdrawal", field)
		}
	}
	for i := 0; i < 2; i++ {
		if r := call("task_withdraw_submission", withdraw); r.IsError {
			t.Fatal(r.Content)
		}
	}
	current, _ = db.GetTask(c, task.ID)
	if current.Status != "doing" || current.Submission == nil {
		t.Fatal("withdrawal lost source")
	}
	if r := call("task_submit_result", map[string]string{"task_id": task.ID, "request_id": "mcp-submit-after-release", "claim_id": mutation.ClaimID, "revision": "1", "summary": "测试发布成功", "sha": current.Submission.SHA, "environment": "test", "tests": "测试环境验证通过"}); r.IsError {
		t.Fatal(r.Content)
	}
	current, _ = db.GetTask(c, task.ID)
	if _, e = s.Act(c, app.Human(), task.ID, app.Action{Action: "test_pass", RequestID: "product-test", Version: current.Version, TestVerification: &domain.TestVerification{Revision: current.Revision, SHA: current.Submission.SHA, Environment: "test", Note: "产品测试通过"}}); e != nil {
		t.Fatal(e)
	}
	args := map[string]string{"task_id": task.ID, "request_id": "mcp-deployed", "claim_id": mutation.ClaimID, "revision": "1", "sha": strings.Repeat("a", 40), "environment": "production", "body": "实际发布成功，发布日志 build-42"}
	if r := call("task_mark_deployed", args); r.IsError {
		t.Fatal(r.Content)
	}
	current, e = db.GetTask(c, task.ID)
	if e != nil || current.Status != "review" || current.Deployment == nil || current.Deployment.Source != "developer" {
		t.Fatal(current, e)
	}
	version := current.Version
	if r := call("task_mark_deployed", args); r.IsError {
		t.Fatal(r.Content)
	}
	current, _ = db.GetTask(c, task.ID)
	if current.Version != version {
		t.Fatal("duplicate release")
	}
	resp, e := http.Get(srv.URL + "/api/health")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/projects", strings.NewReader(`{"name":"csrf"}`))
	req.Header.Set("Origin", "https://untrusted.example")
	resp, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("cross-origin write accepted: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader(`{}`))
	resp, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("MCP allowed missing token")
	}
}
