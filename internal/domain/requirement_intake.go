package domain

// RequirementIntake is a private conversation suggestion, not a task. Resolving
// it creates the shared task and records its identity in the same transaction.
type RequirementIntake struct {
	ID             string       `json:"id"`
	ProjectID      string       `json:"project_id"`
	ConversationID string       `json:"conversation_id"`
	OriginRunID    string       `json:"origin_run_id"`
	OwnerID        string       `json:"owner_id"`
	Version        int          `json:"version"`
	Status         string       `json:"status"`
	Title          string       `json:"title"`
	Kind           string       `json:"kind"`
	Description    string       `json:"description"`
	Acceptance     []string     `json:"acceptance"`
	Attachments    []Attachment `json:"attachments"`
	Reason         string       `json:"reason"`
	BugDetails     *BugDetails  `json:"bug_details,omitempty"`
	MissingDetails []string     `json:"missing_details"`
	TaskID         string       `json:"task_id,omitempty"`
	ResolvedRunID  string       `json:"resolved_run_id,omitempty"`
	CreatedAt      int64        `json:"created_at"`
	UpdatedAt      int64        `json:"updated_at"`
}

// BugDetails records business facts, not a required technical root cause.
type BugDetails struct {
	Context      string `json:"context"`
	Reproduction string `json:"reproduction"`
	Actual       string `json:"actual"`
	Expected     string `json:"expected"`
	Scope        string `json:"scope"`
}
