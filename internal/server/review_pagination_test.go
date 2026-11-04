package server

import (
	"ai-pm-system/internal/domain"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
)

func TestReviewMessageWindowStableCursorAndDecisionContext(t *testing.T) {
	v := domain.RequirementReview{TaskID: "t", Version: 4, Generation: 44, Messages: []domain.RequirementReviewMessage{}}
	for i := 0; i < 45; i++ {
		v.Messages = append(v.Messages, domain.RequirementReviewMessage{ID: fmt.Sprintf("m%02d", i), At: 100, Generation: i, Role: "user", Body: fmt.Sprintf("message %d", i)})
	}
	v.Messages[2].Role = "assistant"
	v.Messages[2].Result = &domain.RequirementReviewResult{Proposal: &domain.RequirementProposal{}}
	v.Messages[3].Action = "confirm"
	v.Messages[20].Source = &domain.Task{ID: "t", Revision: 1, Description: "before"}
	v.Messages[30].Source = &domain.Task{ID: "t", Revision: 2, Description: "after"}
	read := func(path string) map[string]json.RawMessage {
		t.Helper()
		out, e := reviewMessagePage(httptest.NewRequest("GET", path, nil), v)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := json.Marshal(out)
		if e != nil {
			t.Fatal(e)
		}
		parsed := map[string]json.RawMessage{}
		if e = json.Unmarshal(raw, &parsed); e != nil {
			t.Fatal(e)
		}
		return parsed
	}
	first := read("/?message_limit=20")
	var messages, contexts []domain.RequirementReviewMessage
	json.Unmarshal(first["messages"], &messages)
	json.Unmarshal(first["context_messages"], &contexts)
	if len(messages) != 20 || messages[0].ID != "m25" || messages[19].ID != "m44" {
		t.Fatal(messages)
	}
	required := map[string]bool{"m02": false, "m03": false, "m20": false}
	for _, m := range contexts {
		if _, ok := required[m.ID]; ok {
			required[m.ID] = true
		}
	}
	for id, found := range required {
		if !found {
			t.Fatal("missing decision/diff context", id)
		}
	}
	var page struct {
		Total      int    `json:"total"`
		HasMore    bool   `json:"has_more"`
		NextBefore string `json:"next_before"`
	}
	json.Unmarshal(first["message_page"], &page)
	if page.Total != 45 || !page.HasMore || page.NextBefore != "m25" {
		t.Fatal(page)
	}
	v.Messages = append(v.Messages, domain.RequirementReviewMessage{ID: "new", At: 101, Role: "user"})
	older := read("/?message_limit=20&before_message=m25")
	json.Unmarshal(older["messages"], &messages)
	if len(messages) != 20 || messages[0].ID != "m05" || messages[19].ID != "m24" {
		t.Fatal("cursor shifted after append", messages)
	}
	legacy := read("/")
	json.Unmarshal(legacy["messages"], &messages)
	if len(messages) != 46 || legacy["message_page"] != nil {
		t.Fatal("legacy response changed")
	}
	for _, query := range []string{"/?message_limit=0", "/?message_limit=101", "/?message_limit=bad", "/?message_limit=20&before_message=missing"} {
		if _, e := reviewMessagePage(httptest.NewRequest("GET", query, nil), v); e == nil {
			t.Fatal("invalid cursor accepted", query)
		}
	}
}
