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

func TestMCPDevelopmentAlignmentAcrossOAuthClients(t *testing.T) {
	c := context.Background()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := &app.Service{Store: db}
	p, e := s.SaveProject(c, domain.Project{Name: "开发对齐"})
	if e != nil {
		t.Fatal(e)
	}
	p.Memberships = []domain.ProjectMember{{UserID: "employee", Roles: []string{"developer"}}}
	if e = db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	if e = db.PutMCPAccount(c, domain.MCPAccount{UserID: "employee", Enabled: true}); e != nil {
		t.Fatal(e)
	}
	site := httptest.NewServer((&server.Server{App: s}).Handler())
	defer site.Close()
	connect := func(id, clientID string) *mcp.ClientSession {
		t.Helper()
		raw := "test-access-" + id
		e := db.PutOAuthGrant(c, domain.OAuthGrant{ID: id, ClientID: clientID, UserID: "employee", UserName: "开发者", ProjectID: p.ID, Resource: site.URL + "/mcp", AccessHash: app.Hash(raw), RefreshHash: app.Hash("test-refresh-" + id), AccessExpiresAt: domain.Now() + 60000, RefreshExpiresAt: domain.Now() + 120000, AccountGeneration: 1}, "")
		if e != nil {
			t.Fatal(e)
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "alignment-check", Version: "1"}, nil)
		session, e := client.Connect(c, &mcp.StreamableClientTransport{Endpoint: site.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{raw}}, DisableStandaloneSSE: true}, nil)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { session.Close() })
		return session
	}
	first, other := connect("first", "original-client"), connect("other", "different-client")
	call := func(session *mcp.ClientSession, name string, args map[string]string, wantError bool) string {
		t.Helper()
		r, e := session.CallTool(c, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil {
			if wantError {
				return ""
			}
			t.Fatal(e)
		}
		body := r.Content[0].(*mcp.TextContent).Text
		if r.IsError != wantError {
			t.Fatalf("%s error=%v: %s", name, r.IsError, body)
		}
		return body
	}
	m, e := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "售后记录", Description: "按订单展示", Acceptance: []string{"订单维度"}, RequestID: "create"})
	if e != nil {
		t.Fatal(e)
	}
	m, e = s.Act(c, app.Human(), m.Task.ID, app.Action{Action: "ready", RequestID: "ready"})
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal([]byte(call(first, "task_claim", map[string]string{"task_id": m.Task.ID, "request_id": "claim"}, false)), &m); e != nil {
		t.Fatal(e)
	}
	args := map[string]string{"task_id": m.Task.ID, "request_id": "ask", "claim_id": m.ClaimID, "version": fmt.Sprint(m.Task.Version), "revision": fmt.Sprint(m.Task.Revision), "body": "范围是否覆盖全部订单？", "questions": `[{"id":"scope","label":"哪些订单？","type":"radio","options":["全部","线上"],"required":true}]`}
	bad := map[string]string{}
	for k, v := range args {
		bad[k] = v
	}
	bad["questions"] = `[{"type":"radio","unknown":true}]`
	call(first, "task_request_alignment", bad, true)
	call(first, "task_request_alignment", args, false)
	call(other, "task_claim", map[string]string{"task_id": m.Task.ID, "request_id": "claim"}, true)
	var review domain.RequirementReview
	response, e := http.Get(site.URL + "/api/tasks/" + m.Task.ID + "/requirement-review")
	if e != nil {
		t.Fatal(e)
	}
	e = json.NewDecoder(response.Body).Decode(&review)
	response.Body.Close()
	if e != nil {
		t.Fatal(e)
	}
	if review.Mode != "development_alignment" || review.Status != "needs_input" {
		t.Fatal(review)
	}
	_, e = s.SaveRequirementReview(c, app.Human(), m.Task.ID, app.RequirementReviewInput{Action: "message", RequestID: "reply", Generation: review.Generation, Body: "问题理解偏了，按订单显示售后记录即可"})
	if e != nil {
		t.Fatal(e)
	}
	contextBody := call(first, "task_get_context", map[string]string{"task_id": m.Task.ID}, false)
	if !strings.Contains(contextBody, "问题理解偏了") || !strings.Contains(contextBody, "alignment_replied") {
		t.Fatal("missing product correction", contextBody)
	}
	latest, e := db.GetTask(c, m.Task.ID)
	if e != nil {
		t.Fatal(e)
	}
	resume := map[string]string{"task_id": m.Task.ID, "request_id": "resume", "claim_id": m.ClaimID, "version": fmt.Sprint(latest.Version), "revision": fmt.Sprint(latest.Revision), "body": "已理解为按订单显示售后记录，可以继续开发"}
	call(other, "task_resume_development", resume, true)
	work := call(other, "work_list", nil, false)
	if strings.Contains(work, m.Task.ID) {
		t.Fatal("other client sees reserved work", work)
	}
	// Re-authorization/rotated access credentials keep the authenticated client binding.
	rotated := connect("rotated", "original-client")
	var resumed domain.Mutation
	if e = json.Unmarshal([]byte(call(rotated, "task_resume_development", resume, false)), &resumed); e != nil {
		t.Fatal(e)
	}
	if resumed.Task.Status != "doing" || resumed.Task.ClaimClientID != "original-client" || resumed.ClaimID == "" {
		t.Fatal(resumed)
	}
	call(other, "task_resume_development", resume, true)
	call(other, "task_claim", map[string]string{"task_id": m.Task.ID, "request_id": "steal"}, true)
}
