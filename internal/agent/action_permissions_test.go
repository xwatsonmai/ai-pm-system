package agent

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func setRunActions(t *testing.T, m *Manager, r domain.Run, pages, actions []string) {
	t.Helper()
	c := context.Background()
	p, e := m.App.Store.GetProject(c, r.ProjectID)
	if e != nil {
		t.Fatal(e)
	}
	role := domain.ProjectRole{ID: "action-fixture", Name: "Operations", Pages: pages, Actions: actions, Version: 1}
	found := false
	for i := range p.ProjectRoles {
		if p.ProjectRoles[i].ID == role.ID {
			p.ProjectRoles[i] = role
			found = true
		}
	}
	if !found {
		p.ProjectRoles = append(p.ProjectRoles, role)
	}
	for i := range p.Memberships {
		if p.Memberships[i].UserID == r.OwnerID {
			p.Memberships[i].Roles = []string{"action-fixture"}
		}
	}
	p.AccessVersion++
	if e = m.App.Store.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
}

func bugProposal() json.RawMessage {
	return json.RawMessage(`{"title":"退款列表按钮无响应","kind":"bug","description":"点击已支付订单的退款按钮没有弹窗","acceptance":["点击后显示退款弹窗"],"reason":"实际行为偏离预期","attachment_ids":[],"missing_details":[],"bug_details":{"context":"测试环境订单页","reproduction":"打开已支付订单，点击退款","actual":"没有弹窗","expected":"显示退款弹窗","scope":"恢复退款入口"}}`)
}

func TestOperationsQuestionCanCreateBugWithoutTaskOrCodeRead(t *testing.T) {
	m, db, r, _ := collaborationToolsFixture(t)
	c := context.Background()
	r.Prompt, r.StartedAt = "创建Bug", domain.Now()
	if e := db.PutRun(c, r); e != nil {
		t.Fatal(e)
	}
	setRunActions(t, m, r, []string{"chat"}, []string{domain.ActionAskAgent, domain.ActionCreateBug, domain.ActionCreateCollaboration})
	system := domain.Actor{ID: "platform", Kind: "platform", ProjectID: r.ProjectID}
	contextData, e := m.callTool(c, system, r, "capabilities", "project_context", json.RawMessage(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	capabilities := contextData.(map[string]any)["capabilities"].(map[string]any)
	if !slices.Equal(capabilities["readable_pages"].([]string), []string{"chat"}) || !slices.Contains(capabilities["actions"].([]string), domain.ActionCreateBug) || slices.Contains(capabilities["actions"].([]string), domain.ActionCreateRequirement) {
		t.Fatal("project_context advertised incorrect capabilities", capabilities)
	}
	for _, name := range []string{"requirement_pool", "requirement_detail", "repository_read", "project_version_list"} {
		if _, e := m.callTool(c, system, r, "read", name, json.RawMessage(`{}`)); !errors.Is(e, domain.ErrForbidden) {
			t.Fatalf("%s was readable: %v", name, e)
		}
	}
	if _, e := m.callTool(c, system, r, "requirement", "propose_requirement", json.RawMessage(`{"kind":"requirement"}`)); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("Bug capability proposed a requirement", e)
	}
	proposed, e := m.callTool(c, system, r, "propose", "propose_requirement", bugProposal())
	if e != nil {
		t.Fatal(e)
	}
	proposal := proposed.(domain.RequirementIntake)
	input, _ := json.Marshal(map[string]string{"proposal_id": proposal.ID, "action": "create", "confirmation": "创建Bug"})
	created, e := m.callTool(c, system, r, "confirm", "resolve_requirement_proposal", input)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := json.Marshal(created)
	var receipt struct {
		domain.RequirementIntake
		Receipt map[string]any `json:"creation_receipt"`
	}
	if e = json.Unmarshal(data, &receipt); e != nil {
		t.Fatal(e)
	}
	if receipt.TaskID == "" || receipt.Receipt["task_id"] != receipt.TaskID || receipt.Receipt["task_status"] != "ready" {
		t.Fatal("missing real receipt", string(data))
	}
	for _, field := range []string{"task", "description", "pending_questions", "review_status", "submission"} {
		if _, exists := receipt.Receipt[field]; exists {
			t.Fatal("create-only receipt contains task detail", field)
		}
	}
	if _, e = m.App.RequirementDetail(c, domain.Actor{ID: r.OwnerID, Kind: "human"}, r.ProjectID, app.RequirementDetailQuery{TaskID: receipt.TaskID}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("creation granted task read", e)
	}
	// Tool-level access remains type-specific for existing suggestions as well.
	p, _ := db.GetProject(c, r.ProjectID)
	p.ProjectRoles[len(p.ProjectRoles)-1].Actions = []string{domain.ActionAskAgent, domain.ActionCreateCollaboration}
	p.AccessVersion++
	if e = db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	if _, e = m.callTool(c, system, r, "revoked", "resolve_requirement_proposal", input); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("revoked Bug capability reused a receipt", e)
	}
}

