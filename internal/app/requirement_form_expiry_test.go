package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"testing"
)

func TestReviewFormExpiryRejectsStaleAnswersAndPreservesReceipts(t *testing.T) {
	s, p := setup(t)
	c, a := context.Background(), app.Human()
	created, err := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "表单过期", Description: "需求原文", RequestID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Task.ID
	save := func(in app.RequirementReviewInput) domain.RequirementReview {
		t.Helper()
		v, e := s.SaveRequirementReview(c, a, id, in)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	question := &domain.RequirementReviewResult{Body: "确认范围", Decision: "clarify", Questions: []domain.RequirementQuestion{{ID: "scope", Label: "覆盖哪些订单？", Type: "radio", Options: []string{"全部", "部分"}}}}
	finish := func(v domain.RequirementReview, run string, result *domain.RequirementReviewResult) domain.RequirementReview {
		t.Helper()
		if e := s.FinishRequirementReview(c, id, v.Generation, run, result, ""); e != nil {
			t.Fatal(e)
		}
		out, e := s.RequirementReview(c, a, id)
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	v := finish(save(app.RequirementReviewInput{Action: "start", RequestID: "start"}), "first", question)
	old := v.Messages[len(v.Messages)-1].ID
	// Free-form corrections invalidate the sheet even without a reply_to or a new form.
	v = save(app.RequirementReviewInput{Action: "message", RequestID: "correct", Body: "理解不对，请只考虑历史订单"})
	if v.Messages[1].FormExpiredAt == 0 || v.Messages[1].FormExpiredReason == "" {
		t.Fatal("expiry was not persisted", v)
	}
	version := v.Version
	for _, in := range []app.RequirementReviewInput{
		{Action: "message", RequestID: "stale-answers", ReplyTo: old, Answers: map[string][]string{"scope": {"全部"}}},
		{Action: "message", RequestID: "stale-notes", ReplyTo: old, QuestionNotes: map[string]app.RequirementQuestionNoteInput{"scope": {Body: "旧轮说明"}}},
	} {
		if _, e := s.SaveRequirementReview(c, a, id, in); !errors.Is(e, domain.ErrConflict) {
			t.Fatalf("expired form accepted: %v", e)
		}
	}
	v = finish(v, "second", question)
	current := v.Messages[len(v.Messages)-1].ID
	if current == old {
		t.Fatal("new form reused an old message ID")
	}
	in := app.RequirementReviewInput{Action: "message", RequestID: "current-answers", ReplyTo: current, Answers: map[string][]string{"scope": {"部分"}}}
	v = save(in)
	if v.Version != version+2 {
		t.Fatal("stale submissions changed the conversation", v.Version)
	}
	count := len(v.Messages)
	// A response-loss retry still resolves the receipt after its form has expired.
	v = save(in)
	if len(v.Messages) != count {
		t.Fatal("retry appended duplicate answers")
	}
	if _, e := s.SaveRequirementReview(c, a, id, app.RequirementReviewInput{Action: "message", RequestID: "second-window", ReplyTo: current, Answers: in.Answers}); !errors.Is(e, domain.ErrConflict) {
		t.Fatalf("another window reused submitted form: %v", e)
	}
	result := &domain.RequirementReviewResult{Body: "旧问题不再适用，请补充新的目标。", Decision: "clarify", ExpireFormIDs: []string{old}}
	v = finish(v, "retire", result)
	stored, e := s.Store.GetRequirementReview(c, id)
	if e != nil || stored.Messages[1].FormExpiredReason != "Agent 已根据最新需求将此表单设为过期。" || len(stored.Messages[1].Result.Questions) != 1 {
		t.Fatal("Agent expiry lost history", stored, e)
	}
	formal, _ := s.Store.GetTask(c, id)
	if formal.Description != "需求原文" || formal.Revision != created.Task.Revision {
		t.Fatal("expiry changed official requirement")
	}
	// A bad Agent form ID is a durable failed review, not a stuck running turn.
	v = save(app.RequirementReviewInput{Action: "retry", RequestID: "bad-agent-id"})
	v = finish(v, "invalid", &domain.RequirementReviewResult{Body: "无法定位", Decision: "clarify", ExpireFormIDs: []string{"foreign-form"}})
	if v.Status != "failed" || v.Error == "" {
		t.Fatal("invalid expiry was not reported", v)
	}
}

func TestReviewFormExpiryAfterSourceRevisionChanges(t *testing.T) {
	s, p := setup(t)
	c, a := context.Background(), app.Human()
	created, err := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "版本更新", RequestID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Task.ID
	v, err := s.SaveRequirementReview(c, a, id, app.RequirementReviewInput{Action: "start", RequestID: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishRequirementReview(c, id, v.Generation, "first", &domain.RequirementReviewResult{Body: "旧范围", Decision: "clarify", Questions: []domain.RequirementQuestion{{ID: "scope", Label: "范围？", Type: "text"}}}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EditTask(c, a, id, app.TaskInput{Title: "版本更新", Description: "新的范围", Version: created.Task.Version, RequestID: "edit"}); err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveRequirementReview(c, a, id, app.RequirementReviewInput{Action: "message", RequestID: "stale-revision", ReplyTo: "rm_first", Answers: map[string][]string{"scope": {"旧范围"}}})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("old revision form accepted: %v", err)
	}
}
