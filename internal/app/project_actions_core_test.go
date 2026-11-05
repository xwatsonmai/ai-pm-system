package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"context"
	"errors"
	"testing"
)

func coreActionRole(t *testing.T, s *app.Service, p domain.Project, owner, member domain.Actor, pages, actions []string) domain.Project {
	t.Helper()
	var e error
	p, e = s.SaveProjectRole(context.Background(), owner, p.ID, "", app.RoleInput{Name: domain.ID("Action role "), Pages: pages, Actions: actions, Version: p.AccessVersion})
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.SetProjectMemberAccess(context.Background(), owner, p.ID, member.ID, []string{p.ProjectRoles[len(p.ProjectRoles)-1].ID}, []string{}, false, p.AccessVersion)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestCoreActionUnionExplicitRevocationAndLegacyPUT(t *testing.T) {
	s, p, owner, member := accessFixture(t)
	c := context.Background()
	p = coreActionRole(t, s, p, owner, member, []string{"chat"}, []string{domain.ActionAskAgent, domain.ActionCreateBug, domain.ActionCreateCollaboration})
	first := p.ProjectRoles[len(p.ProjectRoles)-1].ID
	p, e := s.SaveProjectRole(c, owner, p.ID, "", app.RoleInput{Name: "Second grant", Pages: []string{}, Actions: []string{domain.ActionCreateRequirement}, Version: p.AccessVersion})
	if e != nil {
		t.Fatal(e)
	}
	second := p.ProjectRoles[len(p.ProjectRoles)-1].ID
	p, e = s.SetProjectMemberAccess(c, owner, p.ID, member.ID, []string{first, second}, []string{}, false, p.AccessVersion)
	if e != nil {
		t.Fatal(e)
	}
	if !app.CanPerformAction(member, p, domain.ActionCreateRequirement) || !app.CanPerformAction(member, p, domain.ActionCreateLinkedBug) || app.CanAccessPage(member, p, "tasks") {
		t.Fatal("action union changed read pages")
	}
	p, e = s.SaveProjectRole(c, owner, p.ID, first, app.RoleInput{Name: "No actions", Pages: []string{"chat"}, Actions: []string{}, Version: p.AccessVersion})
	if e != nil {
		t.Fatal(e)
	}
	if app.CanPerformAction(member, p, domain.ActionCreateBug) || !app.CanPerformAction(member, p, domain.ActionCreateRequirement) {
		t.Fatal("explicit revoke or union was ignored")
	}
	// An old client's omitted actions cannot revive an explicitly revoked grant.
	p, e = s.SaveProjectRole(c, owner, p.ID, first, app.RoleInput{Name: "Old PUT", Pages: []string{"chat", "tasks"}, Version: p.AccessVersion})
	if e != nil {
		t.Fatal(e)
	}
	if app.CanPerformAction(member, p, domain.ActionCreateBug) || app.CanPerformAction(member, p, domain.ActionRunAnalysis) {
		t.Fatal("old PUT broadened action grants from new pages")
	}
	p, e = s.SetProjectMemberAccess(c, owner, p.ID, member.ID, []string{first}, []string{}, false, p.AccessVersion)
	if e != nil {
		t.Fatal(e)
	}
	if app.CanPerformAction(member, p, domain.ActionCreateRequirement) {
		t.Fatal("removed role kept its action")
	}
	p = coreActionRole(t, s, p, owner, member, []string{"reviews", "chat"}, nil)
	if app.CanPerformAction(member, p, domain.ActionCreateBug) || app.CanPerformAction(member, p, domain.ActionCreateRequirement) || !app.CanPerformAction(member, p, domain.ActionCreateLinkedBug) {
		t.Fatal("legacy reviews+chat broadened independent creation")
	}
	if app.CanPerformAction(domain.Actor{ID: "outsider", Kind: "human", PlatformAdmin: true}, p, domain.ActionCreateBug) {
		t.Fatal("platform admin gained business action")
	}
	if !app.CanPerformAction(domain.Actor{ID: "legacy-token", Kind: "developer", ProjectID: p.ID}, p, domain.ActionCreateBug) || app.CanPerformAction(domain.Actor{ID: "legacy-token", Kind: "developer", ProjectID: "other"}, p, domain.ActionCreateBug) {
		t.Fatal("legacy token project scope changed")
	}
}

func TestCoreChatOperatorCanFileBugAndCollaborationButNotRequirement(t *testing.T) {
	s, p, owner, member := accessFixture(t)
	c := context.Background()
	p = coreActionRole(t, s, p, owner, member, []string{"chat"}, []string{domain.ActionAskAgent, domain.ActionCreateBug, domain.ActionCreateCollaboration})
	bug, e := s.CreateTask(c, member, p.ID, app.TaskInput{Kind: "bug", Title: "Reported issue", Description: "Observed issue", RequestID: "operator-bug"})
	if e != nil {
		t.Fatal("chat operator cannot file an independent Bug", e)
	}
	if _, e = s.CreateTask(c, member, p.ID, app.TaskInput{Title: "Feature", RequestID: "operator-requirement"}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("Bug grant created a requirement", e)
	}
	if _, e = s.Context(c, member, p.ID, bug.Task.ID); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("creation action granted the task read page", e)
	}
	r := domain.Run{ID: "operator-question", ProjectID: p.ID, ConversationID: "operator-conversation", Kind: "question", OwnerID: member.ID, OwnerKind: member.Kind, Status: "succeeded"}
	if e = s.Store.PutRun(c, r); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ProposeRequirement(c, r.ID, app.RequirementIntakeInput{Kind: "requirement", Title: "Feature", Description: "Feature scope", Acceptance: []string{"Works"}, Reason: "Follow up"}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("question proposal bypassed requirement action", e)
	}
	proposal, e := s.ProposeRequirement(c, r.ID, app.RequirementIntakeInput{Kind: "bug", Title: "Confirmed issue", Description: "Observed issue", Acceptance: []string{"Issue corrected"}, Reason: "Follow up", BugDetails: &domain.BugDetails{Context: "Web", Reproduction: "Open page", Actual: "Wrong result", Expected: "Correct result", Scope: "Fix result"}})
	if e != nil {
		t.Fatal(e)
	}
	items, e := s.RequirementIntakes(c, member, p.ID)
	if e != nil || len(items) != 1 {
		t.Fatal("chat owner cannot read own proposal", e)
	}
	if _, e = s.CreateCollaboration(c, member, app.CollaborationInput{OriginRunID: r.ID, RequestID: "operator-collaboration", Title: "Investigate", Brief: "Read-only investigation", Environment: "test"}); e != nil {
		t.Fatal(e)
	}
	role := p.ProjectRoles[len(p.ProjectRoles)-1]
	p, e = s.SaveProjectRole(c, owner, p.ID, role.ID, app.RoleInput{Name: role.Name, Pages: role.Pages, Actions: []string{domain.ActionAskAgent}, Version: p.AccessVersion})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ResolveRequirementIntake(c, member, proposal.ID, app.ResolveRequirementIntakeInput{Action: "create", RequestID: "revoked-confirm"}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("old question card bypassed action revocation", e)
	}
	if _, e = s.CreateCollaboration(c, domain.Actor{ID: "platform", Kind: "platform", ProjectID: p.ID}, app.CollaborationInput{OriginRunID: r.ID, RequestID: "revoked-platform", Title: "Investigate again", Brief: "Read only", Environment: "test"}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("platform lent revoked collaboration authority", e)
	}
	if _, e = s.ResolveRequirementIntake(c, member, proposal.ID, app.ResolveRequirementIntakeInput{Action: "dismiss", RequestID: "dismiss-after-revoke"}); e != nil {
		t.Fatal("revocation prevented dismissing one's old suggestion", e)
	}
}

