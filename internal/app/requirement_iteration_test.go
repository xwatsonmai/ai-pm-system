package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
)

func acceptIterationTask(t *testing.T, s *app.Service, task domain.Task) domain.Task {
	t.Helper()
	c := context.Background()
	for _, stage := range []string{"ready", "claim", "submit", "test_pass", "mark_deployed", "accept"} {
		if stage == "ready" && task.Status == "ready" {
			continue
		}
		in := app.Action{Action: stage, RequestID: domain.ID("step_"), Revision: task.Revision}
		if stage == "submit" {
			in.Submission = &domain.Submission{Summary: "已交付", SHA: "accepted-" + task.ID, Tests: "业务和回归通过"}
		}
		if stage == "test_pass" {
			in.Version = task.Version
			in.TestVerification = &domain.TestVerification{Revision: task.Revision, SHA: task.Submission.SHA, Environment: "测试", Note: "产品验证通过"}
		}
		if stage == "mark_deployed" {
			in.Deployment = &domain.Deployment{Revision: task.Revision, SHA: task.Submission.SHA, Environment: "生产", Body: "实际发布完成"}
		}
		if stage == "accept" {
			checks := make([]string, len(task.Acceptance))
			for i := range checks {
				checks[i] = "passed"
			}
			in.Review = &domain.Review{Revision: task.Revision, SHA: task.Submission.SHA, Environment: "生产", Checks: checks, Note: "业务验收通过"}
		}
		actor := app.Human()
		if stage == "claim" {
			actor = developer(task.ProjectID, "dev")
		}
		next, e := s.Act(c, actor, task.ID, in)
		if e != nil {
			t.Fatal(stage, e)
		}
		task = next.Task
	}
	return task
}
func iterationInput(source domain.Task, request string) app.RequirementIterationInput {
	return app.RequirementIterationInput{SourceVersion: source.Version, TaskInput: app.TaskInput{Title: "本轮体验改进", Description: "只补充本轮新增的业务规则", Acceptance: []string{"新增规则生效"}, RequestID: request}}
}
func TestRequirementIterationOnlyAfterAcceptance(t *testing.T) {
	for _, stage := range []string{"clarify", "ready", "doing", "pending_release", "review", "closed", "rejected", "canceled"} {
		t.Run(stage, func(t *testing.T) {
			s, p := setup(t)
			task, _, _ := taskAtStage(t, s, p.ID, "clarify")
			if stage == "closed" || stage == "rejected" || stage == "canceled" {
				action := "close"
				if stage == "rejected" {
					action = "decline"
				}
				if stage == "canceled" {
					action = "cancel"
				}
				m, e := s.Act(context.Background(), app.Human(), task.ID, app.Action{Action: action, Body: "结束", RequestID: "end"})
				if e != nil {
					t.Fatal(e)
				}
				task = m.Task
			} else if stage != "clarify" {
				task, _, _ = taskAtStage(t, s, p.ID, stage)
			}
			_, e := s.CreateRequirementIteration(context.Background(), app.Human(), task.ID, iterationInput(task, "invalid"))
			if e == nil {
				t.Fatal("unaccepted task used as iteration source", stage)
			}
		})
	}
}
func TestRequirementIterationPreservesCompletedMilestoneAndFrozenContext(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	made, e := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "订单退款", Description: "已验收的完整需求", Acceptance: []string{"退款记录可查"}, RequestID: "source"})
	if e != nil {
		t.Fatal(e)
	}
	source := acceptIterationTask(t, s, made.Task)
	v := versionDetail(t, s, source.ProjectVersionID).ProjectVersion
	ended, e := s.ActProjectVersion(c, app.Human(), v.ID, app.ProjectVersionAction{Action: "complete", Version: v.Version, Body: "本期完成", RequestID: "complete"})
	if e != nil {
		t.Fatal(e)
	}
	before, _ := s.Store.GetTask(c, source.ID)
	input := iterationInput(source, "new-iteration")
	child, e := s.CreateRequirementIteration(c, app.Human(), source.ID, input)
	if e != nil {
		t.Fatal(e)
	}
	if child.Task.ProjectVersionID == v.ID || child.Task.ProjectVersionID == "" || child.Task.Status != "clarify" || child.Task.Submission != nil || child.Task.Deployment != nil || child.Task.Review != nil || child.Task.Revision != 1 {
		t.Fatal("iteration inherited lifecycle or ended milestone", child)
	}
	actual, _ := s.Store.GetTask(c, source.ID)
	if !reflect.DeepEqual(before, actual) {
		t.Fatal("accepted source was changed")
	}
	old := versionDetail(t, s, v.ID).ProjectVersion
	if !reflect.DeepEqual(*ended.ProjectVersion, old) {
		t.Fatal("ended milestone snapshot changed")
	}
	frozen := child.Task.Iteration.Source
	if frozen.ID != source.ID || frozen.Description != source.Description || frozen.Review == nil || !frozen.Review.Accepted || frozen.Deployment.SHA != source.Deployment.SHA || child.Task.Iteration.RootTaskID != source.ID {
		t.Fatal("accepted source context missing", child)
	}
	// A later source edit cannot alter the context used when the child was created.
	_, e = s.EditTask(c, app.Human(), source.ID, app.TaskInput{Title: source.Title, Kind: source.Kind, Description: "后续改写的原单", Acceptance: source.Acceptance, Version: source.Version, RequestID: "edit-source"})
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s.CreateRequirementIteration(c, app.Human(), source.ID, input)
	if e != nil || replay.Task.ID != child.Task.ID {
		t.Fatal("lost-response retry failed after source changed", replay, e)
	}
	current, _ := s.Store.GetTask(c, child.Task.ID)
	if current.Iteration.Source.Description != "已验收的完整需求" {
		t.Fatal("frozen context changed")
	}
}
func TestRequirementIterationRepeatedChainsAndConcurrentAppend(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	root := acceptIterationTask(t, s, create(t, s, p.ID))
	first, e := s.CreateRequirementIteration(c, app.Human(), root.ID, iterationInput(root, "first"))
	if e != nil {
		t.Fatal(e)
	}
	parent := acceptIterationTask(t, s, first.Task)
	second, e := s.CreateRequirementIteration(c, app.Human(), parent.ID, iterationInput(parent, "second"))
	if e != nil {
		t.Fatal(e)
	}
	meta := second.Task.Iteration
	if meta.ParentTaskID != parent.ID || meta.RootTaskID != root.ID || meta.Source.Iteration != nil || meta.Baseline == nil || meta.Baseline.ID != root.ID || meta.Baseline.Iteration != nil {
		t.Fatal("chain lost context or grew recursively", meta)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.CreateRequirementIteration(c, app.Human(), root.ID, iterationInput(root, domain.ID("parallel_")))
			failures <- e
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	history, e := s.RequirementIterations(c, app.Human(), second.Task.ID, 0, 3)
	if e != nil || history.Total != 14 || len(history.Items) != 3 || !history.HasMore || history.Root.ID != root.ID {
		t.Fatal(history, e)
	}
	ids := map[string]bool{}
	for offset := 0; offset < history.Total; offset += 3 {
		page, e := s.RequirementIterations(c, app.Human(), root.ID, offset, 3)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range page.Items {
			if ids[v.ID] {
				t.Fatal("duplicate pagination item")
			}
			ids[v.ID] = true
		}
	}
	if len(ids) != 14 {
		t.Fatal("iterations omitted")
	}
	raw, _ := json.Marshal(history)
	var lean map[string]any
	json.Unmarshal(raw, &lean)
	for _, item := range lean["items"].([]any) {
		if _, ok := item.(map[string]any)["description"]; ok {
			t.Fatal("history duplicated documents")
		}
	}
}

type iterationCommitRace struct {
	storage.Store
	before func()
	once   sync.Once
}

func (s *iterationCommitRace) CommitVersionPlan(c context.Context, v []domain.VersionWrite, t []domain.VersionTaskWrite, e []domain.Event, r *domain.Receipt, b ...domain.MilestoneBatch) error {
	s.once.Do(s.before)
	return s.Store.CommitVersionPlan(c, v, t, e, r, b...)
}
func TestRequirementIterationAtomicSourceGuardRollsBackNewMilestone(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	source := acceptIterationTask(t, s, create(t, s, p.ID))
	v := versionDetail(t, s, source.ProjectVersionID).ProjectVersion
	if _, e := s.ActProjectVersion(c, app.Human(), v.ID, app.ProjectVersionAction{Action: "complete", Version: v.Version, Body: "完成", RequestID: "end"}); e != nil {
		t.Fatal(e)
	}
	underlying := s.Store
	race := &iterationCommitRace{Store: underlying}
	race.before = func() {
		other := &app.Service{Store: underlying}
		_, e := other.EditTask(c, app.Human(), source.ID, app.TaskInput{Title: source.Title, Kind: source.Kind, Description: source.Description, Acceptance: source.Acceptance, Reviewer: "新负责人", Version: source.Version, RequestID: "source-race"})
		if e != nil {
			t.Fatal(e)
		}
	}
	s.Store = race
	if _, e := s.CreateRequirementIteration(c, app.Human(), source.ID, iterationInput(source, "stale")); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("source guard bypassed", e)
	}
	tasks, _ := underlying.ListTasks(c, p.ID)
	versions, _ := underlying.ListProjectVersions(c, p.ID)
	state, _ := underlying.GetCurrentMilestone(c, p.ID)
	if len(tasks) != 1 || len(versions) != 1 || state.VersionID != "" {
		t.Fatal("failed creation left orphan task or milestone", len(tasks), len(versions), state)
	}
}
