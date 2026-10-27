package domain

// Source and Baseline are frozen, flat snapshots. They never embed another
// iteration snapshot, so a long series does not produce recursively growing JSON.
type RequirementIteration struct {
	ParentTaskID string `json:"parent_task_id"`
	RootTaskID   string `json:"root_task_id"`
	Source       Task   `json:"source"`
	Baseline     *Task  `json:"baseline,omitempty"`
}

type RequirementIterationRef struct {
	ParentTaskID string `json:"parent_task_id"`
	RootTaskID   string `json:"root_task_id"`
}
