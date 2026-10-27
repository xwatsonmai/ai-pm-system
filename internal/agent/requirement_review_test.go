package agent_test

import (
	"ai-pm-system/internal/agent"
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/gitrepo"
	"ai-pm-system/internal/storage/sqlite"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReviewAdmissionFailureAndRestartPreserveMessages(t *testing.T) {
	c := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "pm.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &app.Service{Store: db}
	p, _ := s.SaveProject(c, domain.Project{Name: "No model"})
	task, _ := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "保存优先", Description: "完整需求原文", RequestID: "create"})
	v, err := s.SaveRequirementReview(c, app.Human(), task.Task.ID, app.RequirementReviewInput{Action: "message", Body: "纠正 Agent 的长篇说明", RequestID: "message"})
	if err != nil {
		t.Fatal(err)
	}
	m := agent.New(c, s, nil, "missing", dir)
	m.KickRequirementReview(task.Task.ID)
	v, _ = s.RequirementReview(c, app.Human(), task.Task.ID)
	// The failure is recorded as an event after the saved input.
	if v.Status != "failed" || !strings.Contains(v.Error, "模型") || len(v.Messages) != 3 || v.Messages[2].Action != "review_failed" {
		t.Fatalf("admission discarded input: %+v", v)
	}
	v, err = s.SaveRequirementReview(c, app.Human(), task.Task.ID, app.RequirementReviewInput{Action: "retry", RequestID: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BeginRequirementReview(c, v.TaskID, v.Generation, "pending"); err != nil {
		t.Fatal(err)
	}
	if err = db.PutRun(c, domain.Run{ID: "pending", ProjectID: p.ID, TaskID: v.TaskID, Kind: "requirement_review", ReviewGeneration: v.Generation, Status: "running"}); err != nil {
		t.Fatal(err)
	}
	m.Close()
	db.Close()
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = &app.Service{Store: db}
	m = agent.New(c, s, nil, "missing", dir)
	defer m.Close()
	if err = m.Recover(c); err != nil {
		t.Fatal(err)
	}
	v, _ = s.RequirementReview(c, app.Human(), task.Task.ID)
	if v.Status != "failed" || len(v.Messages) != 5 || v.Messages[1].Body != "纠正 Agent 的长篇说明" || v.Messages[3].Action != "review_retried" || v.Messages[3].ActorKind != "human" || v.Messages[4].Action != "review_failed" {
		t.Fatal("restart lost input", v)
	}
}

func TestPiRequirementReviewStructuredConversation(t *testing.T) {
	worker, _ := filepath.Abs("../../runtime/worker.mjs")
	if _, err := os.Stat(filepath.Join(filepath.Dir(worker), "node_modules/@earendil-works/pi-coding-agent")); err != nil {
		t.Skip("run npm ci --prefix runtime for Pi integration")
	}
	var sawCorrection, sawDocument, sawClarification, sawNote, sawNoteImage atomic.Bool
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		raw, _ := json.Marshal(body)
		text := string(raw)
		if strings.Contains(text, "逐题说明_unique_815") {
			sawNote.Store(true)
			if strings.Contains(text, "data:image/png;base64,") {
				sawNoteImage.Store(true)
			}
		}
		correction := strings.Contains(text, "改为按订单退款")
		clarification := strings.Contains(text, "这个问题是什么意思？我不懂技术，能举个例子吗？")
		if clarification {
			sawClarification.Store(true)
		}
		if correction {
			sawCorrection.Store(true)
		}
		if strings.Contains(text, "review_document_unique_784") {
			sawDocument.Store(true)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(delta any, finish any) {
			b, _ := json.Marshal(map[string]any{"id": "fixture", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		messages, _ := body["messages"].([]any)
		hasTool := false
		for _, msg := range messages {
			if msg.(map[string]any)["role"] == "user" {
				hasTool = false
			}
			if msg.(map[string]any)["role"] == "tool" {
				hasTool = true
			}
		}
		if !hasTool {
			topic := domain.RequirementTopic{ID: "grouping", Title: "退款记录怎么查看", Kind: "detail", Status: "open"}
			result := app.RequirementReviewSubmission{Reply: "已阅读原始需求和文档，请确认退款范围。", Decision: "clarify", Agenda: []domain.RequirementTopic{topic}, Ask: &app.RequirementAskInput{TopicID: "grouping", Kind: "form", Questions: []domain.RequirementQuestion{{ID: "scope", Label: "按什么分组？", Type: "radio", Options: []string{"客户", "订单"}, Recommended: []string{"订单"}, Reason: "退款可追溯到订单", Required: true}}}}
			if clarification {
				result.Reply = "我是在问：退款记录按客户汇总，还是每笔订单分开看。例如同一客户买了两次，按订单查看时会看到两笔独立记录。这还不是你的选择，你可以直接说希望怎样查看。"
				result.Ask.Questions[0].Label = "退款记录按客户汇总，还是每笔订单分开看？"
			}
			if correction {
				topic.Status, topic.Conclusion = "resolved", "按订单退款"
				result = app.RequirementReviewSubmission{Reply: "已按纠正重新理解，请确认需求稿。", Decision: "ready", Agenda: []domain.RequirementTopic{topic}, Proposal: &domain.RequirementProposal{Description: "按订单退款", Acceptance: []string{"每笔退款关联原订单"}}}
			}
			args, _ := json.Marshal(result)
			send(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "review_result", "type": "function", "function": map[string]string{"name": "submit_requirement_review", "arguments": string(args)}}}}, nil)
			send(map[string]any{}, "tool_calls")
		} else {
			send(map[string]string{"content": "评审已提交。"}, nil)
			send(map[string]any{}, "stop")
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
	profile, err := s.SaveProfile(c, domain.Profile{Name: "Fixture", Provider: "fixture", Model: "fixture-model", SupportsImages: true, BaseURL: model.URL + "/v1", API: "openai-completions"}, "fixture-key")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.PutPlatformSettings(c, domain.PlatformSettings{ProfileID: profile.ID}); err != nil {
		t.Fatal(err)
	}
	p, _ := s.SaveProject(c, domain.Project{Name: "Review", ProfileID: profile.ID})
	file, err := s.SaveAttachment(c, app.Human(), p.ID, "rules.md", []byte("review_document_unique_784"))
	if err != nil {
		t.Fatal(err)
	}
	task, _ := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "退款", Description: "退款需求", AttachmentIDs: []string{file.ID}, RequestID: "create"})
	m := agent.New(c, s, &gitrepo.Repositories{Root: filepath.Join(dir, "repos"), App: s}, worker, dir)
	defer m.Close()
	wait := func() domain.RequirementReview {
		t.Helper()
		for end := time.Now().Add(20 * time.Second); time.Now().Before(end); {
			v, e := s.RequirementReview(c, app.Human(), task.Task.ID)
			if e != nil {
				t.Fatal(e)
			}
			if v.Status != "queued" && v.Status != "running" {
				return v
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("review timeout")
		return domain.RequirementReview{}
	}
	if _, err = s.SaveRequirementReview(c, app.Human(), task.Task.ID, app.RequirementReviewInput{Action: "start", RequestID: "start"}); err != nil {
		t.Fatal(err)
	}
	m.KickRequirementReview(task.Task.ID)
	v := wait()
	if v.Status != "needs_input" || len(v.Messages) != 2 || len(v.Messages[1].Result.Questions) != 1 {
		t.Fatalf("structured review failed: %+v", v)
	}
	// Asking for an explanation with every form field blank is a real message,
	// persisted before the next model call, and never an implicit confirmation.
	question := "这个问题是什么意思？我不懂技术，能举个例子吗？"
	v, err = s.SaveRequirementReview(c, app.Human(), task.Task.ID, app.RequirementReviewInput{Action: "message", Body: question, ReplyTo: v.Messages[1].ID, RequestID: "explain"})
	if err != nil || v.Status != "queued" || v.Messages[len(v.Messages)-1].Body != question || len(v.Messages[len(v.Messages)-1].Answers) != 0 {
		t.Fatalf("blank form blocked or lost clarification: %+v %v", v, err)
	}
	m.KickRequirementReview(task.Task.ID)
	v = wait()
	current, _ := db.GetTask(c, task.Task.ID)
	if !sawClarification.Load() || v.Status != "needs_input" || len(v.Messages[len(v.Messages)-1].Result.Questions) != 1 || v.Messages[1].FormExpiredAt == 0 || current.Description != "退款需求" || current.Status != "clarify" {
		t.Fatalf("explanation changed business decision or lost context: %+v %+v", v, current)
	}
	var imageData bytes.Buffer
	_ = png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	noteImage, err := s.SaveAttachment(c, app.Human(), p.ID, "逐题截图.png", imageData.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveRequirementReview(c, app.Human(), task.Task.ID, app.RequirementReviewInput{Action: "message", Body: "改为按订单退款", ReplyTo: v.Messages[len(v.Messages)-1].ID, QuestionNotes: map[string]app.RequirementQuestionNoteInput{"scope": {Body: "逐题说明_unique_815", AttachmentIDs: []string{noteImage.ID}}}, RequestID: "correct"}); err != nil {
		t.Fatal(err)
	}
	m.KickRequirementReview(task.Task.ID)
	v = wait()
	if v.Status != "ready_to_confirm" || !sawCorrection.Load() || !sawDocument.Load() || !sawNote.Load() || !sawNoteImage.Load() {
		t.Fatalf("continuation missing context: %+v", v)
	}
	current, _ = db.GetTask(c, task.Task.ID)
	if current.Description != "退款需求" || current.Status != "clarify" {
		t.Fatal("Agent applied proposal without product confirmation")
	}
}
