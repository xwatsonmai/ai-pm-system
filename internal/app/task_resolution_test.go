package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

func taskAtStage(t *testing.T, s *app.Service, p, stage string) (domain.Task, domain.Mutation, domain.RequirementReview) {
	t.Helper()
	c := context.Background()
	a := app.Human()
	made, e := s.CreateTask(c, a, p, app.TaskInput{Title: "可结束需求", Description: "原始需求与业务范围", Acceptance: []string{"验收条件"}, RequestID: domain.ID("create_")})
	if e != nil {
		t.Fatal(e)
	}
	review, e := s.SaveRequirementReview(c, a, made.Task.ID, app.RequirementReviewInput{Action: "start", RequestID: domain.ID("review_")})
	if e != nil {
		t.Fatal(e)
	}
	if stage == "clarify" {
		return made.Task, domain.Mutation{}, review
	}
	current, e := s.Act(c, a, made.Task.ID, app.Action{Action: "ready", RequestID: domain.ID("ready_")})
	if e != nil {
		t.Fatal(e)
	}
	if stage == "ready" {
		return current.Task, domain.Mutation{}, review
	}
	dev := developer(p, "dev")
	claim, e := s.Act(c, dev, made.Task.ID, app.Action{Action: "claim", RequestID: domain.ID("claim_")})
	if e != nil {
		t.Fatal(e)
	}
	if stage == "doing" {
		return claim.Task, claim, review
	}
	current, e = s.Act(c, dev, made.Task.ID, app.Action{Action: "submit", RequestID: domain.ID("submit_"), ClaimID: claim.ClaimID, Revision: 1, Submission: &domain.Submission{Summary: "已开发", SHA: "v1", Tests: "已验证"}})
	if e != nil {
		t.Fatal(e)
	}
	if stage == "testing" {
		return current.Task, claim, review
	}
	current.Task = passTesting(t, s, current.Task)
	if stage == "pending_release" {
		return current.Task, claim, review
	}
	current, e = s.Act(c, dev, made.Task.ID, app.Action{Action: "mark_deployed", RequestID: domain.ID("deploy_"), ClaimID: claim.ClaimID, Deployment: &domain.Deployment{Revision: 1, SHA: "v1", Environment: "production", Body: "已上线"}})
	if e != nil {
		t.Fatal(e)
	}
	return current.Task, claim, review
}

