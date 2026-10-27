package agent_test

import (
	"ai-pm-system/internal/agent"
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/gitrepo"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPiQuestionSuggestConfirmAndDirectCreation(t *testing.T) {
	worker, _ := filepath.Abs("../../runtime/worker.mjs")
	if _, err := os.Stat(filepath.Join(filepath.Dir(worker), "node_modules/@earendil-works/pi-coding-agent")); err != nil {
		t.Skip("install Pi runtime dependencies")
	}
	var sawFiles, sawProactivePolicy atomic.Bool
	var creationReceipts atomic.Int32
	idPattern := regexp.MustCompile("intake_[0-9a-f]{24}")
	var fileID string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		all, _ := json.Marshal(body)
		text := string(all)
		if strings.Contains(text, "当前会话附件唯一标记") {
			sawFiles.Store(true)
		}
		if strings.Contains(text, "你应主动识别") && strings.Contains(text, "协同任务仅用于让开发方只读排查") {
			sawProactivePolicy.Store(true)
		}
		toolsRaw, _ := json.Marshal(body["tools"])
		if strings.Contains(string(toolsRaw), "submit_requirement_review") {
			http.Error(w, "fixture review unavailable", 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(delta any, finish any) {
			b, _ := json.Marshal(map[string]any{"id": "fixture", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		finish := func(text string) { send(map[string]string{"content": text}, nil); send(map[string]any{}, "stop") }
		if !strings.Contains(string(toolsRaw), "propose_requirement") {
			finish("问答创建")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		messages := body["messages"].([]any)
		userIndex := 0
		userText := ""
		for i, msg := range messages {
			m := msg.(map[string]any)
			if m["role"] == "user" {
				userIndex = i
				b, _ := json.Marshal(m["content"])
				userText = string(b)
			}
		}
		var results []string
		for _, msg := range messages[userIndex+1:] {
			m := msg.(map[string]any)
			if m["role"] == "tool" {
				b, _ := json.Marshal(m["content"])
				value, ok := m["content"].(string)
				if !ok {
					value = string(b)
				}
				results = append(results, value)
			}
		}
		direct := strings.Contains(userText, "DIRECT_CREATE")
		confirmed := strings.Contains(userText, "用户输入：创建吧")
		name := ""
		var args any
		if len(results) > 0 && strings.Contains(results[len(results)-1], "\"created\"") {
			var receipt struct {
				domain.RequirementIntake
				Receipt struct {
					TaskID     string `json:"task_id"`
					TaskStatus string `json:"task_status"`
					NextActor  string `json:"next_actor"`
					NextAction string `json:"next_action"`
				} `json:"creation_receipt"`
			}
			if err := json.Unmarshal([]byte(results[len(results)-1]), &receipt); err != nil {
				t.Error(err)
			}
			if receipt.Receipt.TaskID != receipt.TaskID || receipt.Receipt.NextAction == "" {
				t.Error("creation omitted actual status and next action", receipt)
			}
			if receipt.Kind == "bug" && (receipt.Receipt.TaskStatus != "ready" || receipt.Receipt.NextActor != "developer") {
				t.Error("Bug receipt not ready", receipt)
			}
			if receipt.Kind == "requirement" && (receipt.Receipt.TaskStatus != "clarify" || len(receipt.MissingDetails) != 1) {
				t.Error("requirement receipt omitted pending details", receipt)
			}
			creationReceipts.Add(1)
			finish("需求已保存到需求池；" + receipt.Receipt.NextAction + "。")
		} else if (direct && len(results) > 0) || (confirmed && len(results) == 0) {
			source := userText
			if len(results) > 0 {
				source = results[len(results)-1]
			}
			confirmation := "创建吧"
			if direct {
				confirmation = "请创建需求 DIRECT_CREATE"
			}
			name = "resolve_requirement_proposal"
			args = map[string]string{"proposal_id": idPattern.FindString(source), "action": "create", "confirmation": confirmation}
		} else if len(results) == 0 {
			kind := "bug"
			title := "退款重复扣款"
			files := []string{fileID}
			if direct {
				kind = "requirement"
				title = "退款进度展示"
				files = []string{}
			}
			name = "propose_requirement"
			var details *domain.BugDetails
			if kind == "bug" {
				details = &domain.BugDetails{Context: "支付订单页", Reproduction: "同一订单发起一次支付", Actual: "扣款两次", Expected: "只扣一次", Scope: "修复重复扣款"}
			}
			missing := []string{}
			if kind == "requirement" {
				missing = append(missing, "到账时间展示规则")
			}
			args = app.RequirementIntakeInput{Title: title, Kind: kind, BugDetails: details, MissingDetails: missing, Description: "## 完整描述\n用户反馈、预期与待确认范围。根因尚未验证。", Acceptance: []string{"能够核对退款结果"}, AttachmentIDs: files, Reason: "需要在需求池持续跟进"}
		} else {
			finish("这是一个疑似 Bug，是否创建为 Bug 修复？")
		}
		if name != "" {
			b, _ := json.Marshal(args)
			send(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("call-%d", len(messages)), "type": "function", "function": map[string]string{"name": name, "arguments": string(b)}}}}, nil)
			send(map[string]any{}, "tool_calls")
		}
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
	db.PutPlatformSettings(c, domain.PlatformSettings{ProfileID: profile.ID})
	p, _ := s.SaveProject(c, domain.Project{Name: "Intake"})
	f, err := s.SaveAttachment(c, app.Human(), p.ID, "rules.md", []byte("当前会话附件唯一标记"))
	if err != nil {
		t.Fatal(err)
	}
	fileID = f.ID
	m := agent.New(c, s, &gitrepo.Repositories{Root: filepath.Join(dir, "repos"), App: s}, worker, dir)
	defer m.Close()
	wait := func(id string) domain.Run {
		t.Helper()
		for end := time.Now().Add(15 * time.Second); time.Now().Before(end); {
			r, _ := db.GetRun(c, id)
			if r.Status != "queued" && r.Status != "running" {
				if r.Status != "succeeded" {
					t.Fatalf("question failed: %+v", r)
				}
				return r
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("question timeout")
		return domain.Run{}
	}
	r, err := m.StartAs(c, app.Human(), p.ID, "", "question", "退款后出现重复扣款，这是怎么回事？", "", fileID)
	if err != nil {
		t.Fatal(err)
	}
	wait(r.ID)
	if tasks, _ := db.ListTasks(c, p.ID); len(tasks) != 0 {
		t.Fatal("proactive suggestion created without confirmation")
	}
	suggestions, _ := s.RequirementIntakes(c, app.Human(), p.ID)
	if len(suggestions) != 1 || suggestions[0].Status != "suggested" {
		t.Fatal("suggestion not persisted", suggestions)
	}
	follow, err := m.StartAs(c, app.Human(), p.ID, "", "question", "创建吧", r.ID)
	if err != nil {
		t.Fatal(err)
	}
	wait(follow.ID)
	suggestions, _ = s.RequirementIntakes(c, app.Human(), p.ID)
	if suggestions[0].Status != "created" {
		t.Fatal("natural language confirmation did not create", suggestions)
	}
	task, _ := db.GetTask(c, suggestions[0].TaskID)
	if task.Kind != "bug" || task.Status != "ready" || len(task.Attachments) != 1 {
		t.Fatal("Bug missing attachment or not ready for development", task)
	}
	m.ReviewCreatedRequirement(task.ID)
	if _, err := db.GetRequirementReview(c, task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("Bug entered independent review", err)
	}
	direct, err := m.StartAs(c, app.Human(), p.ID, "", "question", "请创建需求 DIRECT_CREATE", "")
	if err != nil {
		t.Fatal(err)
	}
	wait(direct.ID)
	tasks, _ := db.ListTasks(c, p.ID)
	if len(tasks) != 2 {
		t.Fatal("direct request not saved", tasks)
	}
	for _, task := range tasks {
		if task.Kind != "requirement" {
			continue
		}
		if task.Status != "clarify" {
			t.Fatal("iteration requirement skipped review", task)
		}
		for end := time.Now().Add(10 * time.Second); ; {
			v, _ := db.GetRequirementReview(c, task.ID)
			if v.Status == "failed" {
				break
			}
			if time.Now().After(end) {
				t.Fatalf("requirement review never failed: %+v", v)
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	if !sawFiles.Load() {
		t.Fatal("model did not receive attachment text")
	}
	// The tool catalog and injected instruction must make the proactive path discoverable.
	if !sawProactivePolicy.Load() {
		t.Fatal("proactive creation and read-only collaboration instructions missing")
	}
	if creationReceipts.Load() != 2 {
		t.Fatal("missing creation receipts", creationReceipts.Load())
	}
}
