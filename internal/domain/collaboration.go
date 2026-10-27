package domain

// Collaboration carries only the explicitly shared investigation brief. The
// original private conversation is never included in the developer work inbox.
type Collaboration struct {
	ID             string                `json:"id"`
	ProjectID      string                `json:"project_id"`
	TaskID         string                `json:"task_id,omitempty"`
	ConversationID string                `json:"conversation_id,omitempty"`
	OriginRunID    string                `json:"origin_run_id,omitempty"`
	OwnerID        string                `json:"owner_id,omitempty"`
	OwnerKind      string                `json:"owner_kind,omitempty"`
	Title          string                `json:"title"`
	Brief          string                `json:"brief"`
	Environment    string                `json:"environment"`
	Scope          string                `json:"scope"`
	Status         string                `json:"status"`
	Version        int                   `json:"version"`
	Claimant       string                `json:"claimant,omitempty"`
	ClaimantName   string                `json:"claimant_name,omitempty"`
	ClaimHash      string                `json:"-"`
	LeaseUntil     int64                 `json:"lease_until,omitempty"`
	Updates        []CollaborationUpdate `json:"updates"`
	Result         *InvestigationResult  `json:"result,omitempty"`
	CreatedAt      int64                 `json:"created_at"`
	UpdatedAt      int64                 `json:"updated_at"`
}

type CollaborationUpdate struct {
	Kind  string `json:"kind"`
	Body  string `json:"body"`
	Actor string `json:"actor"`
	At    int64  `json:"at"`
}

type InvestigationResult struct {
	ID                string `json:"id"`
	Outcome           string `json:"outcome"`
	Summary           string `json:"summary"`
	Evidence          string `json:"evidence"`
	Environment       string `json:"environment"`
	ObservedAt        string `json:"observed_at"`
	DeploymentVersion string `json:"deployment_version"`
	Limitations       string `json:"limitations"`
	Actor             string `json:"actor"`
	At                int64  `json:"at"`
}

type CollaborationMutation struct {
	Collaboration Collaboration `json:"collaboration"`
	ClaimID       string        `json:"claim_id,omitempty"`
}

// ReplyJob is a transactional outbox entry, independent of investigation state.
// A stable RunID makes retries publish at most one visible answer.
type ReplyJob struct {
	ID              string `json:"id"`
	CollaborationID string `json:"collaboration_id"`
	ProjectID       string `json:"project_id"`
	RunID           string `json:"run_id"`
	Status          string `json:"status"`
	Version         int    `json:"version"`
	Attempts        int    `json:"attempts"`
	NextAt          int64  `json:"next_at"`
	Error           string `json:"error,omitempty"`
}

type CollaborationView struct {
	Collaboration
	Reply *ReplyJob `json:"reply,omitempty"`
}
