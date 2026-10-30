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

func collaborationFixture(t *testing.T) (*app.Service, domain.Project, domain.Collaboration, domain.Actor) {
	t.Helper()
	s, p := setup(t)
	r := domain.Run{ID: "origin", ProjectID: p.ID, ConversationID: "private-conversation", Kind: "question", OwnerID: app.Human().ID, OwnerKind: "human", Prompt: "private original text", Status: "succeeded"}
	if err := s.Store.PutRun(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	in := app.CollaborationInput{OriginRunID: r.ID, RequestID: "create", Title: "回调调查", Brief: "订单 trace-42 的回调失败，请核对日志", Environment: "production"}
	v, err := s.CreateCollaboration(context.Background(), app.Human(), in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.CreateCollaboration(context.Background(), app.Human(), in)
	if err != nil || again.Collaboration.ID != v.Collaboration.ID {
		t.Fatal("create retry", again, err)
	}
	in.Brief = "different"
	if _, err = s.CreateCollaboration(context.Background(), app.Human(), in); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("reused request accepted", err)
	}
	return s, p, v.Collaboration, domain.Actor{ID: "developer", Name: "本地开发", Kind: "developer", ProjectID: p.ID}
}

func TestCollaborationClaimsLeaseFencingAndAtomicResult(t *testing.T) {
	s, p, v, dev := collaborationFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	wins := make(chan domain.CollaborationMutation, 2)
	for _, req := range []string{"one", "two"} {
		wg.Add(1)
		go func(req string) {
			defer wg.Done()
			m, err := s.ActCollaboration(ctx, dev, v.ID, app.CollaborationAction{Action: "claim", RequestID: req})
			if err == nil {
				wins <- m
			} else if !errors.Is(err, domain.ErrConflict) {
				t.Error(err)
			}
		}(req)
	}
	wg.Wait()
	close(wins)
	if len(wins) != 1 {
		t.Fatalf("claims won: %d", len(wins))
	}
	first := <-wins
	current, _ := s.Store.GetCollaboration(ctx, v.ID)
	old := current.Version
	current.Version++
	current.LeaseUntil = domain.Now() - 1
	if err := s.Store.CommitCollaboration(ctx, current, old, nil, nil); err != nil {
		t.Fatal(err)
	}
	dev2 := domain.Actor{ID: "developer2", Kind: "developer", Name: "接手人", ProjectID: p.ID}
	second, err := s.ActCollaboration(ctx, dev2, v.ID, app.CollaborationAction{Action: "claim", RequestID: "takeover"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ActCollaboration(ctx, dev, v.ID, app.CollaborationAction{Action: "report", RequestID: "late", ClaimID: first.ClaimID, Body: "late report"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("expired claimant wrote", err)
	}
	result := &domain.InvestigationResult{Outcome: "confirmed", Summary: "回调签名配置不一致", Evidence: "脱敏日志 trace-42", Environment: "production", ObservedAt: "2026-10-14 10:00 +08:00", DeploymentVersion: "abc123", Limitations: "尚未执行修复"}
	in := app.CollaborationAction{Action: "submit", RequestID: "submit", ClaimID: second.ClaimID, Version: second.Collaboration.Version - 1, Result: result}
	if _, err = s.ActCollaboration(ctx, dev2, v.ID, in); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale version accepted", err)
	}
	in.Version = second.Collaboration.Version
	done, err := s.ActCollaboration(ctx, dev2, v.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.ActCollaboration(ctx, dev2, v.ID, in)
	if err != nil || retry.Collaboration.Result.ID != done.Collaboration.Result.ID {
		t.Fatal(retry, err)
	}
	jobs, err := s.Store.ListReplyJobs(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "pending" {
		t.Fatal(jobs, err)
	}
	if done.Collaboration.OriginRunID != "" || done.Collaboration.ConversationID != "" || done.Collaboration.OwnerID != "" {
		t.Fatal("developer result exposed private origin")
	}
	in.RequestID = "overwrite"
	in.Result.Summary = "overwritten"
	if _, err = s.ActCollaboration(ctx, dev2, v.ID, in); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("result overwritten", err)
	}
	stored, _ := s.Store.GetCollaboration(ctx, v.ID)
	if stored.Result.Summary != "回调签名配置不一致" {
		t.Fatal(stored.Result)
	}
}

func TestCollaborationVisibilityCancellationAndReconciliation(t *testing.T) {
	s, p, v, dev := collaborationFixture(t)
	ctx := context.Background()
	work, err := s.WorkList(ctx, dev)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(work)
	for _, secret := range []string{"private-conversation", "private original text", "origin_run_id", "owner_kind"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("private context exposed", secret)
		}
	}
	if _, err = s.GetCollaboration(ctx, domain.Actor{ID: "another-user", Kind: "human"}, v.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("another user read collaboration", err)
	}
	foreign := dev
	foreign.ProjectID = "foreign"
	if _, err = s.GetCollaboration(ctx, foreign, v.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("cross project read", err)
	}
	claimed, err := s.ActCollaboration(ctx, dev, v.ID, app.CollaborationAction{Action: "claim", RequestID: "claim"})
	if err != nil {
		t.Fatal(err)
	}
	changes, stop := s.SubscribeWork(p.ID)
	defer stop()
	if _, err = s.ActCollaboration(ctx, app.Human(), v.ID, app.CollaborationAction{Action: "cancel", RequestID: "cancel"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changes:
	default:
		t.Fatal("no work change hint")
	}
	if _, err = s.ActCollaboration(ctx, dev, v.ID, app.CollaborationAction{Action: "report", RequestID: "after-cancel", ClaimID: claimed.ClaimID, Body: "late"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("late write accepted", err)
	}
	work, err = s.WorkList(ctx, dev)
	if err != nil || len(work["collaborations"].([]domain.Collaboration)) != 0 {
		t.Fatal("canceled task still offered", work, err)
	}
}

func TestDeletedConversationStopsCollaborationAndAutomaticRecursion(t *testing.T) {
	s, p, v, dev := collaborationFixture(t)
	ctx := context.Background()
	r, _ := s.Store.GetRun(ctx, "origin")
	r.TriggerType = "collaboration_result"
	r.ID = "automatic"
	if err := s.Store.PutRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCollaboration(ctx, domain.Actor{ID: "platform", Kind: "platform", ProjectID: p.ID}, app.CollaborationInput{RequestID: "recursive", OriginRunID: r.ID, Title: "again", Brief: "again", Environment: "prod"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("automatic recursion", err)
	}
	if err := s.Store.PutConversation(ctx, domain.Conversation{ID: v.ConversationID, ProjectID: p.ID, DeletedAt: domain.Now()}); err != nil {
		t.Fatal(err)
	}
	work, err := s.WorkList(ctx, dev)
	if err != nil || len(work["collaborations"].([]domain.Collaboration)) != 0 {
		t.Fatal(work, err)
	}
	if _, err = s.ActCollaboration(ctx, dev, v.ID, app.CollaborationAction{Action: "claim", RequestID: "deleted"}); err == nil {
		t.Fatal("claimed deleted conversation")
	}
}

// A new user turn may repeat a finished investigation without mutating its
// evidence/outbox. Pending work is still deduplicated for repeated requests.
func TestCollaborationContinuationPreservesHistoryAndReusesActiveWork(t *testing.T) {
	for _, outcome := range []string{"cannot_access", "inconclusive", "confirmed"} {
		t.Run(outcome, func(t *testing.T) {
			s, p, old, dev := collaborationFixture(t)
			ctx := context.Background()
			claimed, err := s.ActCollaboration(ctx, dev, old.ID, app.CollaborationAction{Action: "claim", RequestID: "claim"})
			if err != nil {
				t.Fatal(err)
			}
			done, err := s.ActCollaboration(ctx, dev, old.ID, app.CollaborationAction{Action: "submit", RequestID: "result", ClaimID: claimed.ClaimID, Version: claimed.Collaboration.Version, Result: &domain.InvestigationResult{Outcome: outcome, Summary: "原调查结论", Evidence: "原取证记录", Environment: old.Environment, ObservedAt: "2026-10-30T09:52:15+08:00", DeploymentVersion: "unknown", Limitations: "仅代表原取证时刻"}})
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.Store.GetCollaboration(ctx, old.ID)
			if err != nil {
				t.Fatal(err)
			}
			beforeJSON, _ := json.Marshal(before)
			jobBefore, err := s.Store.GetReplyJob(ctx, done.Collaboration.Result.ID)
			if err != nil {
				t.Fatal(err)
			}
			origin, err := s.Store.GetRun(ctx, "origin")
			if err != nil {
				t.Fatal(err)
			}
			origin.ID, origin.Prompt = "continuation", "环境配置已恢复，再继续刚才的问题"
			if err = s.Store.PutRun(ctx, origin); err != nil {
				t.Fatal(err)
			}
			in := app.CollaborationInput{OriginRunID: origin.ID, RequestID: "continue", Title: old.Title, Brief: old.Brief, Environment: old.Environment}
			fresh, err := s.CreateCollaboration(ctx, app.Human(), in)
			if err != nil || fresh.Collaboration.ID == old.ID || fresh.Collaboration.Status != "pending" {
				t.Fatal(fresh, err)
			}
			var newClaim app.CollaborationAction
			for _, status := range []string{"pending", "investigating", "blocked"} {
				if status == "investigating" {
					claim, e := s.ActCollaboration(ctx, dev, fresh.Collaboration.ID, app.CollaborationAction{Action: "claim", RequestID: "new-claim"})
					if e != nil {
						t.Fatal(e)
					}
					newClaim = app.CollaborationAction{Action: "report", RequestID: "new-blocked", ClaimID: claim.ClaimID, ReportKind: "blocked", Body: "等待本轮环境核验"}
				}
				if status == "blocked" {
					if _, err = s.ActCollaboration(ctx, dev, fresh.Collaboration.ID, newClaim); err != nil {
						t.Fatal(err)
					}
				}
				in.RequestID = "repeat-" + status
				reused, e := s.CreateCollaboration(ctx, app.Human(), in)
				if e != nil || reused.Collaboration.ID != fresh.Collaboration.ID || reused.Collaboration.Status != status {
					t.Fatal("active investigation duplicated", reused, e)
				}
			}
			after, err := s.Store.GetCollaboration(ctx, old.ID)
			afterJSON, _ := json.Marshal(after)
			jobAfter, jobErr := s.Store.GetReplyJob(ctx, done.Collaboration.Result.ID)
			if err != nil || jobErr != nil || string(afterJSON) != string(beforeJSON) || jobAfter != jobBefore {
				t.Fatal("history/outbox changed", after, err, jobErr)
			}
			work, err := s.WorkList(ctx, dev)
			if err != nil {
				t.Fatal(err)
			}
			items := work["collaborations"].([]domain.Collaboration)
			if len(items) != 1 || items[0].ID != fresh.Collaboration.ID {
				t.Fatal("new task missing from inbox", work, err)
			}
			all, err := s.Store.ListCollaborations(ctx, p.ID)
			if err != nil || len(all) != 2 {
				t.Fatal(all, err)
			}
		})
	}
}
