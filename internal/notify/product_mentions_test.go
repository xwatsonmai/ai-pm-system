package notify_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/notify"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAutomaticProductMentionRecoversBeforeReviewAndSendsOnce(t *testing.T) {
	c := context.Background()
	db, hub, _ := setup(t)
	configure(t, hub)
	p := notificationAccessFixture()
	p.ProjectRoles[0].Actions = append(p.ProjectRoles[0].Actions, domain.ActionCreateRequirement)
	p.Memberships[0].Responsibilities = []string{}
	if err := db.PutProject(c, p); err != nil {
		t.Fatal(err)
	}
	if err := db.PutUser(c, domain.User{ID: "manager", Name: "产品甲"}); err != nil {
		t.Fatal(err)
	}
	if err := hub.Bind(c, "fake", "manager", "wx_product"); err != nil {
		t.Fatal(err)
	}
	s := &app.Service{Store: notify.Observed{Store: db, Hub: hub}}
	in := app.TaskInput{Title: "运营新需求", Description: "需求原文", Acceptance: []string{"验收条件"}, RequestID: "create"}
	for i := 0; i < 2; i++ {
		result, err := s.CreateTask(c, domain.Actor{ID: "operator", Kind: "human", Name: "运营成员"}, p.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.GetRequirementReview(c, result.Task.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatal("notification started an Agent review as a side effect", err)
		}
	}
	// Recreate the hub after commit, without consuming the original Changed.
	hub = &notify.Hub{Store: db, Data: db, Vault: hub.Vault, PublicURL: "https://pm.example.com/"}
	configure(t, hub)
	for i := 0; i < 2; i++ {
		hub.ReconcileAll(c)
	}
	queued, err := db.DueDeliveries(c, domain.Now(), 50)
	if err != nil || len(queued) != 1 || queued[0].Mention == nil || queued[0].Mention.Action != domain.RequirementProductMentionAction {
		t.Fatal("creation was not recovered as one product notice", queued, err)
	}
	if open, _ := db.OpenTodos(c, ""); len(open) != 0 {
		t.Fatal("automatic @ manufactured a pending review decision", open)
	}
	fake.mu.Lock()
	fake.fail = errors.New("temporary delivery failure")
	fake.mu.Unlock()
	hub.Dispatch(c)
	delivery, err := db.GetDelivery(c, queued[0].ID)
	if err != nil || delivery.State != "pending" || delivery.Attempts != 1 {
		t.Fatal("transport failure was not retryable", delivery, err)
	}
	delivery.NextAt = 0
	if err = db.PutDelivery(c, delivery); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.fail = nil
	fake.mu.Unlock()
	hub.Dispatch(c)
	for i := 0; i < 2; i++ {
		hub.ReconcileAll(c)
		hub.Dispatch(c)
	}
	delivery, err = db.GetDelivery(c, delivery.ID)
	if err != nil || delivery.State != "sent" || delivery.Attempts != 1 {
		t.Fatal("reconciliation reset the sent reminder or its failure count", delivery, err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	card := fake.sent["wx_product"]
	if len(fake.sent) != 1 || card.Title != "有新需求，请产品协助评审" || !strings.Contains(card.URL, "review_message") || !strings.Contains(card.Fields[0].Value, "@产品甲") {
		t.Fatal("product reminder card or message deep link is missing", fake.sent)
	}
}

func TestAutomaticProductMentionRechecksDutyAndPageBeforeSending(t *testing.T) {
	for _, revoke := range []string{"duty", "page", "membership"} {
		t.Run(revoke, func(t *testing.T) {
			c := context.Background()
			db, hub, _ := setup(t)
			configure(t, hub)
			p := notificationAccessFixture()
			p.ProjectRoles[0].Actions = append(p.ProjectRoles[0].Actions, domain.ActionCreateRequirement)
			if err := db.PutProject(c, p); err != nil {
				t.Fatal(err)
			}
			if err := hub.Bind(c, "fake", "manager", "wx_product"); err != nil {
				t.Fatal(err)
			}
			s := &app.Service{Store: db}
			result, err := s.CreateTask(c, domain.Actor{ID: "operator", Kind: "human"}, p.ID, app.TaskInput{Title: "新需求", RequestID: "create"})
			if err != nil {
				t.Fatal(err)
			}
			if err = hub.Reconcile(c, result.Task.ID); err != nil {
				t.Fatal(err)
			}
			queued, _ := db.DueDeliveries(c, domain.Now(), 50)
			if len(queued) != 1 {
				t.Fatal(queued)
			}
			switch revoke {
			case "duty":
				p.Memberships[1].Responsibilities = []string{}
			case "page":
				p.ProjectRoles[1].Pages = []string{"reviews", "versions"}
			case "membership":
				p.Memberships = append(p.Memberships[:1], p.Memberships[2:]...)
			}
			if err = db.PutProject(c, p); err != nil {
				t.Fatal(err)
			}
			hub.Dispatch(c)
			delivery, err := db.GetDelivery(c, queued[0].ID)
			if err != nil || delivery.State != "dropped" {
				t.Fatal("revoked product recipient retained a pending message", delivery, err)
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if len(fake.sent) != 0 {
				t.Fatal("revoked recipient received requirement content", fake.sent)
			}
		})
	}
}
