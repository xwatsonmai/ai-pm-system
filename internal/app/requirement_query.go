package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
)

// RequirementSummary deliberately omits documents, claims and conversation data.
type RequirementSummary struct {
	ParentRequirementID string                          `json:"parent_requirement_id,omitempty"`
	ScopeSummary        string                          `json:"scope_summary"`
	ReviewStatus        string                          `json:"review_status,omitempty"`
	Iteration           *domain.RequirementIterationRef `json:"iteration,omitempty"`
	ProjectVersionID    string                          `json:"project_version_id,omitempty"`
	ID                  string                          `json:"id"`
	Title               string                          `json:"title"`
	Kind                string                          `json:"kind"`
	Status              string                          `json:"status"`
	Owner               string                          `json:"owner"`
	Claimant            string                          `json:"claimant"`
	Blocker             string                          `json:"blocker,omitempty"`
	Revision            int                             `json:"revision"`
	UpdatedAt           int64                           `json:"updated_at"`
}

func requirementSummary(t domain.Task) RequirementSummary {
	out := RequirementSummary{ParentRequirementID: t.ParentRequirementID, ProjectVersionID: t.ProjectVersionID, ID: t.ID, Title: t.Title, Kind: t.Kind, Status: t.Status, Owner: t.Owner, Claimant: t.Claimant, Blocker: t.Blocker, Revision: t.Revision, UpdatedAt: t.UpdatedAt}

	if t.Iteration != nil {
		out.Iteration = &domain.RequirementIterationRef{ParentTaskID: t.Iteration.ParentTaskID, RootTaskID: t.Iteration.RootTaskID}
	}
	return out
}

type RequirementPoolQuery struct {
	ProjectVersionID string `json:"project_version_id,omitempty"`
	Kind             string `json:"kind"`
	Status           string `json:"status"`
	Blocked          *bool  `json:"blocked,omitempty"`
	Query            string `json:"query"`
	Offset           int    `json:"offset"`
	Limit            int    `json:"limit"`
}

type RequirementCounts struct {
	Total        int                       `json:"total"`
	Blocked      int                       `json:"blocked"`
	ByStatus     map[string]int            `json:"by_status"`
	ByKind       map[string]int            `json:"by_kind"`
	ByKindStatus map[string]map[string]int `json:"by_kind_status"`
}

type RequirementPoolResult struct {
	ProjectID  string               `json:"project_id"`
	AsOf       int64                `json:"as_of"`
	Counts     RequirementCounts    `json:"project_counts"`
	Filters    RequirementPoolQuery `json:"filters"`
	Matched    int                  `json:"matched_total"`
	Items      []RequirementSummary `json:"items"`
	HasMore    bool                 `json:"has_more"`
	NextOffset int                  `json:"next_offset"`
}

