package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func insertTreeTask(t *testing.T, d *DB, v domain.Task) {
	t.Helper()
	if v.Kind == "" {
		v.Kind = "requirement"
	}
	if v.Status == "" {
		v.Status = "ready"
	}
	v.Version = 1
	v.Revision = 1
	b, _ := json.Marshal(v)
	if _, e := d.db.Exec("INSERT INTO tasks(id,project_id,version,claim_hash,payload) VALUES(?,?,1,'',?)", v.ID, v.ProjectID, string(b)); e != nil {
		t.Fatal(e)
	}
}

func TestTaskTreePagesFamiliesAndChildrenWithoutFullDocuments(t *testing.T) {
	d := paginationDB(t)
	c := context.Background()
	insertTreeTask(t, d, domain.Task{ID: "root", ProjectID: "p", Title: "Main requirement", ProjectVersionID: "v1", Description: strings.Repeat("SECRET_BODY", 10000), UpdatedAt: 1})
	for i := 0; i < 45; i++ {
		insertTreeTask(t, d, domain.Task{ID: fmt.Sprintf("bug-%02d", i), ProjectID: "p", Title: "Linked Bug", Kind: "bug", ParentRequirementID: "root", ProjectVersionID: "v1", UpdatedAt: 10})
	}
	insertTreeTask(t, d, domain.Task{ID: "iteration", ProjectID: "p", Title: "Next iteration", ProjectVersionID: "v2", Status: "clarify", UpdatedAt: 20, Iteration: &domain.RequirementIteration{ParentTaskID: "root", RootTaskID: "root", Source: domain.Task{Description: "FROZEN_SECRET"}}})
	insertTreeTask(t, d, domain.Task{ID: "grandchild", ProjectID: "p", Title: "needle issue", Kind: "bug", Status: "verifying", ParentRequirementID: "iteration", ProjectVersionID: "v2", UpdatedAt: 500})
	for i := 0; i < 25; i++ {
		insertTreeTask(t, d, domain.Task{ID: fmt.Sprintf("other-%02d", i), ProjectID: "p", Title: "Independent requirement", UpdatedAt: 100})
	}
	root, e := d.QueryTaskTree(c, "p", domain.TaskTreeQuery{})
	if e != nil {
		t.Fatal(e)
	}
	if root.Total != 26 || root.MatchingTotal != 73 || len(root.Items) != 20 || root.Items[0].ID != "root" || root.Items[0].ChildrenCount != 46 || root.Items[0].MatchingCount != 48 || root.Items[0].Description != "" {
		t.Fatal(root)
	}
	seen := map[string]bool{}
	for page := 1; page <= 3; page++ {
		children, e := d.QueryTaskTree(c, "p", domain.TaskTreeQuery{ParentID: "root", TaskListQuery: domain.TaskListQuery{PageQuery: domain.PageQuery{Page: page}}})
		if e != nil || children.Total != 46 {
			t.Fatal(children, e)
		}
		for _, node := range children.Items {
			if seen[node.ID] || node.TreeParentID != "root" {
				t.Fatal("duplicate or wrong parent", node.ID)
			}
			seen[node.ID] = true
			if node.ID == "iteration" && (node.Relation != "iteration" || node.Iteration.Source.Description != "") {
				t.Fatal("iteration source leaked", node)
			}
		}
	}
	if len(seen) != 46 {
		t.Fatal("children lost across pages", len(seen))
	}
	last, e := d.QueryTaskTree(c, "p", domain.TaskTreeQuery{TaskListQuery: domain.TaskListQuery{PageQuery: domain.PageQuery{Page: 999}}})
	if e != nil || last.Page.Page != 2 || len(last.Items) != 6 {
		t.Fatal(last, e)
	}
	for _, query := range []domain.TaskListQuery{{PageQuery: domain.PageQuery{Query: "needle"}}, {Status: "verifying"}} {
		filtered, e := d.QueryTaskTree(c, "p", domain.TaskTreeQuery{TaskListQuery: query})
		if e != nil || filtered.Total != 1 || filtered.MatchingTotal != 1 || filtered.Items[0].ID != "root" || filtered.Items[0].MatchesFilter || filtered.Items[0].ChildrenCount != 1 {
			t.Fatal(filtered, e)
		}
		children, e := d.QueryTaskTree(c, "p", domain.TaskTreeQuery{TaskListQuery: query, ParentID: "root"})
		if e != nil || children.Total != 1 || children.Items[0].ID != "iteration" || children.Items[0].MatchesFilter {
			t.Fatal(children, e)
		}
		grand, e := d.QueryTaskTree(c, "p", domain.TaskTreeQuery{TaskListQuery: query, ParentID: "iteration"})
		if e != nil || grand.Total != 1 || grand.Items[0].ID != "grandchild" || !grand.Items[0].MatchesFilter {
			t.Fatal(grand, e)
		}
	}
	version, e := d.QueryTaskTree(c, "p", domain.TaskTreeQuery{TaskListQuery: domain.TaskListQuery{ProjectVersionID: "v2"}})
	if e != nil || version.Total != 1 || version.MatchingTotal != 2 || version.Items[0].ProjectVersionID != "v1" || version.Items[0].MatchesFilter {
		t.Fatal("cross-version ancestors lost", version, e)
	}
	if _, e = d.db.Exec(`INSERT INTO requirement_reviews(task_id,version,payload) VALUES('iteration',1,'{"task_revision":1,"status":"needs_input"}')`); e != nil {
		t.Fatal(e)
	}
	pending, e := d.QueryTaskTree(c, "p", domain.TaskTreeQuery{TaskListQuery: domain.TaskListQuery{Status: "review_pending"}})
	if e != nil || pending.MatchingTotal != 1 || pending.Items[0].PendingCount != 1 {
		t.Fatal(pending, e)
	}
}

