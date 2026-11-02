package domain

// RequirementChange preserves the submitted baseline while the same requirement
// is re-reviewed. Previous rounds remain in the review's immutable snapshots.
type RequirementChange struct {
	ID                  string `json:"id"`
	Status              string `json:"status"` // reviewing, applied, canceled
	FromStatus          string `json:"from_status"`
	BaseRevision        int    `json:"base_revision"`
	AppliedRevision     int    `json:"applied_revision,omitempty"`
	Branch              string `json:"branch"`
	SHA                 string `json:"sha"`
	SourceBugID         string `json:"source_bug_id,omitempty"`
	SourceBugVersion    int    `json:"source_bug_version,omitempty"`
	SourceReviewVersion int    `json:"source_review_version,omitempty"`
	Reason              string `json:"reason"`
	Actor               string `json:"actor"`
	At                  int64  `json:"at"`
	ConfirmedBy         string `json:"confirmed_by,omitempty"`
	ConfirmedAt         int64  `json:"confirmed_at,omitempty"`
}

func (t Task) ReviewingChange() bool {
	return t.RequirementChange != nil && t.RequirementChange.Status == "reviewing"
}

func (t Task) DevelopingChange() bool {
	return t.RequirementChange != nil && t.RequirementChange.Status == "applied" && t.RequirementChange.AppliedRevision == t.Revision
}

func RequirementChangeAllowed(status string) bool {
	return RequirementBugReporting(status) || status == "pending_release"
}
