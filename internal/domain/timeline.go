package domain

type GitCommit struct {
	SHA     string   `json:"sha"`
	Title   string   `json:"title"`
	Author  string   `json:"author"`
	At      int64    `json:"at"`
	TaskIDs []string `json:"task_ids"`
}
type GitTimeline struct {
	Commits    []GitCommit `json:"commits"`
	Anchor     string      `json:"anchor"`
	HasMore    bool        `json:"has_more"`
	NextOffset int         `json:"next_offset"`
	SyncedAt   int64       `json:"synced_at"`
	Warning    string      `json:"warning,omitempty"`
}
