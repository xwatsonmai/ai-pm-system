package server_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/server"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductManagementPersonalMCP(t *testing.T) {
	c := context.Background()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := &app.Service{Store: db}
	p, e := s.SaveProject(c, domain.Project{Name: "产品工作台"})
	if e != nil {
		t.Fatal(e)
	}
	p.Memberships = []domain.ProjectMember{{UserID: "employee", Role: "product"}}
	db.PutProject(c, p)
	db.PutMCPAccount(c, domain.MCPAccount{UserID: "employee", Enabled: true})
	site := httptest.NewServer((&server.Server{App: s}).Handler())
	defer site.Close()
	const token = "test-personal-product"
	db.PutOAuthGrant(c, domain.OAuthGrant{ID: "g", UserID: "employee", UserName: "产品", ProjectID: p.ID, Resource: site.URL + "/mcp", AccessHash: app.Hash(token), RefreshHash: app.Hash("refresh"), AccessExpiresAt: domain.Now() + 60000, RefreshExpiresAt: domain.Now() + 120000, AccountGeneration: 1}, "")
	connect := func(raw string) *mcp.ClientSession {
		t.Helper()
		client := mcp.NewClient(&mcp.Implementation{Name: "product-test", Version: "1"}, nil)
		session, e := client.Connect(c, &mcp.StreamableClientTransport{Endpoint: site.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{raw}}, DisableStandaloneSSE: true}, nil)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { session.Close() })
		return session
	}
	session := connect(token)
	tools, e := session.ListTools(c, nil)
	if e != nil || len(tools.Tools) != 41 {
		t.Fatal(len(tools.Tools), e)
	}
	call := func(name string, args map[string]string, wantError bool) string {
		t.Helper()
		r, e := session.CallTool(c, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil {
			if wantError {
				return ""
			}
			t.Fatal(e)
		}
		if r.IsError != wantError {
			t.Fatalf("%s: %v %s", name, r.IsError, r.Content[0].(*mcp.TextContent).Text)
		}
		return r.Content[0].(*mcp.TextContent).Text
	}
	grant := func(request string) map[string]string {
		return map[string]string{"request_id": request, "user_confirmed": "true", "user_instruction": "按我们讨论的方案保存这个产品操作"}
	}
	bad := grant("bad")
	bad["input"] = `{"title":"无授权"}`
	bad["user_confirmed"] = "false"
	call("requirement_create", bad, true)
	args := grant("file")
	args["name"] = "产品说明.md"
	args["content_base64"] = base64.StdEncoding.EncodeToString([]byte("# 原始说明\n保留完整文档。"))
	var uploaded struct{ Attachment domain.Attachment }
	json.Unmarshal([]byte(call("requirement_attachment_upload", args, false)), &uploaded)
	if repeat := call("requirement_attachment_upload", args, false); !strings.Contains(repeat, uploaded.Attachment.ID) {
		t.Fatal("file duplicate")
	}
	args = grant("create")
	args["input"] = fmt.Sprintf(`{"title":"质检体验","kind":"bug","description":"完整图文原文","acceptance":["结果正确"],"attachment_ids":[%q]}`, uploaded.Attachment.ID)
	var made domain.Mutation
	json.Unmarshal([]byte(call("requirement_create", args, false)), &made)
	if made.Task.ProjectVersionID == "" || made.Task.Status != "clarify" || len(made.Task.Attachments) != 1 {
		t.Fatal(made)
	}
	if again := call("requirement_create", args, false); !strings.Contains(again, made.Task.ID) {
		t.Fatal("create duplicate")
	}
	var review domain.RequirementReview
	json.Unmarshal([]byte(call("requirement_review_get", map[string]string{"task_id": made.Task.ID}, false)), &review)
	if review.Status != "failed" {
		t.Fatal("missing-model failure did not preserve creation", review)
	}
	edit := grant("edit")
	edit["task_id"] = made.Task.ID
	edit["input"] = fmt.Sprintf(`{"title":"质检体验优化","version":%d}`, made.Task.Version)
	var changed domain.Mutation
	json.Unmarshal([]byte(call("requirement_update", edit, false)), &changed)
	if changed.Task.Kind != "bug" || changed.Task.Description != "完整图文原文" || len(changed.Task.Attachments) != 1 {
		t.Fatal("partial fields lost", changed)
	}
	previousGeneration := review.Generation
	json.Unmarshal([]byte(call("requirement_review_get", map[string]string{"task_id": made.Task.ID}, false)), &review)
	if review.TaskRevision != changed.Task.Revision || review.Source.Title != changed.Task.Title || review.Generation <= previousGeneration {
		t.Fatal("new revision was not independently reviewed", review)
	}
	newGeneration := review.Generation
	first := call("requirement_update", edit, false)
	if !strings.Contains(first, changed.Task.ID) {
		t.Fatal(first)
	}
	json.Unmarshal([]byte(call("requirement_review_get", map[string]string{"task_id": made.Task.ID}, false)), &review)
	if review.Generation != newGeneration {
		t.Fatal("network retry repeated review", review)
	}
	message := grant("message")
	message["task_id"] = made.Task.ID
	message["input"] = fmt.Sprintf(`{"expected_task_version":%d,"body":"我不填表，你先解释问题的意思"}`, changed.Task.Version)
	call("requirement_review_message", message, false)
	json.Unmarshal([]byte(call("requirement_review_get", map[string]string{"task_id": made.Task.ID}, false)), &review)
	supplement := review.Messages[len(review.Messages)-2]
	if review.Status != "failed" || review.Messages[len(review.Messages)-1].Action != "review_failed" || supplement.Body != "我不填表，你先解释问题的意思" || supplement.Authorization.UserID != "employee" || supplement.ActorID != "employee" || supplement.ActorKind != "human" || supplement.Actor != "产品" {
		t.Fatal("saved supplement/audit lost", review)
	}
	// Simulate the independent agent finishing a later retry; confirmation is an
	// explicit product action, never delegated through ordinary developer actions.
	retry := grant("retry")
	retry["task_id"] = made.Task.ID
	retry["input"] = fmt.Sprintf(`{"action":"retry","expected_task_version":%d}`, changed.Task.Version)
	// API service is absent in this test, so prepare the retry using the service.
	r, e := s.SaveReviewFromMCP(c, domain.Actor{ID: "employee", UserID: "employee", Kind: "developer", Name: "产品 · MCP", ProjectID: p.ID}, made.Task.ID, app.RequirementReviewInput{Action: "retry", ExpectedTaskVersion: changed.Task.Version, RequestID: "prepare-confirm"}, app.ProductAuthorization{UserConfirmed: true, UserInstruction: "重新评审"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.BeginRequirementReview(c, made.Task.ID, r.Generation, "agent"); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishRequirementReview(c, made.Task.ID, r.Generation, "agent", &domain.RequirementReviewResult{Body: "需求明确", Decision: "ready", Proposal: &domain.RequirementProposal{Description: "确认稿", Acceptance: []string{"正确"}}}, ""); e != nil {
		t.Fatal(e)
	}
	confirm := grant("confirm")
	confirm["task_id"] = made.Task.ID
	confirm["version"] = fmt.Sprint(changed.Task.Version)
	confirm["generation"] = fmt.Sprint(r.Generation)
	call("requirement_review_confirm", confirm, false)
	fresh, _ := db.GetTask(c, made.Task.ID)
	if fresh.Status != "ready" {
		t.Fatal(fresh)
	}
	// An accepted source can be iterated through the real product MCP; the old
	// document and delivery stay complete, while the child starts a fresh review.
	sourceMade, e := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "已验收的退款能力", Description: "已验收原文与上下文", Acceptance: []string{"退款可追溯"}, AttachmentIDs: []string{uploaded.Attachment.ID}, RequestID: "iteration-source"})
	if e != nil {
		t.Fatal(e)
	}
	source := sourceMade.Task
	for _, stage := range []string{"ready", "claim", "submit", "test_pass", "mark_deployed", "accept"} {
		in := app.Action{Action: stage, RequestID: "source-" + stage, Revision: source.Revision}
		if stage == "submit" {
			in.Submission = &domain.Submission{Summary: "已实现", SHA: "source-sha", Tests: "通过"}
		}
		if stage == "test_pass" {
			in.Version = source.Version
			in.TestVerification = &domain.TestVerification{Revision: source.Revision, SHA: source.Submission.SHA, Environment: "测试", Note: "产品验证通过"}
		}
		if stage == "mark_deployed" {
			in.Deployment = &domain.Deployment{Revision: source.Revision, SHA: "source-sha", Environment: "fixture", Body: "实际发布"}
		}
		if stage == "accept" {
			in.Review = &domain.Review{Revision: source.Revision, SHA: "source-sha", Environment: "fixture", Checks: []string{"passed"}}
		}
		m, e := s.Act(c, app.Human(), source.ID, in)
		if e != nil {
			t.Fatal(e)
		}
		source = m.Task
	}
	iterationArgs := grant("iterate")
	iterationArgs["task_id"] = source.ID
	iterationArgs["input"] = fmt.Sprintf(`{"source_version":%d,"title":"退款能力追加迭代","description":"新增部分退款"}`, source.Version)
	var child domain.Mutation
	json.Unmarshal([]byte(call("requirement_iterate", iterationArgs, false)), &child)
	if child.Task.Iteration == nil || child.Task.Iteration.Source.Description != "已验收原文与上下文" || len(child.Task.Iteration.Source.Attachments) != 1 || child.Task.Status != "clarify" || child.Task.ProjectVersionID != source.ProjectVersionID || child.Task.Submission != nil {
		t.Fatal("MCP iteration lost accepted context", child)
	}
	if !strings.Contains(call("requirement_iterate", iterationArgs, false), child.Task.ID) {
		t.Fatal("iteration retry duplicated task")
	}
	if history := call("requirement_iterations", map[string]string{"task_id": child.Task.ID, "limit": "1"}, false); !strings.Contains(history, `"total":1`) || !strings.Contains(history, source.ID) {
		t.Fatal(history)
	}
	json.Unmarshal([]byte(call("requirement_review_get", map[string]string{"task_id": child.Task.ID}, false)), &review)
	if review.Status != "failed" || review.Source.Iteration == nil {
		t.Fatal("review failure dropped iteration context", review)
	}
	parentAfter, _ := db.GetTask(c, source.ID)
	if parentAfter.Status != "done" || parentAfter.Version != source.Version {
		t.Fatal("iteration reopened source")
	}
	wrong := grant("iterate-unaccepted")
	wrong["task_id"] = child.Task.ID
	wrong["input"] = fmt.Sprintf(`{"source_version":%d,"title":"不该创建"}`, child.Task.Version)
	call("requirement_iterate", wrong, true)
	var current app.ProjectVersionDetail
	json.Unmarshal([]byte(call("project_milestone_get", nil, false)), &current)
	second := grant("second")
	second["input"] = `{"name":"不能并行"}`
	call("project_version_create", second, true)
	versionEdit := grant("version-edit")
	versionEdit["project_version_id"] = current.ProjectVersion.ID
	versionEdit["input"] = fmt.Sprintf(`{"name":"质检里程碑","version":%d,"goal":"产品体验"}`, current.ProjectVersion.Version)
	call("project_version_update", versionEdit, false)
	context := call("project_version_get_context", map[string]string{"project_version_id": current.ProjectVersion.ID}, false)
	if !strings.Contains(context, "employee") || !strings.Contains(context, versionEdit["user_instruction"]) {
		t.Fatal("version audit missing", context)
	}
	if timeline := call("project_timeline", map[string]string{"limit": "1"}, false); !strings.Contains(timeline, `"has_more":true`) || !strings.Contains(timeline, `"git"`) {
		t.Fatal(timeline)
	}
	other, _ := s.SaveProject(c, domain.Project{Name: "other"})
	foreign, _ := s.CreateTask(c, app.Human(), other.ID, app.TaskInput{Title: "private", RequestID: "foreign"})
	call("requirement_review_get", map[string]string{"task_id": foreign.Task.ID}, true)
	call("requirement_iterations", map[string]string{"task_id": foreign.Task.ID}, true)
	foreignIteration := grant("foreign-iteration")
	foreignIteration["task_id"] = foreign.Task.ID
	foreignIteration["input"] = `{"source_version":1,"title":"无权创建"}`
	call("requirement_iterate", foreignIteration, true)
	_, legacy, e := s.CreateToken(c, p.ID, "legacy")
	if e != nil {
		t.Fatal(e)
	}
	out, e := connect(legacy).CallTool(c, &mcp.CallToolParams{Name: "requirement_create", Arguments: args})
	if e != nil || !out.IsError {
		t.Fatal("legacy product writes", out, e)
	}
	var latest app.ProjectVersionDetail
	json.Unmarshal([]byte(call("project_milestone_get", nil, false)), &latest)
	closing := grant("close")
	closing["project_version_id"] = latest.ProjectVersion.ID
	closing["action"] = "close"
	closing["version"] = fmt.Sprint(latest.ProjectVersion.Version)
	closing["body"] = "本期结束，继续下期"
	call("project_version_manage", closing, false)
	fresh, _ = db.GetTask(c, made.Task.ID)
	if fresh.ProjectVersionID == latest.ProjectVersion.ID || fresh.ProjectVersionID == "" || fresh.Status != "ready" {
		t.Fatal("closing via MCP lost live work", fresh)
	}
}
