package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReviewDependenciesRequireRecheckWithoutRewritingOtherTasks(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a := app.Human()
	create := func(title string) domain.Task {
		t.Helper()
		v, e := s.CreateTask(c, a, p.ID, app.TaskInput{Title: title, Description: title, RequestID: title})
		if e != nil {
			t.Fatal(e)
		}
		return v.Task
	}
	first, second := create("显示尺寸"), create("导出尺寸")
	start := func(id, key string) domain.RequirementReview {
		t.Helper()
		v, e := s.SaveRequirementReview(c, a, id, app.RequirementReviewInput{Action: "start", RequestID: key})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	av, bv := start(first.ID, "start-a"), start(second.ID, "start-b")
	ready := &domain.RequirementReviewResult{Body: "已确认范围", Decision: "ready", Questions: []domain.RequirementQuestion{}, Proposal: &domain.RequirementProposal{Description: "显示像素宽高", Acceptance: []string{"可见宽高"}}}
	evidence := &domain.RequirementReviewEvidence{References: []domain.RequirementReviewReference{{TaskID: second.ID, Revision: second.Revision}}}
	if e := s.FinishRequirementReview(c, first.ID, av.Generation, "", ready, "", evidence); e != nil {
		t.Fatal(e)
	}
	av, _ = s.RequirementReview(c, a, first.ID)
	if av.NeedsRecheck {
		t.Fatal(av)
	}
	time.Sleep(2 * time.Millisecond)
	conflict := &domain.RequirementReviewResult{Body: "导出单位存在冲突", Decision: "clarify", Relations: []domain.RequirementRelation{{TaskID: first.ID, Kind: "conflict", Reason: "一个要求像素，一个要求毫米，需确认统一口径"}}}
	if e := s.FinishRequirementReview(c, second.ID, bv.Generation, "", conflict, ""); e != nil {
		t.Fatal(e)
	}
	av, e := s.RequirementReview(c, a, first.ID)
	if e != nil || !av.NeedsRecheck || len(av.IncomingRelations) != 1 || av.IncomingRelations[0].TaskID != second.ID {
		t.Fatal(av, e)
	}
	if !strings.Contains(av.RecheckReason, "「导出尺寸」") {
		t.Fatal("conflict source not named:", av.RecheckReason)
	}
	_, e = s.SaveRequirementReview(c, a, first.ID, app.RequirementReviewInput{Action: "confirm", RequestID: "stale-confirm", Generation: av.Generation})
	if !errors.Is(e, domain.ErrConflict) {
		t.Fatal("stale draft was confirmed", e)
	}
	unchanged, _ := s.Store.GetTask(c, first.ID)
	if unchanged.Revision != first.Revision || unchanged.Description != first.Description {
		t.Fatal("another assessment rewrote original", unchanged)
	}
	av, e = s.SaveRequirementReview(c, a, first.ID, app.RequirementReviewInput{Action: "retry", RequestID: "retry"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FinishRequirementReview(c, first.ID, av.Generation, "", ready, "", evidence); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveRequirementReview(c, a, first.ID, app.RequirementReviewInput{Action: "confirm", RequestID: "fresh-confirm", Generation: av.Generation}); e != nil {
		t.Fatal("fresh check cannot confirm", e)
	}
}

// Later requirements check themselves against earlier ones, so new
// requirements, unread edits, development progress and other reviews moving on
// leave an earlier review confirmable.
func TestEarlierReviewStaysFixedAsLaterRequirementsArrive(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a := app.Human()
	x, _ := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "本需求", RequestID: "x"})
	y, _ := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "模板积分", Description: strings.Repeat("范围", 1000), RequestID: "y"})
	other, _ := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "无关需求", RequestID: "other"})
	v := domain.RequirementReview{TaskID: x.Task.ID, ProjectID: p.ID, Evidence: &domain.RequirementReviewEvidence{References: []domain.RequirementReviewReference{{TaskID: y.Task.ID, Revision: y.Task.Revision}}}}
	fixed := func(after string) {
		t.Helper()
		if r, e := s.ReviewRecheckReason(c, v); e != nil || r != "" {
			t.Fatal("locked after "+after+":", r, e)
		}
	}
	_, _ = s.CreateTask(c, a, p.ID, app.TaskInput{Title: "新增范围", RequestID: "z"})
	fixed("a new requirement")
	if _, e := s.EditTask(c, a, other.Task.ID, app.TaskInput{Title: "无关需求", Description: "改了范围", Version: other.Task.Version, RequestID: "edit-other"}); e != nil {
		t.Fatal(e)
	}
	fixed("an unread edit")
	changed := y.Task
	changed.Status = "doing"
	changed.Submission = &domain.Submission{Summary: "不进入评审的开发日志"}
	changed.Version++
	if e := s.Store.CommitTask(c, changed, y.Task.Version, domain.Event{ProjectID: p.ID, TaskID: changed.ID, Kind: "fixture-progress"}, nil); e != nil {
		t.Fatal(e)
	}
	fixed("development progress")
	// Rechecking either side used to bump its review and lock the other again.
	if _, e := s.SaveRequirementReview(c, a, y.Task.ID, app.RequirementReviewInput{Action: "start", RequestID: "start-y"}); e != nil {
		t.Fatal(e)
	}
	fixed("the read requirement's review moved on")
	pool, e := s.RequirementPool(c, a, p.ID, app.RequirementPoolQuery{Limit: 1})
	if e != nil || !pool.HasMore || len([]rune(pool.Items[0].ScopeSummary)) > 330 {
		t.Fatal(pool, e)
	}
}

func TestReviewRecheckNamesChangedReferences(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a := app.Human()
	x, _ := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "本需求", RequestID: "x"})
	y, _ := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "模板积分", Description: "点数", RequestID: "y"})
	v := domain.RequirementReview{TaskID: x.Task.ID, ProjectID: p.ID, Evidence: &domain.RequirementReviewEvidence{References: []domain.RequirementReviewReference{{TaskID: y.Task.ID, Revision: y.Task.Revision}}}}
	reason := func() string {
		t.Helper()
		r, e := s.ReviewRecheckReason(c, v)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	edited, e := s.EditTask(c, a, y.Task.ID, app.TaskInput{Title: "模板积分", Description: "点数支持两位小数", Version: y.Task.Version, RequestID: "edit-y"})
	if e != nil {
		t.Fatal(e)
	}
	if r := reason(); r != "评审参考的需求「模板积分」有修改，确认前需要重新核对。" {
		t.Fatal(r)
	}
	v.Evidence.References[0].Revision = edited.Task.Revision
	if r := reason(); r != "" {
		t.Fatal("rechecked reference still locked:", r)
	}
	if _, e = s.Act(c, a, y.Task.ID, app.Action{Action: "close", Body: "并入其他需求", RequestID: "close-y"}); e != nil {
		t.Fatal(e)
	}
	if r := reason(); r != "评审参考的需求「模板积分」已结束，确认前需要重新核对。" {
		t.Fatal(r)
	}
	v.Evidence.References[0].Ended = true // read again after it ended
	if r := reason(); r != "" {
		t.Fatal("ended reference read after closing still locked:", r)
	}
}
