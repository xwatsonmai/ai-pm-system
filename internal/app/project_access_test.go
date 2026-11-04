package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
)

func accessFixture(t *testing.T) (*app.Service, domain.Project, domain.Actor, domain.Actor) {
	t.Helper()
	s, _ := setup(t)
	c := context.Background()
	owner := domain.Actor{ID: "role-owner", Kind: "human", Name: "Owner"}
	member := domain.Actor{ID: "role-member", Kind: "human", Name: "Member"}
	for _, a := range []domain.Actor{owner, member} {
		if e := s.Store.PutUser(c, domain.User{ID: a.ID, Name: a.Name}); e != nil {
			t.Fatal(e)
		}
	}
	p, e := s.SaveProjectAs(c, owner, domain.Project{Name: "Role test"})
	if e != nil {
		t.Fatal(e)
	}
	return s, p, owner, member
}
func TestCustomProjectRolesDoNotInferPrivilegesOrDutiesFromNames(t *testing.T) {
	s, p, owner, member := accessFixture(t)
	c := context.Background()
	p, e := s.SaveProjectRole(c, owner, p.ID, "", app.RoleInput{Name: "观察者", Pages: []string{"overview"}, Version: p.AccessVersion})
	if e != nil {
		t.Fatal(e)
	}
	role := p.ProjectRoles[len(p.ProjectRoles)-1]
	p, e = s.SetProjectMemberAccess(c, owner, p.ID, member.ID, []string{role.ID}, []string{"tester"}, false, p.AccessVersion)
	if e != nil {
		t.Fatal(e)
	}
	if !app.CanAccessPage(member, p, "overview") || app.CanAccessPage(member, p, "tasks") || app.CanManage(member, p) {
		t.Fatal("custom grants ignored")
	}
	p, e = s.SaveProjectRole(c, owner, p.ID, role.ID, app.RoleInput{Name: "管理员外观", Pages: []string{"overview", "tasks"}, Version: p.AccessVersion})
	if e != nil {
		t.Fatal(e)
	}
	if app.CanManage(member, p) || !app.CanAccessPage(member, p, "tasks") {
		t.Fatal("name became authorization")
	}
	var m domain.ProjectMember
	for _, v := range p.Memberships {
		if v.UserID == member.ID {
			m = v
		}
	}
	if !m.ResponsibleFor("tester") || m.ResponsibleFor("product") || m.ResponsibleFor("developer") {
		t.Fatal("role rename changed notification duties", m)
	}
	// Removing a page revokes an already-existing personal MCP actor as well.
	oauth := domain.Actor{ID: member.ID, UserID: member.ID, Kind: "developer", ProjectID: p.ID}
	p, e = s.SaveProjectRole(c, owner, p.ID, role.ID, app.RoleInput{Name: "只读概览", Pages: []string{"overview"}, Version: p.AccessVersion})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RequirePages(c, oauth, p.ID, "tasks"); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("old connection retained revoked page", e)
	}
	if _, e = s.CreateRequirementFromMCP(c, oauth, app.TaskInput{Title: "Denied", RequestID: "no"}, app.ProductAuthorization{UserConfirmed: true, UserInstruction: "创建"}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("MCP bypassed page", e)
	}
	if _, e = s.DeleteProjectRole(c, owner, p.ID, role.ID, "", p.AccessVersion); e == nil {
		t.Fatal("assigned role deleted without replacement")
	}
	p, e = s.DeleteProjectRole(c, owner, p.ID, role.ID, "developer", p.AccessVersion)
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range p.Memberships {
		if m.UserID == member.ID && (!slices.Equal(m.Roles, []string{"developer"}) || !slices.Equal(m.Responsibilities, []string{"tester"})) {
			t.Fatal("replacement lost independent duties", m)
		}
	}
}
func TestProjectRoleLastManagerAndConcurrentEdits(t *testing.T) {
	s, p, owner, _ := accessFixture(t)
	c := context.Background()
	if _, e := s.SaveProjectRole(c, owner, p.ID, "admin", app.RoleInput{Name: "管理员", Pages: []string{"overview"}, Version: p.AccessVersion}); e == nil {
		t.Fatal("last manager stripped")
	}
	if _, e := s.SetProjectMemberAccess(c, owner, p.ID, owner.ID, []string{"product"}, []string{}, false, p.AccessVersion); e == nil {
		t.Fatal("last manager demoted")
	}
	if _, e := s.DeleteProjectRole(c, owner, p.ID, "admin", "product", p.AccessVersion); e == nil {
		t.Fatal("last manager replaced with nonmanager")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"Role A", "Role B"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_, e := s.SaveProjectRole(c, owner, p.ID, "", app.RoleInput{Name: name, Pages: []string{}, Version: p.AccessVersion})
			results <- e
		}(name)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, domain.ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("stale access revision accepted", success, conflict)
	}
}
func TestProjectContextFiltersRestrictedPages(t *testing.T) {
	s, p, owner, member := accessFixture(t)
	c := context.Background()
	p.Repository = "https://example.com/repo.git"
	p.Branch = "main"
	p.SHA = "private-sha"
	p.CredentialID = "private-credential"
	if e := s.Store.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CreateTask(c, owner, p.ID, app.TaskInput{Title: "Secret requirement", RequestID: "task"}); e != nil {
		t.Fatal(e)
	}
	p, e := s.SaveProjectRole(c, owner, p.ID, "", app.RoleInput{Name: "概览", Pages: []string{"overview"}, Version: p.AccessVersion})
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.SetProjectMemberAccess(c, owner, p.ID, member.ID, []string{p.ProjectRoles[len(p.ProjectRoles)-1].ID}, []string{}, false, p.AccessVersion)
	if e != nil {
		t.Fatal(e)
	}
	out, e := s.Context(c, member, p.ID, "")
	if e != nil {
		t.Fatal(e)
	}
	filtered := out["project"].(domain.Project)
	if filtered.Repository != "" || filtered.SHA != "" || filtered.CredentialID != "" || len(filtered.ProjectRoles) != 0 || len(out["tasks"].([]domain.Task)) != 0 || len(out["project_versions"].([]app.ProjectVersionSummary)) != 0 {
		t.Fatal("aggregate leaked restricted page", out)
	}
}

