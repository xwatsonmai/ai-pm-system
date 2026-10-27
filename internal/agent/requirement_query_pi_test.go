package agent_test

import (
	"ai-pm-system/internal/agent"
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/gitrepo"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPiQuestionQueriesPoolAndSupplementOnDemand(t *testing.T) {
	worker, _ := filepath.Abs("../../runtime/worker.mjs")
	if _, err := os.Stat(filepath.Join(filepath.Dir(worker), "node_modules/@earendil-works/pi-coding-agent")); err != nil {
		t.Skip("install Pi runtime dependencies")
	}
	var toolResults atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(delta any, finish any) {
			b, _ := json.Marshal(map[string]any{"id": "query-fixture", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		done := func(text string) {
			send(map[string]string{"content": text}, nil)
			send(map[string]any{}, "stop")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
		catalog, _ := json.Marshal(body["tools"])
		if !strings.Contains(string(catalog), "requirement_pool") {
			done("需求查询")
			return
		}
		messages := body["messages"].([]any)
		latestUser := 0
		var prompt string
		for i, msg := range messages {
			m := msg.(map[string]any)
			if m["role"] == "user" {
				latestUser = i
				b, _ := json.Marshal(m["content"])
				prompt = string(b)
			}
		}
		if strings.Contains(prompt, "UNRELATED_DOCUMENT_MUST_NOT_BE_INJECTED") || strings.Contains(prompt, "TARGET_DOCUMENT_ONLY_ON_DEMAND") {
			t.Error("full document injected into default question context")
		}
		var results []string
		for _, msg := range messages[latestUser+1:] {
			m := msg.(map[string]any)
			if m["role"] == "tool" {
				text, ok := m["content"].(string)
				if !ok {
					b, _ := json.Marshal(m["content"])
					text = string(b)
				}
				results = append(results, text)
			}
		}
		name, args := "requirement_pool", `{"kind":"requirement","limit":1}`
		if len(results) == 1 {
			var pool app.RequirementPoolResult
			if err := json.Unmarshal([]byte(results[0]), &pool); err != nil || pool.Counts.Total != 2 || pool.Matched != 1 || len(pool.Items) != 1 {
				t.Errorf("pool tool result: %s %v", results[0], err)
				done("查询失败")
				return
			}
			toolResults.Add(1)
			name, args = "requirement_detail", `{"task_id":"`+pool.Items[0].ID+`","review_limit":1}`
		} else if len(results) >= 2 {
			var detail app.RequirementDetailResult
			if err := json.Unmarshal([]byte(results[1]), &detail); err != nil {
				t.Error(err)
				done("查询失败")
				return
			}
			toolResults.Add(1)
			if strings.Contains(prompt, "我已经补充") {
				if detail.Review.State != "reviewing" || detail.Review.LatestSupplement == nil || detail.Review.LatestSupplement.Body != "保留90天" || len(detail.Review.PendingQuestions) != 0 {
					t.Errorf("stale supplement detail: %+v", detail)
				}
				done("已收到保留90天的补充，正在评审，尚未进入开发。")
			} else {
				if detail.Review.State != "awaiting_input" || len(detail.Review.PendingQuestions) != 1 {
					t.Errorf("missing current questions: %+v", detail)
				}
				done("共2条事项，1条迭代需求仍需确认保存天数。")
			}
			return
		}
		send(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("query-%d", len(messages)), "type": "function", "function": map[string]string{"name": name, "arguments": args}}}}, nil)
		send(map[string]any{}, "tool_calls")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	c := context.Background()
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "pm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &app.Service{Store: db}
	profile, err := s.SaveProfile(c, domain.Profile{Name: "Fixture", Provider: "fixture", Model: "fixture-model", BaseURL: model.URL + "/v1", API: "openai-completions"}, "fixture-key")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.PutPlatformSettings(c, domain.PlatformSettings{ProfileID: profile.ID}); err != nil {
		t.Fatal(err)
	}
	p, err := s.SaveProject(c, domain.Project{Name: "query"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "图片保留", Description: "TARGET_DOCUMENT_ONLY_ON_DEMAND", RequestID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "另一个Bug", Kind: "bug", Description: "UNRELATED_DOCUMENT_MUST_NOT_BE_INJECTED", RequestID: "unrelated"}); err != nil {
		t.Fatal(err)
	}
	v, err := s.SaveRequirementReview(c, app.Human(), target.Task.ID, app.RequirementReviewInput{Action: "start", RequestID: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishRequirementReview(c, target.Task.ID, v.Generation, "", &domain.RequirementReviewResult{Body: "需补充时间", Decision: "clarify", Questions: []domain.RequirementQuestion{{ID: "days", Label: "保留多少天", Type: "text"}}}, ""); err != nil {
		t.Fatal(err)
	}
	m := agent.New(c, s, &gitrepo.Repositories{Root: filepath.Join(dir, "repos"), App: s}, worker, dir)
	defer m.Close()
	wait := func(id string) domain.Run {
		t.Helper()
		for end := time.Now().Add(15 * time.Second); time.Now().Before(end); {
			r, e := db.GetRun(c, id)
			if e != nil {
				t.Fatal(e)
			}
			if r.Status != "running" && r.Status != "queued" {
				if r.Status != "succeeded" {
					t.Fatalf("query failed: %+v", r)
				}
				return r
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("query timed out")
		return domain.Run{}
	}
	r, err := m.StartAs(c, app.Human(), p.ID, "", "question", "有多少需求，还有哪些要补充？", "")
	if err != nil {
		t.Fatal(err)
	}
	first := wait(r.ID)
	if len(first.Steps) != 2 || !strings.Contains(first.Output, "保存天数") {
		t.Fatal(first)
	}
	if _, err = s.SaveRequirementReview(c, app.Human(), target.Task.ID, app.RequirementReviewInput{Action: "message", Body: "保留90天", RequestID: "answer"}); err != nil {
		t.Fatal(err)
	}
	r, err = m.StartAs(c, app.Human(), p.ID, "", "question", "我已经补充了，为什么还要补充？", r.ID)
	if err != nil {
		t.Fatal(err)
	}
	second := wait(r.ID)
	if len(second.Steps) != 2 || !strings.Contains(second.Output, "已收到") || toolResults.Load() != 4 {
		t.Fatal(second, toolResults.Load())
	}
}
