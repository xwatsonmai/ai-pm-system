package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"testing"
)

func TestAttachmentAccessUsesBindingsBeforeUploader(t *testing.T) {
	s, p, owner, member := accessFixture(t)
	c := context.Background()
	var err error
	p, err = s.SetProjectMemberAccess(c, owner, p.ID, member.ID, []string{"developer"}, []string{}, false, p.AccessVersion)
	if err != nil {
		t.Fatal(err)
	}
	file, err := s.SaveAttachment(c, member, p.ID, "spec.txt", []byte("task document"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachmentAs(c, member, p.ID, file.ID); err != nil {
		t.Fatal("uploader cannot preview draft", err)
	}
	if _, err = s.AttachmentAs(c, owner, p.ID, file.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("another project member read an unbound draft", err)
	}
	if _, err = s.CreateTask(c, owner, p.ID, app.TaskInput{Title: "Steal draft", AttachmentIDs: []string{file.ID}, RequestID: "steal"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("draft ID was reused by another member", err)
	}
	if _, err = s.CreateTask(c, member, p.ID, app.TaskInput{Title: "Spec", AttachmentIDs: []string{file.ID}, RequestID: "bind"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachmentAs(c, owner, p.ID, file.ID); err != nil {
		t.Fatal("task member cannot read a bound attachment", err)
	}
	p, _ = s.Store.GetProject(c, p.ID)
	p, err = s.SaveProjectRole(c, owner, p.ID, "", app.RoleInput{Name: "Chat only", Pages: []string{"chat"}, Version: p.AccessVersion})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.SetProjectMemberAccess(c, owner, p.ID, member.ID, []string{p.ProjectRoles[len(p.ProjectRoles)-1].ID}, []string{}, false, p.AccessVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachmentAs(c, member, p.ID, file.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("former task uploader kept bound-file access through chat", err)
	}
	private, err := s.SaveAttachment(c, member, p.ID, "private.txt", []byte("private conversation"))
	if err != nil {
		t.Fatal(err)
	}
	r := domain.Run{ID: "private-run", ProjectID: p.ID, Kind: "question", ConversationID: "private-conversation", OwnerID: member.ID, OwnerKind: member.Kind, Attachments: []domain.Attachment{private}}
	if err = s.Store.PutRun(c, r); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachmentAs(c, member, p.ID, private.ID); err != nil {
		t.Fatal("own conversation file denied", err)
	}
	if _, err = s.AttachmentAs(c, owner, p.ID, private.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("project administrator read another member's private file", err)
	}
	if _, err = s.CreateTask(c, owner, p.ID, app.TaskInput{Title: "Steal private file", AttachmentIDs: []string{private.ID}, RequestID: "steal-private"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("private-chat ID was copied into a shared task", err)
	}
	if err = s.Store.PutConversation(c, domain.Conversation{ID: r.ConversationID, ProjectID: p.ID, DeletedAt: domain.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachmentAs(c, member, p.ID, private.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("deleted conversation became an uploader-readable draft", err)
	}
}

func TestLegacyAttachmentRequiresRealBindingOrRecordedUploader(t *testing.T) {
	s, p, owner, _ := accessFixture(t)
	c := context.Background()
	legacy := domain.Attachment{ID: "legacy", ProjectID: p.ID, Name: "old.txt", Kind: "document", Content: []byte("old bytes"), Text: "old bytes"}
	if err := s.Store.PutAttachment(c, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachmentAs(c, owner, p.ID, legacy.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("unowned legacy draft opened to project members", err)
	}
	created, err := s.CreateTask(c, owner, p.ID, app.TaskInput{Title: "Legacy bound file", RequestID: "legacy-task"})
	if err != nil {
		t.Fatal(err)
	}
	task := created.Task
	task.Attachments = []domain.Attachment{legacy}
	task.Version++
	if err = s.Store.CommitTask(c, task, created.Task.Version, domain.Event{ProjectID: p.ID, TaskID: task.ID, Kind: "fixture"}, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := s.AttachmentAs(c, owner, p.ID, legacy.ID); err != nil || string(got.Content) != "old bytes" {
		t.Fatal("real pre-upgrade task attachment lost", err)
	}
	personal := legacy
	personal.ID = "old-personal-upload"
	personal.Authorization = &domain.TaskActionAuthorization{Source: "mcp", UserID: owner.ID}
	if err = s.Store.PutAttachment(c, personal); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachmentAs(c, owner, p.ID, personal.ID); err != nil {
		t.Fatal("recorded legacy personal uploader lost draft access", err)
	}
}
