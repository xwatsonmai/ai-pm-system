package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRequirementDiscussionKeepsOneRoomAndCannotMutateWorkflow(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	m, err := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "所在地精度", Description: "记录国家地区", RequestID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	id := m.Task.ID
	v, err := s.SaveRequirementReview(c, app.Human(), id, app.RequirementReviewInput{Action: "message", Body: "精确到国家即可", RequestID: "scope"})
	if err != nil {
		t.Fatal(err)
	}
	conversation := v.ConversationID
	proposal := &domain.RequirementReviewResult{Decision: "ready", Body: "范围明确", Proposal: &domain.RequirementProposal{Description: "记录国家地区", Acceptance: []string{"展示国家"}}}
	if err = s.FinishRequirementReview(c, id, v.Generation, "formal-review", proposal, ""); err != nil {
		t.Fatal(err)
	}
	v, err = s.SaveRequirementReview(c, app.Human(), id, app.RequirementReviewInput{Action: "confirm", Generation: v.Generation, RequestID: "confirm"})
	if err != nil {
		t.Fatal(err)
	}
	m, err = s.Act(c, developer(p.ID, "dev"), id, app.Action{Action: "claim", RequestID: "claim", Branch: "feature/location"})
	if err != nil {
		t.Fatal(err)
	}
	m, err = s.Act(c, app.Human(), id, app.Action{Action: "submit", Revision: m.Task.Revision, RequestID: "submit", Submission: &domain.Submission{SHA: strings.Repeat("a", 40), Branch: "feature/location", Summary: "实现国家级定位", Tests: "定向自测"}})
	if err != nil {
		t.Fatal(err)
	}
	before := m.Task
	input := app.RequirementReviewInput{Action: "discuss", Body: "为什么只显示国家，是 Bug 吗？", RequestID: "question"}
	v, err = s.SaveRequirementReview(c, app.Human(), id, input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.SaveRequirementReview(c, app.Human(), id, input)
	if err != nil || again.Version != v.Version || v.ConversationID != conversation {
		t.Fatal("room changed or input duplicated", err)
	}
	// Even a syntactically valid formal proposal cannot be applied as an answer.
	if err = s.FinishRequirementReview(c, id, v.Generation, "bad-answer", proposal, ""); err != nil {
		t.Fatal(err)
	}
	v, _ = s.RequirementReview(c, app.Human(), id)
	if v.Status != "failed" {
		t.Fatal("formal action accepted during discussion", v.Status)
	}
	v, err = s.SaveRequirementReview(c, app.Human(), id, app.RequirementReviewInput{Action: "retry", RequestID: "retry-answer"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishRequirementReview(c, id, v.Generation, "answer", &domain.RequirementReviewResult{Decision: "answer", Body: "已确认范围是国家级；如需省市级，请使用变更当前需求。"}, ""); err != nil {
		t.Fatal(err)
	}
	v, err = s.RequirementReview(c, app.Human(), id)
	if err != nil || v.Status != "answered" || v.ConversationID != conversation {
		t.Fatal(v.Status, err)
	}
	after, _ := s.Store.GetTask(c, id)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("discussion changed task, claim or delivery evidence")
	}
	var prompt map[string]any
	if err = json.Unmarshal([]byte(app.ReviewPrompt(v)), &prompt); err != nil {
		t.Fatal(err)
	}
	text := app.ReviewPrompt(v)
	for _, want := range []string{"精确到国家即可", "认领了开发", "提交了开发结果", before.Submission.SHA, "feature/location"} {
		if !strings.Contains(text, want) {
			t.Fatalf("continuous context missing %q", want)
		}
	}
	for _, action := range []string{"confirm", "confirm_rejection"} {
		if _, err = s.SaveRequirementReview(c, app.Human(), id, app.RequirementReviewInput{Action: action, Generation: v.Generation, RequestID: action + "-after-answer"}); err == nil {
			t.Fatal("answer authorized", action)
		}
	}
	// A real change keeps the very same room/session and requires explicit confirmation.
	v, err = s.SaveRequirementReview(c, app.Human(), id, app.RequirementReviewInput{Action: "request_change", ExpectedTaskVersion: after.Version, Body: "本轮细化到省市", RequestID: "change"})
	if err != nil || v.ConversationID != conversation || v.Mode != "testing_change" {
		t.Fatal("change forked room", v, err)
	}
}

func TestDiscussionAfterClosure(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	task := testingRequirement(t, s, p.ID)
	v, err := s.SaveRequirementReview(c, app.Human(), task.ID, app.RequirementReviewInput{Action: "discuss", Body: "这个实现的依据是什么？", RequestID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	// Closing a task retires the running formal turn, without forbidding later questions.
	_, err = s.Act(c, app.Human(), task.ID, app.Action{Action: "close", Version: task.Version, Body: "阶段结束", RequestID: "close"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishRequirementReview(c, task.ID, v.Generation, "late", &domain.RequirementReviewResult{Decision: "answer", Body: "旧回复"}, ""); err != nil {
		t.Fatal(err)
	}
	v, err = s.SaveRequirementReview(c, app.Human(), task.ID, app.RequirementReviewInput{Action: "discuss", Body: "关闭前的决定是什么？", RequestID: "after-close"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginRequirementReview(c, task.ID, v.Generation, "closed-answer"); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishRequirementReview(c, task.ID, v.Generation, "closed-answer", &domain.RequirementReviewResult{Decision: "answer", Body: "原评审保留，需求保持关闭。"}, ""); err != nil {
		t.Fatal(err)
	}
	stored, _ := s.Store.GetTask(c, task.ID)
	if stored.Status != "closed" || stored.Submission.SHA != task.Submission.SHA {
		t.Fatal("closed discussion reopened or damaged delivery")
	}
	room, err := s.RequirementReview(c, app.Human(), task.ID)
	if err != nil || room.Status != "answered" || room.Source.Status != "closed" {
		t.Fatal(room, err)
	}
}

func TestDiscussionCannotAnswerAgainstReplacedSubmission(t *testing.T) {
	s, actor, before, withdraw := prematureSubmission(t)
	c := context.Background()
	v, err := s.SaveRequirementReview(c, app.Human(), before.Task.ID, app.RequirementReviewInput{Action: "discuss", Body: "提测版本如何实现？", RequestID: "question"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Act(c, actor, before.Task.ID, withdraw); err != nil {
		t.Fatal(err)
	}
	current, err := s.Act(c, actor, before.Task.ID, app.Action{Action: "submit", RequestID: "new-submission", ClaimID: withdraw.ClaimID, Revision: before.Task.Revision, Submission: &domain.Submission{SHA: strings.Repeat("b", 40), Summary: "新实现", Tests: "已自测"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishRequirementReview(c, before.Task.ID, v.Generation, "stale-answer", &domain.RequirementReviewResult{Decision: "answer", Body: "旧代码的结论"}, ""); err != nil {
		t.Fatal(err)
	}
	room, err := s.RequirementReview(c, app.Human(), before.Task.ID)
	if err != nil || room.Status != "failed" || !strings.Contains(room.Error, "提测版本已更新") {
		t.Fatal(room.Error, err)
	}
	after, _ := s.Store.GetTask(c, before.Task.ID)
	if !reflect.DeepEqual(after, current.Task) {
		t.Fatal("stale answer overwrote the new submission")
	}
}
