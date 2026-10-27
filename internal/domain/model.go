package domain

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrNotFound = errors.New("记录不存在")
var ErrConflict = errors.New("记录已变化，请刷新后重试")
var ErrForbidden = errors.New("当前身份不能执行此操作")

// ErrRequestReused is a conflict that retrying can never resolve: the caller
// sent different content under a request_id it already used.
var ErrRequestReused = fmt.Errorf("%w: request_id 已用于不同内容", ErrConflict)

func ID(prefix string) string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}
func Now() int64 { return time.Now().UnixMilli() }

type Project struct {
	AgentReadMode string          `json:"agent_read_mode,omitempty"`
	AgentRef      string          `json:"agent_ref,omitempty"`
	OwnerID       string          `json:"owner_id"`
	OwnerName     string          `json:"owner_name"`
	AccessMembers []string        `json:"access_members"`
	Memberships   []ProjectMember `json:"memberships"`
	AuthMode      string          `json:"auth_mode"`
	CredentialID  string          `json:"credential_id,omitempty"`
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Repository    string          `json:"repository"`
	Branch        string          `json:"branch"`
	ProfileID     string          `json:"profile_id"`
	Members       []string        `json:"members"`
	SHA           string          `json:"sha"`
	SyncedAt      int64           `json:"synced_at"`
	SyncError     string          `json:"sync_error"`
	CreatedAt     int64           `json:"created_at"`
}

// BugContext pins the requirement and code under test when a linked Bug was filed.
type BugContext struct {
	TaskID      string   `json:"task_id"`
	Revision    int      `json:"revision"`
	Version     int      `json:"version"`
	Branch      string   `json:"branch"`
	SHA         string   `json:"sha"`
	Description string   `json:"description"`
	Acceptance  []string `json:"acceptance"`
}

type TestVerification struct {
	Revision    int    `json:"revision"`
	SHA         string `json:"sha"`
	Environment string `json:"environment"`
	Note        string `json:"note"`
	Actor       string `json:"actor"`
	At          int64  `json:"at"`
}

type Task struct {
	ParentRequirementID string            `json:"parent_requirement_id,omitempty"`
	BugContext          *BugContext       `json:"bug_context,omitempty"`
	TestVerification    *TestVerification `json:"test_verification,omitempty"`

	Alignment        *DevelopmentAlignment `json:"alignment,omitempty"`
	ClaimClientID    string                `json:"claim_client_id,omitempty"`
	Iteration        *RequirementIteration `json:"iteration,omitempty"`
	ProjectVersionID string                `json:"project_version_id,omitempty"`
	ID               string                `json:"id"`
	ProjectID        string                `json:"project_id"`
	Title            string                `json:"title"`
	Kind             string                `json:"kind"`
	Description      string                `json:"description"`
	Attachments      []Attachment          `json:"attachments,omitempty"`
	Acceptance       []string              `json:"acceptance"`
	Priority         string                `json:"priority"`
	Status           string                `json:"status"`
	Owner            string                `json:"owner"`
	Reviewer         string                `json:"reviewer"`
	Branch           string                `json:"branch"`
	Blocker          string                `json:"blocker"`
	Revision         int                   `json:"revision"`
	Version          int                   `json:"version"`
	Claimant         string                `json:"claimant"`
	ClaimHash        string                `json:"-"`
	Submission       *Submission           `json:"submission,omitempty"`
	Deployment       *Deployment           `json:"deployment,omitempty"`
	Review           *Review               `json:"review,omitempty"`
	Resolution       *TaskResolution       `json:"resolution,omitempty"`
	Creator          *ActorRef             `json:"creator,omitempty"`
	CreatedAt        int64                 `json:"created_at"`
	UpdatedAt        int64                 `json:"updated_at"`
}
type Submission struct {
	Branch      string   `json:"branch,omitempty"`
	Summary     string   `json:"summary"`
	SHA         string   `json:"sha"`
	Environment string   `json:"environment"`
	Tests       string   `json:"tests"`
	Limitations string   `json:"limitations"`
	References  []string `json:"references"`
	Revision    int      `json:"revision"`
	At          int64    `json:"at"`
}
type Review struct {
	Revision    int      `json:"revision"`
	SHA         string   `json:"sha"`
	Environment string   `json:"environment"`
	Checks      []string `json:"checks"`
	Note        string   `json:"note"`
	Actor       string   `json:"actor"`
	Accepted    bool     `json:"accepted"`
	At          int64    `json:"at"`
}

