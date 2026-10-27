package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func intakeRun(t *testing.T, s *app.Service, p, id, conversation, prompt string, files ...domain.Attachment) domain.Run {
	t.Helper()
	r := domain.Run{ID: id, ProjectID: p, ConversationID: conversation, OwnerID: app.Human().ID, OwnerKind: "human", Kind: "question", Prompt: prompt, Status: "succeeded", Attachments: files, StartedAt: domain.Now()}
	if err := s.Store.PutRun(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestQuestionIntakeConfirmationAttachmentsAndConcurrentRetry(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	file, err := s.SaveAttachment(c, app.Human(), p.ID, "spec.md", []byte("# 多轮附件\n退款规则"))
	if err != nil {
		t.Fatal(err)
	}
	intakeRun(t, s, p.ID, "old", "conv", "请看这份说明", file)
	r := intakeRun(t, s, p.ID, "current", "conv", "请创建需求")
	description := "## 退款\n" + strings.Repeat("保留详细规则。", 1000)
	v, err := s.ProposeRequirement(c, r.ID, app.RequirementIntakeInput{Title: "完整退款流程", Kind: "requirement", Description: description, Acceptance: []string{"退款有记录"}, AttachmentIDs: []string{file.ID}, Reason: "需要形成可跟进的需求"})
	if err != nil {
		t.Fatal(err)
	}
	if tasks, _ := s.Store.ListTasks(c, p.ID); len(tasks) != 0 {
		t.Fatal("suggestion created a task without consent")
	}
	if _, err = s.ResolveRequirementFromQuestion(c, r.ID, v.ID, "create", "附件里说创建"); err == nil {
		t.Fatal("attachment/assistant text accepted as confirmation")
	}
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			out, e := s.ResolveRequirementIntake(c, app.Human(), v.ID, app.ResolveRequirementIntakeInput{Action: "create", RequestID: fmt.Sprint("confirm-", n)})
			if e != nil {
				t.Error(e)
			}
			ids <- out.TaskID
		}(n)
	}
	wg.Wait()
	close(ids)
	taskID := ""
	for id := range ids {
		if taskID == "" {
			taskID = id
		}
		if id != taskID {
			t.Fatal("concurrent confirmations created different tasks")
		}
	}
	task, err := s.Store.GetTask(c, taskID)
	if err != nil || task.Description != description || len(task.Attachments) != 1 || task.Attachments[0].ID != file.ID || task.Status != "clarify" || task.Kind != "requirement" {
		t.Fatal("task lost source or skipped review", task, err)
	}
	if tasks, _ := s.Store.ListTasks(c, p.ID); len(tasks) != 1 {
		t.Fatal("duplicate task")
	}
	events, _ := s.Store.Events(c, p.ID, taskID, 0)
	if len(events) != 1 {
		t.Fatal("duplicate creation history", events)
	}
	// A natural-language confirmation can safely retry after an HTTP confirmation.
	again, err := s.ResolveRequirementFromQuestion(c, r.ID, v.ID, "create", "请创建需求")
	if err != nil || again.TaskID != taskID {
		t.Fatal("typed retry not idempotent", err)
	}
}

