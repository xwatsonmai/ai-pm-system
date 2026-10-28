package domain

import "slices"

// RequirementReviewSummary keeps pool polling independent of review documents.
type RequirementReviewSummary struct {
	TaskID       string `json:"task_id"`
	TaskRevision int    `json:"task_revision"`
	Status       string `json:"status"`
}

// RequirementReview is the durable conversation before a requirement is ready.
// It is separate from Review, which records delivery acceptance.
type RequirementReview struct {
	Mode                        string                     `json:"mode,omitempty"`
	ArchivedAlignmentGeneration int                        `json:"archived_alignment_generation,omitempty"`
	ConversationID              string                     `json:"conversation_id,omitempty"`
	QueuedAt                    int64                      `json:"queued_at,omitempty"`
	Evidence                    *RequirementReviewEvidence `json:"evidence,omitempty"`
	NeedsRecheck                bool                       `json:"needs_recheck,omitempty"`
	RecheckReason               string                     `json:"recheck_reason,omitempty"`
	Compaction                  *RunCompaction             `json:"compaction,omitempty"`
	IncomingRelations           []RequirementRelation      `json:"incoming_relations,omitempty"`
	// Creator is derived on read; older tasks fall back to the creation event's actor name.
	Creator *ActorRef `json:"creator,omitempty"`
	// Progress is derived on read while a round runs: what the Agent is doing.
	Progress []RequirementReviewProgress `json:"progress,omitempty"`
	// Agenda is the Agent's latest full topic list, kept across rounds.
	Agenda       []RequirementTopic         `json:"agenda,omitempty"`
	TaskID       string                     `json:"task_id"`
	ProjectID    string                     `json:"project_id"`
	Version      int                        `json:"version"`
	Generation   int                        `json:"generation"`
	TaskRevision int                        `json:"task_revision"`
	Status       string                     `json:"status"`
	Error        string                     `json:"error,omitempty"`
	RunID        string                     `json:"run_id,omitempty"`
	Source       Task                       `json:"source"`
	Messages     []RequirementReviewMessage `json:"messages"`
	UpdatedAt    int64                      `json:"updated_at"`
}

type RequirementReviewMessage struct {
	Authorization     *TaskActionAuthorization           `json:"authorization,omitempty"`
	ID                string                             `json:"id"`
	Role              string                             `json:"role"`
	Body              string                             `json:"body"`
	Actor             string                             `json:"actor,omitempty"`
	ActorID           string                             `json:"actor_id,omitempty"`
	ActorKind         string                             `json:"actor_kind,omitempty"` // human, platform or dev_agent
	Action            string                             `json:"action,omitempty"`     // message or confirm for human input; event type for system messages
	Event             *RequirementReviewEvent            `json:"event,omitempty"`
	Generation        int                                `json:"generation"`
	At                int64                              `json:"at"`
	ReplyTo           string                             `json:"reply_to,omitempty"`
	Answers           map[string][]string                `json:"answers,omitempty"`
	QuestionNotes     map[string]RequirementQuestionNote `json:"question_notes,omitempty"`
	Attachments       []Attachment                       `json:"attachments,omitempty"`
	Result            *RequirementReviewResult           `json:"result,omitempty"`
	FormExpiredReason string                             `json:"form_expired_reason,omitempty"`
	FormExpiredAt     int64                              `json:"form_expired_at,omitempty"`
	// Preserve original documents and later revisions independently of AI proposals.
	Source *Task `json:"source,omitempty"`
}

type RequirementReviewProgress struct {
	Label  string `json:"label"`
	Status string `json:"status"` // running, done or failed
	At     int64  `json:"at"`
}

// RequirementReviewEvent holds the details of a system event message, whose
// Body already reads as a full sentence for clients that only show text.
type RequirementReviewEvent struct {
	FromRevision int    `json:"from_revision,omitempty"`
	ToRevision   int    `json:"to_revision,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// RequirementReviewNote is appended to an existing review in the same
// transaction as the task write that caused it. A Source snapshot on the
// message is replaced by the task as committed.
type RequirementReviewNote struct {
	Message     RequirementReviewMessage
	ExpireForms string // non-empty retires open forms with this reason
}

// ExpireForms retires question forms. Nil ids retire every open form; explicit
// ids also record the Agent's reason on a form that had already expired.
func (v *RequirementReview) ExpireForms(ids []string, reason string, at int64) {
	for i := range v.Messages {
		m := &v.Messages[i]
		if m.Role != "assistant" || m.Result == nil || len(m.Result.Questions) == 0 || (ids != nil && !slices.Contains(ids, m.ID)) {
			continue
		}
		if m.FormExpiredAt == 0 {
			m.FormExpiredAt = at
			m.FormExpiredReason = reason
		} else if ids != nil {
			m.FormExpiredReason = reason
		}
	}
}

// A question note belongs to ReplyTo + question ID, independently of selections.
type RequirementQuestionNote struct {
	Body        string       `json:"body,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

type RequirementReviewResult struct {
	Relations []RequirementRelation `json:"relations,omitempty"`
	Body      string                `json:"body"`
	Analysis  string                `json:"analysis,omitempty"`
	Decision  string                `json:"decision"` // clarify, ready, infeasible, reject_bug (linked testing Bugs only)
	Agenda    []RequirementTopic    `json:"agenda,omitempty"`
	// Ask is the round's single question. Form questions stay in Questions so
	// answer links, notes and expiry work for every round.
	Ask           *RequirementAsk       `json:"ask,omitempty"`
	Questions     []RequirementQuestion `json:"questions"`
	Proposal      *RequirementProposal  `json:"proposal,omitempty"`
	ExpireFormIDs []string              `json:"expire_form_ids,omitempty"`
}

// Evidence records what was actually read, independently of the model's memory.
type RequirementReviewEvidence struct {
	CatalogFingerprint string `json:"catalog_fingerprint"`
	// Catalog lets a recheck name the requirement that changed. It is nil only
	// in older evidence, which has just the fingerprint.
	Catalog    []RequirementCatalogEntry    `json:"catalog"`
	References []RequirementReviewReference `json:"references"`
}

type RequirementCatalogEntry struct {
	TaskID string `json:"task_id"`
	Scope  string `json:"scope"` // short digest of revision, title, description and acceptance
	Ended  bool   `json:"ended,omitempty"`
}

type RequirementReviewReference struct {
	Title         string `json:"title,omitempty"`
	TaskID        string `json:"task_id"`
	Revision      int    `json:"revision"`
	ReviewVersion int    `json:"review_version"`
}

type RequirementRelation struct {
	Title  string `json:"title,omitempty"`
	TaskID string `json:"task_id"`
	Kind   string `json:"kind"` // overlap, dependency, conflict
	Reason string `json:"reason"`
}
type RequirementQuestion struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // text, textarea, select, radio, multiselect
	Options     []string `json:"options"`
	Recommended []string `json:"recommended"`
	Reason      string   `json:"reason"`
	Required    bool     `json:"required"` // signals importance, never blocks free-form corrections
}

// RequirementTopic is one business decision on the Agent's agenda.
type RequirementTopic struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`   // direction or detail
	Status     string `json:"status"` // open, resolved or dropped
	Conclusion string `json:"conclusion,omitempty"`
}

type RequirementAsk struct {
	Kind         string   `json:"kind"` // direction (asked in plain text) or form
	TopicID      string   `json:"topic_id"`
	Question     string   `json:"question,omitempty"`
	QuickReplies []string `json:"quick_replies,omitempty"`
}

type RequirementProposal struct {
	Description string   `json:"description"`
	Acceptance  []string `json:"acceptance"`
}