func TestCoreCreationCannotBeBypassedByKindConversionOrBugIteration(t *testing.T) {
	s, p, owner, member := accessFixture(t)
	c := context.Background()
	created, e := s.CreateTask(c, owner, p.ID, app.TaskInput{Kind: "bug", Title: "Existing Bug", Description: "Old issue", RequestID: "original-bug"})
	if e != nil {
		t.Fatal(e)
	}
	accepted, e := s.CreateTask(c, owner, p.ID, app.TaskInput{Title: "Accepted requirement", Description: "Accepted scope", Acceptance: []string{"Works"}, RequestID: "accepted-original"})
	if e != nil {
		t.Fatal(e)
	}
	accepted.Task = acceptIterationTask(t, s, accepted.Task)
	p = coreActionRole(t, s, p, owner, member, []string{"tasks", "chat"}, []string{domain.ActionCreateBug})
	input := app.TaskInput{Kind: "requirement", Title: created.Task.Title, Description: created.Task.Description, Version: created.Task.Version, RequestID: "convert-bug"}
	if _, e = s.EditTask(c, member, created.Task.ID, input); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("Bug was converted to a requirement without its action", e)
	}
	personal := domain.Actor{ID: member.ID, UserID: member.ID, Kind: "developer", ProjectID: p.ID}
	kind := "requirement"
	if _, e = s.PatchRequirementFromMCP(c, personal, created.Task.ID, app.RequirementPatch{Kind: &kind, Version: created.Task.Version, RequestID: "mcp-convert"}, app.ProductAuthorization{UserConfirmed: true, UserInstruction: "Convert this Bug"}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("MCP patch bypassed the kind action", e)
	}
	iteration := iterationInput(accepted.Task, "bug-iteration-bypass")
	iteration.Kind = "bug"
	if _, e = s.CreateRequirementIteration(c, member, accepted.Task.ID, iteration); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("kind=bug bypassed new requirement iteration permission", e)
	}
	if _, e = s.SaveRequirementReview(c, member, accepted.Task.ID, app.RequirementReviewInput{Action: "request_change", RequestID: "feedback-to-requirement", Body: "Turn feedback into new scope", ExpectedTaskVersion: accepted.Task.Version}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("requirement-change path bypassed initiation permission", e)
	}
}

