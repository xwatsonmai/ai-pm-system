package agent

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestLiveRunBeforeCheckpointAndConversationRules(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m := New(ctx, &app.Service{Store: db}, nil, "", "")
	run := domain.Run{ID: "r", ProjectID: "p", ConversationID: "c", Kind: "question", Status: "running", Output: "开始"}
	if err = db.PutRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	updates, cancel := m.WatchRun(run.ID)
	run.Output += "，实时文字🙂"
	run.Steps = []domain.Step{{Name: "read", Status: "running"}}
	m.publishRun(run)
	run.Steps[0].Status = "done"
	select {
	case <-updates:
	default:
		t.Fatal("missing notification")
	}
	live, err := m.ReadLiveRun(ctx, run.ID)
	if err != nil || live.Output != run.Output || live.Steps[0].Status != "running" {
		t.Fatalf("live snapshot: %+v %v", live, err)
	}
	stored, _ := db.GetRun(ctx, run.ID)
	if stored.Output != "开始" {
		t.Fatal("live token forced a database write")
	}
	for range 1000 {
		m.publishRun(run)
	}
	if len(updates) != 1 {
		t.Fatal("slow consumer backlog is unbounded")
	}
	if err = db.PutConversation(ctx, domain.Conversation{ID: "c", ProjectID: "p", Title: "手动标题"}); err != nil {
		t.Fatal(err)
	}
	live, err = m.ReadLiveRun(ctx, run.ID)
	if err != nil || live.ConversationTitle != "手动标题" || live.TitleStatus != "manual" {
		t.Fatal("live title overwrote manual title")
	}
	if err = db.PutConversation(ctx, domain.Conversation{ID: "c", ProjectID: "p", DeletedAt: domain.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.ReadLiveRun(ctx, run.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("deleted live conversation visible", err)
	}
	cancel()
	cancel()
	m.clearLiveRun(run.ID)
	if len(m.runWatchers) != 0 || len(m.liveRuns) != 0 {
		t.Fatal("stream state leaked")
	}
}
