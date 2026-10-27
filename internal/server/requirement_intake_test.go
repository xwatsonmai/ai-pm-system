package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntakeHTTPBugSkipsReviewOnCreateAndRetry(t *testing.T) {
	c := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "pm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &app.Service{Store: db}
	p, err := s.SaveProject(c, domain.Project{Name: "Bug project"})
	if err != nil {
		t.Fatal(err)
	}
	r := domain.Run{ID: "question", ProjectID: p.ID, ConversationID: "conv", OwnerID: app.Human().ID, OwnerKind: "human", Kind: "question", Prompt: "创建 Bug", StartedAt: domain.Now()}
	if err := db.PutRun(c, r); err != nil {
		t.Fatal(err)
	}
	v, err := s.ProposeRequirement(c, r.ID, app.RequirementIntakeInput{Title: "重复扣款", Kind: "bug", BugDetails: &domain.BugDetails{Context: "支付订单页", Reproduction: "同一订单发起一次支付", Actual: "扣款两次", Expected: "只扣一次", Scope: "修复重复扣款"}, Description: "一次支付扣款两次", Acceptance: []string{"只扣一次"}, Reason: "实际行为偏离预期"})
	if err != nil {
		t.Fatal(err)
	}
	// With no review service configured, Bug creation must still be claimable
	// and must not save a failed review through the HTTP fallback.
	handler := (&Server{App: s}).Handler()
	resolve := func() domain.RequirementIntake {
		t.Helper()
		req := httptest.NewRequest("POST", "http://localhost/api/requirement-intakes/"+v.ID+"/resolve", strings.NewReader(`{"action":"create","request_id":"confirm"}`))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), actorKey{}, app.Human()))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		var out domain.RequirementIntake
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("resolve failed: %d %s", w.Code, w.Body.String())
		}
		return out
	}
	out := resolve()
	task, err := db.GetTask(c, out.TaskID)
	if err != nil || task.Status != "ready" {
		t.Fatal("Bug not ready", task, err)
	}
	claimed, err := s.Act(c, domain.Actor{ID: "dev", Kind: "developer", ProjectID: p.ID}, task.ID, app.Action{Action: "claim", RequestID: "claim"})
	if err != nil || claimed.Task.Status != "doing" {
		t.Fatal("Bug not claimable", claimed, err)
	}
	if again := resolve(); again.TaskID != out.TaskID {
		t.Fatal("retry duplicated Bug", again)
	}
	if _, err := db.GetRequirementReview(c, task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("Bug created an independent review", err)
	}
	current, err := db.GetTask(c, task.ID)
	if err != nil || current.Status != "doing" || current.Claimant != "dev" {
		t.Fatal("retry reset developer work", current, err)
	}
}

func TestIntakeHTTPDurabilityAndPrivacy(t *testing.T) {
	c := context.Background()
	path := filepath.Join(t.TempDir(), "pm.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &app.Service{Store: db}
	p := domain.Project{ID: "p", Name: "Project", OwnerID: "alice", AccessMembers: []string{"bob"}}
	if err = db.PutProject(c, p); err != nil {
		t.Fatal(err)
	}
	r := domain.Run{ID: "question", ProjectID: "p", ConversationID: "conv", OwnerID: "alice", OwnerKind: "human", Kind: "question", Prompt: "原始长文", StartedAt: domain.Now()}
	db.PutRun(c, r)
	v, err := s.ProposeRequirement(c, r.ID, app.RequirementIntakeInput{Title: "对话需求", Kind: "requirement", Description: strings.Repeat("原始长文。", 1000), Reason: "明确目标"})
	if err != nil {
		t.Fatal(err)
	}
	handler := (&Server{App: s}).Handler()
	call := func(who, method, path, body string, want int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), actorKey{}, domain.Actor{ID: who, Kind: "human", Name: who}))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%d: %s", w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	if body := call("bob", "GET", "/api/projects/p/requirement-intakes", "", 200); strings.TrimSpace(string(body)) != "[]" {
		t.Fatal("private suggestion leaked", string(body))
	}
	endpoint := "/api/requirement-intakes/" + v.ID + "/resolve"
	call("bob", "POST", endpoint, `{"action":"create","request_id":"confirm"}`, 403)
	var out domain.RequirementIntake
	json.Unmarshal(call("alice", "POST", endpoint, `{"action":"create","request_id":"confirm"}`, 200), &out)
	review, err := s.RequirementReview(c, domain.Actor{ID: "alice", Kind: "human"}, out.TaskID)
	if err != nil || review.Status != "failed" || review.Source.Description != v.Description {
		t.Fatal("review failure discarded saved content", review, err)
	}
	// Simulate restart / lost HTTP response; the same resolve request returns the
	// same task and preserves the failed, retryable review.
	db.Close()
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = &app.Service{Store: db}
	handler = (&Server{App: s}).Handler()
	var again domain.RequirementIntake
	json.Unmarshal(call("alice", "POST", endpoint, `{"action":"create","request_id":"confirm"}`, 200), &again)
	if again.TaskID != out.TaskID {
		t.Fatal("restart duplicated creation")
	}
	tasks, _ := db.ListTasks(c, "p")
	if len(tasks) != 1 || tasks[0].Description != v.Description {
		t.Fatal("restart lost task")
	}
	r2, _ := db.GetRun(c, r.ID)
	if r2.Prompt != "原始长文" {
		t.Fatal("original conversation lost")
	}
}
