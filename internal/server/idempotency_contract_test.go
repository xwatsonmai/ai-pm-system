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

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The client contract: every write tool replays its first result for the same
// request_id and arguments, rejects a reused request_id with other arguments,
// and reports failures with a stable error code.
type contractHarness struct {
	t       *testing.T
	c       context.Context
	db      *sqlite.DB
	s       *app.Service
	p       domain.Project
	session *mcp.ClientSession
}

func newContractHarness(t *testing.T) *contractHarness {
	t.Helper()
	c := context.Background()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s := &app.Service{Store: db}
	p, e := s.SaveProject(c, domain.Project{Name: "幂等契约"})
	if e != nil {
		t.Fatal(e)
	}
	p.Memberships = []domain.ProjectMember{{UserID: app.Human().ID, Roles: []string{"admin"}}, {UserID: "employee", Roles: []string{"developer"}}}
	if e = db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	if e = db.PutMCPAccount(c, domain.MCPAccount{UserID: "employee", Enabled: true}); e != nil {
		t.Fatal(e)
	}
	site := httptest.NewServer((&server.Server{App: s}).Handler())
	t.Cleanup(site.Close)
	raw := "contract-access"
	if e = db.PutOAuthGrant(c, domain.OAuthGrant{ID: "grant", ClientID: "desktop-client", UserID: "employee", UserName: "开发者", ProjectID: p.ID, Resource: site.URL + "/mcp", AccessHash: app.Hash(raw), RefreshHash: app.Hash("contract-refresh"), AccessExpiresAt: domain.Now() + 600000, RefreshExpiresAt: domain.Now() + 1200000, AccountGeneration: 1}, ""); e != nil {
		t.Fatal(e)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "contract-client", Version: "1"}, nil)
	session, e := client.Connect(c, &mcp.StreamableClientTransport{Endpoint: site.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{raw}}, DisableStandaloneSSE: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { session.Close() })
	return &contractHarness{t: t, c: c, db: db, s: s, p: p, session: session}
}

func (h *contractHarness) call(name string, args map[string]string) *mcp.CallToolResult {
	h.t.Helper()
	r, e := h.session.CallTool(h.c, &mcp.CallToolParams{Name: name, Arguments: args})
	if e != nil {
		h.t.Fatal(e)
	}
	return r
}

func (h *contractHarness) ok(name string, args map[string]string) string {
	h.t.Helper()
	r := h.call(name, args)
	body := r.Content[0].(*mcp.TextContent).Text
	if r.IsError {
		h.t.Fatalf("%s failed: %s", name, body)
	}
	return body
}

// Calls twice with identical arguments: both answers must match and the task
// must change only once.
func (h *contractHarness) replay(name string, args map[string]string, taskID string) string {
	h.t.Helper()
	first := h.ok(name, args)
	before, e := h.db.GetTask(h.c, taskID)
	if e != nil {
		h.t.Fatal(e)
	}
	second := h.ok(name, args)
	after, _ := h.db.GetTask(h.c, taskID)
	if first != second {
		h.t.Fatalf("%s replay changed the answer:\n%s\n%s", name, first, second)
	}
	if before.Version != after.Version {
		h.t.Fatalf("%s replay changed the task again: version %d -> %d", name, before.Version, after.Version)
	}
	return first
}

func (h *contractHarness) failure(name string, args map[string]string) (code string, retryable bool, message string) {
	h.t.Helper()
	r := h.call(name, args)
	if !r.IsError {
		h.t.Fatalf("%s should fail: %v", name, r.Content)
	}
	message = r.Content[0].(*mcp.TextContent).Text
	raw, _ := json.Marshal(r.StructuredContent)
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if e := json.Unmarshal(raw, &body); e != nil || body.Error.Code == "" {
		h.t.Fatalf("%s error has no structured code: %s (%s)", name, raw, message)
	}
	if body.Error.Message != message {
		h.t.Fatalf("structured message %q differs from text %q", body.Error.Message, message)
	}
	return body.Error.Code, body.Error.Retryable, message
}

// on reports failures to t, for use inside subtests.
func (h *contractHarness) on(t *testing.T) *contractHarness {
	c := *h
	c.t = t
	return &c
}

func (h *contractHarness) readyTask(title string) domain.Task {
	h.t.Helper()
	m, e := h.s.CreateTask(h.c, app.Human(), h.p.ID, app.TaskInput{Title: title, Description: "契约测试", Acceptance: []string{"通过"}, RequestID: domain.ID("r_")})
	if e != nil {
		h.t.Fatal(e)
	}
	m, e = h.s.Act(h.c, app.Human(), m.Task.ID, app.Action{Action: "ready", RequestID: domain.ID("r_")})
	if e != nil {
		h.t.Fatal(e)
	}
	return m.Task
}

