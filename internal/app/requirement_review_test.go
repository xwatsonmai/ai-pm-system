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

func TestRequirementReviewDurabilityAndConfirmation(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a := app.Human()
	original := strings.Repeat("一段完整需求与截图说明。", 1000)
	created, err := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "独立评审", Description: original, RequestID: "create"})
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
	v := save(app.RequirementReviewInput{Action: "start", RequestID: "start"})
	if v.Status != "queued" || len(v.Messages) != 1 || v.Messages[0].Source.Description != original {
		t.Fatalf("original lost: %+v", v)
	}
	if err = s.FinishRequirementReview(c, id, v.Generation, "", nil, "模型配置缺失"); err != nil {
		t.Fatal(err)
	}
	message := app.RequirementReviewInput{Action: "message", Body: "你的理解不对，实际需要按订单分组。", RequestID: "correction"}
	v = save(message)
	v = save(message)
	if len(v.Messages) != 3 || v.Messages[1].Action != "review_failed" || v.Messages[2].Body != message.Body || v.Messages[2].ActorID != a.ID {
		t.Fatal("duplicate message or missing input", v)
	}
	if err = s.BeginRequirementReview(c, id, v.Generation, "r1"); err != nil {
		t.Fatal(err)
	}
	result := &domain.RequirementReviewResult{Body: "按订单分组，仍需确认范围。", Decision: "clarify", Questions: []domain.RequirementQuestion{{ID: "scope", Label: "范围？", Type: "radio", Options: []string{"全部", "部分"}, Recommended: []string{"全部"}, Required: true}}}
	if err = s.FinishRequirementReview(c, id, v.Generation, "r1", result, ""); err != nil {
		t.Fatal(err)
	}
	v, _ = s.RequirementReview(c, a, id)
	// Required questions never block a free-form correction or partial answer.
	v = save(app.RequirementReviewInput{Action: "message", Body: "整个方向不对，我们需要的是退款。", RequestID: "ignore-form"})
	if err = s.FinishRequirementReview(c, id, v.Generation-1, "r1", result, ""); err != nil {
		t.Fatal(err)
	}
	if current, _ := s.RequirementReview(c, a, id); current.Status != "queued" || len(current.Messages) != 5 {
		t.Fatal("stale result replaced correction", current)
	}
	proposal := &domain.RequirementReviewResult{Body: "已理解退款场景，待产品确认。", Decision: "ready", Questions: []domain.RequirementQuestion{}, Proposal: &domain.RequirementProposal{Description: "## 退款\n保留审计记录", Acceptance: []string{"退款记录可追溯"}}}
	if err = s.BeginRequirementReview(c, id, v.Generation, "r2"); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishRequirementReview(c, id, v.Generation, "r2", proposal, ""); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Store.GetTask(c, id)
	if before.Description != original || before.Status != "clarify" {
		t.Fatal("Agent changed official requirements")
	}
	changes, stop := s.SubscribeWork(p.ID)
	defer stop()
	v = save(app.RequirementReviewInput{Action: "confirm", RequestID: "confirm", Generation: v.Generation})
	select {
	case <-changes:
	default:
		t.Fatal("confirmed requirement did not notify work subscribers")
	}
	confirmed, _ := s.Store.GetTask(c, id)
	if v.Status != "confirmed" || confirmed.Status != "ready" || confirmed.Description != proposal.Proposal.Description || confirmed.Revision != before.Revision+1 {
		t.Fatal("confirmation not applied", v, confirmed)
	}
	v = save(app.RequirementReviewInput{Action: "confirm", RequestID: "confirm", Generation: v.Generation})
	again, _ := s.Store.GetTask(c, id)
	if again.Version != confirmed.Version || v.Messages[0].Source.Description != original {
		t.Fatal("confirmation retried or original discarded")
	}
	v = save(app.RequirementReviewInput{Action: "message", RequestID: "after-confirm", Body: "再补充一个边界情况"})
	reopened, _ := s.Store.GetTask(c, id)
	if reopened.Status != "clarify" || reopened.Description != confirmed.Description || v.Source.Status != "clarify" {
		t.Fatal("confirmed requirement cannot re-enter review", v, reopened)
	}

}