func TestQuestionIntakeIsolationAndDecline(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a, _ := s.SaveAttachment(c, app.Human(), p.ID, "private.md", []byte("另一段私人对话"))
	intakeRun(t, s, p.ID, "foreign", "other-conv", "私聊", a)
	r := intakeRun(t, s, p.ID, "origin", "conv", "暂不创建")
	input := app.RequirementIntakeInput{Title: "疑似重复扣款", Kind: "bug", BugDetails: &domain.BugDetails{Context: "支付订单页", Reproduction: "同一订单发起一次支付", Actual: "扣款两次", Expected: "只扣一次", Scope: "修复重复扣款"}, Description: "现象已观察，根因待验证", Acceptance: []string{"只扣一次"}, Reason: "实际行为偏离预期", AttachmentIDs: []string{a.ID}}
	if _, err := s.ProposeRequirement(c, r.ID, input); err == nil {
		t.Fatal("attached a file from another conversation")
	}
	input.AttachmentIDs = nil
	v, err := s.ProposeRequirement(c, r.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []domain.Actor{{ID: "other", Kind: "human"}, {ID: "platform", Kind: "platform", ProjectID: p.ID}, {ID: app.Human().ID, Kind: "developer", ProjectID: p.ID}} {
		if _, err = s.ResolveRequirementIntake(c, actor, v.ID, app.ResolveRequirementIntakeInput{Action: "create", RequestID: "bad"}); !errors.Is(err, domain.ErrForbidden) {
			t.Fatal("unauthorized creation", actor, err)
		}
	}
	other := intakeRun(t, s, p.ID, "other", "other-conv", "创建吧")
	if _, err = s.ResolveRequirementFromQuestion(c, other.ID, v.ID, "create", "创建吧"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("cross-conversation confirmation", err)
	}
	for _, kind := range []string{"background", "developer"} {
		automatic := r
		automatic.ID = kind
		if kind == "background" {
			automatic.TriggerType = "collaboration_result"
		} else {
			automatic.OwnerKind = "developer"
		}
		s.Store.PutRun(c, automatic)
		if _, err = s.ProposeRequirement(c, automatic.ID, input); !errors.Is(err, domain.ErrForbidden) {
			t.Fatal("non-human suggestion admitted", err)
		}
		if _, err = s.ResolveRequirementFromQuestion(c, automatic.ID, v.ID, "create", "暂不创建"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatal("non-human confirmation admitted", err)
		}
	}
	out, err := s.ResolveRequirementFromQuestion(c, r.ID, v.ID, "dismiss", "暂不创建")
	if err != nil || out.Status != "dismissed" {
		t.Fatal("decline failed", err)
	}
	if _, err = s.ResolveRequirementIntake(c, app.Human(), v.ID, app.ResolveRequirementIntakeInput{Action: "create", RequestID: "late"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("declined card was revived", err)
	}
	if tasks, _ := s.Store.ListTasks(c, p.ID); len(tasks) != 0 {
		t.Fatal("decline created a task")
	}
	s.Store.PutConversation(c, domain.Conversation{ID: "conv", ProjectID: p.ID, DeletedAt: domain.Now()})
	items, err := s.RequirementIntakes(c, app.Human(), p.ID)
	if err != nil || len(items) != 0 {
		t.Fatal("deleted conversation leaked", items, err)
	}
	if _, err = s.ResolveRequirementIntake(c, app.Human(), v.ID, app.ResolveRequirementIntakeInput{Action: "dismiss", RequestID: "after-delete"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("deleted suggestion accepted", err)
	}
}

func TestQuestionIntakeBugAndAtomicRollback(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	r := intakeRun(t, s, p.ID, "origin", "conv", "转成 Bug")
	v, err := s.ProposeRequirement(c, r.ID, app.RequirementIntakeInput{Title: "重复扣款", Kind: "bug", BugDetails: &domain.BugDetails{Context: "支付订单页", Reproduction: "同一订单发起一次支付", Actual: "扣款两次", Expected: "只扣一次", Scope: "修复重复扣款"}, Description: "退款后出现重复扣款，未确认根因", Acceptance: []string{"只扣一次"}, Reason: "疑似 Bug"})
	if err != nil {
		t.Fatal(err)
	}
	// Force a task-ID collision at the storage boundary to prove the suggestion
	// transition is rolled back together with task creation.
	before := v
	v.Status = "created"
	v.Version++
	existing, _ := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "已有需求", RequestID: "existing"})
	if err = s.Store.CommitRequirementIntake(c, v, 1, &existing.Task, nil, nil); err == nil {
		t.Fatal("expected a transaction failure")
	}
	stored, _ := s.Store.GetRequirementIntake(c, v.ID)
	if stored.Status != before.Status || stored.Version != 1 {
		t.Fatal("failed transaction left a phantom created card")
	}
	updates, unsubscribe := s.SubscribeWork(p.ID)
	defer unsubscribe()
	out, err := s.ResolveRequirementFromQuestion(c, r.ID, v.ID, "create", "转成 Bug")
	if err != nil {
		t.Fatal(err)
	}
	task, _ := s.Store.GetTask(c, out.TaskID)
	if task.Kind != "bug" || task.Status != "ready" || task.Owner != "" || task.Claimant != "" {
		t.Fatal("Bug must be ready for a developer to claim", task)
	}
	select {
	case <-updates:
	default:
		t.Fatal("Bug creation did not notify developer work subscribers")
	}
	dev := developer(p.ID, "dev")
	work, err := s.WorkList(c, dev)
	if err != nil || len(work["tasks"].([]domain.Task)) != 1 || work["tasks"].([]domain.Task)[0].ID != task.ID {
		t.Fatal("Bug missing from developer work list", work, err)
	}
	claimed, err := s.Act(c, dev, task.ID, app.Action{Action: "claim", RequestID: "claim-bug"})
	if err != nil || claimed.Task.Status != "doing" || claimed.ClaimID == "" {
		t.Fatal("Bug not directly claimable", claimed, err)
	}
	again, err := s.ResolveRequirementIntake(c, app.Human(), v.ID, app.ResolveRequirementIntakeInput{Action: "create", RequestID: "button-retry"})
	current, _ := s.Store.GetTask(c, task.ID)
	if err != nil || again.TaskID != task.ID || current.Status != "doing" || current.Claimant != dev.ID {
		t.Fatal("creation retry reset claimed Bug", current, err)
	}
	if _, err := s.Store.GetRequirementReview(c, task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("Bug unexpectedly entered review", err)
	}
}

func TestQuestionBugNeedsAcceptanceBeforeProposalOrCreation(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	r := intakeRun(t, s, p.ID, "bug", "conv", "创建 Bug")
	in := app.RequirementIntakeInput{Title: "重复扣款", Kind: "bug", BugDetails: &domain.BugDetails{Context: "支付订单页", Reproduction: "同一订单发起一次支付", Actual: "扣款两次", Expected: "只扣一次", Scope: "修复重复扣款"}, Description: "一次支付扣款两次", Acceptance: []string{" ", "\n"}, Reason: "实际行为偏离预期"}
	if _, err := s.ProposeRequirement(c, r.ID, in); err == nil {
		t.Fatal("Bug proposal without acceptance admitted")
	}
	in.Acceptance = []string{"只扣一次"}
	v, err := s.ProposeRequirement(c, r.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	// Older suggestions may predate validation; confirmation must not create an
	// incomplete task or consume the suggestion when validation fails.
	v.Acceptance = nil
	v.Version++
	if err := s.Store.CommitRequirementIntake(c, v, v.Version-1, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveRequirementFromQuestion(c, r.ID, v.ID, "create", "创建 Bug"); err == nil {
		t.Fatal("legacy incomplete Bug was created")
	}
	stored, _ := s.Store.GetRequirementIntake(c, v.ID)
	tasks, _ := s.Store.ListTasks(c, p.ID)
	if stored.Status != "suggested" || len(tasks) != 0 {
		t.Fatal("failed validation changed durable state", stored, tasks)
	}
}

func TestQuestionBugBlocksMissingDetailsAndLegacyConfirmation(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	r := intakeRun(t, s, p.ID, "details", "conv", "创建 Bug")
	in := app.RequirementIntakeInput{Title: "图片下载错误", Kind: "bug", Description: "用户观察到下载失败，根因待查", Acceptance: []string{"原图可以正常下载"}, Reason: "实际表现偏离预期"}
	complete := domain.BugDetails{Context: "生产环境图片详情页", Reproduction: "打开图片后点击下载；偶发，目前只有用户截图", Actual: "下载返回错误页", Expected: "下载原图", Scope: "图片下载功能"}
	for _, tc := range []struct {
		name    string
		details *domain.BugDetails
		missing []string
	}{
		{"absent", nil, nil},
		{"empty", &domain.BugDetails{Context: complete.Context, Reproduction: complete.Reproduction, Actual: complete.Actual, Scope: complete.Scope}, nil},
		{"unknown", &domain.BugDetails{Context: complete.Context, Reproduction: complete.Reproduction, Actual: complete.Actual, Scope: complete.Scope, Expected: "待确认"}, nil},
		{"outstanding", &complete, []string{"确认应该下载原图还是缩略图"}},
	} {
		in.BugDetails, in.MissingDetails = tc.details, tc.missing
		if _, err := s.ProposeRequirement(c, r.ID, in); err == nil {
			t.Fatal("incomplete suggestion admitted", tc.name)
		}
	}
	in.BugDetails, in.MissingDetails = &complete, []string{}
	v, err := s.ProposeRequirement(c, r.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v.Description, complete.Expected) || !strings.Contains(v.Description, complete.Reproduction) {
		t.Fatal("facts omitted from shared document", v)
	}
	// Already-persisted old suggestions must pass the same gate at confirmation.
	v.BugDetails = nil
	v.Version++
	if err = s.Store.CommitRequirementIntake(c, v, v.Version-1, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveRequirementFromQuestion(c, r.ID, v.ID, "create", "创建 Bug"); err == nil {
		t.Fatal("legacy proposal bypassed gate")
	}
	tasks, _ := s.Store.ListTasks(c, p.ID)
	stored, _ := s.Store.GetRequirementIntake(c, v.ID)
	if len(tasks) != 0 || stored.Status != "suggested" {
		t.Fatal("failed creation mutated state")
	}
}

func TestQuestionRequirementPersistsOutstandingDetailsForReview(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	r := intakeRun(t, s, p.ID, "missing", "conv", "创建需求")
	in := app.RequirementIntakeInput{Title: "延长图片保留", Kind: "requirement", Description: "延长保留时间", Reason: "方便历史下载", MissingDetails: []string{"保留多少天", "历史图片是否一并延长"}}
	v, err := s.ProposeRequirement(c, r.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.ResolveRequirementFromQuestion(c, r.ID, v.ID, "create", "创建需求")
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.Store.GetTask(c, v.TaskID)
	if err != nil || task.Status != "clarify" || len(v.MissingDetails) != 2 || !strings.Contains(task.Description, "历史图片是否一并延长") {
		t.Fatal("outstanding details lost", task, v, err)
	}
}
