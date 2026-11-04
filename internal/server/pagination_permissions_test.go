package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestArtifactPaginationKeepsPageGrantsOnEveryPage(t *testing.T) {
	c := context.Background()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	owner := domain.Actor{ID: "owner", Kind: "human"}
	p, e := svc.SaveProjectAs(c, owner, domain.Project{Name: "Page access"})
	if e != nil {
		t.Fatal(e)
	}
	made, e := svc.CreateTask(c, owner, p.ID, app.TaskInput{Title: "Task", RequestID: "create"})
	if e != nil {
		t.Fatal(e)
	}
	p, _ = db.GetProject(c, p.ID)
	for _, entry := range []struct {
		id    string
		pages []string
	}{{"overview", []string{"overview"}}, {"tasks", []string{"tasks"}}, {"reviews", []string{"reviews"}}, {"source-reviewer", []string{"reviews", "code"}}} {
		p.ProjectRoles = append(p.ProjectRoles, domain.ProjectRole{ID: entry.id, Name: entry.id, Pages: entry.pages, Version: 1})
		p.Memberships = append(p.Memberships, domain.ProjectMember{UserID: entry.id, Roles: []string{entry.id}, Responsibilities: []string{}})
	}
	p.AccessVersion++
	if e = db.PutProject(c, p); e != nil {
		t.Fatal(e)
	}
	for _, v := range []domain.Artifact{{ID: "analysis", ProjectID: p.ID, Kind: "analysis", Body: "PUBLIC_ANALYSIS", At: 1}, {ID: "analysis-next", ProjectID: p.ID, Kind: "analysis", Body: "PUBLIC_ANALYSIS_SECOND", At: 5}, {ID: "task-analysis", ProjectID: p.ID, TaskID: made.Task.ID, Kind: "analysis", Body: "PRIVATE_TASK_ANALYSIS", At: 2}, {ID: "delivery", ProjectID: p.ID, TaskID: made.Task.ID, Kind: "deployment", Body: "PRIVATE_DELIVERY", At: 3}, {ID: "git", ProjectID: p.ID, Kind: "git", Body: "PRIVATE_GIT", Data: json.RawMessage(`{"commits":["PRIVATE_GIT_DATA"]}`), At: 4}} {
		if e = db.PutArtifact(c, v); e != nil {
			t.Fatal(e)
		}
	}
	if e = db.AppendEvent(c, domain.Event{ProjectID: p.ID, Kind: "git_sync", Body: "PRIVATE_GIT_EVENT", Data: json.RawMessage(`{"commits":["PRIVATE_GIT_DATA"]}`), At: 5}); e != nil {
		t.Fatal(e)
	}
	h := (&Server{App: svc}).Handler()
	call := func(user, path string, status int) string {
		t.Helper()
		r := httptest.NewRequest("GET", "http://localhost"+path, nil)
		r = r.WithContext(context.WithValue(r.Context(), actorKey{}, domain.Actor{ID: user, Kind: "human"}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", user, path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	base := "/api/projects/" + p.ID
	// Exercise two actual readable pages, not two requests that both normalize
	// to a single last page. Exact IDs detect duplicated/ignored page offsets.
	for i, expected := range []string{"analysis-next", "analysis"} {
		page := strconv.Itoa(i + 1)
		raw := call("overview", base+"/artifacts?page="+page+"&page_size=1", 200)
		var result domain.Page[domain.Artifact]
		if e := json.Unmarshal([]byte(raw), &result); e != nil {
			t.Fatal(e)
		}
		if result.Page != i+1 || result.PageSize != 1 || result.Total != 2 || len(result.Items) != 1 || result.Items[0].ID != expected || strings.Contains(raw, "PRIVATE_") {
			t.Fatal("overview pagination duplicated rows or crossed evidence scope", raw)
		}
	}
	// The shared pager intentionally returns the last valid page when an item
	// was deleted or a filter shrank the list. It must report that actual page.
	raw := call("overview", base+"/artifacts?page=3&page_size=1", 200)
	var normalized domain.Page[domain.Artifact]
	if e := json.Unmarshal([]byte(raw), &normalized); e != nil {
		t.Fatal(e)
	}
	if normalized.Page != 2 || normalized.PageSize != 1 || normalized.Total != 2 || len(normalized.Items) != 1 || normalized.Items[0].ID != "analysis" || strings.Contains(raw, "PRIVATE_") {
		t.Fatal("out-of-range response misreported its page or crossed scope", raw)
	}
	call("overview", base+"/artifacts?delivery=true&page=1", 403)
	taskPage := call("tasks", base+"/artifacts?task_id="+made.Task.ID+"&page=1", 200)
	if strings.Contains(taskPage, "PRIVATE_") || !strings.Contains(taskPage, `"total":0`) {
		t.Fatal("task evidence pagination bypassed compact restriction", taskPage)
	}
	compact := call("tasks", "/api/tasks/"+made.Task.ID+"?compact=true", 200)
	if strings.Contains(compact, "PRIVATE_") {
		t.Fatal("compact detail leaked evidence", compact)
	}
	review := call("reviews", base+"/artifacts?page=1", 200)
	if !strings.Contains(review, "PRIVATE_DELIVERY") || strings.Contains(review, "PRIVATE_GIT") || !strings.Contains(review, `"total":4`) {
		t.Fatal("review page Git filtering failed", review)
	}
	source := call("source-reviewer", base+"/artifacts?page=1", 200)
	if !strings.Contains(source, "PRIVATE_GIT") || !strings.Contains(source, `"total":5`) {
		t.Fatal(source)
	}
	for _, path := range []string{base + "/context", base + "/events?page=1"} {
		raw := call("overview", path, 200)
		if strings.Contains(raw, "PRIVATE_GIT") {
			t.Fatal("event/source aggregate bypass", raw)
		}
	}
}
