package storage

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
)

// PagedQueries is an optional optimized query adapter. Keeping it separate
// leaves existing third-party Store implementations source compatible.
type PagedQueries interface {
	QueryTasks(context.Context, string, domain.TaskListQuery) (domain.Page[domain.Task], error)
	TaskListStats(context.Context, string) (domain.TaskListStats, error)
	QueryDocuments(context.Context, domain.ListDocumentQuery) (domain.Page[json.RawMessage], error)
	QueryMembers(context.Context, string, domain.PageQuery) (domain.Page[json.RawMessage], error)
	QueryTodos(context.Context, string, bool, domain.PageQuery, []string) (domain.Page[json.RawMessage], error)
	QueryReviewSummaries(context.Context, []string) ([]domain.RequirementReviewSummary, error)
}

// TaskTreeQueries keeps the hierarchy extension optional for other adapters.
type TaskTreeQueries interface {
	QueryTaskTree(context.Context, string, domain.TaskTreeQuery) (domain.TaskTreePage, error)
}

type LineageQueries interface {
	LineageVersions(context.Context, string, domain.PageQuery) (domain.Page[domain.LineageVersion], error)
	QueryLineage(context.Context, string, string, domain.LineageQuery) (domain.LineagePage, error)
}
