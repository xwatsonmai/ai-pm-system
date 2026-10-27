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

func alignmentTask(t *testing.T) (*app.Service, domain.Actor, domain.Mutation) {
	t.Helper()
	s, p := setup(t)
	d := developer(p.ID, "employee")
	d.ClientID = "client-original"
	m, e := s.Act(context.Background(), d, create(t, s, p.ID).ID, app.Action{Action: "claim", RequestID: "claim"})
	if e != nil {
		t.Fatal(e)
	}
	return s, d, m
}

func askAlignment(t *testing.T, s *app.Service, d domain.Actor, m domain.Mutation) domain.Mutation {
	t.Helper()
	in := app.Action{Action: "request_alignment", RequestID: domain.ID("ask_"), ClaimID: m.ClaimID, Version: m.Task.Version, Revision: m.Task.Revision, Body: "开发发现：退款范围需要产品确认", Questions: []domain.RequirementQuestion{
		{ID: "scope", Label: "覆盖哪些订单？", Type: "radio", Options: []string{"全部订单", "线上订单"}, Required: true},
		{ID: "edge", Label: "特殊场景？", Type: "textarea", Required: true},
	}}
	res, e := s.Act(context.Background(), d, m.Task.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	if res.Task.Status != "aligning" {
		t.Fatal(res)
	}
	again, e := s.Act(context.Background(), d, m.Task.ID, in)
	if e != nil || again.Task.Version != res.Task.Version {
		t.Fatal("request retry", e)
	}
	res.ClaimID = m.ClaimID
	return res
}

func productReply(t *testing.T, s *app.Service, m domain.Mutation, body string, answers map[string][]string) domain.Mutation {
	t.Helper()
	c := context.Background()
	v, e := s.RequirementReview(c, app.Human(), m.Task.ID)
	if e != nil {
		t.Fatal(e)
	}
	in := app.RequirementReviewInput{Action: "message", RequestID: domain.ID("reply_"), Body: body, Answers: answers, Generation: v.Generation}
	if len(answers) > 0 {
		in.ReplyTo = v.Messages[len(v.Messages)-1].ID
	}
	v, e = s.SaveRequirementReview(c, app.Human(), m.Task.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	before := v.Version
	v, e = s.SaveRequirementReview(c, app.Human(), m.Task.ID, in)
	if e != nil || v.Version != before {
		t.Fatal("reply retry", e)
	}
	task, e := s.Store.GetTask(c, m.Task.ID)
	if e != nil {
		t.Fatal(e)
	}
	if task.Status != "ready" || task.Alignment.Status != "replied" || v.Status != "alignment_replied" {
		t.Fatal("product incorrectly completed alignment", task, v)
	}
	return domain.Mutation{Task: task, ClaimID: m.ClaimID}
}

func TestDevelopmentAlignmentClientBindingAndRepeatedRounds(t *testing.T) {
	s, d, m := alignmentTask(t)
	c := context.Background()
	other := d
	other.ClientID = "client-other"
	m = askAlignment(t, s, d, m)
	// A start/open never starts a platform review, and late platform output is inert.
	v, e := s.SaveRequirementReview(c, app.Human(), m.Task.ID, app.RequirementReviewInput{Action: "start", RequestID: "open"})
	if e != nil || v.Status != "needs_input" || v.Mode != "development_alignment" {
		t.Fatal(v, e)
	}
	if e = s.FinishRequirementReview(c, m.Task.ID, v.Generation, "old", nil, "late failure"); e != nil {
		t.Fatal(e)
	}
	for _, actor := range []domain.Actor{other, app.Human()} {
		_, e = s.Act(c, actor, m.Task.ID, app.Action{Action: "resume_development", RequestID: domain.ID("bad_"), ClaimID: m.ClaimID, Version: m.Task.Version, Revision: m.Task.Revision, Body: "继续"})
		if !errors.Is(e, domain.ErrForbidden) {
			t.Fatal("wrong client can complete", e)
		}
	}
	// A required unanswered field does not prevent a partial reply.
	m = productReply(t, s, m, "先覆盖线上订单", map[string][]string{"scope": {"线上订单"}})
	work, e := s.WorkList(c, other)
	if e != nil || len(work["tasks"].([]domain.Task)) != 0 {
		t.Fatal("reserved task leaked into other client's work", work, e)
	}
	work, e = s.WorkList(c, d)
	if e != nil || len(work["tasks"].([]domain.Task)) != 1 {
		t.Fatal("owner cannot find reply", work, e)
	}
	for _, actor := range []domain.Actor{d, other, app.Human()} {
		if _, e = s.Act(c, actor, m.Task.ID, app.Action{Action: "claim", RequestID: domain.ID("claim_")}); e == nil {
			t.Fatal("generic claim skipped reading alignment")
		}
	}
	if _, e = s.Act(c, other, m.Task.ID, app.Action{Action: "resume_development", RequestID: "other", ClaimID: m.ClaimID, Version: m.Task.Version, Revision: m.Task.Revision, Body: "理解了"}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal(e)
	}
	ctx, e := s.Context(c, d, d.ProjectID, m.Task.ID)
	if e != nil || ctx["requirement_review"].(domain.RequirementReview).Status != "alignment_replied" {
		t.Fatal("MCP context missing full reply", ctx, e)
	}
	// Still unclear: ask again directly while waiting to resume.
	oldGeneration := v.Generation
	m = askAlignment(t, s, d, m)
	if _, e = s.SaveRequirementReview(c, app.Human(), m.Task.ID, app.RequirementReviewInput{Action: "message", RequestID: "stale", Generation: oldGeneration, Body: "旧页面的回复"}); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("stale round accepted", e)
	}
	m = productReply(t, s, m, "你理解偏了，不涉及退款，实际是按订单展示售后记录", nil)
	before := m
	resume := app.Action{Action: "resume_development", RequestID: "resume", ClaimID: m.ClaimID, Version: m.Task.Version, Revision: m.Task.Revision, Body: "按订单展示售后记录，保留原支付流程"}
	m, e = s.Act(c, d, m.Task.ID, resume)
	if e != nil || m.Task.Status != "doing" || m.Task.Alignment.Status != "completed" || m.ClaimID == "" || m.ClaimID == before.ClaimID {
		t.Fatal(m, e)
	}
	again, e := s.Act(c, d, m.Task.ID, resume)
	if e != nil || again.ClaimID != m.ClaimID {
		t.Fatal("resume retry", e)
	}
	if _, e = s.Act(c, other, m.Task.ID, resume); !errors.Is(e, domain.ErrConflict) && !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("other client replayed owner's receipt", e)
	}
	if _, e = s.Act(c, d, m.Task.ID, app.Action{Action: "report", RequestID: "old-claim", ClaimID: before.ClaimID, Body: "old"}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("old credential retained", e)
	}
	if _, e = s.Act(c, d, m.Task.ID, app.Action{Action: "report", RequestID: "new-claim", ClaimID: m.ClaimID, Body: "继续开发"}); e != nil {
		t.Fatal("new credential unusable", e)
	}
}

