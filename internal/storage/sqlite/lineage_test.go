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

func insertLineageVersion(t *testing.T, d *DB, v domain.ProjectVersion) {
	t.Helper()
	v.Version = 1
	raw, _ := json.Marshal(v)
	if _, e := d.db.Exec("INSERT INTO project_versions(id,project_id,name_key,version,payload) VALUES(?,?,?,1,?)", v.ID, v.ProjectID, strings.ToLower(v.Name), string(raw)); e != nil {
		t.Fatal(e)
	}
}
func TestLineageHistoricalScopeRelationsAndPagination(t *testing.T) {
	d := paginationDB(t)
	c := context.Background()
	root := domain.Task{ID: "a-root", ProjectID: "p", ProjectVersionID: "v1", Title: "Original title", Kind: "requirement", Status: "done", Revision: 1, Description: "SECRET_DOCUMENT"}
	child := domain.Task{ID: "b-sub", ProjectID: "p", ProjectVersionID: "v1", Title: "Export", Kind: "requirement", Status: "ready", ParentTaskID: root.ID}
	bug := domain.Task{ID: "c-bug", ProjectID: "p", ProjectVersionID: "v1", Title: "Tax bug", Kind: "bug", Status: "ready", ParentRequirementID: root.ID}
	deep := domain.Task{ID: "d-deep", ProjectID: "p", ProjectVersionID: "v1", Title: "Deep search target", Kind: "bug", Status: "ready", ParentRequirementID: child.ID}
	standalone := domain.Task{ID: "e-other", ProjectID: "p", ProjectVersionID: "v1", Title: "Standalone", Kind: "requirement", Status: "ready"}
	v := domain.ProjectVersion{ID: "v1", ProjectID: "p", Name: "Historical release", Status: "completed"}
	for _, task := range []domain.Task{root, child, bug, deep, standalone} {
		v.Scope = append(v.Scope, domain.VersionScopeItem{Task: task})
		insertTreeTask(t, d, task)
	}
	v.Scope = append(v.Scope, domain.VersionScopeItem{Task: domain.Task{ID: "removed", Kind: "bug"}, Removed: true})
	insertLineageVersion(t, d, v)
	insertLineageVersion(t, d, domain.ProjectVersion{ID: "v2", ProjectID: "p", Name: "Next release", Status: "active"})
	updated := root
	updated.Title = "NEW_TITLE_MUST_NOT_REPLACE_HISTORY"
	updated.Revision = 2
	updated.ProjectVersionID = "v2"
	raw, _ := json.Marshal(updated)
	_, _ = d.db.Exec("UPDATE tasks SET payload=? WHERE id=?", string(raw), root.ID)
	insertTreeTask(t, d, domain.Task{ID: "f-future", ProjectID: "p", Title: "Next iteration", ProjectVersionID: "v2", Iteration: &domain.RequirementIteration{ParentTaskID: root.ID, Source: domain.Task{Description: "FROZEN_SECRET"}}})
	insertTreeTask(t, d, domain.Task{ID: "foreign", ProjectID: "other-project", Title: "FOREIGN_SECRET", ParentTaskID: root.ID})
	rows, e := d.QueryLineage(c, "p", "v1", domain.LineageQuery{PageQuery: domain.PageQuery{Page: 1, PageSize: 1}})
	if e != nil || rows.Total != 2 || len(rows.Items) != 1 || rows.Items[0].Title != root.Title {
		t.Fatalf("root page: %+v %v", rows, e)
	}
	if rows.Version.Requirements != 3 || rows.Version.Bugs != 2 || rows.Version.Completed != 1 || rows.MatchingTotal != 5 {
		t.Fatalf("full-scope counts: %+v", rows)
	}
	if !rows.Items[0].Historical || !rows.Items[0].CurrentAvailable {
		t.Fatal("later revision was not identified as historical")
	}
	children, e := d.QueryLineage(c, "p", "v1", domain.LineageQuery{ParentID: root.ID})
	if e != nil || children.Total != 3 {
		t.Fatalf("children: %+v %v", children, e)
	}
	seen := map[string]domain.LineageNode{}
	for _, n := range children.Items {
		seen[n.ID] = n
	}
	if seen[child.ID].Relation != "subrequirement" || seen[bug.ID].Relation != "bug" || seen["f-future"].Relation != "iteration" || seen["f-future"].InScope || seen["f-future"].ChildrenCount != 0 || seen["f-future"].VersionName != "Next release" {
		t.Fatal(seen)
	}
	encoded, _ := json.Marshal(children)
	for _, secret := range []string{"SECRET_DOCUMENT", "FROZEN_SECRET", "FOREIGN_SECRET", "NEW_TITLE_MUST_NOT_REPLACE_HISTORY"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("private or wrong-version data leaked")
		}
	}
	search, e := d.QueryLineage(c, "p", "v1", domain.LineageQuery{PageQuery: domain.PageQuery{Query: "Deep search"}})
	if e != nil || search.MatchingTotal != 1 || len(search.Items) != 1 || search.Items[0].ID != root.ID {
		t.Fatalf("search path: %+v %v", search, e)
	}
	last, e := d.QueryLineage(c, "p", "v1", domain.LineageQuery{PageQuery: domain.PageQuery{Page: 99, PageSize: 1}})
	if e != nil || last.Page.Page != 2 || last.Items[0].ID != standalone.ID {
		t.Fatalf("clamped page: %+v %v", last, e)
	}
	if _, e = d.QueryLineage(c, "other-project", "v1", domain.LineageQuery{}); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("cross-project version", e)
	}
	if _, e = d.QueryLineage(c, "p", "v1", domain.LineageQuery{ParentID: "foreign"}); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("cross-project parent", e)
	}
}