func TestResolveEveryActiveTaskStage(t *testing.T) {
	for _, stage := range []string{"clarify", "ready", "doing", "pending_release", "review"} {
		for _, operation := range []string{"close", "decline"} {
			t.Run(stage+"/"+operation, func(t *testing.T) {
				s, p := setup(t)
				c, a := context.Background(), app.Human()
				task, claim, review := taskAtStage(t, s, p.ID, stage)
				for _, actor := range []domain.Actor{developer(p.ID, "dev"), {ID: "platform", Kind: "platform", ProjectID: p.ID}} {
					if _, e := s.Act(c, actor, task.ID, app.Action{Action: operation, RequestID: domain.ID("unauthorized_"), Body: "擅自结束"}); !errors.Is(e, domain.ErrForbidden) {
						t.Fatal("non-human resolved task", e)
					}
				}
				if _, e := s.Act(c, a, task.ID, app.Action{Action: operation, RequestID: "empty"}); e == nil {
					t.Fatal("missing reason accepted")
				}
				changes, stop := s.SubscribeWork(p.ID)
				defer stop()
				in := app.Action{Action: operation, RequestID: "resolve", Version: task.Version, Body: "业务方案调整，停止推进", ClaimID: "never-share-this-credential"}
				ended, e := s.Act(c, a, task.ID, in)
				if e != nil {
					t.Fatal(e)
				}
				expected := map[string]string{"close": "closed", "decline": "rejected"}[operation]
				if ended.Task.Status != expected || ended.Task.Resolution.FromStatus != stage || ended.Task.Resolution.Reason != in.Body || ended.Task.Resolution.Actor != a.Name || ended.Task.Resolution.At == 0 {
					t.Fatal("incomplete resolution", ended)
				}
				if ended.Task.Description != task.Description || ended.Task.Submission != nil && ended.Task.Submission.SHA != "v1" || ended.Task.Deployment != nil && ended.Task.Deployment.SHA != "v1" {
					t.Fatal("evidence was lost")
				}
				stored, _ := s.Store.GetTask(c, task.ID)
				if stored.ClaimHash != "" || stored.Claimant != "" {
					t.Fatal("claim not revoked")
				}
				select {
				case <-changes:
				default:
					t.Fatal("developers not notified")
				}
				retry, e := s.Act(c, a, task.ID, in)
				if e != nil || retry.Task.Version != ended.Task.Version {
					t.Fatal("retry duplicated resolution", e)
				}
				rr, e := s.RequirementReview(c, a, task.ID)
				if e != nil || rr.Status != "closed" || rr.Generation <= review.Generation {
					t.Fatal("review not closed atomically", rr, e)
				}
				beforeMessages := len(rr.Messages)
				late := &domain.RequirementReviewResult{Body: "晚到结果", Decision: "ready", Proposal: &domain.RequirementProposal{Description: "晚到需求稿", Acceptance: []string{"旧条件"}}}
				if e = s.FinishRequirementReview(c, task.ID, review.Generation, "", late, ""); e != nil {
					t.Fatal(e)
				}
				rr, _ = s.RequirementReview(c, a, task.ID)
				if rr.Status != "closed" || len(rr.Messages) != beforeMessages {
					t.Fatal("late review revived task")
				}
				if _, e = s.SaveRequirementReview(c, a, task.ID, app.RequirementReviewInput{Action: "message", RequestID: "late-msg", Body: "结束后的补充"}); !errors.Is(e, domain.ErrConflict) {
					t.Fatal("closed review accepted input", e)
				}
				for _, action := range []string{"report", "submit", "mark_deployed", "release", "claim"} {
					if _, e = s.Act(c, developer(p.ID, "dev"), task.ID, app.Action{Action: action, RequestID: "late-" + action, ClaimID: claim.ClaimID, Body: "过期提交"}); !errors.Is(e, domain.ErrConflict) {
						t.Fatal("old developer operation accepted", action, e)
					}
				}
				work, e := s.WorkList(c, developer(p.ID, "dev"))
				if e != nil {
					t.Fatal(e)
				}
				for _, item := range work["tasks"].([]domain.Task) {
					if item.ID == task.ID {
						t.Fatal("ended task still actionable")
					}
				}
				detail, e := s.RequirementDetail(c, a, p.ID, app.RequirementDetailQuery{TaskID: task.ID})
				if e != nil || detail.NextActor != "none" {
					t.Fatal("query still asks developers to act", e)
				}
				pool, e := s.RequirementPool(c, a, p.ID, app.RequirementPoolQuery{Status: expected})
				if e != nil || pool.Matched != 1 {
					t.Fatal("terminal filter omitted task", e)
				}
				if _, e = s.EditTask(c, a, task.ID, app.TaskInput{Title: task.Title, Description: "偷偷恢复", Version: ended.Task.Version, RequestID: "edit"}); !errors.Is(e, domain.ErrConflict) {
					t.Fatal("editing bypassed reopen", e)
				}
				events, _ := s.Store.Events(c, p.ID, task.ID, 0)
				last := events[len(events)-1]
				data, _ := json.Marshal(last)
				if last.Kind != operation || strings.Contains(string(data), "never-share-this-credential") || !strings.Contains(string(last.Data), "from_status") {
					t.Fatal("resolution history missing or credential leaked")
				}
				reopened, e := s.Act(c, a, task.ID, app.Action{Action: "reopen", RequestID: "reopen", Version: ended.Task.Version, Body: "调整后重新考虑"})
				if e != nil || reopened.Task.Status != "clarify" || reopened.Task.Revision != task.Revision+1 || reopened.Task.Resolution != nil {
					t.Fatal("invalid reopen", reopened, e)
				}
				if _, e = s.SaveRequirementReview(c, a, task.ID, app.RequirementReviewInput{Action: "start", RequestID: "restart-review"}); e != nil {
					t.Fatal(e)
				}
				rr, _ = s.RequirementReview(c, a, task.ID)
				if rr.Source.Status != "clarify" || rr.Generation <= review.Generation+1 {
					t.Fatal("old review reused after reopen")
				}
			})
		}
	}
}

func TestCloseRacesWithReviewInputAndConfirmation(t *testing.T) {
	for _, mode := range []string{"message", "confirm"} {
		t.Run(mode, func(t *testing.T) {
			for n := 0; n < 8; n++ {
				s, p := setup(t)
				c, a := context.Background(), app.Human()
				task, _, review := taskAtStage(t, s, p.ID, "clarify")
				result := &domain.RequirementReviewResult{Body: "待确认", Decision: "ready", Proposal: &domain.RequirementProposal{Description: "已整理需求", Acceptance: []string{"可验证"}}}
				if e := s.FinishRequirementReview(c, task.ID, review.Generation, "", result, ""); e != nil {
					t.Fatal(e)
				}
				start := make(chan struct{})
				var wg sync.WaitGroup
				var closeErr, reviewErr error
				wg.Add(2)
				go func() {
					defer wg.Done()
					<-start
					_, closeErr = s.Act(c, a, task.ID, app.Action{Action: "close", RequestID: "close", Version: task.Version, Body: "停止开发"})
				}()
				go func() {
					defer wg.Done()
					<-start
					_, reviewErr = s.SaveRequirementReview(c, a, task.ID, app.RequirementReviewInput{Action: mode, RequestID: "competing", Body: map[string]string{"message": "新说明"}[mode], Generation: review.Generation})
				}()
				close(start)
				wg.Wait()
				if closeErr != nil && !errors.Is(closeErr, domain.ErrConflict) {
					t.Fatal(closeErr)
				}
				if reviewErr != nil && !errors.Is(reviewErr, domain.ErrConflict) {
					t.Fatal(reviewErr)
				}
				now, _ := s.Store.GetTask(c, task.ID)
				rr, _ := s.RequirementReview(c, a, task.ID)
				if closeErr == nil {
					if now.Status != "closed" || rr.Status != "closed" {
						t.Fatal("racing review revived closed task", now.Status, rr.Status)
					}
				} else if mode != "confirm" || now.Status != "ready" {
					t.Fatal("unexpected race outcome", closeErr, now.Status)
				}
			}
		})
	}
}
