package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func paginationDB(t *testing.T) *DB {
	t.Helper()
	db, e := Open(filepath.Join(t.TempDir(), "page.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
func TestTaskPaginationSQLFilterSummaryAndStableTies(t *testing.T) {
	d := paginationDB(t)
	c := context.Background()
	for i := 0; i < 205; i++ {
		status := "ready"
		if i%3 == 0 {
			status = "review"
		}
		p := "p"
		if i >= 200 {
			p = "hidden"
		}
		v := domain.Task{ID: fmt.Sprintf("t%03d", i), ProjectID: p, Title: fmt.Sprintf("task %03d", i), Status: status, Kind: "requirement", Description: strings.Repeat("BODY_SENTINEL", 2000), Revision: 1, Version: 1, UpdatedAt: 100, ProjectVersionID: "v1"}
		raw, _ := json.Marshal(v)
		if _, e := d.db.Exec(`INSERT INTO tasks(id,project_id,version,claim_hash,payload) VALUES(?,?,1,'',?)`, v.ID, p, string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	first, e := d.QueryTasks(c, "p", domain.TaskListQuery{PageQuery: domain.PageQuery{Page: 1, PageSize: 20}})
	if e != nil {
		t.Fatal(e)
	}
	if first.Total != 200 || len(first.Items) != 20 || first.Items[0].ID != "t199" || first.Items[0].Description != "" {
		t.Fatal(first)
	}
	seen := map[string]bool{}
	for page := 1; page <= 10; page++ {
		v, e := d.QueryTasks(c, "p", domain.TaskListQuery{PageQuery: domain.PageQuery{Page: page, PageSize: 20}})
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range v.Items {
			if seen[item.ID] {
				t.Fatal("duplicate", item.ID)
			}
			seen[item.ID] = true
		}
	}
	filtered, e := d.QueryTasks(c, "p", domain.TaskListQuery{PageQuery: domain.PageQuery{Query: "task 19"}, Status: "ready", ProjectVersionID: "v1"})
	if e != nil || filtered.Total != 7 {
		t.Fatal(filtered, e)
	}
	last, e := d.QueryTasks(c, "p", domain.TaskListQuery{PageQuery: domain.PageQuery{Page: 999, PageSize: 20}})
	if e != nil || last.Page != 10 || len(last.Items) != 20 {
		t.Fatal(last, e)
	}
	stats, e := d.TaskListStats(c, "p")
	if e != nil || stats.Total != 200 || stats.ByStatus["review"] != 67 || stats.Attention != 67 {
		t.Fatal(stats, e)
	}
	// Like the UI, a stale review revision cannot mark the new source pending.
	if _, e = d.db.Exec(`UPDATE tasks SET payload=json_set(payload,'$.status','clarify') WHERE id='t199'`); e != nil {
		t.Fatal(e)
	}
	for _, revision := range []int{0, 1} {
		raw := fmt.Sprintf(`{"task_revision":%d,"status":"ready_to_confirm"}`, revision)
		if _, e = d.db.Exec(`INSERT OR REPLACE INTO requirement_reviews(task_id,version,payload) VALUES('t199',1,?)`, raw); e != nil {
			t.Fatal(e)
		}
		v, e := d.QueryTasks(c, "p", domain.TaskListQuery{Status: "review_pending"})
		if e != nil || v.Total != revision {
			t.Fatal(v, e)
		}
	}
}
func TestDocumentPagesApplyPrivacyBeforeCountAndLimit(t *testing.T) {
	d := paginationDB(t)
	c := context.Background()
	for i := 0; i < 45; i++ {
		owner := "alice"
		if i%2 == 0 {
			owner = "bob"
		}
		r := domain.Run{ID: fmt.Sprintf("r%03d", i), ProjectID: "p", ConversationID: fmt.Sprintf("c%03d", i), Kind: "question", OwnerID: owner, OwnerKind: "human", Prompt: "question", StartedAt: 100 + int64(i), Status: "succeeded"}
		if e := d.PutRun(c, r); e != nil {
			t.Fatal(e)
		}
	}
	if e := d.PutConversation(c, domain.Conversation{ID: "c001", ProjectID: "p", DeletedAt: 1}); e != nil {
		t.Fatal(e)
	}
	q := domain.ListDocumentQuery{Resource: "conversations", ProjectID: "p", UserID: "alice", ActorKind: "human", PageQuery: domain.PageQuery{Page: 1, PageSize: 20}}
	v, e := d.QueryDocuments(c, q)
	if e != nil || v.Total != 21 || len(v.Items) != 20 {
		t.Fatal(v, e)
	}
	q.Page = 2
	v, e = d.QueryDocuments(c, q)
	if e != nil || len(v.Items) != 1 {
		t.Fatal(v, e)
	}
	q.Resource = "runs"
	q.ConversationID = "c002"
	q.Page = 1
	v, e = d.QueryDocuments(c, q)
	if e != nil || v.Total != 0 {
		t.Fatal("private run leaked", v, e)
	}
	p := domain.Project{ID: "p", Name: "P", Memberships: []domain.ProjectMember{{UserID: "alice", Roles: []string{"product"}}}}
	if e = d.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	for _, resource := range []string{"projects", "platform-projects", "profiles", "artifacts", "delivery-evidence", "tokens", "intakes", "collaborations", "accounts", "admins", "grants", "events", "version-options"} {
		q = domain.ListDocumentQuery{Resource: resource, ProjectID: "p", UserID: "alice", ActorKind: "human"}
		if _, e = d.QueryDocuments(c, q); e != nil {
			t.Fatal(resource, e)
		}
	}
	members, e := d.QueryMembers(c, "p", domain.PageQuery{})
	if e != nil || members.Total != 1 {
		t.Fatal(members, e)
	}
	if _, e = d.QueryDocuments(c, domain.ListDocumentQuery{Resource: "documents;DROP TABLE tasks"}); e == nil {
		t.Fatal("untrusted resource accepted")
	}
}
func TestTodosBeyondHundredAndPermissionFiltering(t *testing.T) {
	d := paginationDB(t)
	c := context.Background()
	for i := 0; i < 130; i++ {
		project := "p"
		if i < 10 {
			project = "hidden"
		}
		id := fmt.Sprintf("td_%03d", i)
		raw := fmt.Sprintf(`{"id":%q,"task_id":"t","project_id":%q,"open":true}`, id, project)
		if _, e := d.db.Exec(`INSERT INTO notify_todos(id,task_id,open,created_at,payload) VALUES(?,'t',1,?,?)`, id, i, raw); e != nil {
			t.Fatal(e)
		}
		if _, e := d.db.Exec(`INSERT INTO notify_recipients(todo_id,user_id) VALUES(?,'alice')`, id); e != nil {
			t.Fatal(e)
		}
	}
	v, e := d.QueryTodos(c, "alice", true, domain.PageQuery{Page: 6, PageSize: 20}, []string{"p"})
	if e != nil || v.Total != 120 || len(v.Items) != 20 {
		t.Fatal(v, e)
	}
	v, e = d.QueryTodos(c, "alice", true, domain.PageQuery{}, nil)
	if e != nil || v.Total != 0 {
		t.Fatal(v, e)
	}
}
