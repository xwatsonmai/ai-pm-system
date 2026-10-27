package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func prematureSubmission(t *testing.T) (*app.Service, domain.Actor, domain.Mutation, app.Action) {
	t.Helper()
	s, actor, claim := alignmentTask(t)
	m, err := s.Act(context.Background(), actor, claim.Task.ID, app.Action{Action: "submit", RequestID: "premature-submit", ClaimID: claim.ClaimID,
		Branch: "feature/manual-test", Revision: claim.Task.Revision, Submission: &domain.Submission{SHA: strings.Repeat("a", 40), Branch: "feature/manual-test", Summary: "implemented", Tests: "local tests passed"}})
	if err != nil {
		t.Fatal(err)
	}
	in := app.Action{Action: "withdraw_submission", RequestID: "withdraw", ClaimID: claim.ClaimID, Version: m.Task.Version, Revision: m.Task.Revision,
		SubmissionSHA: m.Task.Submission.SHA, Body: "尚未发布测试环境，提前提测", UserConfirmed: true, UserInstruction: "撤回提前提测，保留认领和提交"}
	return s, actor, m, in
}

func TestWithdrawSubmissionPreservesClaimSourceAndIdempotency(t *testing.T) {
	s, actor, before, in := prematureSubmission(t)
	ctx := context.Background()
	result, err := s.Act(ctx, actor, before.Task.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	a, b := before.Task, result.Task
	if b.Status != "doing" || b.Version != a.Version+1 || b.Revision != a.Revision || b.Claimant != a.Claimant || b.ClaimClientID != a.ClaimClientID || b.ClaimHash != a.ClaimHash || b.Branch != a.Branch || b.Owner != a.Owner || !reflect.DeepEqual(b.Submission, a.Submission) {
		t.Fatal("withdraw changed claim or source evidence")
	}
	again, err := s.Act(ctx, actor, a.ID, in)
	if err != nil || again.Task.Version != b.Version {
		t.Fatal("lost response retry", err)
	}
	in.Body = "changed parameters"
	if _, err = s.Act(ctx, actor, a.ID, in); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("idempotency mismatch accepted", err)
	}
	// Original claim is still valid; deployment and testing are not fabricated.
	_, err = s.Act(ctx, actor, a.ID, app.Action{Action: "report", RequestID: "report-after-withdraw", ClaimID: in.ClaimID, Body: "waiting for manual test release"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWithdrawSubmissionRejectsStaleUnauthorizedAndUnconfirmedRequests(t *testing.T) {
	variants := map[string]func(*domain.Actor, *app.Action){
		"different client": func(a *domain.Actor, in *app.Action) { a.ClientID = "other" },
		"wrong claim":      func(a *domain.Actor, in *app.Action) { in.ClaimID = "wrong" },
		"stale version":    func(a *domain.Actor, in *app.Action) { in.Version-- },
		"missing version":  func(a *domain.Actor, in *app.Action) { in.Version = 0 },
		"stale revision":   func(a *domain.Actor, in *app.Action) { in.Revision++ },
		"wrong SHA":        func(a *domain.Actor, in *app.Action) { in.SubmissionSHA = strings.Repeat("b", 40) },
		"no confirmation":  func(a *domain.Actor, in *app.Action) { in.UserConfirmed = false },
		"no instruction":   func(a *domain.Actor, in *app.Action) { in.UserInstruction = "" },
		"no reason":        func(a *domain.Actor, in *app.Action) { in.Body = "" },
	}
	for name, change := range variants {
		t.Run(name, func(t *testing.T) {
			s, actor, before, in := prematureSubmission(t)
			change(&actor, &in)
			if _, err := s.Act(context.Background(), actor, before.Task.ID, in); err == nil {
				t.Fatal("invalid withdrawal accepted")
			}
			after, _ := s.Store.GetTask(context.Background(), before.Task.ID)
			if after.Status != "testing" || after.Version != before.Task.Version {
				t.Fatal("rejected request mutated task")
			}
		})
	}
}

func TestWithdrawSubmissionCannotEraseTestingOrBugWork(t *testing.T) {
	t.Run("tested", func(t *testing.T) {
		s, actor, before, in := prematureSubmission(t)
		latest := passTesting(t, s, before.Task)
		in.Version = latest.Version
		if _, err := s.Act(context.Background(), actor, latest.ID, in); err == nil {
			t.Fatal("tested result withdrawn")
		}
	})
	t.Run("linked bug", func(t *testing.T) {
		s, actor, before, in := prematureSubmission(t)
		fileTestBug(t, s, before.Task)
		latest, _ := s.Store.GetTask(context.Background(), before.Task.ID)
		in.Version = latest.Version
		if _, err := s.Act(context.Background(), actor, latest.ID, in); err == nil {
			t.Fatal("bug work withdrawn")
		}
	})
}