func TestLineageCycleAndVersionDirectoryCounts(t *testing.T) {
	d := paginationDB(t)
	c := context.Background()
	v := domain.ProjectVersion{ID: "v", ProjectID: "p", Name: "Cycle", Status: "active"}
	for _, item := range []domain.Task{{ID: "a", ProjectID: "p", ProjectVersionID: "v", Kind: "requirement", ParentTaskID: "b"}, {ID: "b", ProjectID: "p", ProjectVersionID: "v", Kind: "requirement", ParentTaskID: "a"}} {
		insertTreeTask(t, d, item)
		v.Scope = append(v.Scope, domain.VersionScopeItem{Task: item})
	}
	insertLineageVersion(t, d, v)
	for i := 0; i < 15; i++ {
		insertLineageVersion(t, d, domain.ProjectVersion{ID: fmt.Sprint("v-", i), ProjectID: "p", Name: fmt.Sprint("Other ", i), Status: "planned"})
	}
	graph, e := d.QueryLineage(c, "p", "v", domain.LineageQuery{})
	if e != nil || graph.Total != 1 || graph.Items[0].ID != "a" {
		t.Fatalf("cycle: %+v %v", graph, e)
	}
	_, _ = d.db.Exec("DELETE FROM tasks WHERE id='b'")
	child, e := d.QueryLineage(c, "p", "v", domain.LineageQuery{ParentID: "a"})
	if e != nil || len(child.Items) != 1 || !child.Items[0].Historical || child.Items[0].CurrentAvailable {
		t.Fatalf("missing current task should retain historical scope: %+v %v", child, e)
	}
	versions, e := d.LineageVersions(c, "p", domain.PageQuery{PageSize: 12})
	if e != nil || versions.Total != 16 || len(versions.Items) != 12 || versions.Items[0].Requirements != 2 {
		t.Fatalf("directory: %+v %v", versions, e)
	}
	versions, e = d.LineageVersions(c, "p", domain.PageQuery{Page: 2, PageSize: 12})
	if e != nil || len(versions.Items) != 4 {
		t.Fatal(versions, e)
	}
	versions, e = d.LineageVersions(c, "p", domain.PageQuery{Query: "Cycle"})
	if e != nil || versions.Total != 1 || versions.Items[0].Requirements != 2 {
		t.Fatal(versions, e)
	}
}
