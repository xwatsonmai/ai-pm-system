package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func setup(t *testing.T) (*app.Service, domain.Project) {
	t.Helper()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s := &app.Service{Store: db}
	p, e := s.SaveProject(context.Background(), domain.Project{Name: "测试项目"})
	if e != nil {
		t.Fatal(e)
	}
	return s, p
}
func create(t *testing.T, s *app.Service, p string) domain.Task {
	t.Helper()
	m, e := s.CreateTask(context.Background(), app.Human(), p, app.TaskInput{Title: "部分退款", Description: "已支付订单支持部分退款", Acceptance: []string{"累计退款不能超出实付金额"}, RequestID: domain.ID("req_")})
	if e != nil {
		t.Fatal(e)
	}
	m, e = s.Act(context.Background(), app.Human(), m.Task.ID, app.Action{Action: "ready", RequestID: domain.ID("req_")})
	if e != nil {
		t.Fatal(e)
	}
	return m.Task
}
func developer(p, id string) domain.Actor {
	return domain.Actor{ID: id, Name: id, Kind: "developer", ProjectID: p}
}

func TestEvidenceRetryCannotRewriteHistory(t *testing.T) {
	s, p := setup(t)
	changes := 0
	s.OnChange = func(_, _, _ string) { changes++ }
	c := context.Background()
	in := domain.Artifact{ID: "delivery_once", ProjectID: p.ID, Kind: "deployment", SHA: strings.Repeat("b", 40), Environment: "test", Body: "部署任务记录：build-42"}
	first, err := s.Artifact(c, developer(p.ID, "client"), in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Artifact(c, developer(p.ID, "client"), in)
	if err != nil || first.At != again.At {
		t.Fatalf("retry differs: %+v %v", again, err)
	}
	if changes != 1 {
		t.Fatal("evidence must trigger analysis once, not on retry")
	}
	in.Body = "different outcome"
	if _, err = s.Artifact(c, developer(p.ID, "client"), in); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("evidence overwritten: %v", err)
	}
	stored, err := s.Store.GetArtifact(c, in.ID)
	if err != nil || stored.Body != first.Body {
		t.Fatal("original evidence lost", err)
	}
}

