package notify_test

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/notify"
	"testing"
)

func TestRequirementChangeRequiresFullProductReviewAndHidesTransferredFeedback(t *testing.T) {
	p := domain.Project{ID: "p", OwnerID: "pm", Memberships: []domain.ProjectMember{{UserID: "pm", Roles: []string{"product"}}, {UserID: "dev", Roles: []string{"developer"}}}}
	task := domain.Task{ID: "requirement", ProjectID: "p", Kind: "requirement", Status: "clarify", Revision: 1, Creator: &domain.ActorRef{ID: "pm"}, RequirementChange: &domain.RequirementChange{ID: "change", Status: "reviewing"}}
	review := domain.RequirementReview{TaskID: task.ID, TaskRevision: 1, Status: "ready_to_confirm", Generation: 1}
	todos := notify.Derive(task, &review, p)
	if len(todos) != 1 || todos[0].Kind != "review_confirm" || todos[0].Action != "" {
		t.Fatal("change could be confirmed from a one-click card without reviewing transfer", todos)
	}
	bug := domain.Task{ID: "feedback", ProjectID: "p", Kind: "bug", Status: "ready", ChangeRequestID: "change", ParentRequirementID: task.ID, Creator: &domain.ActorRef{ID: "pm"}}
	if todos = notify.Derive(bug, nil, p); len(todos) != 0 {
		t.Fatal("transferred feedback sent a claim notification", todos)
	}
}
