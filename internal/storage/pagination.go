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