func TestReviewConcurrencyStaleProposalAndProjectIsolation(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a := app.Human()
	created, _ := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "并发补充", RequestID: "create"})
	id := created.Task.ID
	v, err := s.SaveRequirementReview(c, a, id, app.RequirementReviewInput{Action: "start", RequestID: "start"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.SaveRequirementReview(c, a, id, app.RequirementReviewInput{Action: "message", Body: "并发保存", RequestID: domain.ID("input_")})
			if e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	v, _ = s.RequirementReview(c, a, id)
	if len(v.Messages) != 9 {
		t.Fatal("lost concurrent message", len(v.Messages))
	}
	if err = s.BeginRequirementReview(c, id, v.Generation, "run"); err != nil {
		t.Fatal(err)
	}
	_, err = s.EditTask(c, a, id, app.TaskInput{Title: "新修订", Description: "最新要求", Version: created.Task.Version, RequestID: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	proposal := &domain.RequirementReviewResult{Body: "旧稿", Decision: "ready", Proposal: &domain.RequirementProposal{Description: "旧稿", Acceptance: []string{"旧条件"}}}
	if err = s.FinishRequirementReview(c, id, v.Generation, "run", proposal, ""); err != nil {
		t.Fatal(err)
	}
	v, _ = s.RequirementReview(c, a, id)
	if v.Status != "failed" {
		t.Fatal("stale review accepted", v)
	}
	if _, err = s.SaveRequirementReview(c, a, id, app.RequirementReviewInput{Action: "confirm", RequestID: "bad", Generation: v.Generation}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale proposal confirmed", err)
	}
	other, _ := s.SaveProject(c, domain.Project{Name: "other"})
	outsider := domain.Actor{ID: "dev", Kind: "developer", ProjectID: other.ID}
	if _, err = s.RequirementReview(c, outsider, id); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("cross-project read allowed", err)
	}
	if _, err = s.SaveRequirementReview(c, domain.Actor{ID: "dev", Kind: "developer", ProjectID: p.ID}, id, app.RequirementReviewInput{Action: "message", RequestID: "dev", Body: "x"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("developer edited review", err)
	}
}

func TestRequirementReviewPoolSummaries(t *testing.T) {
	s, p := setup(t)
	c, human := context.Background(), app.Human()
	other, err := s.SaveProject(c, domain.Project{Name: "其他项目"})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, project := range []domain.Project{p, other} {
		created, e := s.CreateTask(c, human, project.ID, app.TaskInput{Title: "后台评审", Description: "原始文档不能混入摘要", RequestID: project.ID})
		if e != nil {
			t.Fatal(e)
		}
		v, e := s.SaveRequirementReview(c, human, created.Task.ID, app.RequirementReviewInput{Action: "start", RequestID: project.ID})
		if e != nil {
			t.Fatal(e)
		}
		if e = s.FinishRequirementReview(c, v.TaskID, v.Generation, "", &domain.RequirementReviewResult{Body: "评审私有长文", Decision: "clarify", Questions: []domain.RequirementQuestion{}}, ""); e != nil {
			t.Fatal(e)
		}
		if project.ID == p.ID {
			id = created.Task.ID
		}
	}
	summaries := func(status string) {
		t.Helper()
		ctx, e := s.Context(c, human, p.ID, "")
		if e != nil {
			t.Fatal(e)
		}
		list := ctx["requirement_reviews"].([]domain.RequirementReviewSummary)
		if len(list) != 1 || list[0].TaskID != id || list[0].Status != status || list[0].TaskRevision != 1 {
			t.Fatalf("wrong project or status: %+v", list)
		}
	}
	summaries("needs_input")
	v, err := s.SaveRequirementReview(c, human, id, app.RequirementReviewInput{Action: "message", Body: "自由纠正", RequestID: "correction"})
	if err != nil {
		t.Fatal(err)
	}
	summaries("queued")
	if err = s.FinishRequirementReview(c, id, v.Generation, "", nil, "模型暂不可用"); err != nil {
		t.Fatal(err)
	}
	summaries("failed")
	ctx, err := s.Context(c, developer(p.ID, "dev"), p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := ctx["requirement_reviews"]; exists {
		t.Fatal("UI review summaries leaked into developer context")
	}
	if _, err = s.Context(c, developer(other.ID, "outsider"), p.ID, ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cross-project access: %v", err)
	}
}
