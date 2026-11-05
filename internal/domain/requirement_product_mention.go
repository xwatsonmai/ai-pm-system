package domain

import "encoding/json"

const RequirementProductMentionAction = "notify_product"

func RequirementProductMentionID(task string) string { return "product_mention_" + task }

// CreatedRequirementProductMention reads the reminder saved atomically with
// creation. A review read never invents recipients from today's role settings.
func CreatedRequirementProductMention(e Event) (RequirementReviewMessage, bool) {
	var data struct {
		ProductMention *RequirementReviewMessage `json:"product_mention"`
	}
	if e.Kind != "created" || e.TaskID == "" || json.Unmarshal(e.Data, &data) != nil || data.ProductMention == nil {
		return RequirementReviewMessage{}, false
	}
	m := *data.ProductMention
	if m.ID != RequirementProductMentionID(e.TaskID) || m.Role != "system" || m.ActorKind != "platform" || m.ActorID != "system" || m.Action != RequirementProductMentionAction || m.At != e.At || len(m.Mentions) == 0 {
		return RequirementReviewMessage{}, false
	}
	return m, true
}