func (h *contractHarness) task(id string) domain.Task {
	h.t.Helper()
	t, e := h.db.GetTask(h.c, id)
	if e != nil {
		h.t.Fatal(e)
	}
	return t
}

func (h *contractHarness) testPass(id string) {
	h.t.Helper()
	current := h.task(id)
	if _, e := h.s.Act(h.c, app.Human(), id, app.Action{Action: "test_pass", RequestID: domain.ID("r_"), Version: current.Version, TestVerification: &domain.TestVerification{Revision: current.Revision, SHA: current.Submission.SHA, Environment: "test", Note: "通过"}}); e != nil {
		h.t.Fatal(e)
	}
}

func claimOf(t *testing.T, body string) string {
	t.Helper()
	var m domain.Mutation
	if e := json.Unmarshal([]byte(body), &m); e != nil || m.ClaimID == "" {
		t.Fatalf("no claim in %s (%v)", body, e)
	}
	return m.ClaimID
}

func TestWriteToolsReplayTheirFirstResult(t *testing.T) {
	h := newContractHarness(t)
	task := h.readyTask("完整生命周期")
	sha := strings.Repeat("b", 40)

	claimArgs := map[string]string{"task_id": task.ID, "request_id": "claim", "branch": "ai-pm/" + task.ID}
	claim := claimOf(t, h.replay("task_claim", claimArgs, task.ID))
	if code, retryable, _ := h.failure("task_claim", map[string]string{"task_id": task.ID, "request_id": "claim", "branch": "other"}); code != "idempotency_conflict" || retryable {
		t.Fatalf("reused request_id: %s %v", code, retryable)
	}

	h.replay("task_report_work", map[string]string{"task_id": task.ID, "request_id": "report", "claim_id": claim, "body": "进展", "report_kind": "progress"}, task.ID)

	current := h.task(task.ID)
	ask := map[string]string{"task_id": task.ID, "request_id": "ask", "claim_id": claim, "version": fmt.Sprint(current.Version), "revision": fmt.Sprint(current.Revision), "body": "范围？", "questions": `[{"id":"q1","label":"覆盖哪些订单？","type":"textarea","options":[],"recommended":[],"reason":"","required":false}]`}
	h.replay("task_request_alignment", ask, task.ID)
	review, e := h.db.GetRequirementReview(h.c, task.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.s.SaveRequirementReview(h.c, app.Human(), task.ID, app.RequirementReviewInput{Action: "message", RequestID: "reply", Generation: review.Generation, Body: "全部订单"}); e != nil {
		t.Fatal(e)
	}
	current = h.task(task.ID)
	resume := map[string]string{"task_id": task.ID, "request_id": "resume", "claim_id": claim, "version": fmt.Sprint(current.Version), "revision": fmt.Sprint(current.Revision), "body": "按全部订单继续"}
	renewed := claimOf(t, h.replay("task_resume_development", resume, task.ID))
	if renewed == claim {
		t.Fatal("resume should hand out a new claim")
	}

	submit := map[string]string{"task_id": task.ID, "request_id": "submit", "claim_id": renewed, "revision": fmt.Sprint(h.task(task.ID).Revision), "summary": "已实现", "sha": sha, "environment": "test", "tests": "通过"}
	first := h.replay("task_submit_result", submit, task.ID)
	if h.task(task.ID).Status != "testing" {
		t.Fatal(h.task(task.ID).Status)
	}
	h.testPass(task.ID)
	// A response lost long ago is still answered from the receipt after the task moved on.
	if again := h.ok("task_submit_result", submit); again != first {
		t.Fatal("late replay changed the answer")
	}

	h.replay("task_rework", map[string]string{"task_id": task.ID, "request_id": "rework", "claim_id": renewed, "body": "上线前发现问题"}, task.ID)
	if h.task(task.ID).Status != "doing" {
		t.Fatal(h.task(task.ID).Status)
	}
	submit["request_id"] = "submit-2"
	h.replay("task_submit_result", submit, task.ID)
	h.testPass(task.ID)
	h.replay("task_mark_deployed", map[string]string{"task_id": task.ID, "request_id": "deployed", "claim_id": renewed, "revision": submit["revision"], "sha": sha, "environment": "production", "body": "已上线"}, task.ID)
	if h.task(task.ID).Status != "review" {
		t.Fatal(h.task(task.ID).Status)
	}

	released := h.readyTask("放弃认领")
	releasedClaim := claimOf(t, h.ok("task_claim", map[string]string{"task_id": released.ID, "request_id": "claim-released"}))
	h.replay("task_release", map[string]string{"task_id": released.ID, "request_id": "release", "claim_id": releasedClaim, "body": "交接"}, released.ID)

	closed := h.readyTask("关闭需求")
	h.replay("task_close", map[string]string{"task_id": closed.ID, "request_id": "close", "version": fmt.Sprint(closed.Version), "body": "不做了", "user_confirmed": "true", "user_instruction": "关闭这个需求"}, closed.ID)
}

func TestCollaborationToolsReplayTheirFirstResult(t *testing.T) {
	h := newContractHarness(t)
	run := domain.Run{ID: "origin", ProjectID: h.p.ID, Kind: "question", ConversationID: "conversation", Prompt: "prompt", OwnerID: app.Human().ID, OwnerKind: "human", Status: "succeeded"}
	if e := h.db.PutRun(h.c, run); e != nil {
		t.Fatal(e)
	}
	created, e := h.s.CreateCollaboration(h.c, app.Human(), app.CollaborationInput{RequestID: "create", OriginRunID: run.ID, Title: "日志核查", Brief: "trace-42", Environment: "prod"})
	if e != nil {
		t.Fatal(e)
	}
	id := created.Collaboration.ID
	twice := func(name string, args map[string]string) string {
		t.Helper()
		first, second := h.ok(name, args), h.ok(name, args)
		if first != second {
			t.Fatalf("%s replay changed the answer:\n%s\n%s", name, first, second)
		}
		return first
	}
	var claimed domain.CollaborationMutation
	if e = json.Unmarshal([]byte(twice("collaboration_claim", map[string]string{"collaboration_id": id, "request_id": "c-claim"})), &claimed); e != nil || claimed.ClaimID == "" {
		t.Fatal(e)
	}
	twice("collaboration_report", map[string]string{"collaboration_id": id, "request_id": "c-report", "claim_id": claimed.ClaimID, "body": "正在查", "report_kind": "progress"})
	current, e := h.s.GetCollaboration(h.c, domain.Actor{ID: "employee", Kind: "developer", ProjectID: h.p.ID}, id)
	if e != nil {
		t.Fatal(e)
	}
	twice("collaboration_submit_result", map[string]string{"collaboration_id": id, "request_id": "c-submit", "claim_id": claimed.ClaimID, "version": fmt.Sprint(current.Version), "outcome": "confirmed", "summary": "找到原因", "evidence": "trace-42 的错误日志", "environment": "prod", "observed_at": "2026-10-24 10:00 +08:00", "deployment_version": "v1", "limitations": "无"})
}

func TestToolErrorsCarryStableCodes(t *testing.T) {
	h := newContractHarness(t)
	task := h.readyTask("错误码")
	claim := claimOf(t, h.ok("task_claim", map[string]string{"task_id": task.ID, "request_id": "claim"}))
	cases := []struct {
		name      string
		tool      string
		args      map[string]string
		code      string
		retryable bool
	}{
		{"missing argument", "task_report_work", map[string]string{"task_id": task.ID, "request_id": "r1", "claim_id": claim}, "invalid_request", false},
		{"wrong claim", "task_report_work", map[string]string{"task_id": task.ID, "request_id": "r2", "claim_id": "not-the-claim", "body": "x"}, "forbidden", false},
		{"unknown task", "task_get_context", map[string]string{"task_id": "tsk_missing"}, "not_found", false},
		{"stale version", "task_request_alignment", map[string]string{"task_id": task.ID, "request_id": "r3", "claim_id": claim, "version": "1", "revision": "1", "body": "x"}, "conflict", false},
		{"stale revision", "task_submit_result", map[string]string{"task_id": task.ID, "request_id": "r4", "claim_id": claim, "revision": "99", "summary": "s", "sha": strings.Repeat("c", 40), "tests": "t"}, "conflict", false},
	}
	// Every argument is a string on the wire; a typed number is a client bug,
	// not something to resend.
	r, e := h.session.CallTool(h.c, &mcp.CallToolParams{Name: "task_request_alignment", Arguments: map[string]any{"task_id": task.ID, "request_id": "typed", "claim_id": claim, "version": 3, "revision": 1, "body": "x"}})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(r.StructuredContent)
	if !r.IsError || !strings.Contains(string(raw), `"code":"invalid_request"`) {
		t.Fatalf("typed argument: %s", raw)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, retryable, message := h.on(t).failure(tc.tool, tc.args)
			if code != tc.code || retryable != tc.retryable {
				t.Fatalf("got %s (retryable=%v) for %q, want %s", code, retryable, message, tc.code)
			}
		})
	}
}