func TestDevelopmentAlignmentReopenClaimAppendsAndRevokes(t *testing.T) {
	s, d, m := alignmentTask(t)
	c := context.Background()
	m = askAlignment(t, s, d, m)
	initialGeneration := 0
	v, _ := s.RequirementReview(c, app.Human(), m.Task.ID)
	initialGeneration = v.Generation
	m = productReply(t, s, m, "理解不对：只展示售后记录，不修改退款", nil)
	oldClaim := m.ClaimID
	in := app.Action{Action: "reopen_claim", RequestID: "reopen-claim", Version: m.Task.Version, Revision: m.Task.Revision, Body: "原客户端环境已丢失"}
	if _, e := s.Act(c, d, m.Task.ID, in); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("developer unlocked task", e)
	}
	m, e := s.Act(c, app.Human(), m.Task.ID, in)
	if e != nil || m.Task.Claimant != "" || m.Task.ClaimHash != "" || m.Task.Alignment != nil || m.Task.Status != "ready" {
		t.Fatal(m, e)
	}
	for _, text := range []string{"需求评审对齐补充", "覆盖哪些订单？", "理解不对：只展示售后记录，不修改退款"} {
		if !strings.Contains(m.Task.Description, text) {
			t.Fatal("missing appendix", text, m.Task.Description)
		}
	}
	again, e := s.Act(c, app.Human(), m.Task.ID, in)
	if e != nil || again.Task.Description != m.Task.Description {
		t.Fatal("reopen retry duplicated appendix", e)
	}
	stored, _ := s.Store.GetTask(c, m.Task.ID)
	if stored.ClaimHash != "" {
		t.Fatal("claim hash column was not revoked")
	}
	if _, e = s.Act(c, d, m.Task.ID, app.Action{Action: "report", RequestID: "revoked", ClaimID: oldClaim, Body: "late write"}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal(e)
	}
	if _, e = s.SaveRequirementReview(c, app.Human(), m.Task.ID, app.RequirementReviewInput{Action: "message", RequestID: "expired-form", Generation: initialGeneration, ReplyTo: v.Messages[len(v.Messages)-1].ID, Answers: map[string][]string{"scope": {"全部订单"}}}); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("old form revived", e)
	}
	other := d
	other.ClientID = "replacement-client"
	work, e := s.WorkList(c, other)
	if e != nil || len(work["tasks"].([]domain.Task)) != 1 {
		t.Fatal("not public", e)
	}
	claimed, e := s.Act(c, other, m.Task.ID, app.Action{Action: "claim", RequestID: "new-owner"})
	if e != nil || !strings.Contains(claimed.Task.Description, "理解不对") {
		t.Fatal("new client lost context", e)
	}
}

