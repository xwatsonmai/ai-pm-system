package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSubrequirementCreationPermissionsRetryAndImmutableParent(t *testing.T) {
	s, p, owner, member := accessFixture(t)
	c := context.Background()
	parent, e := s.CreateTask(c, owner, p.ID, app.TaskInput{Title: "Parent", Kind: "requirement", RequestID: "parent"})
	if e != nil {
		t.Fatal(e)
	}
	in := app.TaskInput{Title: "Child", Kind: "requirement", ParentTaskID: parent.Task.ID, RequestID: "child"}
	child, e := s.CreateTask(c, owner, p.ID, in)
	if e != nil || child.Task.ParentTaskID != parent.Task.ID || child.Task.ProjectVersionID != parent.Task.ProjectVersionID {
		t.Fatal(child, e)
	}
	_, e = s.EditTask(c, owner, parent.Task.ID, app.TaskInput{Title: "Parent becomes Bug", Kind: "bug", Version: parent.Task.Version, RequestID: "convert-parent"})
	if e == nil || !strings.Contains(e.Error(), "已有子需求") {
		t.Fatal("parent conversion broke the subrequirement relation", e)
	}
	again, e := s.CreateTask(c, owner, p.ID, in)
	if e != nil || again.Task.ID != child.Task.ID {
		t.Fatal("retry created duplicate", again, e)
	}
	if _, e = s.CreateTask(c, member, p.ID, in); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("missing access", e)
	}
	other, e := s.SaveProjectAs(c, owner, domain.Project{Name: "Other"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateTask(c, owner, other.ID, in); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("foreign parent", e)
	}
	edit := app.TaskInput{Title: "Child corrected", Kind: "requirement", Version: child.Task.Version, RequestID: "edit"}
	changed, e := s.EditTask(c, owner, child.Task.ID, edit)
	if e != nil || changed.Task.ParentTaskID != parent.Task.ID {
		t.Fatal("legacy edit lost relation", changed, e)
	}
	edit.ParentTaskID = child.Task.ID
	edit.RequestID = "cycle"
	edit.Version = changed.Task.Version
	if _, e = s.EditTask(c, owner, child.Task.ID, edit); e == nil {
		t.Fatal("editor introduced cycle")
	}
	in.RequestID = "mixed"
	in.Kind = "bug"
	if _, e = s.CreateTask(c, owner, p.ID, in); e == nil {
		t.Fatal("Bug accepted as split requirement")
	}
}