func TestLifecycleRevisionAndAuthority(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	task := create(t, s, p.ID)
	dev := developer(p.ID, "dev1")
	claim, e := s.Act(c, dev, task.ID, app.Action{Action: "claim", RequestID: "claim-1"})
	if e != nil {
		t.Fatal(e)
	}
	repeat, e := s.Act(c, dev, task.ID, app.Action{Action: "claim", RequestID: "claim-1"})
	if e != nil || repeat.ClaimID != claim.ClaimID {
		t.Fatalf("claim not idempotent: %+v %v", repeat, e)
	}
	_, e = s.Act(c, dev, task.ID, app.Action{Action: "report", RequestID: "claim-1", ClaimID: claim.ClaimID, Body: "reuse key"})
	if !errors.Is(e, domain.ErrConflict) {
		t.Fatalf("reused key accepted: %v", e)
	}
	_, e = s.Act(c, dev, task.ID, app.Action{Action: "report", RequestID: "wrong", ClaimID: "wrong", Body: "bad"})
	if !errors.Is(e, domain.ErrForbidden) {
		t.Fatal(e)
	}
	edit, e := s.EditTask(c, app.Human(), task.ID, app.TaskInput{Title: task.Title, Description: "增加重复退款验证", Acceptance: task.Acceptance, Version: claim.Task.Version, RequestID: "revise"})
	if e != nil {
		t.Fatal(e)
	}
	in := app.Action{Action: "submit", RequestID: "submit-stale", ClaimID: claim.ClaimID, Revision: 1, Submission: &domain.Submission{Summary: "完成退款", SHA: strings.Repeat("a", 40), Tests: "边界检查通过"}}
	_, e = s.Act(c, dev, task.ID, in)
	if !errors.Is(e, domain.ErrConflict) {
		t.Fatalf("stale revision accepted: %v", e)
	}
	in.RequestID = "submit-current"
	in.Revision = edit.Task.Revision
	m, e := s.Act(c, dev, task.ID, in)
	if e != nil || m.Task.Status != "testing" {
		t.Fatalf("submit: %v", e)
	}
	again, e := s.Act(c, dev, task.ID, in)
	if e != nil || again.Task.Version != m.Task.Version {
		t.Fatalf("submit retry: %v", e)
	}
	if _, e = s.Act(c, app.Human(), task.ID, app.Action{Action: "accept", RequestID: "too-early", Review: &domain.Review{Revision: 2, SHA: strings.Repeat("a", 40), Environment: "test", Checks: []string{"passed"}}}); e == nil {
		t.Fatal("accepted before release")
	}
	m.Task = passTesting(t, s, m.Task)
	m, e = s.Act(c, dev, task.ID, app.Action{Action: "mark_deployed", RequestID: "release", ClaimID: claim.ClaimID, Deployment: &domain.Deployment{Revision: 2, SHA: strings.Repeat("a", 40), Environment: "test", Body: "实际发布成功，发布日志 build-42"}})
	if e != nil || m.Task.Status != "review" {
		t.Fatalf("release: %v", e)
	}
	review := app.Action{Action: "accept", RequestID: "accept", Review: &domain.Review{Revision: 2, SHA: strings.Repeat("a", 40), Environment: "test", Checks: []string{"passed"}}}
	_, e = s.Act(c, dev, task.ID, review)
	if !errors.Is(e, domain.ErrForbidden) {
		t.Fatalf("developer accepted task: %v", e)
	}
	m, e = s.Act(c, app.Human(), task.ID, review)
	if e != nil || m.Task.Status != "done" {
		t.Fatalf("review: %v", e)
	}
	events, e := s.Store.Events(c, p.ID, task.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	if len(events) != 8 {
		t.Fatalf("unexpected events (duplicate mutation?): %d", len(events))
	}
	history, _ := json.Marshal(events)
	if strings.Contains(string(history), claim.ClaimID) {
		t.Fatal("claim credential leaked into shared history")
	}
	artifacts, _ := s.Store.ListArtifacts(c, p.ID)
	if len(artifacts) != 0 {
		t.Fatal("acceptance invented deployment evidence")
	}
}
func TestConcurrentClaimAndHandoff(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	task := create(t, s, p.ID)
	var wg sync.WaitGroup
	results := make(chan domain.Mutation, 2)
	errs := make(chan error, 2)
	for _, name := range []string{"a", "b"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			m, e := s.Act(c, developer(p.ID, name), task.ID, app.Action{Action: "claim", RequestID: "claim"})
			if e == nil {
				results <- m
			} else {
				errs <- e
			}
		}(name)
	}
	wg.Wait()
	close(results)
	close(errs)
	if len(results) != 1 || len(errs) != 1 {
		t.Fatalf("claims winners=%d losers=%d", len(results), len(errs))
	}
	winner := <-results
	oldActor := developer(p.ID, winner.Task.Claimant)
	_, e := s.Act(c, app.Human(), task.ID, app.Action{Action: "release", RequestID: "handoff", Body: "已有提交待检查"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Act(c, developer(p.ID, "new"), task.ID, app.Action{Action: "claim", RequestID: "new-claim"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Act(c, oldActor, task.ID, app.Action{Action: "report", RequestID: "old-report", ClaimID: winner.ClaimID, Body: "late"})
	if !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("old claim still valid", e)
	}
}
func TestPersistenceAndSecretRedaction(t *testing.T) {
	file := filepath.Join(t.TempDir(), "test.db")
	c := context.Background()
	db, e := sqlite.Open(file)
	if e != nil {
		t.Fatal(e)
	}
	s := &app.Service{Store: db}
	p, e := s.SaveProject(c, domain.Project{Name: "durable"})
	if e != nil {
		t.Fatal(e)
	}
	task := create(t, s, p.ID)
	profile, e := s.SaveProfile(c, domain.Profile{Name: "test", Provider: "openai", Model: "test-model"}, "secret-never-serialize")
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = sqlite.Open(file)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	loaded, e := db.GetTask(c, task.ID)
	if e != nil || loaded.Title != task.Title {
		t.Fatalf("task not persisted %v", e)
	}
	stored, e := db.GetProfile(c, profile.ID)
	if e != nil || stored.APIKey != "secret-never-serialize" {
		t.Fatal("profile lost", e)
	}
	b, _ := json.Marshal(stored)
	if strings.Contains(string(b), "secret-never-serialize") {
		t.Fatal("key leaked")
	}
	projects, e := db.ListProjects(c)
	if e != nil || len(projects) != 1 {
		t.Fatal("projects not persisted", e)
	}
}
func TestCanConnectInitiallyEmptyRepository(t *testing.T) {
	s, p := setup(t)
	p.Repository = "/tmp/example"
	if _, e := s.SaveProject(context.Background(), p); e != nil {
		t.Fatal(e)
	}
}