func TestReviewsOnlyPageCanTestFileBugReleaseAndAccept(t *testing.T) {
	s, p, owner, reviewer := accessFixture(t)
	c := context.Background()
	p, e := s.SaveProjectRole(c, owner, p.ID, "", app.RoleInput{Name: "验收人员", Pages: []string{"reviews"}, Version: p.AccessVersion})
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.SetProjectMemberAccess(c, owner, p.ID, reviewer.ID, []string{p.ProjectRoles[len(p.ProjectRoles)-1].ID}, []string{"tester"}, false, p.AccessVersion)
	if e != nil {
		t.Fatal(e)
	}
	task := testingRequirement(t, s, p.ID)
	if _, e = s.CreateTask(c, reviewer, p.ID, app.TaskInput{Title: "独立需求", RequestID: "standalone"}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("review page created ordinary task", e)
	}
	feedback, e := s.CreateTask(c, reviewer, p.ID, app.TaskInput{Title: "测试反馈", Kind: "bug", Description: "实际测试问题", ParentRequirementID: task.ID, ParentVersion: task.Version, RequestID: "linked"})
	if e != nil {
		t.Fatal("reviewer cannot file linked bug", e)
	}
	if _, e = s.SaveAttachment(c, reviewer, p.ID, "evidence.txt", []byte("test evidence")); e != nil {
		t.Fatal("reviewer cannot upload bug evidence", e)
	}
	if _, e = s.Act(c, reviewer, feedback.Task.ID, app.Action{Action: "close", Body: "反馈无需继续处理", RequestID: "close-feedback"}); e != nil {
		t.Fatal("reviewer cannot resolve feedback", e)
	}
	// Use another requirement to run a complete testing -> release -> acceptance.
	task = testingRequirement(t, s, p.ID)
	passed, e := s.Act(c, reviewer, task.ID, app.Action{Action: "test_pass", Version: task.Version, RequestID: "review-only-test", TestVerification: &domain.TestVerification{Revision: task.Revision, SHA: task.Submission.SHA, Environment: "test", Note: "验证通过"}})
	if e != nil {
		t.Fatal("reviewer cannot pass test", e)
	}
	deployed, e := s.Act(c, reviewer, task.ID, app.Action{Action: "mark_deployed", Version: passed.Task.Version, RequestID: "review-only-release", Deployment: &domain.Deployment{Revision: task.Revision, SHA: task.Submission.SHA, Environment: "production", Body: "实际部署记录"}})
	if e != nil {
		t.Fatal("reviewer cannot register deployment", e)
	}
	accepted, e := s.Act(c, reviewer, task.ID, app.Action{Action: "accept", Version: deployed.Task.Version, RequestID: "review-only-accept", Review: &domain.Review{Revision: task.Revision, SHA: task.Submission.SHA, Environment: "production", Checks: []string{"passed"}, Note: "验收通过"}})
	if e != nil || accepted.Task.Status != "done" {
		t.Fatal("reviewer cannot accept", accepted, e)
	}
	if _, e = s.Artifact(c, reviewer, domain.Artifact{ProjectID: p.ID, Kind: "deployment", SHA: "actual-sha", Environment: "production", Body: "实际交付证据"}); e != nil {
		t.Fatal("reviewer cannot record evidence", e)
	}
	for _, action := range []string{"test_pass", "verify_bug", "return_bug", "start_retest", "accept", "reject", "mark_deployed"} {
		if e = s.RequireTaskAction(c, reviewer, task, action); e != nil {
			t.Fatalf("reviews denied %s: %v", action, e)
		}
	}
	if e = s.RequireTaskAction(c, reviewer, task, "close"); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("review page can close ordinary requirements", e)
	}
}
