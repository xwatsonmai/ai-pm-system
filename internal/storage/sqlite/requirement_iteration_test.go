package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRequirementIterationsSurviveReopen(t *testing.T) {
	c := context.Background()
	path := filepath.Join(t.TempDir(), "iterations.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	parent := domain.Task{ID: "accepted", ProjectID: "p", Title: "已验收需求", Description: "带图片与文档的原文", Status: "done", Revision: 2, Version: 9}
	if err = db.CommitTask(c, parent, 0, domain.Event{ProjectID: "p", TaskID: parent.ID, Kind: "created"}, nil); err != nil {
		t.Fatal(err)
	}
	child := domain.Task{ID: "iteration", ProjectID: "p", Title: "本轮变化", Status: "clarify", Revision: 1, Version: 1, CreatedAt: domain.Now(), Iteration: &domain.RequirementIteration{ParentTaskID: parent.ID, RootTaskID: parent.ID, Source: parent}}
	if err = db.CommitVersionPlan(c, nil, []domain.VersionTaskWrite{{Value: child}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	items, total, err := db.ListRequirementIterations(c, "p", parent.ID, 0, 10)
	if err != nil || total != 1 || len(items) != 1 || !reflect.DeepEqual(items[0], child) {
		t.Fatal("iteration relation and frozen snapshot not persisted", items, total, err)
	}
	got, err := db.GetTask(c, parent.ID)
	if err != nil || !reflect.DeepEqual(got, parent) {
		t.Fatal("source changed during reopen/migration", got, err)
	}
	child.Version++
	child.Status = "closed"
	if err = db.CommitTask(c, child, 1, domain.Event{ProjectID: "p", TaskID: child.ID, Kind: "closed"}, nil); err != nil {
		t.Fatal(err)
	}
	items, total, err = db.ListRequirementIterations(c, "p", parent.ID, 0, 10)
	if err != nil || total != 1 || len(items) != 1 || items[0].Status != "closed" || items[0].Iteration.Source.Description != parent.Description {
		t.Fatal("history did not return current state with frozen source", items, total, err)
	}
	items, total, err = db.ListRequirementIterations(c, "other-project", parent.ID, 0, 10)
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatal("iteration history leaked across projects", items, total, err)
	}
}
