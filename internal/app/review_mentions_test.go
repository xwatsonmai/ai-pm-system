package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"testing"
)

func TestReviewMentionsRequireSelectedProjectMembersAndSurviveRetry(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	for _, u := range []domain.User{{ID: "mai", Name: "麦青"}, {ID: "other", Name: "麦青"}} {
		if err := s.Store.PutUser(c, u); err != nil {
			t.Fatal(err)
		}
	}
	p.Memberships = []domain.ProjectMember{{UserID: "local-owner", Roles: []string{"admin"}}, {UserID: "mai", Roles: []string{"product"}}}
	if err := s.Store.PutProject(c, p); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateTask(c, app.Human(), p.ID, app.TaskInput{Title: "讨论提及", Description: "检查成员提及", RequestID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Task.ID
	plain, err := s.SaveRequirementReview(c, app.Human(), id, app.RequirementReviewInput{Action: "message", RequestID: "plain", Body: "@麦青 @123 a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Messages[len(plain.Messages)-1].Mentions) != 0 {
		t.Fatal("ordinary @text became a mention")
	}
	valid := app.RequirementReviewInput{Action: "message", RequestID: "mention", Body: "😀请 @麦青 看一下，@麦青", Mentions: []domain.ReviewMention{{UserID: "mai", Name: "麦青", Start: 4, End: 7}, {UserID: "mai", Name: "麦青", Start: 12, End: 15}}}
	// UTF-16 offsets include both code units of the leading emoji.
	v, err := s.SaveRequirementReview(c, app.Human(), id, valid)
	if err != nil {
		t.Fatal(err)
	}
	last := v.Messages[len(v.Messages)-1]
	if len(last.Mentions) != 2 || last.Mentions[0].UserID != "mai" {
		t.Fatalf("mentions not persisted: %+v", last)
	}
	for _, tc := range []struct {
		name     string
		mentions []domain.ReviewMention
	}{
		{"outside project", []domain.ReviewMention{{UserID: "other", Name: "麦青", Start: 4, End: 7}}},
		{"forged name", []domain.ReviewMention{{UserID: "mai", Name: "另一个人", Start: 4, End: 7}}},
		{"wrong offset", []domain.ReviewMention{{UserID: "mai", Name: "麦青", Start: 3, End: 6}}},
		{"negative offset", []domain.ReviewMention{{UserID: "mai", Name: "麦青", Start: -1, End: 2}}},
		{"outside body", []domain.ReviewMention{{UserID: "mai", Name: "麦青", Start: 4, End: 99}}},
		{"overlap", []domain.ReviewMention{{UserID: "mai", Name: "麦青", Start: 4, End: 7}, {UserID: "mai", Name: "麦青", Start: 4, End: 7}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := valid
			in.RequestID = tc.name
			in.Mentions = tc.mentions
			if _, err := s.SaveRequirementReview(c, app.Human(), id, in); err == nil {
				t.Fatal("invalid mention accepted")
			}
		})
	}
	changed := valid
	changed.Mentions = nil
	if _, err = s.SaveRequirementReview(c, app.Human(), id, changed); !errors.Is(err, domain.ErrRequestReused) {
		t.Fatalf("receipt ignored mention metadata: %v", err)
	}
	p.Memberships = p.Memberships[:1]
	if err = s.Store.PutProject(c, p); err != nil {
		t.Fatal(err)
	}
	again, err := s.SaveRequirementReview(c, app.Human(), id, valid)
	if err != nil || len(again.Messages) != len(v.Messages) {
		t.Fatalf("retry after membership change duplicated/lost saved input: %v", err)
	}
	valid.RequestID = "removed"
	if _, err = s.SaveRequirementReview(c, app.Human(), id, valid); err == nil {
		t.Fatal("removed member accepted")
	}
}

func TestDevelopmentAlignmentKeepsSelectedMentionIdentities(t *testing.T) {
	s, developer, mutation := alignmentTask(t)
	c := context.Background()
	mutation = askAlignment(t, s, developer, mutation)
	p, err := s.Store.GetProject(c, mutation.Task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.PutUser(c, domain.User{ID: "mai", Name: "麦青"}); err != nil {
		t.Fatal(err)
	}
	p.Memberships = append(domain.ProjectMembers(p), domain.ProjectMember{UserID: "mai", Roles: []string{"product"}})
	if err = s.Store.PutProject(c, p); err != nil {
		t.Fatal(err)
	}
	v, err := s.RequirementReview(c, app.Human(), mutation.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.SaveRequirementReview(c, app.Human(), mutation.Task.ID, app.RequirementReviewInput{Action: "message", RequestID: "alignment-mention", Generation: v.Generation, Body: "@麦青 请确认", Mentions: []domain.ReviewMention{{UserID: "mai", Name: "麦青", Start: 0, End: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	last := v.Messages[len(v.Messages)-1]
	if last.Action != "alignment_reply" || len(last.Mentions) != 1 || last.Mentions[0].UserID != "mai" {
		t.Fatalf("alignment lost selected identity: %+v", last)
	}
}