func TestOldRequirementProposalCannotBeConfirmedByBugPermissionOrSpoofedKind(t *testing.T) {
	m, db, r, _ := collaborationToolsFixture(t)
	c := context.Background()
	r.Prompt = "创建吧"
	if e := db.PutRun(c, r); e != nil {
		t.Fatal(e)
	}
	proposal, e := m.App.ProposeRequirement(c, r.ID, app.RequirementIntakeInput{Title: "原需求建议", Kind: "requirement", Description: "用户希望增加筛选", Acceptance: []string{"支持筛选"}, Reason: "明确目标"})
	if e != nil {
		t.Fatal(e)
	}
	setRunActions(t, m, r, []string{"chat"}, []string{domain.ActionAskAgent, domain.ActionCreateBug})
	input, _ := json.Marshal(map[string]string{"proposal_id": proposal.ID, "action": "create", "confirmation": "创建吧", "kind": "bug"})
	if _, e = m.callTool(c, domain.Actor{ID: "platform", Kind: "platform", ProjectID: r.ProjectID}, r, "spoof", "resolve_requirement_proposal", input); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("persisted requirement kind was bypassed", e)
	}
	stored, _ := db.GetRequirementIntake(c, proposal.ID)
	if stored.Status != "suggested" || stored.TaskID != "" {
		t.Fatal("denied confirmation changed the proposal", stored)
	}
	setRunActions(t, m, r, []string{"chat", "reviews"}, []string{domain.ActionAskAgent, domain.ActionCreateLinkedBug})
	if _, e = m.callTool(c, domain.Actor{ID: "platform", Kind: "platform", ProjectID: r.ProjectID}, r, "linked", "propose_requirement", bugProposal()); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("linked-only capability created an independent proposal", e)
	}
}

func TestRunActionRevocationStopsGuardAndToolsButKeepsHistory(t *testing.T) {
	m, db, r, _ := collaborationToolsFixture(t)
	c := context.Background()
	setRunActions(t, m, r, []string{"chat"}, []string{domain.ActionAskAgent})
	ctx, cancel := context.WithCancel(c)
	defer cancel()
	go m.guardRunPermissions(ctx, cancel, r, false)
	p, _ := db.GetProject(c, r.ProjectID)
	p.ProjectRoles[len(p.ProjectRoles)-1].Actions = []string{}
	p.AccessVersion++
	if e := db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("revoked ask capability did not stop the worker")
	}
	if _, e := m.callTool(c, domain.Actor{ID: "platform", Kind: "platform", ProjectID: r.ProjectID}, r, "revoked", "project_context", json.RawMessage(`{}`)); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("revoked worker continued tools", e)
	}
	owner, e := m.runReadActor(c, r)
	if e != nil {
		t.Fatal("action revocation hid historical reading", e)
	}
	project, _ := db.GetProject(c, r.ProjectID)
	if !app.CanReadRun(owner, project, r) {
		t.Fatal("ask revocation removed run history")
	}
}

func TestManualTriageChecksBugActionWithPlatformWriter(t *testing.T) {
	m, db, r, _ := collaborationToolsFixture(t)
	c := context.Background()
	r.Kind = "triage"
	setRunActions(t, m, r, []string{"tasks"}, []string{domain.ActionRunAnalysis, domain.ActionCreateBug})
	input := json.RawMessage(`{"title":"独立的新问题","description":"现象与预期已确认","acceptance":"恢复预期"}`)
	system := domain.Actor{ID: "platform", Kind: "platform", ProjectID: r.ProjectID}
	out, e := m.callTool(c, system, r, "triage", "create_bug", input)
	if e != nil {
		t.Fatal(e)
	}
	task := out.(domain.Mutation).Task
	if task.Status != "ready" || task.Creator == nil || task.Creator.Kind != "platform" {
		t.Fatal("triage lost ready/platform behavior", task)
	}
	p, _ := db.GetProject(c, r.ProjectID)
	p.ProjectRoles[len(p.ProjectRoles)-1].Actions = []string{domain.ActionRunAnalysis}
	p.AccessVersion++
	if e = db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	if _, e = m.callTool(c, system, r, "revoked", "create_bug", input); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("platform worker bypassed revoked owner action", e)
	}
}

func TestReplyRequiresAskActionWhileResultRemainsReadable(t *testing.T) {
	m, _, v, _ := replyFixture(t)
	r := domain.Run{ProjectID: v.ProjectID, OwnerID: v.OwnerID}
	setRunActions(t, m, r, []string{"chat"}, []string{domain.ActionCreateCollaboration})
	if _, e := m.replySource(context.Background(), v.ID); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("reply bypassed missing ask action", e)
	}
	view, e := m.App.GetCollaboration(context.Background(), domain.Actor{ID: v.OwnerID, Kind: v.OwnerKind}, v.ID)
	if e != nil || view.Result == nil || !strings.Contains(view.Result.Summary, "logs") {
		t.Fatal("ask revocation hid completed investigation", view, e)
	}
}

func TestAutomaticRequirementReviewDoesNotRequireOwnersAnalysisAction(t *testing.T) {
	m, _, r, _ := collaborationToolsFixture(t)
	c := context.Background()
	setRunActions(t, m, r, []string{"chat"}, []string{domain.ActionAskAgent, domain.ActionCreateRequirement})
	created, e := m.App.CreateTask(c, domain.Actor{ID: r.OwnerID, Kind: "human"}, r.ProjectID, app.TaskInput{Title: "单独创建的需求", Kind: "requirement", Description: "原文", Acceptance: []string{"业务条件"}, RequestID: "create-only-requirement"})
	if e != nil {
		t.Fatal(e)
	}
	m.ReviewCreatedRequirement(created.Task.ID)
	review, e := m.App.Store.GetRequirementReview(c, created.Task.ID)
	if e != nil || (review.Status != "queued" && review.Status != "running" && review.Status != "failed") {
		t.Fatal("automatic review was blocked by missing manual analysis grant", review, e)
	}
	if strings.Contains(review.Error, domain.ErrForbidden.Error()) {
		t.Fatal("automatic review borrowed the creator's analysis action", review.Error)
	}
}
