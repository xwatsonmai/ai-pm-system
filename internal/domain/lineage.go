package domain

// Lineage is a read-only projection. It never exposes requirement documents,
// frozen iteration sources, credentials, discussions or development receipts.
type LineageVersion struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Requirements int    `json:"requirements"`
	Bugs         int    `json:"bugs"`
	Completed    int    `json:"completed"`
}

type LineageQuery struct {
	PageQuery
	ParentID string
}

type LineageNode struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	Kind             string `json:"kind"`
	Status           string `json:"status"`
	Owner            string `json:"owner"`
	Revision         int    `json:"revision"`
	UpdatedAt        int64  `json:"updated_at"`
	ProjectVersionID string `json:"project_version_id"`
	VersionName      string `json:"version_name"`
	ParentID         string `json:"parent_id"`
	Relation         string `json:"relation"`
	InScope          bool   `json:"in_scope"`
	PathContext      bool   `json:"path_context"`
	ChildrenCount    int    `json:"children_count"`
	Historical       bool   `json:"historical"`
	CurrentAvailable bool   `json:"current_available"`
}

type LineagePage struct {
	Page[LineageNode]
	Version       LineageVersion `json:"project_version"`
	MatchingTotal int            `json:"matching_total"`
}
