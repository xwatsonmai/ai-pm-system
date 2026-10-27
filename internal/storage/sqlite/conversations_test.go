package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestConversationEditsSurviveReopenAndLateRunSaves(t *testing.T) {
	c := context.Background()
	path := filepath.Join(t.TempDir(), "pm.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []domain.Run{{ID: "old", ProjectID: "p", Kind: "question"}, {ID: "first", ProjectID: "p", Kind: "question", ConversationID: "conv"}} {
		if err = db.PutRun(c, r); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.PutConversation(c, domain.Conversation{ID: "old", ProjectID: "p", Title: "旧对话名称"}); err != nil {
		t.Fatal(err)
	}
	if err = db.PutConversation(c, domain.Conversation{ID: "conv", ProjectID: "p", DeletedAt: domain.Now()}); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.PutRun(c, domain.Run{ID: "first", ProjectID: "p", Kind: "question", ConversationID: "conv", ConversationTitle: "late title"}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.GetRun(c, "first"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("deleted run readable", err)
	}
	runs, err := db.ListRuns(c, "p")
	if err != nil || len(runs) != 1 || runs[0].ConversationTitle != "旧对话名称" {
		t.Fatal("edits not durable", runs, err)
	}
}
