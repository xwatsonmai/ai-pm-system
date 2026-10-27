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

func passTesting(t *testing.T, s *app.Service, task domain.Task) domain.Task {
	t.Helper()
	m, err := s.Act(context.Background(), app.Human(), task.ID, app.Action{Action: "test_pass", Version: task.Version, RequestID: domain.ID("test_"), TestVerification: &domain.TestVerification{Revision: task.Revision, SHA: task.Submission.SHA, Environment: "测试环境", Note: "产品逐项测试通过"}})
	if err != nil {
		t.Fatal(err)
	}
	return m.Task
}
func testingRequirement(t *testing.T, s *app.Service, p string) domain.Task {
	t.Helper()
	c := context.Background()
	task := create(t, s, p)
	m, e := s.Act(c, app.Human(), task.ID, app.Action{Action: "claim", RequestID: domain.ID("claim_"), Branch: "feature/refund"})
	if e != nil {
		t.Fatal(e)
	}
	m, e = s.Act(c, app.Human(), task.ID, app.Action{Action: "submit", RequestID: domain.ID("submit_"), Revision: task.Revision, Submission: &domain.Submission{SHA: strings.Repeat("a", 40), Summary: "退款实现", Tests: "自测通过"}})
	if e != nil {
		t.Fatal(e)
	}
	if m.Task.Status != "testing" {
		t.Fatal(m.Task.Status)
	}
	return m.Task
}
func fileTestBug(t *testing.T, s *app.Service, parent domain.Task) domain.Task {
	t.Helper()
	parent, e := s.Store.GetTask(context.Background(), parent.ID)
	if e != nil {
		t.Fatal(e)
	}
	m, e := s.CreateTask(context.Background(), app.Human(), parent.ProjectID, app.TaskInput{Title: "退款重复触发", Kind: "bug", Description: "连续点击退款出现两笔记录", ParentRequirementID: parent.ID, ParentVersion: parent.Version, RequestID: domain.ID("bug_")})
	if e != nil {
		t.Fatal(e)
	}
	return m.Task
}
func reviewBug(t *testing.T, s *app.Service, bug domain.Task, decision string) domain.RequirementReview {
	t.Helper()
	c := context.Background()
	v, e := s.SaveRequirementReview(c, app.Human(), bug.ID, app.RequirementReviewInput{Action: "start", RequestID: domain.ID("review_")})
	if e != nil {
		t.Fatal(e)
	}
	result := &domain.RequirementReviewResult{Decision: decision, Body: "依据提测分支的退款处理逻辑核对重复请求"}
	if decision == "ready" {
		result.Proposal = &domain.RequirementProposal{Description: "重复退款应去重", Acceptance: []string{"重复点击只产生一笔退款"}}
	}
	if decision == "clarify" {
		result.Questions = []domain.RequirementQuestion{{ID: "steps", Label: "怎样操作出现问题？", Type: "textarea"}}
	}
	if e = s.FinishRequirementReview(c, bug.ID, v.Generation, "review_"+bug.ID, result, ""); e != nil {
		t.Fatal(e)
	}
	v, e = s.RequirementReview(c, app.Human(), bug.ID)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestTestingBugReviewRepairAndRetest(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	parent := testingRequirement(t, s, p.ID)
	if _, e := s.Act(c, app.Human(), parent.ID, app.Action{Action: "rework", RequestID: "skip-bug-filing", Body: "测试发现缺陷"}); e == nil {
		t.Fatal("testing requirement returned directly to development")
	}
	first := fileTestBug(t, s, parent)
	second := fileTestBug(t, s, parent)
	if first.BugContext.SHA != parent.Submission.SHA || first.BugContext.Branch != parent.Branch {
		t.Fatal("baseline not pinned")
	}
	parent, _ = s.Store.GetTask(c, parent.ID)
	if parent.Status != "fixing" {
		t.Fatal(parent.Status)
	}
	if _, e := s.Act(c, app.Human(), parent.ID, app.Action{Action: "test_pass", RequestID: "skip-bugs", Version: parent.Version, TestVerification: &domain.TestVerification{Revision: parent.Revision, SHA: parent.Submission.SHA, Environment: "test", Note: "pass"}}); e == nil {
		t.Fatal("unresolved bugs bypassed")
	}
	v := reviewBug(t, s, first, "ready")
	if v.Status != "confirmed" {
		t.Fatal(v.Status)
	}
	first, _ = s.Store.GetTask(c, first.ID)
	if first.Status != "ready" {
		t.Fatal(first.Status)
	}
	// A proposed rejection keeps blocking until product explicitly agrees.
	reject := reviewBug(t, s, second, "reject_bug")
	if reject.Status != "rejection_pending" {
		t.Fatal(reject.Status)
	}
	second, _ = s.Store.GetTask(c, second.ID)
	if second.Status != "clarify" {
		t.Fatal(second.Status)
	}
	if _, e := s.SaveRequirementReview(c, app.Human(), second.ID, app.RequirementReviewInput{Action: "confirm_rejection", Generation: reject.Generation - 1, RequestID: "stale-close"}); !errors.Is(e, domain.ErrConflict) {
		t.Fatal(e)
	}
	if _, e := s.SaveRequirementReview(c, app.Human(), second.ID, app.RequirementReviewInput{Action: "confirm_rejection", Generation: reject.Generation, RequestID: "close-bug"}); e != nil {
		t.Fatal(e)
	}
	m, e := s.Act(c, app.Human(), first.ID, app.Action{Action: "claim", RequestID: "bug-claim", Branch: "fix/duplicate"})
	if e != nil {
		t.Fatal(e)
	}
	m, e = s.Act(c, app.Human(), first.ID, app.Action{Action: "submit", RequestID: "bug-submit", Revision: m.Task.Revision, Submission: &domain.Submission{SHA: strings.Repeat("b", 40), Summary: "去重修复", Tests: "回归通过"}})
	if e != nil || m.Task.Status != "verifying" {
		t.Fatal(m, e)
	}
	parent, _ = s.Store.GetTask(c, parent.ID)
	if parent.Status != "fixing" {
		t.Fatal("developer self-test unblocked parent")
	}
	verify := app.Action{Action: "verify_bug", RequestID: "verify-bug", Version: m.Task.Version, TestVerification: &domain.TestVerification{Revision: m.Task.Revision, SHA: m.Task.Submission.SHA, Environment: "测试环境", Note: "重复点击验证修复有效"}}
	if _, e = s.Act(c, developer(p.ID, "dev"), first.ID, verify); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal(e)
	}
	if _, e = s.Act(c, app.Human(), first.ID, verify); e != nil {
		t.Fatal(e)
	}
	parent, _ = s.Store.GetTask(c, parent.ID)
	if parent.Status != "retest" {
		t.Fatal(parent.Status)
	}
	// Product can reopen a verified repair while the parent is still in testing;
	// preserve the developer claim, invalidate stale verification and block retest.
	resolved, _ := s.Store.GetTask(c, first.ID)
	returned, err := s.Act(c, app.Human(), first.ID, app.Action{Action: "return_bug", Version: resolved.Version, RequestID: "repair-regressed", Body: "再次复现"})
	if err != nil || returned.Task.Status != "doing" || returned.Task.TestVerification != nil || returned.Task.ClaimHash != resolved.ClaimHash {
		t.Fatal("return lost claim or verification state", err)
	}
	blockedParent, _ := s.Store.GetTask(c, parent.ID)
	if blockedParent.Status != "fixing" || blockedParent.Version <= parent.Version {
		t.Fatal("returned Bug did not reblock parent")
	}
	if _, err = s.Act(c, app.Human(), parent.ID, app.Action{Action: "start_retest", Version: parent.Version, RequestID: "stale-retest"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale retest was accepted", err)
	}
	resubmitted, err := s.Act(c, app.Human(), first.ID, app.Action{Action: "submit", Revision: returned.Task.Revision, RequestID: "repair-again", Submission: &domain.Submission{SHA: strings.Repeat("b", 40), Summary: "补充修复验证", Tests: "复现步骤验证"}})
	if err != nil {
		t.Fatal(err)
	}
	verify.Version, verify.RequestID = resubmitted.Task.Version, "verify-again"
	if _, err = s.Act(c, app.Human(), first.ID, verify); err != nil {
		t.Fatal(err)
	}
	parent, _ = s.Store.GetTask(c, parent.ID)
	checked := false
	s.ValidateTestingCode = func(_ context.Context, project, branch, sha string, fixes []string) error {
		checked = true
		if branch != parent.Branch || sha != strings.Repeat("c", 40) || len(fixes) != 1 || fixes[0] != strings.Repeat("b", 40) {
			t.Fatal(branch, sha, fixes)
		}
		return nil
	}
	m, e = s.Act(c, app.Human(), parent.ID, app.Action{Action: "start_retest", RequestID: "retest", Version: parent.Version, TestVerification: &domain.TestVerification{Revision: parent.Revision, SHA: strings.Repeat("c", 40), Environment: "测试环境", Note: "修复已合入需求分支，开始复测"}})
	if e != nil || m.Task.Status != "testing" || !checked {
		t.Fatal(m, e)
	}
	parent = passTesting(t, s, m.Task)
	if parent.Status != "pending_release" || parent.TestVerification == nil {
		t.Fatal(parent)
	}
	// A stale/reopened Bug cannot silently break an already approved release.
	if _, e = s.Act(c, app.Human(), second.ID, app.Action{Action: "reopen", RequestID: "late-reopen", Body: "重新出现"}); e == nil {
		t.Fatal("reopened after release gate")
	}
}

func TestTestingBugCreationRacesTestPass(t *testing.T) {
	s, p := setup(t)
	parent := testingRequirement(t, s, p.ID)
	c := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, e := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "并发 Bug", Kind: "bug", ParentRequirementID: parent.ID, ParentVersion: parent.Version, RequestID: "race-create"})
		errs <- e
	}()
	go func() {
		defer wg.Done()
		_, e := s.Act(c, app.Human(), parent.ID, app.Action{Action: "test_pass", RequestID: "race-pass", Version: parent.Version, TestVerification: &domain.TestVerification{Revision: parent.Revision, SHA: parent.Submission.SHA, Environment: "test", Note: "通过"}})
		errs <- e
	}()
	wg.Wait()
	close(errs)
	success := 0
	for e := range errs {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("both conflicting decisions committed", success)
	}
}

