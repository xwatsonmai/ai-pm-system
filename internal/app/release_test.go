package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReleaseGateAndRevisions(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	task := create(t, s, p.ID)
	dev := developer(p.ID, "dev")
	claim, err := s.Act(c, dev, task.ID, app.Action{Action: "claim", RequestID: "claim"})
	if err != nil {
		t.Fatal(err)
	}
	submit := app.Action{Action: "submit", RequestID: "submit", ClaimID: claim.ClaimID, Revision: 1, Submission: &domain.Submission{Summary: "开发完成", SHA: "version-1", Tests: "本地通过"}}
	pending, err := s.Act(c, dev, task.ID, submit)
	if err != nil || pending.Task.Status != "testing" || pending.Task.Deployment != nil {
		t.Fatal(pending, err)
	}
	pending.Task = passTesting(t, s, pending.Task)
	pool, err := s.RequirementPool(c, app.Human(), p.ID, app.RequirementPoolQuery{Status: "pending_release"})
	if err != nil || pool.Matched != 1 || pool.Counts.ByStatus["pending_release"] != 1 || pool.Items[0].ID != task.ID {
		t.Fatal(pool, err)
	}
	detail, err := s.RequirementDetail(c, app.Human(), p.ID, app.RequirementDetailQuery{TaskID: task.ID})
	if err != nil || detail.NextActor != "developer" || !strings.Contains(detail.NextAction, "等待实际上线") {
		t.Fatal(detail, err)
	}
	work, err := s.WorkList(c, dev)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range work["tasks"].([]domain.Task) {
		if item.ID == task.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("pending release vanished from work list")
	}
	// Merely uploading an artifact cannot move the task into acceptance.
	_, err = s.Artifact(c, dev, domain.Artifact{ID: "release-log", ProjectID: p.ID, TaskID: task.ID, Kind: "deployment", SHA: "version-1", Environment: "production", Body: "发布日志"})
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := s.Store.GetTask(c, task.ID)
	if stored.Status != "pending_release" {
		t.Fatal("generic evidence bypassed confirmation")
	}
	for _, tc := range []struct {
		name      string
		actor     domain.Actor
		in        app.Action
		forbidden bool
	}{
		{"other developer", developer(p.ID, "other"), app.Action{ClaimID: claim.ClaimID}, true},
		{"platform", domain.Actor{ID: "platform", Kind: "platform"}, app.Action{}, true},
		{"missing", app.Human(), app.Action{}, false},
		{"wrong SHA", app.Human(), app.Action{Deployment: &domain.Deployment{Revision: 1, SHA: "other", Environment: "production", Body: "发布"}}, false},
		{"stale revision", app.Human(), app.Action{Deployment: &domain.Deployment{Revision: 2, SHA: "version-1", Environment: "production", Body: "发布"}}, false},
		{"missing environment", app.Human(), app.Action{Deployment: &domain.Deployment{Revision: 1, SHA: "version-1", Body: "发布"}}, false},
		{"missing evidence", app.Human(), app.Action{Deployment: &domain.Deployment{Revision: 1, SHA: "version-1", Environment: "production"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Action, tc.in.RequestID = "mark_deployed", tc.name
			_, e := s.Act(c, tc.actor, task.ID, tc.in)
			if e == nil || (tc.forbidden && !errors.Is(e, domain.ErrForbidden)) {
				t.Fatal(e)
			}
		})
	}
	// An explicit return to development retains the result but requires resubmission.
	reworked, err := s.Act(c, app.Human(), task.ID, app.Action{Action: "rework", RequestID: "rework", Body: "上线前补充检查"})
	if err != nil || reworked.Task.Status != "doing" || reworked.Task.Submission == nil {
		t.Fatal(reworked, err)
	}
	submit.RequestID = "resubmit"
	pending, err = s.Act(c, dev, task.ID, submit)
	if err != nil {
		t.Fatal(err)
	}
	pending.Task = passTesting(t, s, pending.Task)
	release := app.Action{Action: "mark_deployed", RequestID: "released", ClaimID: claim.ClaimID, Deployment: &domain.Deployment{Revision: 1, SHA: "version-1", Environment: "production", Body: "发布成功", Actor: "forged", Source: "forged", At: 1}}
	released, err := s.Act(c, dev, task.ID, release)
	if err != nil || released.Task.Status != "review" || released.Task.Deployment.Actor != dev.Name || released.Task.Deployment.Source != "developer" || released.Task.Deployment.At <= 1 {
		t.Fatal(released, err)
	}
	repeated, err := s.Act(c, dev, task.ID, release)
	if err != nil || repeated.Task.Version != released.Task.Version {
		t.Fatal(repeated, err)
	}
	accept := app.Action{Action: "accept", RequestID: "wrong-env", Review: &domain.Review{Revision: 1, SHA: "version-1", Environment: "local", Checks: []string{"passed"}}}
	if _, err := s.Act(c, app.Human(), task.ID, accept); err == nil {
		t.Fatal("accepted on undeployed environment")
	}
	// Reject -> submit -> release is mandatory for every new development result.
	accept.Action, accept.RequestID, accept.Review.Environment, accept.Review.Note = "reject", "reject", "production", "边界行为不符"
	if _, err := s.Act(c, app.Human(), task.ID, accept); err != nil {
		t.Fatal(err)
	}
	submit.RequestID, submit.Submission.SHA = "v2", "version-2"
	pending, err = s.Act(c, dev, task.ID, submit)
	if err != nil || pending.Task.Deployment != nil || pending.Task.Status != "testing" {
		t.Fatal(pending, err)
	}
	pending.Task = passTesting(t, s, pending.Task)
	// Editing a pending release invalidates it, and an old confirmation cannot advance it.
	edited, err := s.EditTask(c, app.Human(), task.ID, app.TaskInput{Title: task.Title, Description: "新增业务边界", Acceptance: task.Acceptance, Version: pending.Task.Version, RequestID: "edit"})
	if err != nil || edited.Task.Status != "doing" || edited.Task.Revision != 2 {
		t.Fatal(edited, err)
	}
	release.RequestID = "stale-after-edit"
	if _, err := s.Act(c, dev, task.ID, release); err == nil {
		t.Fatal("released obsolete submission")
	}
	events, _ := s.Store.Events(c, p.ID, task.ID, 0)
	count := 0
	for _, event := range events {
		if event.Kind == "mark_deployed" {
			count++
			if event.Body == "" {
				t.Fatal("release history lost")
			}
		}
	}
	if count != 1 {
		t.Fatal("duplicate or missing release event", count)
	}
}
