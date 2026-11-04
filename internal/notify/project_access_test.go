package notify_test

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/notify"
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
