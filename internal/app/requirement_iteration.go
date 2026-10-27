package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type RequirementIterationInput struct {
	TaskInput
	SourceVersion int `json:"source_version"`
}

type RequirementIterationHistory struct {
	Root       RequirementSummary   `json:"root"`
	Items      []RequirementSummary `json:"items"`
	Total      int                  `json:"total"`
	HasMore    bool                 `json:"has_more"`
	NextOffset int                  `json:"next_offset"`
}

func (s *Service) RequirementIterations(c context.Context, a domain.Actor, id string, offset, limit int) (RequirementIterationHistory, error) {
	var out RequirementIterationHistory
	task, e := s.Store.GetTask(c, id)
	if e != nil {
		return out, e
	}
	if e = s.CheckProject(c, a, task.ProjectID, false); e != nil {
		return out, e
	}
	if offset < 0 || limit < 0 || limit > 50 {
		return out, errors.New("offset不能为负，limit为1–50")
	}
	if limit == 0 {
		limit = 10
	}
	root := task
	if task.Iteration != nil {
		root, e = s.Store.GetTask(c, task.Iteration.RootTaskID)
		if e != nil {
			return out, e
		}
		if root.ProjectID != task.ProjectID {
			return out, domain.ErrForbidden
		}
	}
	items, total, e := s.Store.ListRequirementIterations(c, task.ProjectID, root.ID, offset, limit)
	if e != nil {
		return out, e
	}
	out = RequirementIterationHistory{Root: requirementSummary(root), Items: []RequirementSummary{}, Total: total, NextOffset: offset + len(items), HasMore: offset+len(items) < total}
	for _, item := range items {
		out.Items = append(out.Items, requirementSummary(item))
	}
	return out, nil
}

func (s *Service) CreateRequirementIteration(c context.Context, a domain.Actor, id string, in RequirementIterationInput) (domain.Mutation, error) {
	return s.createRequirementIteration(c, a, id, in, nil)
}
func (s *Service) CreateRequirementIterationFromMCP(c context.Context, a domain.Actor, id string, in RequirementIterationInput, grant ProductAuthorization) (domain.Mutation, error) {
	auth, e := productAuthorization(a, grant)
	if e != nil {
		return domain.Mutation{}, e
	}
	return s.createRequirementIteration(c, a, id, in, auth)
}
func (s *Service) createRequirementIteration(c context.Context, a domain.Actor, id string, in RequirementIterationInput, auth *domain.TaskActionAuthorization) (domain.Mutation, error) {
	var out domain.Mutation
	if a.Kind != "human" && auth == nil {
		return out, domain.ErrForbidden
	}
	source, e := s.Store.GetTask(c, id)
	if e != nil {
		return out, e
	}
	if e = s.CheckProject(c, a, source.ProjectID, false); e != nil {
		return out, e
	}
	if in.RequestID == "" {
		return out, errors.New("缺少request_id")
	}
	key := receiptKey(a, source.ProjectID, in.RequestID)
	fp := authorizedFingerprint(struct {
		Operation, ID string
		Input         RequirementIterationInput
	}{"requirement_iteration", id, in}, auth)
	if old, ok, e := s.prior(c, key, fp); ok || e != nil {
		return old, e
	}
	if source.Status != "done" {
		return out, errors.New("只有已验收的需求才能追加迭代；开发完成、待验收或已关闭均不算完成")
	}
	if in.SourceVersion <= 0 || source.Version != in.SourceVersion {
		return out, fmt.Errorf("%w: 原需求已变化，请核对最新内容；迭代草稿仍可保留", domain.ErrConflict)
	}
	if e = validateInput(&in.TaskInput); e != nil {
		return out, e
	}
	files, e := s.taskAttachments(c, source.ProjectID, in.AttachmentIDs)
	if e != nil {
		return out, e
	}
	lineage := &domain.RequirementIteration{ParentTaskID: source.ID, RootTaskID: source.ID}
	if source.Iteration != nil {
		lineage.RootTaskID = source.Iteration.RootTaskID
		baseline := source.Iteration.Source
		if source.Iteration.Baseline != nil {
			baseline = *source.Iteration.Baseline
		}
		baseline.Iteration = nil
		baseline.ClaimHash = ""
		lineage.Baseline = &baseline
	}
	source.Iteration = nil
	source.ClaimHash = ""
	lineage.Source = source
	task := domain.Task{ID: domain.ID("tsk_"), ProjectID: source.ProjectID, Title: in.Title, Kind: in.Kind, Description: in.Description, Acceptance: in.Acceptance, Attachments: files, Priority: in.Priority, Reviewer: in.Reviewer, Status: "clarify", Revision: 1, Version: 1, CreatedAt: domain.Now(), Iteration: lineage}
	task.UpdatedAt = task.CreatedAt
	creator := reviewActor(a, auth)
	task.Creator = &creator
	for attempt := 0; attempt < 32; attempt++ {
		if e = c.Err(); e != nil {
			return out, e
		}
		plan, e := s.newVersionPlan(c, a, source.ProjectID)
		if e != nil {
			return out, e
		}
		candidate := task
		if e = plan.bindTask(&candidate); e != nil {
			if errors.Is(e, domain.ErrConflict) {
				continue
			}
			return out, e
		}
		data := map[string]any{"parent_task_id": source.ID, "root_task_id": lineage.RootTaskID, "iteration_task_id": candidate.ID, "project_version_id": candidate.ProjectVersionID}
		if auth != nil {
			data["authorization"] = auth
		}
		plan.event(candidate, "created", "基于已验收需求追加迭代："+source.Title, data)
		plan.event(source, "iteration_created", "追加迭代："+candidate.Title, data)
		out = domain.Mutation{Task: candidate}
		body, _ := json.Marshal(out)
		batch := plan.batch()
		e = s.Store.CommitVersionPlan(c, nil, []domain.VersionTaskWrite{{Value: candidate}}, nil, &domain.Receipt{Key: key, Fingerprint: fp, Response: body}, batch)
		if e == nil {
			s.NotifyWork(source.ProjectID)
			return out, nil
		}
		if !errors.Is(e, domain.ErrConflict) {
			return out, e
		}
		if old, ok, x := s.prior(c, key, fp); ok || x != nil {
			return old, x
		}
		latest, x := s.Store.GetTask(c, id)
		if x != nil {
			return out, x
		}
		if latest.Version != source.Version || latest.Status != "done" {
			return out, domain.ErrConflict
		}
		time.Sleep(time.Duration(attempt+1) * time.Millisecond)
	}
	return out, domain.ErrConflict
}
