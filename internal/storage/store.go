// Package storage defines database-independent persistence contracts.
// Domain and application code never import a SQL driver or use dialect-specific SQL.
package storage

import (
	"ai-pm-system/internal/domain"
	"context"
)

type Store interface {
	ListRequirementIterations(context.Context, string, string, int, int) ([]domain.Task, int, error)
	ProjectTimelineEvents(context.Context, string, int64, int64, int, int) ([]domain.Event, int, error)
	GetCurrentMilestone(context.Context, string) (domain.CurrentMilestone, error)
	ListProjectVersions(context.Context, string) ([]domain.ProjectVersion, error)
	GetProjectVersion(context.Context, string) (domain.ProjectVersion, error)
	// CommitVersionPlan compares every affected version/task and atomically saves
	// scope, publication snapshots, task changes, events and the retry receipt.
	CommitVersionPlan(context.Context, []domain.VersionWrite, []domain.VersionTaskWrite, []domain.Event, *domain.Receipt, ...domain.MilestoneBatch) error
	GetRequirementIntake(context.Context, string) (domain.RequirementIntake, error)
	ListRequirementIntakes(context.Context, string) ([]domain.RequirementIntake, error)
	// CommitRequirementIntake atomically saves a suggestion or its resolution,
	// optional new task, creation event and retry receipt. expected=0 inserts.
	CommitRequirementIntake(context.Context, domain.RequirementIntake, int, *domain.Task, *domain.Event, *domain.Receipt, ...domain.MilestoneBatch) error
	ListCollaborations(context.Context, string) ([]domain.Collaboration, error)
	GetCollaboration(context.Context, string) (domain.Collaboration, error)
	// CommitCollaboration atomically compares version, saves history, the receipt
	// and optionally a new result-delivery job. expected=0 inserts a new task.
	CommitCollaboration(context.Context, domain.Collaboration, int, *domain.Receipt, *domain.ReplyJob) error
	ListReplyJobs(context.Context) ([]domain.ReplyJob, error)
	GetReplyJob(context.Context, string) (domain.ReplyJob, error)
	// CommitReplyJob atomically compares the job version and optionally saves its run.
	CommitReplyJob(context.Context, domain.ReplyJob, int, *domain.Run) error
	Close() error
	GetPlatformSettings(context.Context) (domain.PlatformSettings, error)
	PutPlatformSettings(context.Context, domain.PlatformSettings) error
	ListPlatformAdmins(context.Context) ([]string, error)
	SetPlatformAdmin(context.Context, string, bool) error
	GetDirectory(context.Context) (domain.DirectorySnapshot, error)
	PutDirectory(context.Context, domain.DirectorySnapshot) error
	GetMCPAccount(context.Context, string) (domain.MCPAccount, error)
	PutMCPAccount(context.Context, domain.MCPAccount) error
	GetOAuthClient(context.Context, string) (domain.OAuthClient, error)
	PutOAuthClient(context.Context, domain.OAuthClient) error
	PutOAuthCode(context.Context, domain.OAuthCode) error
	ConsumeOAuthCode(context.Context, domain.OAuthCode) (domain.OAuthCode, error)
	PutOAuthGrant(context.Context, domain.OAuthGrant, string) error
	FindOAuthGrant(context.Context, string, bool) (domain.OAuthGrant, error)
	ListOAuthGrants(context.Context, string) ([]domain.OAuthGrant, error)
	DeleteOAuthGrant(context.Context, string, string) error
	PutRepositoryCredential(context.Context, domain.RepositoryCredential) error
	GetRepositoryCredential(context.Context, string) (domain.RepositoryCredential, error)
	RepositoryCredentialCount(context.Context) (int, error)
	PutUser(context.Context, domain.User) error
	ListUsers(context.Context) ([]domain.User, error)
	ListProjects(context.Context) ([]domain.Project, error)
	GetProject(context.Context, string) (domain.Project, error)
	PutProject(context.Context, domain.Project) error
	ListTasks(context.Context, string) ([]domain.Task, error)
	GetTask(context.Context, string) (domain.Task, error)
	// CommitTask atomically compares version, stores the task, appends the event
	// and saves an optional idempotency receipt. Terminal tasks also close any
	// existing requirement review in that transaction, and the event's ReviewNote
	// is appended to that review. expected=0 means insert.
	CommitTask(context.Context, domain.Task, int, domain.Event, *domain.Receipt) error
	GetReceipt(context.Context, string) (domain.Receipt, error)
	GetRequirementReview(context.Context, string) (domain.RequirementReview, error)
	ListRequirementReviews(context.Context) ([]domain.RequirementReview, error)
	ListRequirementReviewSummaries(context.Context, string) ([]domain.RequirementReviewSummary, error)
	// CommitRequirementReview compares review version and task revision atomically.
	// An optional task update and event confirm a proposal in the same transaction.
	CommitRequirementReview(context.Context, domain.RequirementReview, int, int, *domain.Task, *domain.Event, *domain.Receipt, ...domain.MilestoneBatch) error
	// Events returns up to 500 events in ascending order. A negative cursor
	// returns the most recent window; nonnegative cursors support incremental reads.
	Events(context.Context, string, string, int64) ([]domain.Event, error)
	AppendEvent(context.Context, domain.Event) error
	ListArtifacts(context.Context, string) ([]domain.Artifact, error)
	GetArtifact(context.Context, string) (domain.Artifact, error)
	// PutArtifact inserts immutable evidence; identical retries succeed and a
	// conflicting ID fails. Internal Git snapshots are replaceable.
	PutArtifact(context.Context, domain.Artifact) error
	// Run reads hide deleted conversations and apply manual title overrides.
	// PutRun must not overwrite the independently stored conversation edits.
	ListRuns(context.Context, string) ([]domain.Run, error)
	GetRun(context.Context, string) (domain.Run, error)
	PutRun(context.Context, domain.Run) error
	GetConversation(context.Context, string) (domain.Conversation, error)
	// PutConversation saves manual title/deletion independently of model updates.
	PutConversation(context.Context, domain.Conversation) error
	PutAttachment(context.Context, domain.Attachment) error
	GetAttachment(context.Context, string) (domain.Attachment, error)
	ListProfiles(context.Context) ([]domain.Profile, error)
	GetProfile(context.Context, string) (domain.Profile, error)
	PutProfile(context.Context, domain.Profile) error
	ListTokens(context.Context, string) ([]domain.Token, error)
	PutToken(context.Context, domain.Token) error
	FindToken(context.Context, string) (domain.Token, error)
	DeleteToken(context.Context, string, string) error
	PutLoginAttempt(context.Context, domain.LoginAttempt) error
	// ConsumeLoginAttempt atomically binds the browser and consumes a valid,
	// unexpired state once, even when multiple callbacks arrive concurrently.
	ConsumeLoginAttempt(context.Context, string, string, int64) (domain.LoginAttempt, error)
	PutWebSession(context.Context, domain.WebSession) error
	GetWebSession(context.Context, string) (domain.WebSession, error)
	DeleteWebSession(context.Context, string) error
	// CreateAccount saves a built-in account and its user record; a taken
	// username is ErrConflict. With firstAdmin it succeeds only while no account
	// exists and grants platform administration in the same transaction.
	CreateAccount(context.Context, domain.Account, bool) error
	GetAccount(context.Context, string) (domain.Account, error)
	FindAccount(context.Context, string) (domain.Account, error)
	ListAccounts(context.Context) ([]domain.Account, error)
	// UpdateAccount compares the expected version and saves the account and its
	// user name. A new password or disabling deletes the user's workspace
	// sessions; disabling or a reset password that must be changed also deletes
	// MCP grants and unredeemed codes.
	UpdateAccount(context.Context, domain.Account, int) error
}