func TestTaskTreeHandlesMissingForeignAndCyclicParents(t *testing.T) {
	d := paginationDB(t)
	c := context.Background()
	for _, v := range []domain.Task{
		{ID: "foreign", ProjectID: "hidden", Title: "FOREIGN_SECRET"},
		{ID: "foreign-child", ProjectID: "p", Kind: "bug", ParentRequirementID: "foreign"},
		{ID: "orphan", ProjectID: "p", Kind: "bug", ParentRequirementID: "missing"},
		{ID: "cycle-a", ProjectID: "p", Iteration: &domain.RequirementIteration{ParentTaskID: "cycle-b"}},
		{ID: "cycle-b", ProjectID: "p", Iteration: &domain.RequirementIteration{ParentTaskID: "cycle-a"}},
		{ID: "leaf", ProjectID: "p", Iteration: &domain.RequirementIteration{ParentTaskID: "cycle-b"}},
	} {
		insertTreeTask(t, d, v)
	}
	root, e := d.QueryTaskTree(c, "p", domain.TaskTreeQuery{})
	if e != nil || root.Total != 3 || root.MatchingTotal != 5 {
		t.Fatal(root, e)
	}
	seen := map[string]bool{}
	queue := append([]domain.TaskTreeNode{}, root.Items...)
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if seen[v.ID] || v.ProjectID != "p" || v.Title == "FOREIGN_SECRET" {
			t.Fatal("cycle or disclosure", v.ID)
		}
		seen[v.ID] = true
		children, e := d.QueryTaskTree(c, "p", domain.TaskTreeQuery{ParentID: v.ID})
		if e != nil {
			t.Fatal(e)
		}
		queue = append(queue, children.Items...)
	}
	if len(seen) != 5 {
		t.Fatal("orphan/cycle tasks disappeared", seen)
	}
	if _, e = d.QueryTaskTree(c, "p", domain.TaskTreeQuery{ParentID: "foreign"}); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("foreign parent accepted", e)
	}
	if _, e = d.QueryTaskTree(c, "p", domain.TaskTreeQuery{TaskListQuery: domain.TaskListQuery{Kind: "invalid"}}); e == nil {
		t.Fatal("invalid filter accepted")
	}
}
