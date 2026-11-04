package server

import (
	"ai-pm-system/internal/domain"
	"errors"
	"net/http"
	"strconv"
)

// reviewMessagePage is opt-in: older clients keep the full durable-room shape.
// A stable message ID anchors older pages, so appended replies cannot shift a
// user's history cursor. Domain evaluation still uses the complete stored room.
func reviewMessagePage(r *http.Request, v domain.RequirementReview) (any, error) {
	values := r.URL.Query()
	if !values.Has("message_limit") && !values.Has("before_message") {
		return v, nil
	}
	limit := 20
	var e error
	if values.Has("message_limit") {
		limit, e = strconv.Atoi(values.Get("message_limit"))
		if e != nil || limit < 1 || limit > 100 {
			return nil, errors.New("message_limit 为 1–100")
		}
	}
	end := len(v.Messages)
	before := values.Get("before_message")
	if len(before) > 200 {
		return nil, errors.New("无效的消息游标")
	}
	if before != "" {
		found := false
		for i, m := range v.Messages {
			if m.ID == before {
				end = i
				found = true
				break
			}
		}
		if !found {
			return nil, domain.ErrConflict
		}
	}
	start := max(0, end-limit)
	all := v.Messages
	v.Messages = append([]domain.RequirementReviewMessage{}, all[start:end]...)
	selected := map[string]bool{}
	for _, m := range v.Messages {
		selected[m.ID] = true
	}
	// Decisions, form activity and diff views retain their required context even
	// when that evidence is outside the visible history window.
	contexts := []domain.RequirementReviewMessage{}
	numbers := map[string]int{}
	proposal := 0
	latestProposal := -1
	add := func(m domain.RequirementReviewMessage) {
		if !selected[m.ID] {
			contexts = append(contexts, m)
			selected[m.ID] = true
		}
	}
	for i, m := range all {
		if m.Role == "assistant" && m.Result != nil && m.Result.Proposal != nil {
			proposal++
			latestProposal = i
			numbers[m.ID] = proposal
		}
		if m.Generation == v.Generation && (m.Result != nil || m.Action == "confirm" || m.Action == "confirm_rejection" || m.ReplyTo != "") {
			add(m)
		}
		if i >= start && i < end && m.Source != nil {
			for j := i - 1; j >= 0; j-- {
				if all[j].Source != nil {
					add(all[j])
					break
				}
			}
		}
	}
	if latestProposal >= 0 {
		add(all[latestProposal])
		for _, m := range all[latestProposal+1:] {
			if m.Action == "confirm" || m.Action == "confirm_rejection" {
				add(m)
			}
		}
	}
	order := map[string]int{}
	for i, m := range all {
		if selected[m.ID] {
			order[m.ID] = i
		}
	}
	cursor := ""
	if start < end {
		cursor = all[start].ID
	}
	return struct {
		domain.RequirementReview
		MessagePage struct {
			Total      int    `json:"total"`
			HasMore    bool   `json:"has_more"`
			NextBefore string `json:"next_before"`
		} `json:"message_page"`
		ContextMessages []domain.RequirementReviewMessage `json:"context_messages"`
		ProposalNumbers map[string]int                    `json:"proposal_numbers"`
		MessageOrder    map[string]int                    `json:"message_order"`
	}{RequirementReview: v, MessagePage: struct {
		Total      int    `json:"total"`
		HasMore    bool   `json:"has_more"`
		NextBefore string `json:"next_before"`
	}{len(all), start > 0, cursor}, ContextMessages: contexts, ProposalNumbers: numbers, MessageOrder: order}, nil
}