// Deployment records a reported release, independently of development tests.
// The task event preserves previous releases when a new result is submitted.
type Deployment struct {
	ProjectVersionID string `json:"project_version_id,omitempty"`
	ReleaseID        string `json:"release_id,omitempty"`
	Revision         int    `json:"revision"`
	SHA              string `json:"sha"`
	Environment      string `json:"environment"`
	Body             string `json:"body"`
	Actor            string `json:"actor"`
	Source           string `json:"source"`
	At               int64  `json:"at"`
}
type Event struct {
	Seq       int64           `json:"seq"`
	ProjectID string          `json:"project_id"`
	TaskID    string          `json:"task_id"`
	Kind      string          `json:"kind"`
	Actor     string          `json:"actor"`
	Body      string          `json:"body"`
	Data      json.RawMessage `json:"data,omitempty"`
	At        int64           `json:"at"`
	// ReviewNote travels with the task write to its review; it is not stored on the event.
	ReviewNote *RequirementReviewNote `json:"-"`
}
type Artifact struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"project_id"`
	TaskID      string          `json:"task_id"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	Source      string          `json:"source"`
	SHA         string          `json:"sha"`
	Environment string          `json:"environment"`
	Data        json.RawMessage `json:"data,omitempty"`
	At          int64           `json:"at"`
}

// Conversation stores user edits independently of streaming run updates.
type Conversation struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Title     string `json:"title,omitempty"`
	DeletedAt int64  `json:"deleted_at,omitempty"`
}

type Run struct {
	ReviewEvidence    *RequirementReviewEvidence `json:"review_evidence,omitempty"`
	Compaction        *RunCompaction             `json:"compaction,omitempty"`
	CodeSnapshot      *CodeSnapshot              `json:"code_snapshot,omitempty"`
	CodeRef           string                     `json:"code_ref,omitempty"`
	ReviewGeneration  int                        `json:"review_generation,omitempty"`
	ReviewResult      *RequirementReviewResult   `json:"review_result,omitempty"`
	TriggerType       string                     `json:"trigger_type,omitempty"`
	SourceEventID     string                     `json:"source_event_id,omitempty"`
	CollaborationID   string                     `json:"collaboration_id,omitempty"`
	ID                string                     `json:"id"`
	ProjectID         string                     `json:"project_id"`
	TaskID            string                     `json:"task_id"`
	ConversationID    string                     `json:"conversation_id"`
	OwnerID           string                     `json:"owner_id,omitempty"`
	OwnerKind         string                     `json:"owner_kind,omitempty"`
	ConversationTitle string                     `json:"conversation_title,omitempty"`
	TitleStatus       string                     `json:"title_status,omitempty"`
	Kind              string                     `json:"kind"`
	Prompt            string                     `json:"prompt"`
	Attachments       []Attachment               `json:"attachments,omitempty"`
	Status            string                     `json:"status"`
	Output            string                     `json:"output"`
	ProcessOutput     string                     `json:"process_output,omitempty"`
	OutputVersion     uint64                     `json:"output_version,omitempty"`
	Error             string                     `json:"error"`
	Steps             []Step                     `json:"steps"`
	Usage             json.RawMessage            `json:"usage,omitempty"`
	StartedAt         int64                      `json:"started_at"`
	FinishedAt        int64                      `json:"finished_at"`
}

