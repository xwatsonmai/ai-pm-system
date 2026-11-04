package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type interleaveVersionCommit struct {
	storage.Store
	before func()
	once   sync.Once
}

func (s *interleaveVersionCommit) CommitVersionPlan(c context.Context, vs []domain.VersionWrite, ts []domain.VersionTaskWrite, es []domain.Event, r *domain.Receipt, bs ...domain.MilestoneBatch) error {
	s.once.Do(s.before)
	return s.Store.CommitVersionPlan(c, vs, ts, es, r, bs...)
}

func TestDefaultSuccessorReadConflictsWithConcurrentChange(t *testing.T) {
	for _, action := range []string{"complete", "close"} {
		for _, explicit := range []bool{false, true} {
			for _, concurrent := range []string{"complete", "close", "edit"} {
				name := action + "/automatic/" + concurrent
				if explicit {
					name = action + "/explicit/" + concurrent
				}
				t.Run(name, func(t *testing.T) {
					s, p := setup(t)
					c := context.Background()
					a := parallelVersion(t, s, p.ID, "A")
					b := parallelVersion(t, s, p.ID, "B")
					base := s.Store
					other := &app.Service{Store: base}
					entered, resume := make(chan struct{}), make(chan struct{})
					var release sync.Once
					defer release.Do(func() { close(resume) })
					s.Store = &interleaveVersionCommit{Store: base, before: func() {
						close(entered)
						<-resume
					}}
					in := app.ProjectVersionAction{Action: action, Version: a.Version, Body: "结束默认版本", RequestID: "end-A"}
					if explicit {
						in.DestinationVersionID, in.DestinationVersion = b.ID, b.Version
					}
					done := make(chan error, 1)
					go func() { _, e := s.ActProjectVersion(c, app.Human(), a.ID, in); done <- e }()
					select {
					case <-entered:
					case <-time.After(5 * time.Second):
						t.Fatal("default transfer did not reach commit")
					}
					var e error
					if concurrent == "edit" {
						_, e = other.SaveProjectVersion(c, app.Human(), p.ID, b.ID, app.ProjectVersionInput{Name: b.Name, Goal: "新目标", Version: b.Version, RequestID: "edit-B"})
					} else {
						_, e = other.ActProjectVersion(c, app.Human(), b.ID, app.ProjectVersionAction{Action: concurrent, Version: b.Version, Body: "结束并行版本", RequestID: "end-B"})
					}
					if e != nil {
						t.Fatal(e)
					}
					release.Do(func() { close(resume) })
					select {
					case e = <-done:
						if !errors.Is(e, domain.ErrConflict) {
							t.Fatalf("stale successor accepted: %v", e)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("default transfer did not finish")
					}
					state, _ := base.GetCurrentMilestone(c, p.ID)
					stored, _ := base.GetProjectVersion(c, a.ID)
					if state.VersionID != a.ID || !reflect.DeepEqual(stored, a) {
						t.Fatal("rejected transfer changed default or source version", state, stored)
					}
				})
			}
		}
	}
}

func TestDefaultTransferLeavesSuccessorVersionAndRetryUnchanged(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	a := parallelVersion(t, s, p.ID, "A")
	b := parallelVersion(t, s, p.ID, "B")
	in := app.ProjectVersionAction{Action: "complete", Version: a.Version, DestinationVersionID: b.ID, DestinationVersion: b.Version, Body: "结束默认版本", RequestID: "transfer"}
	first, e := s.ActProjectVersion(c, app.Human(), a.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	state, _ := s.Store.GetCurrentMilestone(c, p.ID)
	if state.VersionID != b.ID || !reflect.DeepEqual(versionDetail(t, s, b.ID).ProjectVersion, b) {
		t.Fatal("read-only successor was rewritten")
	}
	retry, e := s.ActProjectVersion(c, app.Human(), a.ID, in)
	if e != nil || !reflect.DeepEqual(first, retry) {
		t.Fatal("retry changed the saved transfer", e)
	}
	after, _ := s.Store.GetCurrentMilestone(c, p.ID)
	if after != state {
		t.Fatal("retry changed the default revision")
	}
}

func TestExplicitTaskDestinationCannotCloseBetweenReadAndCommit(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	parallelVersion(t, s, p.ID, "A")
	b := parallelVersion(t, s, p.ID, "B")
	base := s.Store
	other := &app.Service{Store: base}
	s.Store = &interleaveVersionCommit{Store: base, before: func() {
		if _, e := other.ActProjectVersion(c, app.Human(), b.ID, app.ProjectVersionAction{Action: "close", Version: b.Version, Body: "结束", RequestID: "close-B"}); e != nil {
			t.Fatal(e)
		}
	}}
	_, e := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "明确归属 B", ProjectVersionID: b.ID, RequestID: "create-B"})
	if !errors.Is(e, domain.ErrConflict) {
		t.Fatal("task entered a closed version", e)
	}
	tasks, _ := base.ListTasks(c, p.ID)
	if len(tasks) != 0 || len(versionDetail(t, s, b.ID).ProjectVersion.Scope) != 0 {
		t.Fatal("failed creation left task or scope")
	}
}

