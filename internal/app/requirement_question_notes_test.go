package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestQuestionNotesPersistIndependentlyOfChoices(t *testing.T) {
	s, p := setup(t)
	c, a := context.Background(), app.Human()
	created, err := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "逐题补充", Description: "需求原文", RequestID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	taskID := created.Task.ID
	v, err := s.SaveRequirementReview(c, a, taskID, app.RequirementReviewInput{Action: "start", RequestID: "start"})
	if err != nil {
		t.Fatal(err)
	}
	result := &domain.RequirementReviewResult{Body: "请说明范围", Decision: "clarify", Questions: []domain.RequirementQuestion{
		{ID: "scope", Label: "范围", Type: "radio", Options: []string{"全部", "部分"}},
		{ID: "roles", Label: "角色", Type: "multiselect", Options: []string{"客服", "财务"}},
		{ID: "channel", Label: "渠道", Type: "select", Options: []string{"原路退回", "余额"}},
		{ID: "name", Label: "名称", Type: "text"},
	}}
	if err = s.FinishRequirementReview(c, taskID, v.Generation, "first", result, ""); err != nil {
		t.Fatal(err)
	}
	v, _ = s.RequirementReview(c, a, taskID)
	parent := v.Messages[1].ID
	var data bytes.Buffer
	_ = png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	saveImage := func(project string) domain.Attachment {
		t.Helper()
		file, e := s.SaveAttachment(c, a, project, "截图.png", data.Bytes())
		if e != nil {
			t.Fatal(e)
		}
		return file
	}
	photo := saveImage(p.ID)
	input := app.RequirementReviewInput{Action: "message", ReplyTo: parent, RequestID: "notes", QuestionNotes: map[string]app.RequirementQuestionNoteInput{
		"scope":   {Body: "选项都不合适，只处理历史订单。", AttachmentIDs: []string{photo.ID}},
		"roles":   {Body: "仅值班客服可以操作。"},
		"channel": {AttachmentIDs: []string{photo.ID}},
	}}
	for i := 0; i < 2; i++ {
		v, err = s.SaveRequirementReview(c, a, taskID, input)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(v.Messages) != 3 {
		t.Fatalf("duplicated notes: %+v", v.Messages)
	}
	m := v.Messages[2]
	if len(m.Answers) != 0 || m.Body != "" || m.ReplyTo != parent || len(m.QuestionNotes) != 3 || len(m.Attachments) != 0 {
		t.Fatalf("notes treated as a selection: %+v", m)
	}
	if m.QuestionNotes["scope"].Attachments[0].ID != photo.ID || len(m.QuestionNotes["scope"].Attachments[0].Content) != 0 {
		t.Fatal("image association/metadata lost")
	}
	if err = s.FinishRequirementReview(c, taskID, v.Generation, "", nil, "模型失败"); err != nil {
		t.Fatal(err)
	}
	loaded, _ := s.RequirementReview(c, a, taskID)
	if loaded.Status != "failed" || loaded.Messages[2].QuestionNotes["scope"].Body != input.QuestionNotes["scope"].Body {
		t.Fatal("failure lost notes")
	}
	detail, err := s.RequirementDetail(c, a, p.ID, app.RequirementDetailQuery{TaskID: taskID})
	if err != nil || len(detail.Review.LatestSupplement.QuestionNotes) != 3 {
		t.Fatal("latest supplement omitted notes", err)
	}

	other, _ := s.SaveProject(c, domain.Project{Name: "其他项目"})
	// Validate note contents against a fresh form, not the expired first round.
	v, err = s.SaveRequirementReview(c, a, taskID, app.RequirementReviewInput{Action: "retry", RequestID: "validation-round"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishRequirementReview(c, taskID, v.Generation, "validation", result, ""); err != nil {
		t.Fatal(err)
	}
	v, _ = s.RequirementReview(c, a, taskID)
	parent = v.Messages[len(v.Messages)-1].ID
	foreign := saveImage(other.ID)
	doc, err := s.SaveAttachment(c, a, p.ID, "规则.md", []byte("说明"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, reply, id string
		note            app.RequirementQuestionNoteInput
		forbidden       bool
	}{
		{name: "missing parent", id: "scope", note: app.RequirementQuestionNoteInput{Body: "说明"}},
		{name: "unknown question", reply: parent, id: "other", note: app.RequirementQuestionNoteInput{Body: "说明"}},
		{name: "input question", reply: parent, id: "name", note: app.RequirementQuestionNoteInput{Body: "说明"}},
		{name: "foreign image", reply: parent, id: "scope", note: app.RequirementQuestionNoteInput{AttachmentIDs: []string{foreign.ID}}, forbidden: true},
		{name: "document", reply: parent, id: "scope", note: app.RequirementQuestionNoteInput{AttachmentIDs: []string{doc.ID}}},
		{name: "oversize text", reply: parent, id: "scope", note: app.RequirementQuestionNoteInput{Body: strings.Repeat("a", 500001)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, e := s.SaveRequirementReview(c, a, taskID, app.RequirementReviewInput{Action: "message", RequestID: tc.name, ReplyTo: tc.reply, QuestionNotes: map[string]app.RequirementQuestionNoteInput{tc.id: tc.note}})
			if e == nil || (tc.forbidden && !errors.Is(e, domain.ErrForbidden)) {
				t.Fatalf("invalid note accepted: %v", e)
			}
		})
	}
	images := []string{photo.ID}
	for i := 0; i < 5; i++ {
		images = append(images, saveImage(p.ID).ID)
	}
	if _, err = s.SaveRequirementReview(c, a, taskID, app.RequirementReviewInput{Action: "message", RequestID: "combined limit", ReplyTo: parent, AttachmentIDs: []string{doc.ID}, QuestionNotes: map[string]app.RequirementQuestionNoteInput{"scope": {AttachmentIDs: images}}}); err == nil {
		t.Fatal("per-question attachments bypassed message total")
	}
	// Retry the failed round before supplying a new assessment.
	next, err := s.SaveRequirementReview(c, a, taskID, app.RequirementReviewInput{Action: "retry", RequestID: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishRequirementReview(c, taskID, next.Generation, "second", result, ""); err != nil {
		t.Fatal(err)
	}
	next, _ = s.RequirementReview(c, a, taskID)
	parent2 := next.Messages[len(next.Messages)-1].ID
	next, err = s.SaveRequirementReview(c, a, taskID, app.RequirementReviewInput{Action: "message", RequestID: "round2", ReplyTo: parent2, QuestionNotes: map[string]app.RequirementQuestionNoteInput{"scope": {Body: "第二轮的新说明"}}})
	if err != nil {
		t.Fatal(err)
	}
	if next.Messages[2].QuestionNotes["scope"].Body != input.QuestionNotes["scope"].Body || next.Messages[len(next.Messages)-1].QuestionNotes["scope"].Body != "第二轮的新说明" {
		t.Fatal("same question ID crossed rounds")
	}
}

// Existing browser retries must still match receipts written before question_notes existed.
func TestReviewNotesPreserveLegacyRetryFingerprint(t *testing.T) {
	s, p := setup(t)
	c, a := context.Background(), app.Human()
	created, err := s.CreateTask(c, a, p.ID, app.TaskInput{Title: "兼容旧重试", RequestID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	in := app.RequirementReviewInput{Action: "message", RequestID: "legacy", Body: "旧版本发送的消息"}
	first, err := s.SaveRequirementReview(c, a, created.Task.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	oldJSON := fmt.Sprintf(`{"ID":%q,"Input":{"action":"message","request_id":"legacy","body":"旧版本发送的消息","reply_to":"","answers":null,"attachment_ids":null,"generation":0}}`, created.Task.ID)
	receipt, err := s.Store.GetReceipt(c, "requirement-review:"+a.ID+":"+p.ID+":legacy")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Fingerprint != app.Hash(oldJSON) {
		t.Fatal("new optional field invalidated a legacy retry")
	}
	in.QuestionNotes = map[string]app.RequirementQuestionNoteInput{}
	retry, err := s.SaveRequirementReview(c, a, created.Task.ID, in)
	if err != nil || retry.Version != first.Version {
		t.Fatal("empty notes changed retry identity", err)
	}
}