func TestTestingBugClarificationAndRejectedFeedback(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	parent := testingRequirement(t, s, p.ID)
	bug := fileTestBug(t, s, parent)
	v := reviewBug(t, s, bug, "reject_bug")
	v, e := s.SaveRequirementReview(c, app.Human(), bug.ID, app.RequirementReviewInput{Action: "message", RequestID: "disagree", Body: "不是原设计行为，请核对重复点击", Generation: v.Generation})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FinishRequirementReview(c, bug.ID, v.Generation, "clarify", &domain.RequirementReviewResult{Decision: "clarify", Body: "补充操作顺序", Questions: []domain.RequirementQuestion{{ID: "steps", Type: "textarea", Label: "操作顺序"}}}, ""); e != nil {
		t.Fatal(e)
	}
	v, _ = s.RequirementReview(c, app.Human(), bug.ID)
	if v.Status != "needs_input" {
		t.Fatal(v.Status)
	}
	if _, e = s.SaveRequirementReview(c, app.Human(), bug.ID, app.RequirementReviewInput{Action: "confirm_rejection", Generation: v.Generation, RequestID: "old-rejection"}); e == nil {
		t.Fatal("stale rejection closed bug")
	}
	parent, _ = s.Store.GetTask(c, parent.ID)
	if parent.Status != "fixing" {
		t.Fatal(parent.Status)
	}
}

