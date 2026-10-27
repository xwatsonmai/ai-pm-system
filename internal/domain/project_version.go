package domain

// ProjectVersion is a milestone with independently preserved release snapshots,
// distinct from a task's revision/CAS version.
type ProjectVersion struct {
	ID          string                `json:"id"`
	ProjectID   string                `json:"project_id"`
	Name        string                `json:"name"`
	Goal        string                `json:"goal"`
	Owner       string                `json:"owner"`
	TargetDate  string                `json:"target_date"`
	StartedAt   int64                 `json:"started_at,omitempty"`
	EndedAt     int64                 `json:"ended_at,omitempty"`
	Status      string                `json:"status"` // planned, active, completed, closed
	Version     int                   `json:"version"`
	ScopeFrozen bool                  `json:"scope_frozen"`
	Scope       []VersionScopeItem    `json:"scope"`
	Releases    []VersionRelease      `json:"releases"`
	History     []VersionHistoryEntry `json:"history"`
	CreatedAt   int64                 `json:"created_at"`
	UpdatedAt   int64                 `json:"updated_at"`
}

type VersionScopeItem struct {
	Task             Task   `json:"task"`
	Removed          bool   `json:"removed"`
	Published        bool   `json:"published"`
	Reason           string `json:"reason,omitempty"`
	DetachedRevision int    `json:"detached_revision,omitempty"`
}

// Release snapshots are immutable; later acceptance is reflected in Scope only.
type VersionRelease struct {
	ID          string `json:"id"`
	Environment string `json:"environment"`
	Body        string `json:"body"`
	Actor       string `json:"actor"`
	Source      string `json:"source"`
	Tasks       []Task `json:"tasks"`
	At          int64  `json:"at"`
}

type VersionHistoryEntry struct {
	Action          string   `json:"action"`
	Body            string   `json:"body"`
	Actor           string   `json:"actor"`
	UserID          string   `json:"user_id,omitempty"`
	UserInstruction string   `json:"user_instruction,omitempty"`
	TaskIDs         []string `json:"task_ids,omitempty"`
	At              int64    `json:"at"`
}

type VersionWrite struct {
	Value    ProjectVersion
	Expected int
}

type VersionTaskWrite struct {
	Value    Task
	Expected int
}

// CurrentMilestone selects the current default without restricting the version
// model to a single release. Product policy currently opens one milestone.
type CurrentMilestone struct {
	ProjectID string `json:"project_id"`
	VersionID string `json:"version_id"`
	Version   int    `json:"version"`
}
type MilestoneBatch struct {
	State    *CurrentMilestone
	Expected int
	Versions []VersionWrite
	Events   []Event
}
