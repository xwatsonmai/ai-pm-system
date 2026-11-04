package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"sync"
	"testing"
)

func parallelVersion(t *testing.T, s *app.Service, p, name string) domain.ProjectVersion {
	t.Helper()
	m, e := s.SaveProjectVersion(context.Background(), app.Human(), p, "", app.ProjectVersionInput{Name: name, RequestID: domain.ID("ver_")})
	if e != nil {
		t.Fatal(e)
	}
	return *m.ProjectVersion
}
func versionTask(t *testing.T, s *app.Service, p, v string) domain.Task {
	t.Helper()
	m, e := s.CreateTask(context.Background(), app.Human(), p, app.TaskInput{Title: "指定目标版本", Description: "原文", Acceptance: []string{"正确"}, ProjectVersionID: v, RequestID: domain.ID("task_")})
	if e != nil {
		t.Fatal(e)
	}
	return m.Task
}
func TestParallelVersionsDefaultExplicitTargetsAndIndependentCAS(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a := parallelVersion(t, s, p.ID, "A")
	b := parallelVersion(t, s, p.ID, "B")
	state, _ := s.Store.GetCurrentMilestone(c, p.ID)
	if state.VersionID != a.ID {
		t.Fatal("second active version replaced default")
	}
	explicit := versionTask(t, s, p.ID, b.ID)
	implicit := versionTask(t, s, p.ID, "")
	if explicit.ProjectVersionID != b.ID || implicit.ProjectVersionID != a.ID {
		t.Fatal("creation destination mismatch")
	}
	after, _ := s.Store.GetCurrentMilestone(c, p.ID)
	if after.Version != state.Version {
		t.Fatal("ordinary append serialized the default pointer")
	}
	var wg sync.WaitGroup
	for _, v := range []domain.ProjectVersion{a, b} {
		wg.Add(1)
		go func(v domain.ProjectVersion) {
			defer wg.Done()
			current := versionDetail(t, s, v.ID)
			_, e := s.SaveProjectVersion(c, app.Human(), p.ID, v.ID, app.ProjectVersionInput{Name: v.Name, Goal: "independent", Version: current.ProjectVersion.Version, RequestID: domain.ID("edit_")})
			if e != nil {
				t.Error(e)
			}
		}(v)
	}
	wg.Wait()
	b = versionDetail(t, s, b.ID).ProjectVersion
	m, e := s.ActProjectVersion(c, app.Human(), b.ID, app.ProjectVersionAction{Action: "set_default", Version: b.Version, DefaultVersion: state.Version, RequestID: "default", Body: "新工作进入B"})
	if e != nil {
		t.Fatal(e)
	}
	retry, e := s.ActProjectVersion(c, app.Human(), b.ID, app.ProjectVersionAction{Action: "set_default", Version: b.Version, DefaultVersion: state.Version, RequestID: "default", Body: "新工作进入B"})
	if e != nil || retry.ProjectVersion.ID != m.ProjectVersion.ID {
		t.Fatal("default retry", e)
	}
	if versionTask(t, s, p.ID, "").ProjectVersionID != b.ID {
		t.Fatal("default did not switch")
	}
	a = versionDetail(t, s, a.ID).ProjectVersion
	if _, e = s.ActProjectVersion(c, app.Human(), a.ID, app.ProjectVersionAction{Action: "set_default", Version: a.Version, DefaultVersion: state.Version, RequestID: "stale"}); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("stale default accepted", e)
	}
	if got, _ := s.Store.GetTask(c, implicit.ID); got.ProjectVersionID != a.ID {
		t.Fatal("changing default moved existing task")
	}
	page, e := s.ProjectVersionsPage(c, app.Human(), p.ID, domain.VersionListQuery{PageQuery: domain.PageQuery{Page: 1, PageSize: 1}, Status: "active"})
	if e != nil || page.Total != 2 || len(page.Items) != 1 || !page.Items[0].IsDefault {
		t.Fatal(page, e)
	}
}
func TestParallelVersionLinkedBugGroupAndClosedAcceptanceContinuation(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	parent := testingRequirement(t, s, p.ID)
	original := parent.ProjectVersionID
	b := parallelVersion(t, s, p.ID, "并行修复版")
	state, _ := s.Store.GetCurrentMilestone(c, p.ID)
	if _, e := s.ActProjectVersion(c, app.Human(), b.ID, app.ProjectVersionAction{Action: "set_default", Version: b.Version, DefaultVersion: state.Version, RequestID: "default-B"}); e != nil {
		t.Fatal(e)
	}
	bug := fileTestBug(t, s, parent)
	if bug.ProjectVersionID != original {
		t.Fatal("linked Bug followed unrelated default")
	}
	parent, _ = s.Store.GetTask(c, parent.ID)
	b = versionDetail(t, s, b.ID).ProjectVersion
	moved, e := s.AssignProjectVersion(c, app.Human(), p.ID, app.VersionAssignmentInput{ProjectVersionID: b.ID, Version: b.Version, Tasks: []app.VersionTaskRef{{ID: parent.ID, Version: parent.Version}}, Body: "共同转入修复版本", RequestID: "group"})
	if e != nil {
		t.Fatal(e)
	}
	parent, _ = s.Store.GetTask(c, parent.ID)
	bug, _ = s.Store.GetTask(c, bug.ID)
	if len(moved.Tasks) != 2 || parent.ProjectVersionID != b.ID || bug.ProjectVersionID != b.ID || parent.Status != "fixing" {
		t.Fatal("live group split", moved.Tasks, parent, bug)
	}
	// A different released requirement stays in its ended version until follow-up
	// work is requested; its acceptance Bug atomically carries both work items.
	other := passTesting(t, s, testingRequirement(t, s, p.ID))
	published := publishVersion(t, s, other.ProjectVersionID, other).Tasks[0]
	current := versionDetail(t, s, published.ProjectVersionID).ProjectVersion
	if _, e = s.ActProjectVersion(c, app.Human(), current.ID, app.ProjectVersionAction{Action: "close", Version: current.Version, Body: "归档，剩余工作去后继", CreateSuccessor: true, RequestID: "close-B"}); e != nil {
		t.Fatal(e)
	}
	destination := parallelVersion(t, s, p.ID, "验收后续版")
	reported, e := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "已结束版验收 Bug", Kind: "bug", ParentRequirementID: published.ID, ParentVersion: published.Version, ProjectVersionID: destination.ID, RequestID: "ended-bug"})
	if e != nil {
		t.Fatal(e)
	}
	parent, _ = s.Store.GetTask(c, published.ID)
	if reported.Task.ProjectVersionID != destination.ID || parent.ProjectVersionID != destination.ID || parent.Status != "fixing" {
		t.Fatal("closed parent continuation split", reported, parent)
	}
	old := versionDetail(t, s, current.ID).ProjectVersion
	if len(old.Releases) != 1 || old.Releases[0].Tasks[0].Status != "review" || old.Releases[0].Tasks[0].ProjectVersionID != current.ID {
		t.Fatal("release snapshot rewritten")
	}
}
func TestParallelVersionCloseExplicitSuccessorIterationAndIntake(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a := parallelVersion(t, s, p.ID, "A")
	b := parallelVersion(t, s, p.ID, "B")
	task := versionTask(t, s, p.ID, a.ID)
	a = versionDetail(t, s, a.ID).ProjectVersion
	closed, e := s.ActProjectVersion(c, app.Human(), a.ID, app.ProjectVersionAction{Action: "close", Version: a.Version, DestinationVersionID: b.ID, DestinationVersion: b.Version, Body: "延期到B", RequestID: "close"})
	if e != nil {
		t.Fatal(e)
	}
	if len(closed.Tasks) != 1 || closed.Tasks[0].ID != task.ID || closed.Tasks[0].ProjectVersionID != b.ID {
		t.Fatal(closed)
	}
	state, _ := s.Store.GetCurrentMilestone(c, p.ID)
	if state.VersionID != b.ID {
		t.Fatal("default successor mismatch")
	}
	source := acceptIterationTask(t, s, versionTask(t, s, p.ID, b.ID))
	target := parallelVersion(t, s, p.ID, "C")
	input := iterationInput(source, "iterate-C")
	input.ProjectVersionID = target.ID
	child, e := s.CreateRequirementIteration(c, app.Human(), source.ID, input)
	if e != nil || child.Task.ProjectVersionID != target.ID {
		t.Fatal(child, e)
	}
	unchanged, _ := s.Store.GetTask(c, source.ID)
	if unchanged.Status != "done" || unchanged.ProjectVersionID != b.ID {
		t.Fatal("iteration rewrote source")
	}
	run := intakeRun(t, s, p.ID, "parallel-intake", "conv", "创建到C")
	proposal, e := s.ProposeRequirement(c, run.ID, app.RequirementIntakeInput{Title: "问答整理", Kind: "requirement", Description: "完整原文", Acceptance: []string{"结果正确"}, Reason: "需要跟进"})
	if e != nil {
		t.Fatal(e)
	}
	intake, e := s.ResolveRequirementIntake(c, app.Human(), proposal.ID, app.ResolveRequirementIntakeInput{Action: "create", ProjectVersionID: target.ID, RequestID: "intake-C"})
	if e != nil {
		t.Fatal(e)
	}
	created, _ := s.Store.GetTask(c, intake.TaskID)
	if created.ProjectVersionID != target.ID {
		t.Fatal("intake ignored explicit version")
	}
}
