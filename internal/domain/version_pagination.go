package domain

type VersionListQuery struct {
	NamesOnly bool `json:"-"`
	PageQuery
	Status string `json:"status,omitempty"`
}

type VersionScopeQuery struct {
	PageQuery
	Status         string `json:"status,omitempty"`
	IncludeRemoved bool   `json:"include_removed,omitempty"`
}