func TestCorePlatformDelegationDoesNotLendActionOrAttachmentAccess(t *testing.T) {
	s, p, owner, member := accessFixture(t)
	c := context.Background()
	p = coreActionRole(t, s, p, owner, member, []string{"chat"}, []string{domain.ActionCreateBug})
	file, e := s.SaveAttachment(c, owner, p.ID, "private-draft.txt", []byte("Private draft"))
	if e != nil {
		t.Fatal(e)
	}
	writer := domain.Actor{ID: "platform", Kind: "platform", ProjectID: p.ID}
	delegated := app.WithActionActor(c, member)
	if _, e = s.CreateTask(delegated, writer, p.ID, app.TaskInput{Kind: "bug", Title: "Unsafe reuse", Description: "Issue", Acceptance: []string{"Fixed"}, AttachmentIDs: []string{file.ID}, RequestID: "delegated-draft"}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("platform borrowed another person's orphan draft", e)
	}
	created, e := s.CreateTask(delegated, writer, p.ID, app.TaskInput{Kind: "bug", Title: "Delegated Bug", Description: "Issue", Acceptance: []string{"Fixed"}, RequestID: "delegated-ready"})
	if e != nil || created.Task.Status != "ready" || created.Task.Creator == nil || created.Task.Creator.Kind != "platform" {
		t.Fatal("delegation changed triage writer/status", e, created.Task)
	}
	role := p.ProjectRoles[len(p.ProjectRoles)-1]
	p, e = s.SaveProjectRole(c, owner, p.ID, role.ID, app.RoleInput{Name: role.Name, Pages: role.Pages, Actions: []string{}, Version: p.AccessVersion})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RequireAction(delegated, writer, p.ID, domain.ActionCreateBug); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("stale delegated context retained revoked action", e)
	}
	if e = s.RequireAction(app.WithActionActor(c, domain.Actor{ID: "platform", Kind: "platform", ProjectID: p.ID}), writer, p.ID, domain.ActionCreateBug); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("invalid override fell back to platform", e)
	}
}

