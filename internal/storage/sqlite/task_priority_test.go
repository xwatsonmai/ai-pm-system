package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestTaskPriorityBeforePaginationInFlatAndTreeLists(t *testing.T) {
	d := paginationDB(t)
	groups := [][]struct {
		id, status, review string
		stale, converted   bool
		blocker            string
	}{
		{
			{id: "alignment", status: "aligning"},
			{id: "confirm", status: "clarify", review: "ready_to_confirm"},
			{id: "input", status: "clarify", review: "needs_input"},
			{id: "rejection", status: "clarify", review: "rejection_pending"},
			{id: "failed", status: "clarify", review: "failed"},
		},
		{
			{id: "testing", status: "testing"},
			{id: "retest", status: "retest"},
			{id: "verifying", status: "verifying"},
			{id: "release", status: "pending_release"},
			{id: "acceptance", status: "review"},
			{id: "blocked", status: "doing", blocker: "needs investigation"},
		},
		{
			{id: "ready", status: "ready"},
			{id: "doing", status: "doing"},
			{id: "fixing", status: "fixing"},
			{id: "queued", status: "clarify", review: "queued"},
			{id: "running", status: "clarify", review: "running"},
			{id: "no-review", status: "clarify"},
			{id: "stale", status: "clarify", review: "needs_input", stale: true},
			{id: "stale-failure", status: "clarify", review: "failed", stale: true},
			{id: "converted", status: "clarify", review: "ready_to_confirm", converted: true},
			{id: "converted-alignment", status: "aligning", converted: true},
		},
		{
			{id: "done", status: "done"},
			{id: "resolved", status: "resolved"},
			{id: "closed", status: "closed"},
			{id: "rejected", status: "rejected"},
			{id: "canceled", status: "canceled"},
			{id: "finished-blocker", status: "done", blocker: "historical blocker"},
		},
	}
	want := []string{}
	for priority, group := range groups {
		for i, fixture := range group {
			task := domain.Task{ID: fixture.id, ProjectID: "p", Status: fixture.status, UpdatedAt: int64(priority*100 + i), Blocker: fixture.blocker}
			if fixture.converted {
				task.ChangeRequestID = "change"
			}
			insertTreeTask(t, d, task)
			if fixture.review != "" {
				revision := 1
				if fixture.stale {
					revision = 0
				}
				if _, err := d.db.Exec(`INSERT INTO requirement_reviews(task_id,version,payload) VALUES(?,1,?)`, task.ID, fmt.Sprintf(`{"task_revision":%d,"status":%q}`, revision, fixture.review)); err != nil {
					t.Fatal(err)
				}
			}
		}
		for i := len(group) - 1; i >= 0; i-- {
			want = append(want, group[i].id)
		}
	}
	// Same-priority, same-time records retain the ID tie breaker across pages.
	insertTreeTask(t, d, domain.Task{ID: "alignment-z", ProjectID: "p", Status: "aligning"})
	want = append(want, "")
	copy(want[5:], want[4:])
	want[4] = "alignment-z"
	for _, tree := range []bool{false, true} {
		t.Run(fmt.Sprintf("tree=%t", tree), func(t *testing.T) {
			got := []string{}
			for page := 1; len(got) < len(want); page++ {
				q := domain.TaskListQuery{PageQuery: domain.PageQuery{Page: page, PageSize: 3}}
				if tree {
					result, err := d.QueryTaskTree(context.Background(), "p", domain.TaskTreeQuery{TaskListQuery: q})
					if err != nil || result.Total != len(want) {
						t.Fatal(result, err)
					}
					for _, item := range result.Items {
						got = append(got, item.ID)
					}
				} else {
					result, err := d.QueryTasks(context.Background(), "p", q)
					if err != nil || result.Total != len(want) {
						t.Fatal(result, err)
					}
					for _, item := range result.Items {
						got = append(got, item.ID)
					}
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("priority/time/ID ordering:\n got %v\nwant %v", got, want)
			}
		})
	}
}

func TestTaskTreePromotesPendingDescendantsAndUsesTheirTime(t *testing.T) {
	d := paginationDB(t)
	for _, task := range []domain.Task{
		{ID: "old-family", ProjectID: "p", Status: "done", UpdatedAt: 900},
		{ID: "old-alignment", ProjectID: "p", Status: "aligning", ParentTaskID: "old-family", UpdatedAt: 10},
		{ID: "recent-closed", ProjectID: "p", Status: "closed", ParentTaskID: "old-family", UpdatedAt: 1000},
		{ID: "new-family", ProjectID: "p", Status: "done", UpdatedAt: 1},
		{ID: "iteration", ProjectID: "p", Status: "done", Iteration: &domain.RequirementIteration{ParentTaskID: "new-family"}, UpdatedAt: 2},
		{ID: "new-alignment", ProjectID: "p", Status: "aligning", Kind: "bug", ParentRequirementID: "iteration", UpdatedAt: 20},
		{ID: "testing", ProjectID: "p", Status: "testing", UpdatedAt: 2000},
		{ID: "doing", ProjectID: "p", Status: "doing", UpdatedAt: 3000},
		{ID: "closed", ProjectID: "p", Status: "closed", UpdatedAt: 4000},
	} {
		insertTreeTask(t, d, task)
	}
	for _, test := range []struct {
		query domain.TaskTreeQuery
		want  []string
	}{
		{query: domain.TaskTreeQuery{}, want: []string{"new-family", "old-family", "testing", "doing", "closed"}},
		{query: domain.TaskTreeQuery{ParentID: "old-family"}, want: []string{"old-alignment", "recent-closed"}},
		{query: domain.TaskTreeQuery{ParentID: "new-family"}, want: []string{"iteration"}},
		{query: domain.TaskTreeQuery{TaskListQuery: domain.TaskListQuery{Status: "done,closed"}}, want: []string{"closed", "old-family", "new-family"}},
	} {
		result, err := d.QueryTaskTree(context.Background(), "p", test.query)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, item := range result.Items {
			got = append(got, item.ID)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("query %+v: got %v, want %v", test.query, got, test.want)
		}
	}
}