func TestLinkedBugRetryIsolationAndProductAuthority(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	parent := testingRequirement(t, s, p.ID)
	in := app.TaskInput{Title: "幂等缺陷", Kind: "bug", Description: "业务问题", ParentRequirementID: parent.ID, ParentVersion: parent.Version, RequestID: "file-once"}
	if _, err := s.CreateTask(c, developer(p.ID, "dev"), p.ID, in); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
	made, err := s.CreateTask(c, app.Human(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := s.Store.GetTask(c, parent.ID)
	repeated, err := s.CreateTask(c, app.Human(), p.ID, in)
	if err != nil || repeated.Task.ID != made.Task.ID {
		t.Fatal(repeated, err)
	}
	after, _ := s.Store.GetTask(c, parent.ID)
	if after.Version != current.Version {
		t.Fatal("replay bumped parent")
	}
	in.RequestID = "another-stale-report"
	if _, err = s.CreateTask(c, app.Human(), p.ID, in); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	other, err := s.SaveProject(c, domain.Project{Name: "another"})
	if err != nil {
		t.Fatal(err)
	}
	in.RequestID = "cross-project"
	if _, err = s.CreateTask(c, app.Human(), other.ID, in); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = s.Act(c, app.Human(), made.Task.ID, app.Action{Action: "ready", RequestID: "bypass-review"}); err == nil {
		t.Fatal("bypassed Agent Bug review")
	}
	if _, err = s.Act(c, app.Human(), parent.ID, app.Action{Action: "close", RequestID: "orphan-bug", Body: "cancel requirement"}); err == nil {
		t.Fatal("orphaned active Bug")
	}
	// Transient failures persist the Bug and keep the requirement blocked.
	review, err := s.SaveRequirementReview(c, app.Human(), made.Task.ID, app.RequirementReviewInput{Action: "start", RequestID: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishRequirementReview(c, made.Task.ID, review.Generation, "", nil, "代码暂不可读取"); err != nil {
		t.Fatal(err)
	}
	after, _ = s.Store.GetTask(c, parent.ID)
	if after.Status != "fixing" {
		t.Fatal(after.Status)
	}
}

func TestPlatformBugTestingCanReturnWithoutReturningRequirement(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	parent := testingRequirement(t, s, p.ID)
	if _, err := s.Act(c, app.Human(), parent.ID, app.Action{Action: "return_bug", Version: parent.Version, RequestID: "return-requirement", Body: "测试未通过"}); err == nil {
		t.Fatal("requirement bypassed Bug flow")
	}
	m, err := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "独立平台 Bug", Kind: "bug", Description: "平台异常", Acceptance: []string{"异常消失"}, RequestID: "platform-bug"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []app.Action{
		{Action: "ready", RequestID: "platform-ready"},
		{Action: "claim", Branch: "fix/platform", RequestID: "platform-claim"},
		{Action: "submit", Revision: m.Task.Revision, RequestID: "platform-submit", Submission: &domain.Submission{SHA: strings.Repeat("b", 40), Summary: "修复", Tests: "自测"}},
	} {
		m, err = s.Act(c, app.Human(), m.Task.ID, a)
		if err != nil {
			t.Fatal(err)
		}
	}
	action := app.Action{Action: "return_bug", Version: m.Task.Version, RequestID: "platform-return", Body: "仍可复现"}
	if _, err = s.Act(c, developer(p.ID, "dev"), m.Task.ID, action); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
	m, err = s.Act(c, app.Human(), m.Task.ID, action)
	if err != nil || m.Task.Status != "doing" {
		t.Fatal(m.Task.Status, err)
	}
	m, err = s.Act(c, app.Human(), m.Task.ID, app.Action{Action: "submit", Revision: m.Task.Revision, RequestID: "platform-resubmit", Submission: &domain.Submission{SHA: strings.Repeat("c", 40), Summary: "再次修复", Tests: "复现步骤回归"}})
	if err != nil || m.Task.Status != "testing" {
		t.Fatal(m.Task.Status, err)
	}
	passTesting(t, s, m.Task)
}
