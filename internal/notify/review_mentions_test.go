package notify_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/notify"
	"ai-pm-system/internal/notify/wecom"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestReviewMentionsPersistDeduplicateAndSendThroughWecom(t *testing.T) {
	c := context.Background()
	db, hub, _ := setup(t)
	var mu sync.Mutex
	sends := []map[string]any{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/cgi-bin/gettoken" {
			fmt.Fprint(w, `{"errcode":0,"access_token":"local-token","expires_in":7200}`)
			return
		}
		if r.URL.Path != "/cgi-bin/message/send" {
			t.Errorf("unexpected WeCom path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		mu.Lock()
		sends = append(sends, body)
		mu.Unlock()
		fmt.Fprint(w, `{"errcode":0,"errmsg":"ok","msgid":"local-message"}`)
	}))
	defer api.Close()
	notify.Register("wecom", wecom.Spec())
	if err := hub.Configure(c, "wecom", notify.Input{Enabled: true, Values: map[string]string{"corp_id": "local-corp", "agent_id": "1", "secret": "local-secret", "token": "local-token", "aes_key": "jWmYm7qr5nMoAUwZRjGtBxmz3KA1tkAj3ykkR6q2B2C", "api_base": api.URL}}, "admin"); err != nil {
		t.Fatal(err)
	}
	project(t, db, domain.ProjectMember{UserID: "local-owner", Roles: []string{"admin"}}, domain.ProjectMember{UserID: "mai", Roles: []string{"product"}}, domain.ProjectMember{UserID: "unbound", Roles: []string{"developer"}})
	v := task(t, db, "clarify")
	if err := db.PutUser(c, domain.User{ID: "mai", Name: "麦青"}); err != nil {
		t.Fatal(err)
	}
	if err := hub.Bind(c, "wecom", "mai", "wx_mai"); err != nil {
		t.Fatal(err)
	}
	service := &app.Service{Store: notify.Observed{Store: db, Hub: hub}}
	plain := app.RequirementReviewInput{Action: "message", RequestID: "plain", Body: "@麦青"}
	if _, err := service.SaveRequirementReview(c, app.Human(), v.ID, plain); err != nil {
		t.Fatal(err)
	}
	if err := hub.Reconcile(c, v.ID); err != nil {
		t.Fatal(err)
	}
	hub.Dispatch(c)
	mu.Lock()
	count := len(sends)
	mu.Unlock()
	if count != 0 {
		t.Fatal("ordinary @name sent a notification")
	}
	in := app.RequirementReviewInput{Action: "message", RequestID: "selected", Body: "@麦青 看看 @麦青", Mentions: []domain.ReviewMention{{UserID: "mai", Name: "麦青", Start: 0, End: 3}, {UserID: "mai", Name: "麦青", Start: 7, End: 10}}}
	for i := 0; i < 2; i++ {
		if _, err := service.SaveRequirementReview(c, app.Human(), v.ID, in); err != nil {
			t.Fatal(err)
		}
	}
	// Reconciliation recovers the committed input even without consuming Changed.
	for i := 0; i < 2; i++ {
		if err := hub.Reconcile(c, v.ID); err != nil {
			t.Fatal(err)
		}
	}
	queued, err := db.DueDeliveries(c, domain.Now(), 50)
	if err != nil || len(queued) != 1 || queued[0].Mention == nil {
		t.Fatalf("one delivery per message and user: %+v %v", queued, err)
	}
	if open, _ := db.OpenTodos(c, v.ID); len(open) != 0 {
		t.Fatalf("mentions must not invent pending work: %+v", open)
	}
	hub.Dispatch(c)
	if err := hub.Reconcile(c, v.ID); err != nil {
		t.Fatal(err)
	}
	hub.Dispatch(c)
	mu.Lock()
	defer mu.Unlock()
	if len(sends) != 1 || sends[0]["touser"] != "wx_mai" {
		t.Fatalf("WeCom sends: %+v", sends)
	}
	card := sends[0]["template_card"].(map[string]any)
	if card["card_type"] != "text_notice" || !strings.Contains(card["main_title"].(map[string]any)["title"].(string), "@ 了你") {
		t.Fatal(card)
	}
	link, err := url.Parse(card["card_action"].(map[string]any)["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	to, err := url.Parse(link.Query().Get("to"))
	if err != nil || to.Query().Get("task") != v.ID || to.Query().Get("review_message") != queued[0].Mention.MessageID {
		t.Fatalf("message deep link missing: %s %v", link, err)
	}
	failed := queued[0]
	failed.State = "failed"
	if err := db.PutDelivery(c, failed); err != nil {
		t.Fatal(err)
	}
	stored, err := db.GetRequirementReview(c, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	warnings := hub.MentionWarnings(c, stored.Messages[len(stored.Messages)-1])
	if len(warnings) != 1 || !strings.Contains(warnings[0], "推送失败") {
		t.Fatal(warnings)
	}
	if warnings := hub.MentionWarnings(c, domain.RequirementReviewMessage{Mentions: []domain.ReviewMention{{UserID: "unbound", Name: "未绑定成员"}}}); len(warnings) != 1 || !strings.Contains(warnings[0], "未绑定") {
		t.Fatal(warnings)
	}
}

func TestMentionDeliveryRechecksMembershipAndRetriesTransportFailure(t *testing.T) {
	c := context.Background()
	db, hub, _ := setup(t)
	configure(t, hub)
	p := project(t, db, domain.ProjectMember{UserID: "mai", Roles: []string{"product"}})
	v := task(t, db, "clarify")
	if err := hub.Bind(c, "fake", "mai", "wx_mai"); err != nil {
		t.Fatal(err)
	}
	review := domain.RequirementReview{TaskID: v.ID, ProjectID: p.ID, Version: 1, TaskRevision: 1, Status: "queued", Messages: []domain.RequirementReviewMessage{{ID: "rm_mention", Role: "user", ActorKind: "human", Actor: "产品", Action: "message", Body: "@麦青", At: domain.Now(), Mentions: []domain.ReviewMention{{UserID: "mai", Name: "麦青", Start: 0, End: 3}}}}}
	if err := db.CommitRequirementReview(c, review, 0, 1, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	// A restart can happen after the message committed and the task closed,
	// before Changed was consumed. The full pass must still recover the notice.
	v.Status, v.Version, v.UpdatedAt = "closed", v.Version+1, domain.Now()
	if err := db.CommitTask(c, v, v.Version-1, domain.Event{ProjectID: p.ID, TaskID: v.ID, Kind: "closed", At: v.UpdatedAt}, nil); err != nil {
		t.Fatal(err)
	}
	hub.ReconcileAll(c)
	queued, _ := db.DueDeliveries(c, domain.Now(), 50)
	if len(queued) != 1 {
		t.Fatal(queued)
	}
	fake.mu.Lock()
	fake.fail = fmt.Errorf("local channel unavailable")
	fake.mu.Unlock()
	hub.Dispatch(c)
	d, _ := db.GetDelivery(c, queued[0].ID)
	if d.State != "pending" || d.Attempts != 1 || d.NextAt <= domain.Now() {
		t.Fatalf("failed send not scheduled for retry: %+v", d)
	}
	// Being removed before the retry must prevent disclosing the review content.
	p.Memberships = []domain.ProjectMember{{UserID: "someone-else", Roles: []string{"admin"}}}
	if err := db.PutProject(c, p); err != nil {
		t.Fatal(err)
	}
	d.NextAt = 0
	if err := db.PutDelivery(c, d); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.fail = nil
	fake.mu.Unlock()
	hub.Dispatch(c)
	d, _ = db.GetDelivery(c, d.ID)
	if d.State != "dropped" {
		t.Fatal(d)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.sent) != 0 {
		t.Fatal("removed member received content")
	}
}