// Counts describe the whole project; matched_total describes the filters before
// pagination. Neither value is inferred from the returned page by the model.
func (s *Service) RequirementPool(c context.Context, a domain.Actor, project string, q RequirementPoolQuery) (RequirementPoolResult, error) {
	var out RequirementPoolResult
	if err := s.CheckProject(c, a, project, false); err != nil {
		return out, err
	}
	statuses := []string{"clarify", "ready", "doing", "aligning", "testing", "fixing", "retest", "verifying", "resolved", "pending_release", "review", "done", "closed", "rejected", "canceled"}
	if (q.Kind != "" && q.Kind != "bug" && q.Kind != "requirement") || (q.Status != "" && !slices.Contains(statuses, q.Status)) {
		return out, errors.New("无效的类型或状态筛选")
	}
	if q.Offset < 0 || q.Limit < 0 || q.Limit > 50 || len(q.Query) > 500 {
		return out, errors.New("offset 不能为负；limit 为 1–50；query 最多 500 字节")
	}
	if q.Limit == 0 {
		q.Limit = 20
	}
	tasks, err := s.Store.ListTasks(c, project)
	if err != nil {
		return out, err
	}
	if q.ProjectVersionID != "" {
		if _, err = s.TasksForProjectVersion(c, a, project, q.ProjectVersionID); err != nil {
			return out, err
		}
	}
	counts := RequirementCounts{Total: len(tasks), ByStatus: map[string]int{}, ByKind: map[string]int{"requirement": 0, "bug": 0}, ByKindStatus: map[string]map[string]int{}}
	for _, status := range statuses {
		counts.ByStatus[status] = 0
	}
	for _, kind := range []string{"requirement", "bug"} {
		counts.ByKindStatus[kind] = map[string]int{}
		for _, status := range statuses {
			counts.ByKindStatus[kind][status] = 0
		}
	}
	matched := []domain.Task{}
	query := strings.ToLower(strings.TrimSpace(q.Query))
	for _, t := range tasks {
		counts.ByStatus[t.Status]++
		if t.Blocker != "" {
			counts.Blocked++
		}
		counts.ByKind[t.Kind]++
		if counts.ByKindStatus[t.Kind] == nil {
			counts.ByKindStatus[t.Kind] = map[string]int{}
		}
		counts.ByKindStatus[t.Kind][t.Status]++
		if q.ProjectVersionID != "" {
			target := q.ProjectVersionID
			if target == "unplanned" {
				target = ""
			}
			if t.ProjectVersionID != target {
				continue
			}
		}
		if q.Kind != "" && t.Kind != q.Kind || q.Status != "" && t.Status != q.Status {
			continue
		}
		if q.Blocked != nil && *q.Blocked != (t.Blocker != "") {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(t.ID+"\n"+t.Title+"\n"+t.Description+"\n"+t.Owner+"\n"+t.Claimant), query) {
			continue
		}
		matched = append(matched, t)
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].UpdatedAt != matched[j].UpdatedAt {
			return matched[i].UpdatedAt > matched[j].UpdatedAt
		}
		return matched[i].ID < matched[j].ID
	})
	start := min(q.Offset, len(matched))
	end := start + min(q.Limit, len(matched)-start)
	items := make([]RequirementSummary, 0, end-start)
	reviews, err := s.Store.ListRequirementReviewSummaries(c, project)
	if err != nil {
		return out, err
	}
	byTask := map[string]domain.RequirementReviewSummary{}
	for _, v := range reviews {
		byTask[v.TaskID] = v
	}
	for _, t := range matched[start:end] {
		item := requirementSummary(t)
		scope := []rune(strings.Join(strings.Fields(t.Description), " "))
		item.ScopeSummary = string(scope[:min(300, len(scope))])
		if len(scope) > 300 {
			item.ScopeSummary += "…（详情可查询）"
		}
		if v, ok := byTask[t.ID]; ok {
			item.ReviewStatus = v.Status
			if v.TaskRevision != t.Revision {
				item.ReviewStatus = "stale"
			}
		}
		items = append(items, item)
	}
	return RequirementPoolResult{ProjectID: project, AsOf: domain.Now(), Counts: counts, Filters: q, Matched: len(matched), Items: items, HasMore: end < len(matched), NextOffset: end}, nil
}

// QuestionContext is intentionally small. Full project facts remain available to
// non-question runs; question tools retrieve pool data and documents on demand.
func (s *Service) QuestionContext(c context.Context, a domain.Actor, project string, ids []string) (map[string]any, error) {
	if err := s.CheckProject(c, a, project, false); err != nil {
		return nil, err
	}
	p, err := s.Store.GetProject(c, project)
	if err != nil {
		return nil, err
	}
	p.CredentialID = ""
	items := []RequirementSummary{}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		t, err := s.Store.GetTask(c, id)
		if err != nil {
			return nil, err
		}
		if t.ProjectID != project {
			return nil, domain.ErrForbidden
		}
		items = append(items, requirementSummary(t))
	}
	return map[string]any{"project": p, "conversation_tasks": items, "as_of": domain.Now()}, nil
}

type RequirementDetailQuery struct {
	View         string `json:"view,omitempty"` // review omits development receipts
	TaskID       string `json:"task_id"`
	ReviewBefore *int   `json:"review_before,omitempty"`
	ReviewLimit  int    `json:"review_limit"`
}

type RequirementReviewContext struct {
	Version          int                               `json:"version"`
	NeedsRecheck     bool                              `json:"needs_recheck,omitempty"`
	RecheckReason    string                            `json:"recheck_reason,omitempty"`
	Status           string                            `json:"status"`
	State            string                            `json:"state"`
	Generation       int                               `json:"generation"`
	TaskRevision     int                               `json:"task_revision"`
	UpdatedAt        int64                             `json:"updated_at"`
	Error            string                            `json:"error,omitempty"`
	PendingQuestions []domain.RequirementQuestion      `json:"pending_questions"`
	PendingAsk       *domain.RequirementAsk            `json:"pending_ask,omitempty"`
	Agenda           []domain.RequirementTopic         `json:"agenda,omitempty"`
	LatestSupplement *domain.RequirementReviewMessage  `json:"latest_supplement,omitempty"`
	SupplementState  string                            `json:"supplement_state"`
	LatestAssessment *domain.RequirementReviewMessage  `json:"latest_assessment,omitempty"`
	Messages         []domain.RequirementReviewMessage `json:"messages"`
	MessageCount     int                               `json:"message_count"`
	HasMore          bool                              `json:"has_more"`
	NextBefore       int                               `json:"next_before"`
}

