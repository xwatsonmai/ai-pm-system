package domain

// TaskTreeQuery pages one level of the existing Bug/iteration relationships.
// An empty ParentID selects roots. Filters retain ancestor context.
type TaskTreeQuery struct {
	TaskListQuery
	ParentID string
}

type TaskTreeNode struct {
	Task
	TreeParentID  string `json:"tree_parent_id"`
	Relation      string `json:"tree_relation"`
	MatchesFilter bool   `json:"matches_filter"`
	ChildrenCount int    `json:"children_count"`
	MatchingCount int    `json:"matching_count"`
	PendingCount  int    `json:"pending_count"`
}

type TaskTreePage struct {
	Page[TaskTreeNode]
	MatchingTotal int `json:"matching_total"`
}
