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
	fp, catalog, e := s.ReviewCatalog(c, p.ID, first.ID)
	if e != nil {
		t.Fatal(e)
	}
	ready := &domain.RequirementReviewResult{Body: "已确认范围", Decision: "ready", Questions: []domain.RequirementQuestion{}, Proposal: &domain.RequirementProposal{Description: "显示像素宽高", Acceptance: []string{"可见宽高"}}}
	evidence := &domain.RequirementReviewEvidence{CatalogFingerprint: fp, Catalog: catalog, References: []domain.RequirementReviewReference{{TaskID: second.ID, Revision: second.Revision, ReviewVersion: bv.Version}}}
	if e = s.FinishRequirementReview(c, first.ID, av.Generation, "", ready, "", evidence); e != nil {
		t.Fatal(e)
	}
	av, _ = s.RequirementReview(c, a, first.ID)
	if av.NeedsRecheck {
		t.Fatal(av)
	}
	time.Sleep(2 * time.Millisecond)
	conflict := &domain.RequirementReviewResult{Body: "导出单位存在冲突", Decision: "clarify", Relations: []domain.RequirementRelation{{TaskID: first.ID, Kind: "conflict", Reason: "一个要求像素，一个要求毫米，需确认统一口径"}}}
	if e = s.FinishRequirementReview(c, second.ID, bv.Generation, "", conflict, ""); e != nil {
		t.Fatal(e)
	}
	av, e = s.RequirementReview(c, a, first.ID)
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
	currentB, _ := s.Store.GetRequirementReview(c, second.ID)
	evidence.References[0].ReviewVersion = currentB.Version
	if e = s.FinishRequirementReview(c, first.ID, av.Generation, "", ready, "", evidence); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveRequirementReview(c, a, first.ID, app.RequirementReviewInput{Action: "confirm", RequestID: "fresh-confirm", Generation: av.Generation}); e != nil {
		t.Fatal("fresh check cannot confirm", e)
	}
}

func TestReviewCatalogIgnoresDevelopmentProgressButDetectsNewScope(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a := app.Human()
	x, _ := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "本需求", RequestID: "x"})
	y, _ := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "另一个需求", Description: strings.Repeat("范围", 1000), RequestID: "y"})
	before, _, _ := s.ReviewCatalog(c, p.ID, x.Task.ID)
	changed := y.Task
	changed.Status = "doing"
	changed.Submission = &domain.Submission{Summary: "不进入评审的开发日志"}
	changed.Version++
	if e := s.Store.CommitTask(c, changed, y.Task.Version, domain.Event{ProjectID: p.ID, TaskID: changed.ID, Kind: "fixture-progress"}, nil); e != nil {
		t.Fatal(e)
	}
	after, _, _ := s.ReviewCatalog(c, p.ID, x.Task.ID)
	if before != after {
		t.Fatal("development progress invalidates product review")
	}
	pool, e := s.RequirementPool(c, a, p.ID, app.RequirementPoolQuery{Limit: 1})
	if e != nil || !pool.HasMore || len([]rune(pool.Items[0].ScopeSummary)) > 330 {
		t.Fatal(pool, e)
	}
	_, _ = s.CreateTask(c, a, p.ID, app.TaskInput{Title: "新增范围", RequestID: "z"})
	after, _, _ = s.ReviewCatalog(c, p.ID, x.Task.ID)
	if before == after {
		t.Fatal("new scope was ignored")
	}
}

func TestReviewRecheckNamesChangedRequirements(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a := app.Human()
	x, _ := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "本需求", RequestID: "x"})
	y, _ := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "模板积分", Description: "点数", RequestID: "y"})
	fp, catalog, e := s.ReviewCatalog(c, p.ID, x.Task.ID)
	if e != nil {
		t.Fatal(e)
	}
	v := domain.RequirementReview{TaskID: x.Task.ID, ProjectID: p.ID, Evidence: &domain.RequirementReviewEvidence{CatalogFingerprint: fp, Catalog: catalog}}
	reason := func() string {
		t.Helper()
		r, e := s.ReviewRecheckReason(c, v)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	if r := reason(); r != "" {
		t.Fatal("unchanged catalog needs recheck:", r)
	}
	edited, e := s.EditTask(c, a, y.Task.ID, app.TaskInput{Title: "模板积分", Description: "点数支持两位小数", Version: y.Task.Version, RequestID: "edit-y"})
	if e != nil {
		t.Fatal(e)
	}
	if r := reason(); r != "需求「模板积分」有修改。确认前需要重新核查与本需求的交叉与冲突。" {
		t.Fatal(r)
	}
	_, _ = s.CreateTask(c, a, p.ID, app.TaskInput{Title: "分成统计", RequestID: "z"})
	if _, e = s.Act(c, a, edited.Task.ID, app.Action{Action: "close", Body: "并入其他需求", RequestID: "close-y"}); e != nil {
		t.Fatal(e)
	}
	if r := reason(); r != "新增需求「分成统计」；需求「模板积分」已结束。确认前需要重新核查与本需求的交叉与冲突。" {
		t.Fatal(r)
	}
	v.Evidence.Catalog = nil // recorded before per-requirement digests
	if r := reason(); r != "项目中有需求新增、修改或结束，请重新核查需求之间的交叉与冲突。" {
		t.Fatal(r)
	}
}
