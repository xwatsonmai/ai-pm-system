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
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPiCreatesCollaborationAndResumesLatestConversation(t *testing.T) {
	worker, _ := filepath.Abs("../../runtime/worker.mjs")
	if _, err := os.Stat(filepath.Join(filepath.Dir(worker), "node_modules/@earendil-works/pi-coding-agent")); err != nil {
		t.Skip("npm ci --prefix runtime required")
	}
	var requests, titles atomic.Int32
	var continued atomic.Bool
	busy := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		encoded, _ := json.Marshal(body)
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(delta any, finish any) {
			b, _ := json.Marshal(map[string]any{"id": "fixture", "object": "chat.completion.chunk", "model": "fixture-model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		send(map[string]string{"role": "assistant"}, nil)
		if strings.Contains(string(encoded), "根据用户首条发言生成") {
			titles.Add(1)
			send(map[string]string{"content": "线上回调调查"}, nil)
			send(map[string]any{}, "stop")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		n := requests.Add(1)
		if n >= 5 {
			available := map[string]bool{}
			for _, raw := range body["tools"].([]any) {
				available[raw.(map[string]any)["function"].(map[string]any)["name"].(string)] = true
			}
			if !available["get_collaborations"] || !available["create_collaboration"] {
				t.Error("user continuation after automatic result lost collaboration tools", available)
			}
			call := func(name, args string) {
				send(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("retry-%d", n), "type": "function", "function": map[string]string{"name": name, "arguments": args}}}}, nil)
				send(map[string]any{}, "tool_calls")
			}
			switch n {
			case 5:
				messages := body["messages"].([]any)
				current, _ := json.Marshal(messages[len(messages)-1])
				if !strings.Contains(string(current), "再继续刚才的问题") || !strings.Contains(string(current), "历史 cannot_access/inconclusive") || !strings.Contains(string(current), "先调用 get_collaborations") {
					t.Error("current user turn missing continuation policy", string(current))
				}
				// Simulate a model skipping the mandatory fresh query. The server
				// must reject without creating work, and Pi must recover in-turn.
				call("create_collaboration", `{"title":"生产回调调查","brief":"trace-42 续查","environment":"production"}`)
			case 6:
				messages := body["messages"].([]any)
				last, _ := json.Marshal(messages[len(messages)-1])
				if !strings.Contains(string(last), "本轮必须先成功调用 get_collaborations") {
					t.Error("missing query-before-create rejection", string(last))
				}
				call("get_collaborations", `{}`)
			case 7:
				messages := body["messages"].([]any)
				last, _ := json.Marshal(messages[len(messages)-1])
				if !strings.Contains(string(last), "cannot_access") || !strings.Contains(string(last), "as_of") {
					t.Error("retry did not get historical result", string(last))
				}
				call("create_collaboration", `{"title":"生产回调调查","brief":"继续原目标 trace-42；旧调查取证时间 2026-10-14T10:00:00+08:00，曾因连接配置缺失无法访问；本轮用户要求继续，使用执行端最新配置重新核验，不将旧结果视为现状。","environment":"production"}`)
			case 8:
				send(map[string]string{"content": "已重新发起只读调查，等待认领；原访问失败是历史结果，当前尚未重新核验。"}, nil)
				send(map[string]any{}, "stop")
			default:
				t.Error("unexpected continuation request", n)
				send(map[string]any{}, "stop")
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		if n == 1 {
			send(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "investigate", "type": "function", "function": map[string]string{"name": "create_collaboration", "arguments": `{"title":"生产回调调查","brief":"trace-42 回调失败，请核查生产日志与部署配置","environment":"production"}`}}}}, nil)
			send(map[string]any{}, "tool_calls")
		} else {
			answer := "已创建协同任务，等待环境核查。"
			if n == 3 {
				busy <- struct{}{}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				answer = "已记录新的部署线索。"
			}
			if n == 4 {
				continued.Store(strings.Contains(string(encoded), "new-deployment-clue") && strings.Contains(string(encoded), "evidence-trace-42") && strings.Contains(string(encoded), "协同任务结果已返回"))
				for _, raw := range body["tools"].([]any) {
					tool := raw.(map[string]any)["function"].(map[string]any)
					if tool["name"] == "create_collaboration" {
						t.Error("automatic answer can recursively delegate")
					}
				}
				answer = "环境核查已返回：当时连接配置缺失，无法访问。证据 evidence-trace-42；目标未完成。"
			}
			send(map[string]string{"content": answer}, nil)
			send(map[string]any{}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "pm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &app.Service{Store: db}
	ctx := context.Background()
	profile, err := s.SaveProfile(ctx, domain.Profile{Name: "Fixture", Provider: "fixture", Model: "fixture-model", BaseURL: model.URL + "/v1", API: "openai-completions"}, "fixture-key")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.SaveProject(ctx, domain.Project{Name: "Investigation", ProfileID: profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.PutPlatformSettings(ctx, domain.PlatformSettings{ProfileID: profile.ID}); err != nil {
		t.Fatal(err)
	}
	m := agent.New(ctx, s, &gitrepo.Repositories{Root: filepath.Join(dir, "repos"), App: s}, worker, dir)
	defer m.Close()
	waitRun := func(id string) domain.Run {
		t.Helper()
		until := time.Now().Add(15 * time.Second)
		for time.Now().Before(until) {
			r, e := db.GetRun(ctx, id)
			if e == nil && r.Status != "running" && r.Status != "queued" {
				return r
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("run did not complete", id)
		return domain.Run{}
	}
	origin, err := m.Start(ctx, p.ID, "", "question", "生产环境 trace-42 回调失败，请协同排查", "")
	if err != nil {
		t.Fatal(err)
	}
	if r := waitRun(origin.ID); r.Status != "succeeded" {
		t.Fatal(r)
	}
	all, err := db.ListCollaborations(ctx, p.ID)
	if err != nil || len(all) != 1 {
		t.Fatal(all, err)
	}
	v := all[0]
	if v.OriginRunID != origin.ID || v.ConversationID != origin.ConversationID {
		t.Fatal(v)
	}
	followup, err := m.Start(ctx, p.ID, "", "question", "new-deployment-clue", origin.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-busy:
	case <-time.After(5 * time.Second):
		t.Fatal("followup not started")
	}
	dev := domain.Actor{ID: "dev", Name: "开发核查", Kind: "developer", ProjectID: p.ID}
	claimed, err := s.ActCollaboration(ctx, dev, v.ID, app.CollaborationAction{Action: "claim", RequestID: "claim"})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := s.ActCollaboration(ctx, dev, v.ID, app.CollaborationAction{Action: "submit", RequestID: "submit", ClaimID: claimed.ClaimID, Version: claimed.Collaboration.Version, Result: &domain.InvestigationResult{Outcome: "cannot_access", Summary: "连接配置缺失，无法访问", Evidence: "evidence-trace-42", Environment: "production", ObservedAt: "2026-10-14T10:00:00+08:00", DeploymentVersion: "deployed-sha", Limitations: "尚未修复"}})
	if err != nil {
		t.Fatal(err)
	}
	m.StartReplyWorker()
	time.Sleep(1200 * time.Millisecond)
	job, _ := db.GetReplyJob(ctx, completed.Collaboration.Result.ID)
	if job.Status != "pending" {
		t.Fatal("reply raced active user turn", job)
	}
	releaseOnce.Do(func() { close(release) })
	if r := waitRun(followup.ID); r.Status != "succeeded" {
		t.Fatal(r)
	}
	result := waitRun(job.RunID)
	if result.Status != "succeeded" || result.OwnerID != origin.OwnerID || result.ConversationID != origin.ConversationID || result.Prompt != "" || !continued.Load() {
		t.Fatal("bad continuation", result, continued.Load())
	}
	// Resume the same Pi session after its automatic result turn. The old
	// no-recursion instruction must not hide tools or govern the new user turn.
	changes, unsubscribe := s.SubscribeWork(p.ID)
	defer unsubscribe()
	retry, err := m.Start(ctx, p.ID, "", "question", "再继续刚才的问题", result.ID)
	if err != nil {
		t.Fatal(err)
	}
	retried := waitRun(retry.ID)
	if retried.Status != "succeeded" || len(retried.Steps) != 3 || retried.Steps[0].Name != "create_collaboration" || retried.Steps[0].Status != "failed" || retried.Steps[1].Name != "get_collaborations" || retried.Steps[2].Name != "create_collaboration" {
		t.Fatal("continuation did not query and re-dispatch", retried)
	}
	for _, step := range retried.Steps[1:] {
		if step.Status != "done" {
			t.Fatal("continuation tool failed", step)
		}
	}
	all, err = db.ListCollaborations(ctx, p.ID)
	if err != nil || len(all) != 2 {
		t.Fatal("new investigation not persisted", all, err)
	}
	work, err := s.WorkList(ctx, dev)
	if err != nil {
		t.Fatal(err)
	}
	pending := work["collaborations"].([]domain.Collaboration)
	if len(pending) != 1 || pending[0].ID == v.ID || pending[0].Status != "pending" {
		t.Fatal("developer inbox did not receive new investigation", pending)
	}
	fresh, _ := db.GetCollaboration(ctx, pending[0].ID)
	if fresh.OriginRunID != retry.ID || fresh.ConversationID != origin.ConversationID || fresh.Title != v.Title || fresh.Environment != v.Environment {
		t.Fatal("continuation lost origin or scope", fresh)
	}
	old, err := db.GetCollaboration(ctx, v.ID)
	if err != nil || old.Status != "completed" || old.Result.Outcome != "cannot_access" || old.Result.Evidence != "evidence-trace-42" || old.Version != completed.Collaboration.Version {
		t.Fatal("continuation rewrote historical investigation", old, err)
	}
	select {
	case <-changes:
	default:
		t.Fatal("new investigation did not notify developer subscribers")
	}
	if titles.Load() != 1 {
		t.Fatal("automatic reply renamed conversation", titles.Load())
	}
	m.Close()
	m2 := agent.New(ctx, s, nil, worker, dir)
	defer m2.Close()
	if err = m2.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	m2.StartReplyWorker()
	time.Sleep(1100 * time.Millisecond)
	jobs, _ := db.ListReplyJobs(ctx)
	runs, _ := db.ListRuns(ctx, p.ID)
	if len(jobs) != 1 || jobs[0].Status != "delivered" || len(runs) != 4 || requests.Load() != 8 {
		t.Fatal("duplicate continuation after restart", jobs, len(runs), requests.Load())
	}
}

func TestPiQueriesFreshCollaborationAndCancelsFromConversation(t *testing.T) {
	worker, _ := filepath.Abs("../../runtime/worker.mjs")
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(worker), "node_modules/@earendil-works/pi-coding-agent")); err != nil {
		t.Skip("npm ci --prefix runtime required")
	}
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "pm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &app.Service{Store: db}
	// Project and collaboration exist before the first model request. The handler
	// then changes progress so the query must bypass the initial prompt snapshot.
	p := domain.Project{ID: "p", OwnerID: "alice", ProfileID: "profile"}
	origin := domain.Run{ID: "origin", ProjectID: p.ID, ConversationID: "conversation", Kind: "question", OwnerID: "alice", OwnerKind: "human", Status: "succeeded", Prompt: "请调查生产回调"}
	if err = db.PutProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err = db.PutRun(ctx, origin); err != nil {
		t.Fatal(err)
	}
	owner := domain.Actor{ID: "alice", Kind: "human"}
	v, err := s.CreateCollaboration(ctx, owner, app.CollaborationInput{OriginRunID: origin.ID, RequestID: "create", Title: "回调调查", Brief: "trace-42", Environment: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	dev := domain.Actor{ID: "dev", Name: "开发核查", Kind: "developer", ProjectID: p.ID}
	claimed, err := s.ActCollaboration(ctx, dev, v.Collaboration.ID, app.CollaborationAction{Action: "claim", RequestID: "claim"})
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		available := map[string]bool{}
		for _, tool := range body.Tools {
			available[tool.Function.Name] = true
		}
		if !available["get_collaborations"] || !available["cancel_collaboration"] {
			t.Error("Pi did not receive collaboration tools", available)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(delta any, finish any) {
			b, _ := json.Marshal(map[string]any{"id": "fixture", "object": "chat.completion.chunk", "model": "fixture-model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		call := func(name string, args map[string]string) {
			b, _ := json.Marshal(args)
			send(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("call-%d", requests.Load()), "type": "function", "function": map[string]string{"name": name, "arguments": string(b)}}}}, nil)
			send(map[string]any{}, "tool_calls")
		}
		checkLastTool := func(fragments ...string) {
			t.Helper()
			if len(body.Messages) == 0 {
				t.Error("no tool response")
				return
			}
			last := body.Messages[len(body.Messages)-1]
			b, _ := json.Marshal(last.Content)
			if last.Role != "tool" {
				t.Error("missing Pi tool result", last.Role)
			}
			for _, fragment := range fragments {
				if !strings.Contains(string(b), fragment) {
					t.Error("tool response missing", fragment, string(b))
				}
			}
		}
		send(map[string]string{"role": "assistant"}, nil)
		switch requests.Add(1) {
		case 1:
			if _, err := s.ActCollaboration(ctx, dev, v.Collaboration.ID, app.CollaborationAction{Action: "report", RequestID: "fresh-progress", ClaimID: claimed.ClaimID, ReportKind: "blocked", Body: "fresh-evidence：等待生产日志权限"}); err != nil {
				t.Error(err)
			}
			call("get_collaborations", map[string]string{})
		case 2:
			checkLastTool("blocked", "fresh-evidence", "as_of")
			send(map[string]string{"content": "尚未搞定，开发方最新汇报：等待生产日志权限。"}, nil)
			send(map[string]any{}, "stop")
		case 3:
			call("get_collaborations", map[string]string{"collaboration_id": v.Collaboration.ID})
		case 4:
			checkLastTool("blocked", v.Collaboration.ID)
			call("cancel_collaboration", map[string]string{"collaboration_id": v.Collaboration.ID})
		case 5:
			checkLastTool("canceled", v.Collaboration.ID)
			send(map[string]string{"content": "已取消排查。本地执行方收到通知或再次查询后停止。"}, nil)
			send(map[string]any{}, "stop")
		default:
			t.Error("unexpected extra model request")
			send(map[string]any{}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	if err = db.PutProfile(ctx, domain.Profile{ID: p.ProfileID, Provider: "fixture", Model: "fixture-model", BaseURL: model.URL + "/v1", API: "openai-completions"}); err != nil {
		t.Fatal(err)
	}
	if err = db.PutPlatformSettings(ctx, domain.PlatformSettings{ProfileID: p.ProfileID}); err != nil {
		t.Fatal(err)
	}
	m := agent.New(ctx, s, nil, worker, dir)
	defer m.Close()
	parent := origin.ID
	for _, prompt := range []string{"搞定没有？", "不用查了，我知道是什么问题了。"} {
		r, err := m.StartAs(ctx, owner, p.ID, "", "question", prompt, parent)
		if err != nil {
			t.Fatal(err)
		}
		until := time.Now().Add(15 * time.Second)
		for time.Now().Before(until) {
			r, err = db.GetRun(ctx, r.ID)
			if err != nil || (r.Status != "running" && r.Status != "queued") {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil || r.Status != "succeeded" || r.Output == "" {
			t.Fatal(r, err)
		}
		for _, step := range r.Steps {
			if step.Status != "done" {
				t.Fatal("tool failed", step)
			}
		}
		if len(r.Steps) == 0 || r.Steps[0].Name != "get_collaborations" {
			t.Fatal("did not query before replying", r.Steps)
		}
		parent = r.ID
	}
	stored, _ := db.GetCollaboration(ctx, v.Collaboration.ID)
	if stored.Status != "canceled" || requests.Load() != 5 {
		t.Fatal(stored, requests.Load())
	}
	_, err = s.ActCollaboration(ctx, dev, stored.ID, app.CollaborationAction{Action: "submit", RequestID: "late-result", ClaimID: claimed.ClaimID, Version: stored.Version, Result: &domain.InvestigationResult{Outcome: "inconclusive", Summary: "late", Environment: "prod", ObservedAt: "now", DeploymentVersion: "unknown", Limitations: "unknown"}})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatal("late result accepted", err)
	}
	jobs, err := db.ListReplyJobs(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatal("late result queued a reply", jobs, err)
	}
}