func TestClosedVersionLinkedBugContinuationMovesParentAtomically(t *testing.T) {
	for _, operation := range []string{"reopen", "return_bug"} {
		t.Run(operation, func(t *testing.T) {
			s, p := setup(t)
			c := context.Background()
			parent := testingRequirement(t, s, p.ID)
			bug := fileTestBug(t, s, parent)
			var e error
			if operation == "reopen" {
				m, err := s.Act(c, app.Human(), bug.ID, app.Action{Action: "close", Version: bug.Version, Body: "暂不处理", RequestID: "close-bug"})
				if err != nil {
					t.Fatal(err)
				}
				bug = m.Task
			} else {
				reviewBug(t, s, bug, "ready")
				m, err := s.Act(c, developer(p.ID, "dev"), bug.ID, app.Action{Action: "claim", Branch: "fix/bug", RequestID: "claim-bug"})
				if err != nil {
					t.Fatal(err)
				}
				m, err = s.Act(c, app.Human(), bug.ID, app.Action{Action: "submit", Revision: m.Task.Revision, Submission: &domain.Submission{SHA: strings.Repeat("b", 40), Summary: "修复", Tests: "通过"}, RequestID: "submit-bug"})
				if err != nil {
					t.Fatal(err)
				}
				m, err = s.Act(c, app.Human(), bug.ID, app.Action{Action: "verify_bug", Version: m.Task.Version, TestVerification: &domain.TestVerification{Revision: m.Task.Revision, SHA: m.Task.Submission.SHA, Environment: "测试", Note: "验证通过"}, RequestID: "verify-bug"})
				if err != nil {
					t.Fatal(err)
				}
				bug = m.Task
			}
			parent, _ = s.Store.GetTask(c, parent.ID)
			s.ValidateTestingCode = func(context.Context, string, string, string, []string) error { return nil }
			retest, e := s.Act(c, app.Human(), parent.ID, app.Action{Action: "start_retest", Version: parent.Version, RequestID: "retest", TestVerification: &domain.TestVerification{Revision: parent.Revision, SHA: parent.Submission.SHA, Environment: "测试", Note: "复测"}})
			if e != nil {
				t.Fatal(e)
			}
			parent = passTesting(t, s, retest.Task)
			parent = publishVersion(t, s, parent.ProjectVersionID, parent).Tasks[0]
			v := versionDetail(t, s, parent.ProjectVersionID).ProjectVersion
			if _, e = s.ActProjectVersion(c, app.Human(), v.ID, app.ProjectVersionAction{Action: "close", Version: v.Version, Body: "归档", CreateSuccessor: true, RequestID: "archive"}); e != nil {
				t.Fatal(e)
			}
			state, _ := s.Store.GetCurrentMilestone(c, p.ID)
			before := versionDetail(t, s, v.ID).ProjectVersion.Releases
			in := app.Action{Action: operation, Version: bug.Version, Body: "验收发现问题复现", RequestID: "continue-bug"}
			if operation == "reopen" {
				in.DestinationVersionID = state.VersionID
			}
			continued, e := s.Act(c, app.Human(), bug.ID, in)
			if e != nil {
				t.Fatal("cannot continue the linked Bug in an active successor", e)
			}
			stored, _ := s.Store.GetTask(c, parent.ID)
			if stored.ProjectVersionID != state.VersionID || continued.Task.ProjectVersionID != state.VersionID || stored.Status != "fixing" {
				t.Fatal("parent and Bug split across versions", stored, continued.Task)
			}
			if !reflect.DeepEqual(before, versionDetail(t, s, v.ID).ProjectVersion.Releases) {
				t.Fatal("continuation changed the immutable release")
			}
			retry, e := s.Act(c, app.Human(), bug.ID, in)
			if e != nil || retry.Task.Version != continued.Task.Version {
				t.Fatal("continuation retry changed the result", e)
			}
		})
	}
}