func TestDevelopmentAlignmentResumeVersusReopenIsAtomic(t *testing.T) {
	s, d, m := alignmentTask(t)
	c := context.Background()
	m = productReply(t, s, askAlignment(t, s, d, m), "按原需求继续", nil)
	// Two services model separate server instances; correctness must come from CAS.
	second := &app.Service{Store: s.Store}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, entry := range []struct {
		s      *app.Service
		a      domain.Actor
		action string
	}{{s, d, "resume_development"}, {second, app.Human(), "reopen_claim"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := entry.s.Act(c, entry.a, m.Task.ID, app.Action{Action: entry.action, RequestID: entry.action, ClaimID: m.ClaimID, Version: m.Task.Version, Revision: m.Task.Revision, Body: "可以继续"})
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if !errors.Is(e, domain.ErrConflict) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal("concurrent transitions both succeeded", success)
	}
	task, _ := s.Store.GetTask(c, m.Task.ID)
	v, _ := s.RequirementReview(c, app.Human(), m.Task.ID)
	if v.Source.Status != task.Status || v.TaskRevision != task.Revision {
		t.Fatal("torn state", task, v)
	}
}

func TestDevelopmentAlignmentStaleCompletionAndTerminalFencing(t *testing.T) {
	s, d, m := alignmentTask(t)
	c := context.Background()
	m = askAlignment(t, s, d, m)
	if _, e := s.Act(c, d, m.Task.ID, app.Action{Action: "resume_development", RequestID: "too-early", ClaimID: m.ClaimID, Version: m.Task.Version, Revision: m.Task.Revision, Body: "还没收到回复"}); !errors.Is(e, domain.ErrConflict) {
		t.Fatal(e)
	}
	if _, e := s.Act(c, app.Human(), m.Task.ID, app.Action{Action: "reopen_claim", RequestID: "early-unlock", Version: m.Task.Version, Revision: m.Task.Revision}); !errors.Is(e, domain.ErrConflict) {
		t.Fatal(e)
	}
	m = productReply(t, s, m, "补充范围", nil)
	stale := m
	m = productReply(t, s, m, "再纠正一下，以这条为准", nil)
	if _, e := s.Act(c, d, m.Task.ID, app.Action{Action: "resume_development", RequestID: "stale-resume", ClaimID: m.ClaimID, Version: stale.Task.Version, Revision: stale.Task.Revision, Body: "按旧回复开发"}); !errors.Is(e, domain.ErrConflict) {
		t.Fatal(e)
	}
	v, _ := s.RequirementReview(c, app.Human(), m.Task.ID)
	_, e := s.Act(c, app.Human(), m.Task.ID, app.Action{Action: "close", RequestID: "close", Version: m.Task.Version, Body: "产品决定停止"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveRequirementReview(c, app.Human(), m.Task.ID, app.RequirementReviewInput{Action: "message", RequestID: "late", Generation: v.Generation, Body: "晚到回复"}); !errors.Is(e, domain.ErrConflict) {
		t.Fatal(e)
	}
	v, e = s.SaveRequirementReview(c, app.Human(), m.Task.ID, app.RequirementReviewInput{Action: "start", RequestID: "open-closed"})
	if e != nil || v.Status != "closed" {
		t.Fatal(v, e)
	}
	m, e = s.Act(c, app.Human(), m.Task.ID, app.Action{Action: "reopen", RequestID: "reopen", Body: "产品明确重新打开"})
	if e != nil {
		t.Fatal(e)
	}
	v, e = s.SaveRequirementReview(c, app.Human(), m.Task.ID, app.RequirementReviewInput{Action: "start", RequestID: "start-reopened"})
	if e != nil || v.Mode != "" || v.Status != "queued" || m.Task.Alignment != nil {
		t.Fatal(v, e)
	}
}

func TestDevelopmentAlignmentLongRepliesRemainReassignable(t *testing.T) {
	s, d, m := alignmentTask(t)
	c := context.Background()
	long := strings.Repeat("产品的详细补充不能在交接时丢失。", 2000)
	m = productReply(t, s, askAlignment(t, s, d, m), long, nil)
	m, e := s.Act(c, app.Human(), m.Task.ID, app.Action{Action: "reopen_claim", RequestID: "open-long", Version: m.Task.Version, Revision: m.Task.Revision})
	if e != nil || !strings.Contains(m.Task.Description, long) {
		t.Fatal("long alignment cannot be handed over", e)
	}
}
