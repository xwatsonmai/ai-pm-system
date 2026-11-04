package domain

import "errors"

// PageQuery is shared by growing business lists. Legacy calls without paging
// parameters retain their response shape; new callers opt into this contract.
type PageQuery struct {
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
	Query    string `json:"q,omitempty"`
}

func (q PageQuery) Normalized() (PageQuery, error) {
	if q.Page == 0 {
		q.Page = 1
	}
	if q.PageSize == 0 {
		q.PageSize = 20
	}
	if q.Page < 1 || q.Page > 1000000 || q.PageSize < 1 || q.PageSize > 100 || len(q.Query) > 500 {
		return q, errors.New("page 为 1–1000000，page_size 为 1–100，搜索最多 500 字节")
	}
	return q, nil
}
func (q PageQuery) Offset() int { return (q.Page - 1) * q.PageSize }

type Page[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}
type TaskListQuery struct {
	PageQuery
	Status              string `json:"status,omitempty"`
	Kind                string `json:"kind,omitempty"`
	ProjectVersionID    string `json:"project_version_id,omitempty"`
	ParentRequirementID string `json:"parent_requirement_id,omitempty"`
}
type TaskListStats struct {
	Total         int            `json:"total"`
	ByStatus      map[string]int `json:"by_status"`
	Attention     int            `json:"attention"`
	ReviewPending int            `json:"review_pending"`
}

// ListDocumentQuery only allows named resources; adapters must never treat a
// resource supplied by a client as a table name or SQL expression.
type ListDocumentQuery struct {
	PageQuery
	Resource         string
	ProjectID        string
	UserID           string
	ActorKind        string
	LegacyOwner      bool
	TaskID           string
	ConversationID   string
	Active           bool
	ExcludeQuestions bool
	OnlyQuestions    bool
	ArtifactMode     string
	ExcludeGit       bool
	ExcludeGitEvents bool
}