type revokeActionStore struct {
	storage.Store
	revoke func()
}

func (s *revokeActionStore) GetReceipt(c context.Context, key string) (domain.Receipt, error) {
	r, e := s.Store.GetReceipt(c, key)
	if s.revoke != nil {
		action := s.revoke
		s.revoke = nil
		action()
	}
	return r, e
}

func TestCoreActionRecheckedAtFinalWrite(t *testing.T) {
	s, p, owner, member := accessFixture(t)
	c := context.Background()
	p = coreActionRole(t, s, p, owner, member, []string{"chat"}, []string{domain.ActionCreateBug})
	role := p.ProjectRoles[len(p.ProjectRoles)-1]
	wrapped := &revokeActionStore{Store: s.Store}
	s.Store = wrapped
	wrapped.revoke = func() {
		_, e := s.SaveProjectRole(c, owner, p.ID, role.ID, app.RoleInput{Name: role.Name, Pages: role.Pages, Actions: []string{}, Version: p.AccessVersion})
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.CreateTask(c, member, p.ID, app.TaskInput{Kind: "bug", Title: "Race", RequestID: "revoke-between-check-and-commit"}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("write committed after its action was revoked", e)
	}
	tasks, e := s.Store.ListTasks(c, p.ID)
	if e != nil || len(tasks) != 0 {
		t.Fatal("revoked creation left a task", e)
	}
}

func TestCoreMCPExistingEditorCanUploadAfterCreationActionsRevoked(t *testing.T) {
	s, p, owner, member := accessFixture(t)
	c := context.Background()
	existing, e := s.CreateTask(c, owner, p.ID, app.TaskInput{Title: "Existing requirement", Description: "Original scope", RequestID: "existing-editor-source"})
	if e != nil {
		t.Fatal(e)
	}
	p = coreActionRole(t, s, p, owner, member, []string{"tasks"}, []string{})
	personal := domain.Actor{ID: member.ID, UserID: member.ID, Kind: "developer", ProjectID: p.ID}
	grant := app.ProductAuthorization{UserConfirmed: true, UserInstruction: "Attach this clarification to the existing requirement"}
	if app.CanPerformAction(personal, p, domain.ActionCreateRequirement) || app.CanPerformAction(personal, p, domain.ActionCreateBug) {
		t.Fatal("fixture retained an initiation grant")
	}
	file, e := s.UploadProductAttachment(c, personal, "clarification.txt", "existing-editor-upload", []byte("Clarification for the existing requirement"), grant)
	if e != nil {
		t.Fatal("existing editor cannot upload after initiation revocation", e)
	}
	description, ids := "Updated existing scope", []string{file.ID}
	updated, e := s.PatchRequirementFromMCP(c, personal, existing.Task.ID, app.RequirementPatch{Description: &description, AttachmentIDs: &ids, Version: existing.Task.Version, RequestID: "existing-editor-update"}, grant)
	if e != nil || updated.Task.Description != description || len(updated.Task.Attachments) != 1 || updated.Task.Attachments[0].ID != file.ID {
		t.Fatal("existing edit with the uploaded attachment failed", e)
	}
	for _, kind := range []string{"requirement", "bug"} {
		if _, e = s.CreateRequirementFromMCP(c, personal, app.TaskInput{Kind: kind, Title: "Denied new work", RequestID: "existing-editor-create-" + kind}, grant); !errors.Is(e, domain.ErrForbidden) {
			t.Fatal("upload/edit compatibility re-enabled creation", kind, e)
		}
	}
	tasks, e := s.Store.ListTasks(c, p.ID)
	if e != nil || len(tasks) != 1 || tasks[0].ID != existing.Task.ID {
		t.Fatal("compatibility path created extra work", e)
	}
}
