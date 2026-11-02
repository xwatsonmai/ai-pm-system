package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func requestTestingChange(t *testing.T, s *app.Service, parent domain.Task, bug *domain.Task) domain.RequirementReview {
	t.Helper()
	current, err := s.Store.GetTask(context.Background(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := app.RequirementReviewInput{Action: "request_change", RequestID: domain.ID("change_"), ExpectedTaskVersion: current.Version, Body: "本轮改为浏览器定位，保留原记录与其他功能"}
	if bug != nil {
		currentBug, e := s.Store.GetTask(context.Background(), bug.ID)
		if e != nil {
			t.Fatal(e)
		}
		in.SourceBugID, in.SourceBugVersion = bug.ID, currentBug.Version
	}
	v, err := s.SaveRequirementReview(context.Background(), app.Human(), parent.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func confirmTestingChange(t *testing.T, s *app.Service, parent domain.Task, v domain.RequirementReview) domain.Task {
	t.Helper()
	c := context.Background()
	if err := s.FinishRequirementReview(c, parent.ID, v.Generation, "change-review", &domain.RequirementReviewResult{Decision: "ready", Body: "本轮变更范围明确，等待产品确认", Proposal: &domain.RequirementProposal{Description: "改用浏览器定位，保留原功能和异常提示", Acceptance: []string{"浏览器授权后记录位置；拒绝或失败有提示"}}}, ""); err != nil {
		t.Fatal(err)
	}
	current, err := s.Store.GetTask(c, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := s.SaveRequirementReview(c, app.Human(), parent.ID, app.RequirementReviewInput{Action: "confirm", Generation: v.Generation, ExpectedTaskVersion: current.Version, RequestID: domain.ID("confirm_")})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Status != "confirmed" {
		t.Fatal(confirmed.Status)
	}
	current, err = s.Store.GetTask(c, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	return current
}
func TestTestingChangeTransfersFeedbackAndPreservesBranchAndClaim(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	parent := testingRequirement(t, s, p.ID)
	original := parent
	bug := fileTestBug(t, s, parent)
	oldReview := reviewBug(t, s, bug, "reject_bug")
	_, err := s.SaveRequirementReview(c, app.Human(), bug.ID, app.RequirementReviewInput{Action: "message", RequestID: "correction", Body: "不是要求先上线原实现，本轮改为浏览器定位"})
	if err != nil {
		t.Fatal(err)
	}
	v := requestTestingChange(t, s, parent, &bug)
	parent, _ = s.Store.GetTask(c, parent.ID)
	frozen, _ := s.Store.GetTask(c, bug.ID)
	if parent.Status != "clarify" || !parent.ReviewingChange() || frozen.Status == "closed" || frozen.ChangeRequestID != parent.RequirementChange.ID {
		t.Fatal("change did not pause without closing feedback", parent, frozen)
	}
	if !strings.Contains(app.ReviewPrompt(v), "本轮改为浏览器定位") || !strings.Contains(app.ReviewPrompt(v), original.Submission.SHA) {
		t.Fatal("feedback or baseline lost")
	}
	if _, err = s.Act(c, app.Human(), parent.ID, app.Action{Action: "ready", RequestID: "skip-confirm"}); err == nil {
		t.Fatal("skipped change confirmation")
	}
	if _, err = s.SaveRequirementReview(c, app.Human(), bug.ID, app.RequirementReviewInput{Action: "confirm_rejection", Generation: oldReview.Generation, RequestID: "old-rejection"}); err == nil {
		t.Fatal("closed feedback during change review")
	}
	// Late output from the old Bug review must not resume development of that Bug.
	if err = s.FinishRequirementReview(c, bug.ID, oldReview.Generation, "late", &domain.RequirementReviewResult{Decision: "reject_bug", Body: "旧结论"}, ""); err != nil {
		t.Fatal(err)
	}
	parent = confirmTestingChange(t, s, parent, v)
	closed, _ := s.Store.GetTask(c, bug.ID)
	if parent.Status != "doing" || parent.Revision != original.Revision+1 || parent.Branch != original.Branch || parent.ClaimHash != original.ClaimHash || parent.ClaimClientID != original.ClaimClientID || parent.Submission != nil || parent.Deployment != nil || parent.TestVerification != nil || closed.Status != "closed" || !strings.Contains(closed.Resolution.Reason, "转为原需求") {
		t.Fatal("confirmation lost workflow evidence", parent, closed)
	}
	if _, err = s.Act(c, app.Human(), bug.ID, app.Action{Action: "reopen", RequestID: "reopen-transferred", Body: "重复开启旧反馈"}); err == nil {
		t.Fatal("reopened transferred feedback")
	}
	if _, err = s.Act(c, app.Human(), parent.ID, app.Action{Action: "submit", RequestID: "old-submit", Revision: original.Revision, Submission: original.Submission}); err == nil {
		t.Fatal("old revision was submitted")
	}
	fixed := strings.Repeat("c", 40)
	checked := false
	s.ValidateTestingCode = func(_ context.Context, project, branch, sha string, required []string) error {
		checked = true
		if project != p.ID || branch != original.Branch || sha != fixed || len(required) != 1 || required[0] != original.Submission.SHA {
			t.Fatal("baseline not checked", project, branch, sha, required)
		}
		return nil
	}
	result, err := s.Act(c, app.Human(), parent.ID, app.Action{Action: "submit", RequestID: "new-submit", Revision: parent.Revision, Submission: &domain.Submission{Summary: "变更完成", SHA: fixed, Branch: original.Branch, Tests: "本地验证通过"}})
	if err != nil || !checked || result.Task.Status != "testing" {
		t.Fatal(result, err)
	}
	passTesting(t, s, result.Task)
}
func TestTestingChangeKeepsOtherBugsBlockingAndRejectsStaleConfirmation(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	parent := testingRequirement(t, s, p.ID)
	source := fileTestBug(t, s, parent)
	other := fileTestBug(t, s, parent)
	v := requestTestingChange(t, s, parent, &source)
	before, _ := s.Store.GetTask(c, parent.ID)
	// Another feedback mutation invalidates open product forms atomically.
	if _, err := s.Act(c, app.Human(), other.ID, app.Action{Action: "report", RequestID: "other-note", Body: "补充复现"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveRequirementReview(c, app.Human(), parent.ID, app.RequirementReviewInput{Action: "cancel_change", ExpectedTaskVersion: before.Version, Generation: v.Generation, RequestID: "stale-cancel"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	parent = confirmTestingChange(t, s, parent, v)
	remaining, _ := s.Store.GetTask(c, other.ID)
	if domain.TaskFinished(remaining.Status) {
		t.Fatal("unrelated Bug was closed")
	}
	s.ValidateTestingCode = func(context.Context, string, string, string, []string) error { return nil }
	in := app.Action{Action: "submit", RequestID: "blocked-new-submit", Revision: parent.Revision, Submission: &domain.Submission{Summary: "本轮改动完成", SHA: strings.Repeat("c", 40), Tests: "回归通过"}}
	if _, err := s.Act(c, app.Human(), parent.ID, in); err == nil {
		t.Fatal("open Bug bypassed product testing")
	}
	if _, err := s.Act(c, app.Human(), other.ID, app.Action{Action: "close", RequestID: "explicit-other-close", Body: "产品明确取消此独立问题"}); err != nil {
		t.Fatal(err)
	}
	in.RequestID = "after-other-close"
	if _, err := s.Act(c, app.Human(), parent.ID, in); err != nil {
		t.Fatal(err)
	}
}
func TestTestingChangeCancelRetryAndIdempotency(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	parent := testingRequirement(t, s, p.ID)
	bug := fileTestBug(t, s, parent)
	parent, _ = s.Store.GetTask(c, parent.ID)
	original := parent
	input := app.RequirementReviewInput{Action: "request_change", ExpectedTaskVersion: parent.Version, SourceBugID: bug.ID, SourceBugVersion: bug.Version, RequestID: "once-change", Body: "调整本轮定位方案"}
	v, err := s.SaveRequirementReview(c, app.Human(), parent.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.SaveRequirementReview(c, app.Human(), parent.ID, input)
	if err != nil || again.Generation != v.Generation {
		t.Fatal(again, err)
	}
	if err = s.FinishRequirementReview(c, parent.ID, v.Generation, "failed", nil, "模型暂不可用"); err != nil {
		t.Fatal(err)
	}
	parent, _ = s.Store.GetTask(c, parent.ID)
	currentBug, _ := s.Store.GetTask(c, bug.ID)
	if parent.Status != "clarify" || currentBug.Status == "closed" {
		t.Fatal("failed review released requirement")
	}
	cancelled, err := s.SaveRequirementReview(c, app.Human(), parent.ID, app.RequirementReviewInput{Action: "cancel_change", ExpectedTaskVersion: parent.Version, Generation: v.Generation, RequestID: "cancel"})
	if err != nil {
		t.Fatal(err)
	}
	parent, _ = s.Store.GetTask(c, parent.ID)
	currentBug, _ = s.Store.GetTask(c, bug.ID)
	if cancelled.Status != "change_canceled" || parent.Status != original.Status || parent.Revision != original.Revision || parent.Submission.SHA != original.Submission.SHA || currentBug.ChangeRequestID != "" || currentBug.Status != bug.Status {
		t.Fatal("cancel lost original workflow", parent, currentBug)
	}
	// A new change is possible after cancellation; old output cannot overwrite it.
	next := requestTestingChange(t, s, parent, &currentBug)
	if err = s.FinishRequirementReview(c, parent.ID, v.Generation, "late", &domain.RequirementReviewResult{Decision: "ready", Body: "旧结果", Proposal: &domain.RequirementProposal{Description: "旧稿", Acceptance: []string{"旧条件"}}}, ""); err != nil {
		t.Fatal(err)
	}
	latest, _ := s.RequirementReview(c, app.Human(), parent.ID)
	if latest.Generation != next.Generation || latest.Status != "queued" {
		t.Fatal("old model output applied", latest)
	}
}
func TestTestingChangeRacesTestPass(t *testing.T) {
	s, p := setup(t)
	parent := testingRequirement(t, s, p.ID)
	c := context.Background()
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := s.SaveRequirementReview(c, app.Human(), parent.ID, app.RequirementReviewInput{Action: "request_change", ExpectedTaskVersion: parent.Version, Body: "调整当前需求", RequestID: "race-change"})
		errs <- err
	}()
	go func() {
		defer wg.Done()
		_, err := s.Act(c, app.Human(), parent.ID, app.Action{Action: "test_pass", Version: parent.Version, RequestID: "race-pass", TestVerification: &domain.TestVerification{Revision: parent.Revision, SHA: parent.Submission.SHA, Environment: "测试", Note: "通过"}})
		errs <- err
	}()
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("change and test pass both committed", success)
	}
}
func TestTestingChangeAuthorityAndStages(t *testing.T) {
	s, p := setup(t)
	parent := testingRequirement(t, s, p.ID)
	c := context.Background()
	input := app.RequirementReviewInput{Action: "request_change", ExpectedTaskVersion: parent.Version, Body: "调整本轮", RequestID: "developer-change"}
	if _, err := s.SaveRequirementReview(c, developer(p.ID, "dev"), parent.ID, input); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
	unrelated := create(t, s, p.ID)
	input.ExpectedTaskVersion, input.RequestID = unrelated.Version, "ready-change"
	if _, err := s.SaveRequirementReview(c, app.Human(), unrelated.ID, input); err == nil {
		t.Fatal("changed a not-submitted requirement")
	}
	input.ExpectedTaskVersion, input.RequestID = parent.Version, "other-source"
	input.SourceBugID, input.SourceBugVersion = unrelated.ID, unrelated.Version
	if _, err := s.SaveRequirementReview(c, app.Human(), parent.ID, input); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
}

func TestTestingChangeOtherBugResolvedDuringReviewAndReadyFeedbackIsNotClaimable(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	parent := testingRequirement(t, s, p.ID)
	bug := fileTestBug(t, s, parent)
	reviewBug(t, s, bug, "ready")
	v := requestTestingChange(t, s, parent, &bug)
	work, err := s.WorkList(c, developer(p.ID, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	seenParent := false
	for _, item := range work["tasks"].([]domain.Task) {
		if item.ID == bug.ID {
			t.Fatal("transferred feedback still claimable")
		}
		seenParent = seenParent || item.ID == parent.ID
	}
	if !seenParent {
		t.Fatal("original developer lost pending requirement")
	}
	frozenReview, err := s.SaveRequirementReview(c, app.Human(), bug.ID, app.RequirementReviewInput{Action: "start", RequestID: "open-frozen-feedback"})
	if err != nil || frozenReview.Status != "change_pending" {
		t.Fatal("frozen feedback restarted old review", frozenReview, err)
	}
	current, _ := s.Store.GetTask(c, parent.ID)
	if _, err = s.SaveRequirementReview(c, app.Human(), parent.ID, app.RequirementReviewInput{Action: "cancel_change", ExpectedTaskVersion: current.Version, Generation: v.Generation, RequestID: "cancel-ready-source"}); err != nil {
		t.Fatal(err)
	}
	work, err = s.WorkList(c, developer(p.ID, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	seenBug := false
	for _, item := range work["tasks"].([]domain.Task) {
		seenBug = seenBug || item.ID == bug.ID
	}
	if !seenBug {
		t.Fatal("canceled transfer did not restore feedback")
	}
	// With no selected source, other Bugs may finish during change review.
	v = requestTestingChange(t, s, parent, nil)
	if _, err = s.Act(c, app.Human(), bug.ID, app.Action{Action: "close", RequestID: "close-during-change", Body: "产品取消此问题"}); err != nil {
		t.Fatal(err)
	}
	current, _ = s.Store.GetTask(c, parent.ID)
	if _, err = s.SaveRequirementReview(c, app.Human(), parent.ID, app.RequirementReviewInput{Action: "cancel_change", ExpectedTaskVersion: current.Version, Generation: v.Generation, RequestID: "cancel-with-no-bugs"}); err != nil {
		t.Fatal(err)
	}
	current, _ = s.Store.GetTask(c, parent.ID)
	if current.Status != "retest" {
		t.Fatal("canceled to stale fixing after last Bug closed", current.Status)
	}
}
