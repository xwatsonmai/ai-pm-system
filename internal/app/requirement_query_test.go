package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRequirementPoolCountsFiltersPaginationAndIsolation(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	for i, status := range []string{"clarify", "ready", "doing", "review", "done", "canceled"} {
		kind := "requirement"
		if i%2 == 1 {
			kind = "bug"
		}
		task := domain.Task{ID: fmt.Sprint("tsk_", i), ProjectID: p.ID, Title: fmt.Sprint("需求", i), Kind: kind, Status: status, Description: "不应出现在列表的完整正文", Version: 1, Revision: 1, UpdatedAt: int64(i)}
		if status == "doing" {
			task.Blocker = "等待环境"
		}
		if err := s.Store.CommitTask(c, task, 0, domain.Event{ProjectID: p.ID, TaskID: task.ID, Kind: "created"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	a := domain.Actor{ID: "platform", Kind: "platform", ProjectID: p.ID}
	first, err := s.RequirementPool(c, a, p.ID, app.RequirementPoolQuery{Limit: 2})
	if err != nil || first.Counts.Total != 6 || first.Counts.ByKind["bug"] != 3 || first.Counts.ByStatus["doing"] != 1 || first.Counts.ByKindStatus["requirement"]["doing"] != 1 || first.Counts.Blocked != 1 || len(first.Items) != 2 || first.Matched != 6 || !first.HasMore || first.NextOffset != 2 {
		t.Fatalf("bad counts/page: %+v %v", first, err)
	}
	second, err := s.RequirementPool(c, a, p.ID, app.RequirementPoolQuery{Limit: 2, Offset: first.NextOffset})
	if err != nil || second.Items[0].ID == first.Items[0].ID || second.Items[0].ID != "tsk_3" {
		t.Fatal(second, err)
	}
	filtered, err := s.RequirementPool(c, a, p.ID, app.RequirementPoolQuery{Kind: "bug", Status: "ready", Query: "TSK_1"})
	if err != nil || filtered.Counts.Total != 6 || filtered.Matched != 1 || filtered.Items[0].ID != "tsk_1" || filtered.HasMore {
		t.Fatal(filtered, err)
	}
	blocked := true
	filtered, err = s.RequirementPool(c, a, p.ID, app.RequirementPoolQuery{Blocked: &blocked})
	if err != nil || filtered.Matched != 1 || filtered.Items[0].Status != "doing" {
		t.Fatal(filtered, err)
	}
	empty, err := s.RequirementPool(c, a, p.ID, app.RequirementPoolQuery{Offset: 999})
	if err != nil || empty.Matched != 6 || len(empty.Items) != 0 || empty.HasMore {
		t.Fatal(empty, err)
	}
	for _, q := range []app.RequirementPoolQuery{{Status: "blocked"}, {Kind: "other"}, {Limit: 51}, {Offset: -1}} {
		if _, err = s.RequirementPool(c, a, p.ID, q); err == nil {
			t.Fatal("invalid filter accepted", q)
		}
	}
	foreign, _ := s.SaveProject(c, domain.Project{Name: "foreign"})
	foreignTask, _ := s.CreateTask(c, app.Human(), foreign.ID, app.TaskInput{Title: "foreign secret", RequestID: "foreign"})
	if _, err = s.RequirementPool(c, a, foreign.ID, app.RequirementPoolQuery{}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("cross-project list", err)
	}
	if _, err = s.RequirementDetail(c, a, p.ID, app.RequirementDetailQuery{TaskID: foreignTask.Task.ID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("cross-project detail", err)
	}
	ctx, err := s.QuestionContext(c, a, p.ID, []string{"tsk_1", "tsk_1"})
	b, _ := json.Marshal(ctx)
	if err != nil || strings.Contains(string(b), "完整正文") || ctx["tasks"] != nil || ctx["events"] != nil || ctx["artifacts"] != nil || len(ctx["conversation_tasks"].([]app.RequirementSummary)) != 1 {
		t.Fatal("question snapshot is not compact", string(b), err)
	}
	if _, err = s.QuestionContext(c, a, p.ID, []string{foreignTask.Task.ID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("foreign linked task", err)
	}
}

func TestRequirementDetailTracksSupplementWithoutRepeatingOldQuestions(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a := app.Human()
	created, err := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "图片保存时间", Description: "需要延长保存", Acceptance: []string{"按约定时长保存"}, RequestID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Task.ID
	detail := func() app.RequirementDetailResult {
		t.Helper()
		v, e := s.RequirementDetail(c, a, p.ID, app.RequirementDetailQuery{TaskID: id, ReviewLimit: 1})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	if v := detail(); v.Review.State != "not_started" || v.NextActor != "user" {
		t.Fatal(v)
	}
	v, err := s.SaveRequirementReview(c, a, id, app.RequirementReviewInput{Action: "start", RequestID: "start"})
	if err != nil {
		t.Fatal(err)
	}
	question := &domain.RequirementReviewResult{Decision: "clarify", Body: "需要确认保存多久", Questions: []domain.RequirementQuestion{{ID: "days", Label: "保存天数", Type: "text", Required: true}}}
	if err = s.FinishRequirementReview(c, id, v.Generation, "", question, ""); err != nil {
		t.Fatal(err)
	}
	before := detail()
	if before.Review.State != "awaiting_input" || len(before.Review.PendingQuestions) != 1 || before.Review.SupplementState != "none" || !before.Review.HasMore {
		t.Fatal(before)
	}
	file, err := s.SaveAttachment(c, a, p.ID, "rules.md", []byte("保存90天"))
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.SaveRequirementReview(c, a, id, app.RequirementReviewInput{Action: "message", ReplyTo: "rm_", Body: "保存90天", Answers: map[string][]string{"days": {"90"}}, AttachmentIDs: []string{file.ID}, RequestID: "supplement"})
	if err != nil {
		t.Fatal(err)
	}
	queued := detail()
	if queued.Review.State != "reviewing" || queued.Review.SupplementState != "reviewing" || len(queued.Review.PendingQuestions) != 0 || queued.Review.LatestSupplement.Body != "保存90天" || len(queued.Review.LatestSupplement.Attachments) != 1 || queued.NextActor != "agent" {
		t.Fatal("repeated answered question", queued)
	}
	// Pagination uses a stable index into append-only review history, even when a
	// supplement arrives between reads. The latest summary remains current.
	earlier, err := s.RequirementDetail(c, a, p.ID, app.RequirementDetailQuery{TaskID: id, ReviewBefore: &before.Review.NextBefore, ReviewLimit: 1})
	if err != nil || earlier.Review.HasMore || earlier.Review.Messages[0].Role != "system" || earlier.Review.LatestSupplement.Body != "保存90天" {
		t.Fatal(earlier, err)
	}
	if err = s.FinishRequirementReview(c, id, v.Generation, "", nil, "模型超时"); err != nil {
		t.Fatal(err)
	}
	failed := detail()
	if failed.Review.State != "failed" || failed.Review.SupplementState != "needs_reassessment" || len(failed.Review.PendingQuestions) != 0 || failed.Review.LatestSupplement == nil {
		t.Fatal("failure lost saved supplement", failed)
	}
	v, err = s.SaveRequirementReview(c, a, id, app.RequirementReviewInput{Action: "retry", RequestID: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	ready := &domain.RequirementReviewResult{Decision: "ready", Body: "规则已明确，请确认", Questions: []domain.RequirementQuestion{}, Proposal: &domain.RequirementProposal{Description: "保存90天", Acceptance: []string{"到90天前可访问"}}}
	if err = s.FinishRequirementReview(c, id, v.Generation, "", ready, ""); err != nil {
		t.Fatal(err)
	}
	waiting := detail()
	if waiting.Review.State != "awaiting_confirmation" || waiting.Review.SupplementState != "assessed" || waiting.Task.Status != "clarify" || waiting.Task.Description != "需要延长保存" {
		t.Fatal("assessment falsely confirmed task", waiting)
	}
	if _, err = s.SaveRequirementReview(c, a, id, app.RequirementReviewInput{Action: "confirm", Generation: v.Generation, RequestID: "confirm"}); err != nil {
		t.Fatal(err)
	}
	confirmed := detail()
	if confirmed.Task.Status != "ready" || confirmed.Review.State != "not_required" || confirmed.Review.LatestSupplement.Body != "保存90天" || confirmed.NextActor != "developer" {
		t.Fatal(confirmed)
	}
}

func TestRequirementDetailStaleReviewAndHistoricalBug(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a := app.Human()
	created, _ := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "旧Bug", Kind: "bug", Description: "旧现象", Acceptance: []string{"修好"}, RequestID: "bug"})
	id := created.Task.ID
	v, err := s.SaveRequirementReview(c, a, id, app.RequirementReviewInput{Action: "start", RequestID: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishRequirementReview(c, id, v.Generation, "", &domain.RequirementReviewResult{Decision: "clarify", Body: "确认范围", Questions: []domain.RequirementQuestion{{ID: "scope", Label: "范围", Type: "text"}}}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EditTask(c, a, id, app.TaskInput{Title: "旧Bug", Kind: "bug", Description: "已更新的现象", Acceptance: []string{"修好"}, Version: created.Task.Version, RequestID: "edit"}); err != nil {
		t.Fatal(err)
	}
	stale, err := s.RequirementDetail(c, a, p.ID, app.RequirementDetailQuery{TaskID: id})
	if err != nil || stale.Review.State != "stale" || len(stale.Review.PendingQuestions) != 0 {
		t.Fatal(stale, err)
	}
	if _, err = s.Act(c, a, id, app.Action{Action: "ready", RequestID: "ready"}); err != nil {
		t.Fatal(err)
	}
	dev := developer(p.ID, "dev")
	if _, err = s.Act(c, dev, id, app.Action{Action: "claim", RequestID: "claim"}); err != nil {
		t.Fatal(err)
	}
	inProgress, err := s.RequirementDetail(c, a, p.ID, app.RequirementDetailQuery{TaskID: id})
	if err != nil || inProgress.Task.Status != "doing" || inProgress.Review.State != "not_required" || len(inProgress.Review.PendingQuestions) != 0 || inProgress.NextActor != "developer" {
		t.Fatal("historical review overrode development state", inProgress, err)
	}
}

type failedPoolStore struct{ storage.Store }

func (s failedPoolStore) ListTasks(context.Context, string) ([]domain.Task, error) {
	return nil, errors.New("database unavailable")
}

func TestRequirementPoolFailureDoesNotBecomeZero(t *testing.T) {
	s, p := setup(t)
	s.Store = failedPoolStore{s.Store}
	if _, err := s.RequirementPool(context.Background(), app.Human(), p.ID, app.RequirementPoolQuery{}); err == nil {
		t.Fatal("storage error reported as empty pool")
	}
}
