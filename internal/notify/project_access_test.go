package notify_test

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/notify"
	"context"
	"slices"
	"testing"
)

func TestTodoResponsibilitiesRequireTheirPageAndDoNotUseRoleName(t *testing.T) {
	p := domain.Project{ID: "p", AccessVersion: 1, ProjectRoles: []domain.ProjectRole{{ID: "custom", Name: "管理员", Pages: []string{"overview"}, Version: 1}}, Memberships: []domain.ProjectMember{{UserID: "u", Roles: []string{"custom"}, Responsibilities: []string{"tester"}}}}
	task := domain.Task{ID: "t", ProjectID: p.ID, Kind: "requirement", Status: "testing", Revision: 1, Submission: &domain.Submission{SHA: "sha", Environment: "test"}}
	if got := notify.Derive(task, nil, p); len(got) != 0 {
		t.Fatal("notification exposed a forbidden page", got)
	}
	p.ProjectRoles[0].Pages = []string{"reviews"}
	got := notify.Derive(task, nil, p)
	if len(got) != 1 || len(got[0].Recipients) != 1 || got[0].Recipients[0] != "u" {
		t.Fatal("custom role's independent duty ignored", got)
	}
	p.Memberships[0].Responsibilities = []string{}
	p.ProjectRoles[0].Name = "测试"
	if got = notify.Derive(task, nil, p); len(got) != 0 {
		t.Fatal("role label implied notification duty", got)
	}
}

func notificationAccessFixture() domain.Project {
	return domain.Project{ID: "prj_1", Name: "Project", AccessVersion: 1,
		ProjectRoles: []domain.ProjectRole{
			{ID: "operations", Name: "运营", Pages: []string{"chat"}, Actions: []string{domain.ActionAskAgent, domain.ActionCreateBug, domain.ActionCreateCollaboration}},
			{ID: "business", Name: "业务负责人", Pages: []string{"tasks", "reviews"}, Actions: []string{}},
			{ID: "delivery", Name: "交付负责人", Pages: []string{"tasks", "versions"}, Actions: []string{}},
			{ID: "misleading", Name: "产品", Pages: []string{"tasks", "reviews", "versions"}, Actions: []string{}},
		},
		Memberships: []domain.ProjectMember{
			{UserID: "operator", Roles: []string{"operations"}, Responsibilities: []string{"tester"}},
			{UserID: "manager", Roles: []string{"business"}, Responsibilities: []string{"product"}},
			{UserID: "developer", Roles: []string{"delivery"}, Responsibilities: []string{"developer"}},
			{UserID: "no-duty", Roles: []string{"misleading"}, Responsibilities: []string{}},
		},
	}
}

func notificationTask() domain.Task {
	return domain.Task{ID: "tsk_1", ProjectID: "prj_1", Kind: "bug", Title: "Operator Bug", Version: 1, Revision: 1,
		Creator:    &domain.ActorRef{ID: "operator", Kind: "human"},
		Claimant:   "operator",
		Acceptance: []string{"Expected behavior restored"},
		Submission: &domain.Submission{SHA: "0123456789abcdef", Revision: 1, Environment: "test"},
		Deployment: &domain.Deployment{SHA: "0123456789abcdef", Revision: 1, Environment: "production"},
	}
}

func TestTodoFallsBackWhenPreferredRecipientsCannotAccessTheWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name, status, review, kind, recipient string
	}{
		{"review reply", "clarify", "needs_input", "review_reply", "manager"},
		{"review confirmation", "clarify", "ready_to_confirm", "review_confirm", "manager"},
		{"Bug rejection", "clarify", "rejection_pending", "bug_rejection", "manager"},
		{"alignment reply", "doing", "", "alignment_reply", "manager"},
		{"testing", "testing", "", "test", "manager"},
		{"retesting", "retest", "", "retest", "manager"},
		{"Bug verification", "verifying", "", "verify", "manager"},
		{"release", "pending_release", "", "release", "developer"},
		{"acceptance", "review", "", "acceptance", "manager"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, task := notificationAccessFixture(), notificationTask()
			task.Status = tc.status
			var review *domain.RequirementReview
			if tc.review != "" {
				review = &domain.RequirementReview{TaskID: task.ID, Status: tc.review, Generation: 1}
			}
			if tc.kind == "alignment_reply" {
				task.Alignment = &domain.DevelopmentAlignment{Status: "waiting_product"}
			}
			got := notify.Derive(task, review, p)
			if len(got) != 1 || got[0].Kind != tc.kind || !slices.Equal(got[0].Recipients, []string{tc.recipient}) {
				t.Fatalf("unreadable creator/claimant/tester swallowed the fallback: %+v", got)
			}
			// The role named 产品 has no notification duty. If the actual fallback
			// loses its page, neither it nor a member with only that name may receive.
			for i := range p.ProjectRoles {
				if p.ProjectRoles[i].ID == "business" || p.ProjectRoles[i].ID == "delivery" {
					p.ProjectRoles[i].Pages = []string{"chat"}
				}
			}
			if got = notify.Derive(task, review, p); len(got) != 0 {
				t.Fatal("fallback ignored page access or explicit empty duties", got)
			}
		})
	}
}

func TestEligibleCreatorKeepsPriorityWithoutANotificationDuty(t *testing.T) {
	p, task := notificationAccessFixture(), notificationTask()
	task.Status, task.Creator.ID = "review", "no-duty"
	got := notify.Derive(task, nil, p)
	if len(got) != 1 || !slices.Equal(got[0].Recipients, []string{"no-duty"}) {
		t.Fatal("a readable creator lost the existing direct-recipient rule", got)
	}
}

func TestChatOnlyBugCreatorDeliversAcceptanceToAuthorizedProduct(t *testing.T) {
	c := context.Background()
	db, hub, _ := setup(t)
	configure(t, hub)
	if err := db.PutProject(c, notificationAccessFixture()); err != nil {
		t.Fatal(err)
	}
	task := notificationTask()
	task.Status = "review"
	if err := db.CommitTask(c, task, 0, domain.Event{ProjectID: task.ProjectID, TaskID: task.ID, Kind: "deployed", At: domain.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"operator", "manager", "no-duty"} {
		if err := hub.Bind(c, "fake", user, "wx_"+user); err != nil {
			t.Fatal(err)
		}
	}
	if err := hub.Reconcile(c, task.ID); err != nil {
		t.Fatal(err)
	}
	hub.Dispatch(c)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.sent) != 1 || fake.sent["wx_manager"].ID == "" {
		t.Fatal("acceptance was lost or delivered to an unreadable/undesignated member", fake.sent)
	}
}
