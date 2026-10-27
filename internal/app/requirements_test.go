package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"testing"
)

func TestAgentBugCreationIsReadyAndHumanCreationStillNeedsReview(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	agent := domain.Actor{ID: "platform", Kind: "platform", ProjectID: p.ID}
	input := app.TaskInput{Title: "重复扣款", Kind: "bug", Description: "一次支付扣款两次", Acceptance: []string{"只扣一次"}, RequestID: "agent-bug"}
	created, err := s.CreateTask(c, agent, p.ID, input)
	if err != nil || created.Task.Status != "ready" {
		t.Fatal("Agent Bug should be ready", created, err)
	}
	for _, incomplete := range []app.TaskInput{
		{Title: "缺少描述", Kind: "bug", Description: " ", Acceptance: []string{"结果正确"}, RequestID: "missing-description"},
		{Title: "缺少验收", Kind: "bug", Description: "实际行为偏离预期", Acceptance: []string{" "}, RequestID: "missing-acceptance"},
	} {
		if _, err := s.CreateTask(c, agent, p.ID, incomplete); err == nil {
			t.Fatal("incomplete Agent Bug created", incomplete)
		}
	}
	input.RequestID = "manual-bug"
	manual, err := s.CreateTask(c, app.Human(), p.ID, input)
	if err != nil || manual.Task.Status != "clarify" {
		t.Fatal("manual creation must retain its review flow", manual, err)
	}
}

func TestRequirementAttachmentsRevisionAndLegacyEdits(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	file, err := s.SaveAttachment(c, app.Human(), p.ID, "需求说明.md", []byte("# 详细说明\n附件中的业务规则"))
	if err != nil {
		t.Fatal(err)
	}
	input := app.TaskInput{Title: "图文需求", Kind: "requirement", Description: "## 背景\n\n**混合文档**", Acceptance: []string{"验证完整图文"}, AttachmentIDs: []string{file.ID}, RequestID: "rich-create"}
	created, err := s.CreateTask(c, app.Human(), p.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Task.Attachments) != 1 || created.Task.Attachments[0].Name != file.Name || created.Task.Attachments[0].Text != "" || created.Task.Attachments[0].Content != nil {
		t.Fatalf("bad metadata: %+v", created.Task.Attachments)
	}
	repeated, err := s.CreateTask(c, app.Human(), p.ID, input)
	if err != nil || repeated.Task.ID != created.Task.ID {
		t.Fatal("create retry not idempotent", err)
	}
	stored, err := s.Store.GetTask(c, created.Task.ID)
	if err != nil || len(stored.Attachments) != 1 || stored.Description != input.Description {
		t.Fatal("document not persisted", err)
	}
	// Old clients omit the new field. Metadata and the document revision survive.
	input.Version, input.RequestID, input.AttachmentIDs = stored.Version, "legacy-edit", nil
	edited, err := s.EditTask(c, app.Human(), stored.ID, input)
	if err != nil || len(edited.Task.Attachments) != 1 || edited.Task.Revision != stored.Revision {
		t.Fatal("legacy edit discarded files", err)
	}
	// Explicit [] has different semantics and must also have a different receipt fingerprint.
	input.AttachmentIDs = []string{}
	if _, err = s.EditTask(c, app.Human(), stored.ID, input); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("omitted and empty IDs reused same receipt", err)
	}
	input.Version, input.RequestID = edited.Task.Version, "remove-files"
	removed, err := s.EditTask(c, app.Human(), stored.ID, input)
	if err != nil || len(removed.Task.Attachments) != 0 || removed.Task.Revision != edited.Task.Revision+1 {
		t.Fatal("attachment removal did not revise requirement", err)
	}
	input.Version, input.RequestID, input.Kind = removed.Task.Version, "change-kind", "bug"
	bug, err := s.EditTask(c, app.Human(), stored.ID, input)
	if err != nil || bug.Task.Revision != removed.Task.Revision+1 {
		t.Fatal("type change did not revise requirement", err)
	}
	input.Version, input.RequestID = removed.Task.Version, "stale-document"
	if _, err = s.EditTask(c, app.Human(), stored.ID, input); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale document replaced latest revision", err)
	}
}

func TestRequirementAttachmentValidation(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	other, err := s.SaveProject(c, domain.Project{Name: "另一个项目"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := s.SaveAttachment(c, app.Human(), p.ID, "spec.md", []byte("规则"))
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.SaveAttachment(c, app.Human(), other.ID, "spec.md", []byte("另一个项目"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		ids       []string
		forbidden bool
	}{
		{"foreign", []string{foreign.ID}, true}, {"missing", []string{"att_missing"}, false}, {"duplicate", []string{file.ID, file.ID}, false},
		{"count", []string{"1", "2", "3", "4", "5", "6", "7"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "无效附件", AttachmentIDs: tc.ids, RequestID: tc.name})
			if err == nil || (tc.forbidden && !errors.Is(err, domain.ErrForbidden)) {
				t.Fatal("invalid association accepted", err)
			}
		})
	}
	// Validate aggregate metadata sizes without allocating large fixture files.
	var ids []string
	for n := 0; n < 3; n++ {
		a := domain.Attachment{ID: domain.ID("att_"), ProjectID: p.ID, Name: "large.txt", Size: 8 << 20, Kind: "document", Content: []byte("fixture")}
		if err := s.Store.PutAttachment(c, a); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}
	if _, err := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "超限", AttachmentIDs: ids, RequestID: "total-size"}); err == nil {
		t.Fatal("oversize attachments accepted")
	}
}
