package sqlite

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/notify"
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
	p := domain.NormalizeProjectAccess(domain.Project{ID: "p", Name: "P", Memberships: []domain.ProjectMember{{UserID: "alice", Roles: []string{"product"}}}})
	if e := d.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
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
	// Old recipient rows must not expose counts or items after page revocation.
	p.ProjectRoles[0].Pages = []string{}
	if e := d.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	v, e = d.QueryTodos(c, "alice", true, domain.PageQuery{}, []string{"p"})
	if e != nil || v.Total != 0 || len(v.Items) != 0 {
		t.Fatal(v, e)
	}
}

func TestPagedGrantWhitelistAndNestedTaskProjection(t *testing.T) {
	d := paginationDB(t)
	c := context.Background()
	if e := d.PutMCPAccount(c, domain.MCPAccount{UserID: "alice", Enabled: true}); e != nil {
		t.Fatal(e)
	}
	account, _ := d.GetMCPAccount(c, "alice")
	grant := domain.OAuthGrant{ID: "grant", UserID: "alice", ClientID: "client", ProjectID: "p", AccessHash: "PRIVATE_ACCESS_HASH", RefreshHash: "PRIVATE_REFRESH_HASH", PreviousRefreshHash: "PRIVATE_PREVIOUS_HASH", PreviousRefreshUntil: 1, Resource: "PRIVATE_RESOURCE", UserName: "PRIVATE_INTERNAL_NAME", AccountGeneration: account.Generation, AccessExpiresAt: 100, CreatedAt: 1}
	if e := d.PutOAuthGrant(c, grant, ""); e != nil {
		t.Fatal(e)
	}
	page, e := d.QueryDocuments(c, domain.ListDocumentQuery{Resource: "grants", UserID: "alice"})
	if e != nil || page.Total != 1 {
		t.Fatal(page, e)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "PRIVATE_") || strings.Contains(string(raw), "Hash") || strings.Contains(string(raw), "AccountGeneration") {
		t.Fatal("paged grants leaked private storage fields", string(raw))
	}
	source := domain.Task{ID: "source", Description: strings.Repeat("HUGE_SOURCE_SENTINEL", 1000), Acceptance: []string{"HUGE_BASELINE_SENTINEL"}, Attachments: []domain.Attachment{{Name: "SNAPSHOT_ATTACHMENT_SENTINEL"}}}
	task := domain.Task{ID: "iteration", ProjectID: "p", Title: "Iteration", Kind: "requirement", Status: "ready", Revision: 1, Version: 1, Iteration: &domain.RequirementIteration{ParentTaskID: "source", RootTaskID: "root", Source: source, Baseline: &source}, Alignment: &domain.DevelopmentAlignment{Status: "replied"}}
	payload, _ := json.Marshal(task)
	if _, e = d.db.Exec(`INSERT INTO tasks(id,project_id,version,claim_hash,payload) VALUES(?,?,1,'',?)`, task.ID, task.ProjectID, string(payload)); e != nil {
		t.Fatal(e)
	}
	tasks, e := d.QueryTasks(c, "p", domain.TaskListQuery{})
	if e != nil || tasks.Total != 1 {
		t.Fatal(tasks, e)
	}
	raw, _ = json.Marshal(tasks)
	for _, marker := range []string{"HUGE_SOURCE_SENTINEL", "HUGE_BASELINE_SENTINEL", "SNAPSHOT_ATTACHMENT_SENTINEL"} {
		if strings.Contains(string(raw), marker) {
			t.Fatal("list returned frozen nested documents", marker)
		}
	}
	if tasks.Items[0].Iteration.ParentTaskID != "source" || tasks.Items[0].Iteration.RootTaskID != "root" || tasks.Items[0].Alignment.Status != "replied" {
		t.Fatal("light projection lost workflow/lineage", tasks)
	}
	full, e := d.GetTask(c, "iteration")
	if e != nil || full.Iteration.Source.Description != source.Description {
		t.Fatal("projection changed stored detail", full, e)
	}
}

func TestPagedTodosMatchLinkedBugPagePolicyBeforeCount(t *testing.T) {
	d := paginationDB(t)
	c := context.Background()
	p := domain.Project{ID: "p", AccessVersion: 1, ProjectRoles: []domain.ProjectRole{{ID: "reviews", Name: "Review", Pages: []string{"reviews"}, Version: 1}}, Memberships: []domain.ProjectMember{{UserID: "alice", Roles: []string{"reviews"}, Responsibilities: []string{"product"}}}}
	if e := d.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	for _, entry := range []struct{ id, parent string }{{"standalone", ""}, {"linked", "parent"}} {
		task := domain.Task{ID: entry.id, ProjectID: p.ID, Kind: "bug", ParentRequirementID: entry.parent, Status: "clarify", Version: 1, Revision: 1}
		payload, _ := json.Marshal(task)
		if _, e := d.db.Exec(`INSERT INTO tasks(id,project_id,version,claim_hash,payload) VALUES(?,?,1,'',?)`, task.ID, task.ProjectID, string(payload)); e != nil {
			t.Fatal(e)
		}
		todo := notify.Todo{ID: "todo-" + entry.id, Base: "todo-" + entry.id, TaskID: entry.id, ProjectID: p.ID, Kind: "bug_rejection", Subject: entry.id, Recipients: []string{"alice"}, Open: true, CreatedAt: 1}
		if e := d.SaveTodo(c, todo, nil); e != nil {
			t.Fatal(e)
		}
	}
	out, e := d.QueryTodos(c, "alice", true, domain.PageQuery{PageSize: 1}, []string{p.ID})
	if e != nil || out.Total != 1 || len(out.Items) != 1 || strings.Contains(string(out.Items[0]), "standalone") {
		t.Fatal("stale standalone Bug recipient bypassed page before count", out, e)
	}
}

func TestCollaborationPaginationDoesNotReviveDeletedConversations(t *testing.T) {
	d := paginationDB(t)
	c := context.Background()
	for _, id := range []string{"visible", "deleted"} {
		run := domain.Run{ID: "run-" + id, ProjectID: "p", ConversationID: "conversation-" + id, Kind: "question", OwnerID: "alice", OwnerKind: "human", Status: "succeeded"}
		if e := d.PutRun(c, run); e != nil {
			t.Fatal(e)
		}
		collaboration := domain.Collaboration{ID: "collaboration-" + id, ProjectID: "p", OriginRunID: run.ID, ConversationID: run.ConversationID, OwnerID: "alice", OwnerKind: "human", Title: id, Version: 1, Status: "pending"}
		raw, _ := json.Marshal(collaboration)
		if _, e := d.db.Exec(`INSERT INTO collaborations(id,project_id,version,claim_hash,payload) VALUES(?,?,1,'',?)`, collaboration.ID, "p", string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	if e := d.PutConversation(c, domain.Conversation{ID: "conversation-deleted", ProjectID: "p", DeletedAt: 1}); e != nil {
		t.Fatal(e)
	}
	out, e := d.QueryDocuments(c, domain.ListDocumentQuery{Resource: "collaborations", ProjectID: "p", UserID: "alice", ActorKind: "human", PageQuery: domain.PageQuery{PageSize: 1}})
	if e != nil || out.Total != 1 || len(out.Items) != 1 || strings.Contains(string(out.Items[0]), "deleted") {
		t.Fatal("deleted conversation's collaboration leaked before count", out, e)
	}
}