type RequirementDetailResult struct {
	IterationHistory   RequirementIterationHistory `json:"iteration_history"`
	ProjectID          string                      `json:"project_id"`
	AsOf               int64                       `json:"as_of"`
	Task               domain.Task                 `json:"task"`
	Review             RequirementReviewContext    `json:"requirement_review"`
	Events             []domain.Event              `json:"recent_events"`
	EventsTruncated    bool                        `json:"events_truncated"`
	Artifacts          []domain.Artifact           `json:"recent_artifacts"`
	ArtifactsTruncated bool                        `json:"artifacts_truncated"`
	NextActor          string                      `json:"next_actor"`
	NextAction         string                      `json:"next_action"`
}

func (s *Service) RequirementDetail(c context.Context, a domain.Actor, project string, q RequirementDetailQuery) (RequirementDetailResult, error) {
	// A confirmation/revision update must not mix the old task with a new review.
	s.mu.Lock()
	defer s.mu.Unlock()
	var out RequirementDetailResult
	if err := s.CheckProject(c, a, project, false); err != nil {
		return out, err
	}
	if q.TaskID == "" || q.ReviewLimit < 0 || q.ReviewLimit > 20 || (q.ReviewBefore != nil && *q.ReviewBefore < 0) {
		return out, errors.New("需要 task_id；review_limit 为 1–20；review_before 不能为负")
	}
	if q.View != "" && q.View != "review" && q.View != "full" {
		return out, errors.New("view 必须为 review 或 full")
	}
	if q.ReviewLimit == 0 {
		q.ReviewLimit = 10
	}
	t, err := s.Store.GetTask(c, q.TaskID)
	if err != nil {
		return out, err
	}
	if t.ProjectID != project {
		return out, domain.ErrForbidden
	}
	v, err := s.RequirementReview(c, a, t.ID)
	if err != nil {
		return out, err
	}
	review := requirementReviewContext(t, v, q)
	events, err := s.Store.Events(c, project, t.ID, -1)
	if err != nil {
		return out, err
	}
	artifacts, err := s.Store.ListArtifacts(c, project)
	if err != nil {
		return out, err
	}
	files := []domain.Artifact{}
	for _, v := range artifacts {
		if v.TaskID == t.ID {
			files = append(files, v)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].At > files[j].At })
	actor, action := requirementNextAction(t, review)
	history, err := s.RequirementIterations(c, a, t.ID, 0, 10)
	if err != nil {
		return out, err
	}
	if q.View == "review" {
		t = ReviewSource(t)
		events, files = nil, nil
		for i := range review.Messages {
			review.Messages[i].Authorization = nil
			if review.Messages[i].Source != nil {
				source := ReviewSource(*review.Messages[i].Source)
				review.Messages[i].Source = &source
			}
		}
	}
	return RequirementDetailResult{IterationHistory: history, ProjectID: project, AsOf: domain.Now(), Task: t, Review: review, Events: events[max(0, len(events)-20):], EventsTruncated: len(events) > 20, Artifacts: files[:min(10, len(files))], ArtifactsTruncated: len(files) > 10, NextActor: actor, NextAction: action}, nil
}

