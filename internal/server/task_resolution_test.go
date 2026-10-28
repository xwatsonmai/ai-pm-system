package server_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/server"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPExplicitRequirementDecisions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	p, err := svc.SaveProject(ctx, domain.Project{Name: "MCP decisions"})
	if err != nil {
		t.Fatal(err)
	}
	p.Memberships = []domain.ProjectMember{{UserID: "employee", Roles: []string{"developer"}}}
	if err = db.PutProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err = db.PutMCPAccount(ctx, domain.MCPAccount{UserID: "employee", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer((&server.Server{App: svc}).Handler())
	defer srv.Close()
	const raw = "test-personal-oauth-token"
	grant := domain.OAuthGrant{ID: "grant", UserID: "employee", UserName: "开发者", ProjectID: p.ID, Resource: srv.URL + "/mcp", AccessHash: app.Hash(raw), RefreshHash: app.Hash("test-refresh"), AccessExpiresAt: domain.Now() + 60000, RefreshExpiresAt: domain.Now() + 120000, AccountGeneration: 1}
	if err = db.PutOAuthGrant(ctx, grant, ""); err != nil {
		t.Fatal(err)
	}
	connect := func(token string) *mcp.ClientSession {
		t.Helper()
		client := mcp.NewClient(&mcp.Implementation{Name: "decision-check", Version: "1"}, nil)
		session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token}}, DisableStandaloneSSE: true}, nil)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { session.Close() })
		return session
	}
	session := connect(raw)
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, tool := range tools.Tools {
		if tool.Name != "task_close" && tool.Name != "task_decline" && tool.Name != "task_reopen" {
			continue
		}
		found++
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint || !strings.Contains(tool.Description, "用户明确要求") {
			t.Fatal("missing decision semantics", tool)
		}
		raw, _ := json.Marshal(tool.InputSchema)
		var schema struct{ Required []string }
		if e := json.Unmarshal(raw, &schema); e != nil || len(schema.Required) != 6 || !strings.Contains(string(raw), `"enum":["true"]`) {
			t.Fatal("authorization fields not required", string(raw), e)
		}
	}
	if found != 3 {
		t.Fatal("missing decision tools", found)
	}
	call := func(name string, args map[string]string) string {
		t.Helper()
		r, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil || r.IsError {
			t.Fatalf("%s: %v %#v", name, e, r)
		}
		return r.Content[0].(*mcp.TextContent).Text
	}
	bad := func(name string, args map[string]string) {
		t.Helper()
		r, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e == nil && !r.IsError {
			t.Fatalf("unsafe %s accepted: %#v", name, r)
		}
	}
	makeTask := func(project string) domain.Task {
		t.Helper()
		m, e := svc.CreateTask(ctx, app.Human(), project, app.TaskInput{Title: "待决定需求", Description: "保留原文", Acceptance: []string{"保留条件"}, RequestID: domain.ID("create_")})
		if e != nil {
			t.Fatal(e)
		}
		return m.Task
	}
	params := func(task domain.Task, request string) map[string]string {
		return map[string]string{"task_id": task.ID, "request_id": request, "version": fmt.Sprint(task.Version), "body": "业务范围调整", "user_confirmed": "true", "user_instruction": "请关闭这个需求，业务范围已经调整"}
	}
	task := makeTask(p.ID)
	for key, values := range map[string][]string{
		"user_confirmed": {"", "false", "TRUE"}, "user_instruction": {"", " \n"}, "body": {"", " "}, "request_id": {"", " "},
		"version": {"", "0", "-1", "wrong", "1.5", "9999999999999999999999999"},
	} {
		for _, value := range values {
			args := params(task, "invalid")
			args[key] = value
			bad("task_close", args)
		}
	}
	args := params(task, "impersonate")
	args["user_id"] = "somebody-else"
	bad("task_close", args)
	unchanged, _ := db.GetTask(ctx, task.ID)
	if unchanged.Version != task.Version {
		t.Fatal("invalid calls changed task")
	}
	other, _ := svc.SaveProject(ctx, domain.Project{Name: "private"})
	foreign := makeTask(other.ID)
	bad("task_close", params(foreign, "foreign"))
	_, legacy, err := svc.CreateToken(ctx, p.ID, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	r, err := connect(legacy).CallTool(ctx, &mcp.CallToolParams{Name: "task_close", Arguments: params(task, "legacy")})
	if err != nil || !r.IsError {
		t.Fatal("unbound legacy token authorized a decision", r, err)
	}
	// Real HTTP tools must preserve the current-version check and caller's claim.
	ready, err := svc.Act(ctx, app.Human(), task.ID, app.Action{Action: "ready", RequestID: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	var claimed domain.Mutation
	if err = json.Unmarshal([]byte(call("task_claim", map[string]string{"task_id": task.ID, "request_id": "claim"})), &claimed); err != nil {
		t.Fatal(err)
	}
	bad("task_close", params(ready.Task, "stale"))
	if _, err = svc.SaveRequirementReview(ctx, app.Human(), task.ID, app.RequirementReviewInput{Action: "start", RequestID: "start"}); err != nil {
		t.Fatal(err)
	}
	var current struct{ Task domain.Task }
	json.Unmarshal([]byte(call("task_get_context", map[string]string{"task_id": task.ID})), &current)
	args = params(current.Task, "close")
	first := call("task_close", args)
	if repeat := call("task_close", args); repeat != first {
		t.Fatal("lost-response retry changed receipt")
	}
	var ended domain.Mutation
	json.Unmarshal([]byte(first), &ended)
	if ended.Task.Status != "closed" || ended.Task.Resolution.FromStatus != "doing" || ended.Task.Claimant != "" || ended.Task.Description != task.Description {
		t.Fatal("close did not preserve data or revoke claim", ended)
	}
	bad("task_report_work", map[string]string{"task_id": task.ID, "request_id": "late-report", "claim_id": claimed.ClaimID, "body": "过期进展"})
	if body := call("work_list", nil); strings.Contains(body, task.ID) {
		t.Fatal("closed task still actionable", body)
	}
	rr, err := svc.RequirementReview(ctx, app.Human(), task.ID)
	if err != nil || rr.Status != "closed" {
		t.Fatal("MCP close left writable review", rr, err)
	}
	history := call("task_get_updates", map[string]string{"task_id": task.ID, "cursor": "0"})
	if !strings.Contains(history, args["user_instruction"]) || !strings.Contains(history, `"user_id":"employee"`) || !strings.Contains(history, "开发者 · MCP") || strings.Contains(history, raw) || strings.Contains(history, claimed.ClaimID) {
		t.Fatal("authorization history absent or credentials exposed", history)
	}
	args = params(ended.Task, "reopen")
	args["user_instruction"] = "请重新打开这个需求，范围已重新确认"
	var opened domain.Mutation
	json.Unmarshal([]byte(call("task_reopen", args)), &opened)
	if opened.Task.Status != "clarify" || opened.Task.Revision != task.Revision+1 {
		t.Fatal("reopen did not begin new review", opened)
	}
	args = params(opened.Task, "decline")
	args["user_instruction"] = "请拒绝这个需求，当前不在业务范围内"
	var declined domain.Mutation
	json.Unmarshal([]byte(call("task_decline", args)), &declined)
	if declined.Task.Status != "rejected" || !strings.Contains(call("task_list", map[string]string{"status": "rejected"}), task.ID) {
		t.Fatal("decline not readable through MCP", declined)
	}
	// Membership revocation is checked before either a new mutation or a retry.
	p.Memberships = nil
	p.OwnerID, p.AccessMembers = "", nil
	if err = db.PutProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	bad("task_reopen", params(declined.Task, "revoked"))
	bad("task_decline", args)
	final, _ := db.GetTask(ctx, task.ID)
	if final.Version != declined.Task.Version {
		t.Fatal("revoked member changed task")
	}
}
