package server_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/server"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewHTTPPersistsWithoutAgent(t *testing.T) {
	c := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "pm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := &app.Service{Store: db}
	p, _ := service.SaveProject(c, domain.Project{Name: "Review"})
	handler := (&server.Server{App: service}).Handler()
	request := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "http://localhost"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		return w
	}
	w := request("/api/projects/"+p.ID+"/tasks", `{"title":"先保存","description":"原始长文","request_id":"create"}`)
	var created domain.Mutation
	if err = json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/tasks/" + created.Task.ID + "/requirement-review"
	request(endpoint, `{"action":"start","request_id":"start"}`)
	for i := 0; i < 2; i++ {
		w = request(endpoint, `{"action":"message","body":"自由输入纠正，不填写任何表单","request_id":"correction"}`)
	}
	var review domain.RequirementReview
	if err = json.Unmarshal(w.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	// Each unavailable-Agent attempt leaves a failure event after the saved input.
	if review.Status != "failed" || len(review.Messages) != 4 || review.Messages[2].Body != "自由输入纠正，不填写任何表单" || review.Messages[3].Action != "review_failed" {
		t.Fatal("input was rejected by unavailable Agent", review)
	}
	task, _ := db.GetTask(c, created.Task.ID)
	if task.Description != "原始长文" {
		t.Fatal("review replaced original input")
	}
}