func requirementReviewContext(t domain.Task, v domain.RequirementReview, q RequirementDetailQuery) RequirementReviewContext {
	out := RequirementReviewContext{Status: v.Status, State: v.Status, Generation: v.Generation, TaskRevision: v.TaskRevision, UpdatedAt: v.UpdatedAt, Error: v.Error, PendingQuestions: []domain.RequirementQuestion{}, Agenda: v.Agenda, SupplementState: "none", MessageCount: len(v.Messages)}
	out.Version, out.NeedsRecheck, out.RecheckReason = v.Version, v.NeedsRecheck, v.RecheckReason
	// Confirmation is a user message too, but is not a new supplement.
	for i := range v.Messages {
		m := &v.Messages[i]
		legacyConfirmation := m.Action == "" && m.Body == "已确认需求稿与验收条件，需求进入待认领。" && len(m.Answers) == 0 && len(m.Attachments) == 0 && m.ReplyTo == ""
		if m.Role == "user" && m.Action != "confirm" && !legacyConfirmation {
			out.LatestSupplement = m
		}
		if m.Result != nil {
			out.LatestAssessment = m
		}
	}
	current := out.LatestAssessment != nil && out.LatestAssessment.Generation == v.Generation && v.TaskRevision == t.Revision
	switch {
	case t.Status != "clarify":
		out.State = "not_required"
	case v.TaskRevision != t.Revision:
		out.State = "stale"
	case v.Status == "needs_input" && current:
		out.State = "awaiting_input"
		if out.LatestAssessment.Result.Decision == "infeasible" {
			out.State = "infeasible"
		}
		out.PendingQuestions = out.LatestAssessment.Result.Questions
		out.PendingAsk = out.LatestAssessment.Result.Ask
	case v.Status == "rejection_pending" && current:
		out.State = "awaiting_rejection_confirmation"
	case v.Status == "ready_to_confirm" && current:
		out.State = "awaiting_confirmation"
	case v.Status == "queued" || v.Status == "running":
		out.State = "reviewing"
	case v.Status == "needs_input" || v.Status == "ready_to_confirm":
		out.State = "stale"
	}
	if v.NeedsRecheck {
		out.State, out.PendingQuestions, out.PendingAsk = "stale", nil, nil
	}
	if out.LatestSupplement != nil {
		out.SupplementState = "received"
		if out.LatestAssessment != nil && out.LatestAssessment.Generation >= out.LatestSupplement.Generation {
			out.SupplementState = "assessed"
		}
		if out.State == "reviewing" {
			out.SupplementState = "reviewing"
		}
		if out.State == "failed" || out.State == "stale" {
			out.SupplementState = "needs_reassessment"
		}
	}
	end := len(v.Messages)
	if q.ReviewBefore != nil {
		end = min(end, *q.ReviewBefore)
	}
	start := max(0, end-q.ReviewLimit)
	out.Messages = append([]domain.RequirementReviewMessage{}, v.Messages[start:end]...)
	out.HasMore, out.NextBefore = start > 0, start
	return out
}

func requirementNextAction(t domain.Task, v RequirementReviewContext) (string, string) {
	if t.Status == "clarify" {
		switch v.State {
		case "awaiting_rejection_confirmation":
			return "user", "确认 Bug 驳回依据并关闭，或补充说明重新评审；当前仍阻塞所属需求"
		case "awaiting_input":
			return "user", "在需求评审中补充待确认问题，尚未进入开发"
		case "awaiting_confirmation":
			return "user", "确认最新需求稿与验收条件后进入待认领"
		case "reviewing":
			return "agent", "正在评审，完成后查看待补充问题或待确认需求稿，尚未进入开发"
		case "infeasible":
			return "user", "查看评审中的不可行原因，调整需求范围"
		default:
			return "user", "打开需求评审继续或重试；已保存的内容仍在，尚未进入开发"
		}
	}
	if t.Blocker != "" && !domain.TaskFinished(t.Status) {
		return "developer", "核对阻塞原因并对接所需支持"
	}
	switch t.Status {
	case "ready":
		return "developer", "等待开发认领"
	case "doing":
		return "developer", "开发方继续处理并提交验证结果"
	case "testing":
		if t.Kind == "bug" && t.ParentRequirementID == "" {
			return "user", "验证平台 Bug；修复无效则退回此 Bug 修复，通过后进入待上线"
		}
		return "user", "按提测版本测试；发现问题请提起关联 Bug，通过后进入待上线"
	case "fixing":
		return "user", "处理关联 Bug：补充评审、确认驳回或验证修复；全部解决或关闭后待复测"
	case "retest":
		return "user", "全部关联 Bug 已解决或关闭，选择包含修复的代码版本开始复测"
	case "verifying":
		return "user", "在修复版本上验证 Bug，确认有效后记为已解决；无效则退回 Bug 修复"
	case "resolved":
		return "none", "Bug 已由产品验证解决"
	case "pending_release":
		return "developer", "开发已完成，等待实际上线；记录当前版本、上线环境和结果后进入待验收"
	case "review":
		return "user", "当前版本已登记上线，按验收条件在上线环境核验结果"
	case "done":
		return "none", "已通过验收"
	case "canceled":
		return "none", "已取消"
	case "closed":
		return "none", "已关闭，不再处理；需要继续时由产品重新打开并评审"
	case "rejected":
		return "none", "已拒绝，不再处理；需要重新考虑时由产品重新打开并评审"
	}
	return "unknown", "查询事项最新状态"
}
