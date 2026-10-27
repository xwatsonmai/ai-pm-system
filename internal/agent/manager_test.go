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
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This uses the installed Pi SDK, a local model fixture and the actual Go tools.
// It never calls a paid provider or uses a developer's credentials.
func TestPiHostToolsPersistenceAndStop(t *testing.T) {
	worker, _ := filepath.Abs("../../runtime/worker.mjs")
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js required for Pi integration test")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(worker), "node_modules/@earendil-works/pi-coding-agent")); err != nil {
		t.Skip("run npm ci --prefix runtime for Pi integration test")
	}
	var requests, titleRequests atomic.Int32
	var blocking atomic.Bool
	var imageSeen, documentSeen atomic.Bool
	started := make(chan struct{}, 1)
	aborted := make(chan struct{}, 2)
	release := make(chan struct{})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		encoded, _ := json.Marshal(body)
		if strings.Contains(string(encoded), "根据用户首条发言生成") {
			titleRequests.Add(1)
			if strings.Contains(string(encoded), "当前项目事实快照") {
				t.Error("title session received project context")
			}
			if tools, ok := body["tools"].([]any); ok && len(tools) > 0 {
				t.Error("title session received project tools")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"title\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"附件问题分析\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"title\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		if blocking.Load() {
			started <- struct{}{}
			select {
			case <-r.Context().Done():
				aborted <- struct{}{}
			case <-release:
			}
			return
		}
		if strings.Contains(string(encoded), "data:image/png;base64,") {
			imageSeen.Store(true)
		}
		if strings.Contains(string(encoded), "attachment_unique_389") {
			documentSeen.Store(true)
		}
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Error("model key not delivered")
		}
		n := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(delta any, finish any) {
			v, _ := json.Marshal(map[string]any{"id": "fixture", "object": "chat.completion.chunk", "model": "fixture-model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", v)
		}
		send(map[string]any{"role": "assistant"}, nil)
		if n < 3 {
			send(map[string]string{"content": fmt.Sprintf("第%d步：检查项目事实。", n)}, nil)
			name, args := "project_context", `{}`
			if n == 2 {
				name, args = "save_analysis", `{"kind":"analysis","title":"联调记录","body":"项目尚无任务或部署证据。"}`
			}
			send(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("tool_%d", n), "type": "function", "function": map[string]string{"name": name, "arguments": args}}}}, nil)
			send(map[string]any{}, "tool_calls")
		} else {
			send(map[string]string{"content": "项目分析已保存；尚无部署证据。"}, nil)
			send(map[string]any{}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer func() { close(release); model.Close() }()
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "pm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &app.Service{Store: db}
	profile, err := s.SaveProfile(context.Background(), domain.Profile{Name: "Fixture", Provider: "fixture", Model: "fixture-model", BaseURL: model.URL + "/v1", API: "openai-completions", SupportsImages: true}, "fixture-key")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.PutPlatformSettings(context.Background(), domain.PlatformSettings{ProfileID: profile.ID}); err != nil {
		t.Fatal(err)
	}
	p, err := s.SaveProject(context.Background(), domain.Project{Name: "Integration", ProfileID: profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := agent.New(ctx, s, &gitrepo.Repositories{Root: filepath.Join(dir, "repos"), App: s}, worker, dir)
	defer m.Close()
	wait := func(id string) domain.Run {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			r, err := db.GetRun(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != "queued" && r.Status != "running" {
				return r
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("Pi run did not finish")
		return domain.Run{}
	}
	r, err := m.Start(ctx, p.ID, "", "progress", "", "")
	if err != nil {
		t.Fatal(err)
	}
	finished := wait(r.ID)
	if finished.Status != "succeeded" || !strings.Contains(finished.Output, "尚无部署证据") || len(finished.Steps) != 2 {
		t.Fatalf("run failed: %+v", finished)
	}
	if finished.ProcessOutput != "第1步：检查项目事实。\n\n第2步：检查项目事实。" || finished.Output != "项目分析已保存；尚无部署证据。" {
		t.Fatalf("intermediate explanations lost or duplicated in final answer: %+v", finished)
	}
	reopened, err := sqlite.Open(filepath.Join(dir, "pm.db"))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := reopened.GetRun(ctx, r.ID)
	reopened.Close()
	if err != nil || restored.ProcessOutput != finished.ProcessOutput {
		t.Fatal("process history not persisted", err)
	}
	artifacts, err := db.ListArtifacts(ctx, p.ID)
	if err != nil || len(artifacts) != 1 || artifacts[0].Kind != "analysis" {
		t.Fatalf("tool did not persist analysis: %+v %v", artifacts, err)
	}
	var imageData bytes.Buffer
	png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	img, err := s.SaveAttachment(ctx, app.Human(), p.ID, "screen.png", imageData.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.SaveAttachment(ctx, app.Human(), p.ID, "spec.md", []byte("Requirement attachment_unique_389"))
	if err != nil {
		t.Fatal(err)
	}
	r, err = m.Start(ctx, p.ID, "", "question", "结合附件分析", "", img.ID, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result := wait(r.ID); result.Status != "succeeded" || len(result.Attachments) != 2 || result.ConversationTitle != "附件问题分析" || result.TitleStatus != "ready" {
		t.Fatal("attachment run failed", result.Error)
	}
	followup, err := m.Start(ctx, p.ID, "", "question", "继续追问", r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result := wait(followup.ID); result.Status != "succeeded" || result.ConversationID != r.ConversationID || result.TitleStatus != "" {
		t.Fatal("follow-up left original conversation", result)
	}
	if titleRequests.Load() != 1 {
		t.Fatal("conversation renamed on follow-up", titleRequests.Load())
	}
	root, _ := db.GetRun(ctx, r.ID)
	if root.ConversationTitle != "附件问题分析" {
		t.Fatal("AI title not persisted")
	}
	if !imageSeen.Load() || !documentSeen.Load() {
		t.Fatal("model did not receive image bytes and document text")
	}
	// Requirement analysis consumes the persisted document attachments too.
	imageSeen.Store(false)
	documentSeen.Store(false)
	requirement, err := s.CreateTask(ctx, app.Human(), p.ID, app.TaskInput{Title: "图文需求", Description: "见附件", AttachmentIDs: []string{img.ID, doc.ID}, RequestID: "requirement-attachments"})
	if err != nil {
		t.Fatal(err)
	}
	richRun, err := m.Start(ctx, p.ID, requirement.Task.ID, "requirements", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result := wait(richRun.ID); result.Status != "succeeded" || len(result.Attachments) != 2 || !imageSeen.Load() || !documentSeen.Load() {
		t.Fatal("requirement analysis missed attached evidence", result.Error)
	}
	other, err := s.SaveProject(ctx, domain.Project{Name: "Other", ProfileID: profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(ctx, other.ID, "", "question", "cross-project", "", img.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("foreign attachment accepted", err)
	}
	profile.SupportsImages = false
	if _, err = s.SaveProfile(ctx, profile, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(ctx, p.ID, "", "question", "image", "", img.ID); err == nil {
		t.Fatal("text-only model accepted image silently")
	}
	blocking.Store(true)
	r, err = m.Start(ctx, p.ID, "", "question", "等待测试", followup.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("model request not started")
	}
	if _, err = m.Start(ctx, p.ID, "", "question", "重复窗口同时追问", followup.ID); err == nil {
		t.Fatal("accepted concurrent follow-up against a stale parent")
	}
	if _, err = m.EditConversation(ctx, app.Human(), p.ID, r.ConversationID, "人工确认的名称", false); err != nil {
		t.Fatal(err)
	}
	if !m.Stop(r.ID) {
		t.Fatal("could not stop active run")
	}
	if stopped := wait(r.ID); stopped.Status != "stopped" || stopped.ConversationTitle != "人工确认的名称" {
		t.Fatalf("stop not effective: %+v", stopped)
	}
	select {
	case <-aborted:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not cancel model request")
	}
	r, err = m.Start(ctx, p.ID, "", "question", "删除运行中会话", r.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("delete fixture did not start")
	}
	if _, err = m.EditConversation(ctx, app.Human(), p.ID, r.ConversationID, "", true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-aborted:
	case <-time.After(5 * time.Second):
		t.Fatal("delete did not cancel model request")
	}
	if _, err = db.GetRun(ctx, r.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("deleted active conversation still readable", err)
	}
	// Close waits for active workers. Deletion must cancel the blocked model,
	// instead of leaving it running until the normal three-minute deadline.
	closed := make(chan struct{})
	go func() { m.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("deleted worker did not stop")
	}
	if _, err = db.GetRun(ctx, r.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("worker resurrected deleted conversation", err)
	}
}