type RunCompaction struct {
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
	Error        string `json:"error,omitempty"`
	TokensBefore int    `json:"tokens_before,omitempty"`
	At           int64  `json:"at"`
}
type Step struct {
	ID        string          `json:"id,omitempty"`
	Path      string          `json:"path,omitempty"`
	SHA       string          `json:"sha,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Details   json.RawMessage `json:"details,omitempty"`
	Error     string          `json:"error,omitempty"`
	Name      string          `json:"name"`
	Label     string          `json:"label,omitempty"` // what a reader sees while a review runs
	Status    string          `json:"status"`
	At        int64           `json:"at"`
}

type CodeSubmodule struct {
	Path   string `json:"path"`
	SHA    string `json:"sha"`
	Status string `json:"status"`
}
type CodeSnapshot struct {
	ProjectID  string          `json:"project_id"`
	Key        string          `json:"key"`
	SHA        string          `json:"sha,omitempty"`
	Ref        string          `json:"ref,omitempty"`
	Status     string          `json:"status"`
	SyncedAt   int64           `json:"synced_at,omitempty"`
	CreatedAt  int64           `json:"created_at,omitempty"`
	Files      int             `json:"files"`
	Bytes      int64           `json:"bytes"`
	Submodules []CodeSubmodule `json:"submodules,omitempty"`
	Warnings   []string        `json:"warnings,omitempty"`
}
type Profile struct {
	ContextWindow  int    `json:"context_window,omitempty"`
	MaxTokens      int    `json:"max_tokens,omitempty"`
	OwnerID        string `json:"owner_id"`
	ID             string `json:"id"`
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	BaseURL        string `json:"base_url"`
	API            string `json:"api"`
	HasKey         bool   `json:"has_key"`
	SupportsImages bool   `json:"supports_images"`
	APIKey         string `json:"-"`
}

// Attachment metadata is safe to return in run history; raw bytes and extracted
// text stay in the persistence adapter and are loaded only for the model/download.
type Attachment struct {
	UploadFingerprint string                   `json:"upload_fingerprint,omitempty"`
	Authorization     *TaskActionAuthorization `json:"authorization,omitempty"`
	ID                string                   `json:"id"`
	ProjectID         string                   `json:"project_id"`
	Name              string                   `json:"name"`
	MIME              string                   `json:"mime"`
	Size              int                      `json:"size"`
	Kind              string                   `json:"kind"`
	Truncated         bool                     `json:"truncated"`
	At                int64                    `json:"at"`
	Content           []byte                   `json:"-"`
	Text              string                   `json:"-"`
}
type Token struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Hash      string `json:"-"`
	At        int64  `json:"at"`
}
type Actor struct {
	ClientID      string // authenticated OAuth client, never supplied in tool arguments
	ID            string
	Name          string
	Kind          string
	ProjectID     string
	UserID        string
	PlatformAdmin bool
}

// ActorRef records who said or did something in a shared record.
type ActorRef struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Kind string `json:"kind"` // human, platform or dev_agent
}

type User struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	LoginID string `json:"login_id"`
}

// OAuth credentials and session handles never appear in API responses.
type LoginAttempt struct {
	Hash        string
	BrowserHash string
	Namespace   string
	Verifier    string
	ExpiresAt   int64
	ReturnTo    string
}
type WebSession struct {
	Hash      string
	Namespace string
	User      User
	ExpiresAt int64
	// MustChangePassword limits the session to choosing a new password.
	MustChangePassword bool
}

// Account is a built-in sign-in account. The password hash never leaves storage.
type Account struct {
	UserID             string `json:"user_id"`
	Username           string `json:"username"`
	Name               string `json:"name"`
	PasswordHash       string `json:"-"`
	Disabled           bool   `json:"disabled"`
	MustChangePassword bool   `json:"must_change_password"`
	Version            int    `json:"version"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
}

type Receipt struct {
	Key         string
	Fingerprint string
	Response    []byte
}
type Mutation struct {
	Task    Task   `json:"task"`
	ClaimID string `json:"claim_id,omitempty"`
}

// RepositoryCredential stores authenticated ciphertext only.
type RepositoryCredential struct {
	ID         string
	OwnerID    string
	Ciphertext []byte
}
