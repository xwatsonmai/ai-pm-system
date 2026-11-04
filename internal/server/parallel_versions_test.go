package server_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/server"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
)

func TestParallelVersionsPagedHTTPCompatibility(t *testing.T) {
	c := context.Background()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "page.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	service := &app.Service{Store: db}
	project, e := service.SaveProject(c, domain.Project{Name: "paged"})
	if e != nil {
		t.Fatal(e)
	}
	versions := []domain.ProjectVersion{}
	for i := 0; i < 3; i++ {
		v, e := service.SaveProjectVersion(c, app.Human(), project.ID, "", app.ProjectVersionInput{Name: fmt.Sprintf("版本%d", i), Goal: "不可从轻选项搜索的目标", RequestID: fmt.Sprint(i)})
		if e != nil {
			t.Fatal(e)
		}
		versions = append(versions, *v.ProjectVersion)
	}
	for i := 0; i < 3; i++ {
		_, e = service.CreateTask(c, app.Human(), project.ID, app.TaskInput{Title: fmt.Sprint("需求", i), Description: "不可全量进入列表的原文", ProjectVersionID: versions[1].ID, RequestID: fmt.Sprint("task", i)})
		if e != nil {
			t.Fatal(e)
		}
	}
	site := httptest.NewServer((&server.Server{App: service}).Handler())
	defer site.Close()
	get := func(path string, v any) {
		t.Helper()
		response, e := http.Get(site.URL + "/api" + path)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal(response.Status)
		}
		if e = json.NewDecoder(response.Body).Decode(v); e != nil {
			t.Fatal(e)
		}
	}
	var legacy []app.ProjectVersionSummary
	get("/projects/"+project.ID+"/versions", &legacy)
	if len(legacy) != 3 {
		t.Fatal(legacy)
	}
	var page domain.Page[app.ProjectVersionSummary]
	get("/projects/"+project.ID+"/versions?page=2&page_size=1&status=active", &page)
	if page.Total != 3 || len(page.Items) != 1 || page.Items[0].ID != versions[1].ID || page.Items[0].Counts.Included != 3 {
		t.Fatal(page)
	}
	var detail app.ProjectVersionDetail
	get("/project-versions/"+versions[1].ID+"?summary=1", &detail)
	if detail.ProjectVersion.Scope != nil || detail.ProjectVersion.Releases != nil || detail.Summary.Counts.Included != 3 {
		t.Fatal("summary included documents or wrong counts", detail)
	}
	var scope domain.Page[domain.VersionScopeItem]
	get("/project-versions/"+versions[1].ID+"/scope?page=2&page_size=2", &scope)
	if scope.Total != 3 || len(scope.Items) != 1 || scope.Items[0].Task.Description == "" {
		t.Fatal(scope)
	}
	var history domain.Page[domain.VersionHistoryEntry]
	get("/project-versions/"+versions[1].ID+"/history?page=1&page_size=2", &history)
	if history.Total != 4 || len(history.Items) != 2 {
		t.Fatal(history)
	}
	var releases domain.Page[domain.VersionRelease]
	get("/project-versions/"+versions[1].ID+"/releases?page=1&page_size=2", &releases)
	if releases.Total != 0 || releases.Items == nil {
		t.Fatal(releases)
	}
	var choices domain.Page[app.VersionChoice]
	get("/projects/"+project.ID+"/version-options?page=1&q="+url.QueryEscape("不可从轻选项搜索的目标"), &choices)
	if choices.Total != 0 {
		t.Fatal("light options leaked private goals through search", choices)
	}
	get("/projects/"+project.ID+"/version-options?page=1&page_size=1", &choices)
	if choices.Total != 3 || len(choices.Items) != 1 {
		t.Fatal(choices)
	}
	response, e := http.Get(site.URL + "/api/projects/" + project.ID + "/versions?page=0")
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode < 400 {
		t.Fatal("invalid page accepted")
	}
}
