package server

import (
	"ai-pm-system/internal/agent"
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConversationHistoryOwnershipAndLegacyAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	project := domain.Project{ID: "p", Name: "Project", OwnerID: "alice", AccessMembers: []string{"bob"}, ProfileID: "model"}
	if err = db.PutProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err = db.PutProfile(ctx, domain.Profile{ID: "model", BaseURL: "http://127.0.0.1:1", Model: "test"}); err != nil {
		t.Fatal(err)
	}
	if err = db.PutPlatformSettings(ctx, domain.PlatformSettings{ProfileID: "model"}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []domain.Run{
		{ID: "alice-chat", Kind: "question", OwnerID: "alice", OwnerKind: "human"},
		{ID: "bob-chat", Kind: "question", OwnerID: "bob", OwnerKind: "human"},
		{ID: "legacy", Kind: "question"},
		{ID: "analysis", Kind: "progress"},
		{ID: "dev-chat", Kind: "question", OwnerID: "dev", OwnerKind: "developer"},
	} {
		r.ProjectID, r.ConversationID, r.Status = project.ID, "conv-"+r.ID, "succeeded"
		if err = db.PutRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	svc := &app.Service{Store: db}
	worker := filepath.Join(dir, "worker.mjs")
	if err = os.WriteFile(worker, []byte("throw new Error('should not run')"), 0600); err != nil {
		t.Fatal(err)
	}
	m := agent.New(ctx, svc, nil, worker, dir)
	defer m.Close()
	s := &Server{App: svc, Agent: m}
	handler := s.Handler()
	call := func(who, method, path, body string, expected int) string {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), actorKey{}, domain.Actor{ID: who, Kind: "human"}))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != expected {
			t.Fatalf("%s %s as %s: %d %s", method, path, who, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	for who, want := range map[string][]string{"alice": {"alice-chat", "legacy", "analysis"}, "bob": {"bob-chat", "analysis"}} {
		var runs []domain.Run
		if err = json.Unmarshal([]byte(call(who, "GET", "/api/projects/p/runs", "", 200)), &runs); err != nil {
			t.Fatal(err)
		}
		if len(runs) != len(want) {
			t.Fatalf("history leak for %s: %+v", who, runs)
		}
		for _, id := range want {
			found := false
			for _, run := range runs {
				found = found || run.ID == id
			}
			if !found {
				t.Fatal("missing history", who, id)
			}
		}
	}
	call("alice", "GET", "/api/runs/bob-chat", "", 403)
	call("alice", "GET", "/api/runs/bob-chat/events", "", 403)
	call("bob", "GET", "/api/runs/legacy", "", 403)
	call("bob", "POST", "/api/runs/alice-chat/stop", "{}", 403)
	call("bob", "POST", "/api/projects/p/runs", `{"kind":"question","prompt":"steal context","parent_id":"alice-chat"}`, 403)
	call("alice", "POST", "/api/projects/p/runs", `{"kind":"question","prompt":"wrong kind","parent_id":"analysis"}`, 403)
	call("alice", "POST", "/api/projects/p/runs", `{"kind":"question","prompt":""}`, 400)
	runs, _ := db.ListRuns(ctx, "p")
	if len(runs) != 5 {
		t.Fatal("rejected or empty message created conversation")
	}
	dev := domain.Actor{ID: "dev", Kind: "developer", ProjectID: "p"}
	if _, err = s.developerCall(ctx, dev, "analysis_get", map[string]string{"run_id": "alice-chat"}); err == nil {
		t.Fatal("MCP exposed personal question")
	}
	if _, err = s.developerCall(ctx, dev, "analysis_get", map[string]string{"run_id": "dev-chat"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.developerCall(ctx, dev, "analysis_get", map[string]string{"run_id": "analysis"}); err != nil {
		t.Fatal(err)
	}
	call("bob", "PATCH", "/api/projects/p/conversations/conv-alice-chat", `{"title":"stolen"}`, 403)
	call("bob", "DELETE", "/api/projects/p/conversations/conv-alice-chat", "", 403)
	call("bob", "PATCH", "/api/projects/p/conversations/conv-bob-chat", `{"title":"成员自己的会话"}`, 200)
	call("alice", "PATCH", "/api/projects/p/conversations/conv-alice-chat", `{"title":"   "}`, 400)
	call("alice", "PATCH", "/api/projects/p/conversations/conv-alice-chat", `{"title":"`+strings.Repeat("长", 81)+`"}`, 400)
	call("alice", "PATCH", "/api/projects/p/conversations/conv-alice-chat", `{"title":" 手动名称 "}`, 200)
	var renamed domain.Run
	json.Unmarshal([]byte(call("alice", "GET", "/api/runs/alice-chat", "", 200)), &renamed)
	if renamed.ConversationTitle != "手动名称" || renamed.TitleStatus != "manual" {
		t.Fatal("manual title not returned", renamed)
	}
	// Simulate a late AI title/save and another turn; neither may undo the edit.
	renamed.ConversationTitle, renamed.TitleStatus = "迟到的自动标题", "ready"
	if err = db.PutRun(ctx, renamed); err != nil {
		t.Fatal(err)
	}
	renamed.ID = "alice-followup"
	if err = db.PutRun(ctx, renamed); err != nil {
		t.Fatal(err)
	}
	if late, _ := db.GetRun(ctx, renamed.ID); late.ConversationTitle != "手动名称" {
		t.Fatal("AI overwrote manual title")
	}
	call("alice", "DELETE", "/api/projects/p/conversations/conv-alice-chat", "", 200)
	call("alice", "GET", "/api/runs/alice-chat", "", 404)
	call("alice", "GET", "/api/runs/alice-chat/events", "", 404)
	call("alice", "GET", "/api/runs/alice-followup", "", 404)
	call("alice", "POST", "/api/projects/p/runs", `{"kind":"question","prompt":"continue deleted","parent_id":"alice-chat"}`, 404)
	call("alice", "PATCH", "/api/projects/p/conversations/conv-alice-chat", `{"title":"resurrect"}`, 404)
	if err = db.PutRun(ctx, renamed); err != nil {
		t.Fatal(err)
	}
	remaining := call("alice", "GET", "/api/projects/p/runs", "", 200)
	if strings.Contains(remaining, "alice-chat") || strings.Contains(remaining, "alice-followup") {
		t.Fatal("deleted conversation resurrected")
	}
	call("alice", "PATCH", "/api/projects/p/conversations/conv-legacy", `{"title":"保留旧对话"}`, 200)
	call("alice", "DELETE", "/api/projects/p/conversations/conv-legacy", "", 200)
	call("alice", "GET", "/api/runs/legacy", "", 404)
	call("alice", "DELETE", "/api/projects/p/conversations/conv-analysis", "", 404)
	call("alice", "GET", "/api/runs/analysis", "", 200)
}
