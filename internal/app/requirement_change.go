package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"fmt"
)

func (s *Service) prepareRequirementChange(c context.Context, a domain.Actor, t *domain.Task, v *domain.RequirementReview, in RequirementReviewInput) error {
	if a.Kind != "human" {
		return domain.ErrForbidden
	}
	if in.ExpectedTaskVersion <= 0 || in.ExpectedTaskVersion != t.Version {
		return domain.ErrConflict
	}
	if t.Kind != "requirement" || !domain.RequirementChangeAllowed(t.Status) || t.ReviewingChange() {
		return errors.New("仅测试、修复、待复测、待上线或待验收的需求可发起本轮变更")
	}
	if !nonempty(in.Body) || t.Submission == nil || t.Submission.Revision != t.Revision || !nonempty(t.Submission.SHA) || !nonempty(t.Branch) {
		return errors.New("请填写变更原因；原需求须有当前提测版本和开发分支")
	}
	change := &domain.RequirementChange{ID: domain.ID("chg_"), Status: "reviewing", FromStatus: t.Status, BaseRevision: t.Revision, Branch: t.Branch, SHA: t.Submission.SHA, SourceBugID: in.SourceBugID, SourceBugVersion: in.SourceBugVersion, Reason: in.Body, Actor: a.Name, At: domain.Now()}
	if in.SourceBugID != "" {
		bug, err := s.Store.GetTask(c, in.SourceBugID)
		if err != nil {
			return err
		}
		if bug.ProjectID != t.ProjectID || bug.ParentRequirementID != t.ID || bug.Kind != "bug" {
			return domain.ErrForbidden
		}
		if bug.Version != in.SourceBugVersion || bug.ChangeRequestID != "" {
			return domain.ErrConflict
		}
		if (bug.Status != "clarify" && bug.Status != "ready") || bug.ClaimHash != "" {
			return errors.New("仅尚未开始修复的关联反馈可转为需求变更")
		}
		v.Messages = append(v.Messages, domain.RequirementReviewMessage{ID: domain.ID("rm_"), Role: "system", Action: "change_feedback", Body: "本次变更来源反馈；原报告、讨论与附件保留，确认变更前不关闭。", Source: &bug, Attachments: bug.Attachments, At: change.At, Generation: v.Generation + 1})
		if prior, err := s.Store.GetRequirementReview(c, bug.ID); err == nil {
			change.SourceReviewVersion = prior.Version
			// Preserve the product's corrections, rather than treating the prior
			// Bug Agent's classification as a final product decision.
			start := max(0, len(prior.Messages)-10)
			for _, message := range prior.Messages[start:] {
				if message.Role == "user" && message.Action == "message" {
					message.ID, message.Action, message.At = domain.ID("rm_"), "change_feedback", change.At
					message.Generation, message.ReplyTo = v.Generation+1, ""
					v.Messages = append(v.Messages, message)
				}
			}
		} else if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
	}
	before := *t
	v.Messages = append(v.Messages, attributed(domain.RequirementReviewMessage{ID: domain.ID("rm_"), Role: "system", Action: "change_requested", Body: "产品发起本轮需求变更，暂停测试和发布；确认新需求稿后在原开发分支继续，不需要先合入主线或发布。", Source: &before, At: change.At, Generation: v.Generation + 1}, reviewActor(a, nil)))
	t.RequirementChange, t.Status = change, "clarify"
	t.Version++
	t.UpdatedAt = change.At
	v.Mode, v.Agenda = "testing_change", nil
	return nil
}

func confirmRequirementChange(t *domain.Task, v domain.RequirementReview, a domain.Actor, in RequirementReviewInput) error {
	if a.Kind != "human" {
		return domain.ErrForbidden
	}
	if in.ExpectedTaskVersion <= 0 || in.ExpectedTaskVersion != t.Version {
		return domain.ErrConflict
	}
	if !t.ReviewingChange() || t.RequirementChange.BaseRevision != t.Revision {
		return domain.ErrConflict
	}
	change := *t.RequirementChange
	change.Status, change.AppliedRevision = "applied", t.Revision+1
	change.ConfirmedBy, change.ConfirmedAt = a.Name, domain.Now()
	t.RequirementChange = &change
	t.Submission, t.Deployment, t.Review, t.TestVerification, t.Alignment = nil, nil, nil, nil, nil
	seen := map[string]bool{}
	for _, file := range t.Attachments {
		seen[file.ID] = true
	}
	for _, message := range v.Messages {
		if message.At < change.At {
			continue
		}
		files := append([]domain.Attachment{}, message.Attachments...)
		for _, note := range message.QuestionNotes {
			files = append(files, note.Attachments...)
		}
		for _, file := range files {
			if !seen[file.ID] {
				t.Attachments = append(t.Attachments, file)
				seen[file.ID] = true
			}
		}
	}
	return nil
}

func (s *Service) cancelRequirementChange(c context.Context, a domain.Actor, t *domain.Task, v *domain.RequirementReview, in RequirementReviewInput) error {
	if a.Kind != "human" {
		return domain.ErrForbidden
	}
	if !t.ReviewingChange() || in.ExpectedTaskVersion <= 0 || in.ExpectedTaskVersion != t.Version || in.Generation != v.Generation {
		return domain.ErrConflict
	}
	change := *t.RequirementChange
	change.Status = "canceled"
	t.RequirementChange, t.Status = &change, change.FromStatus
	if t.Status == "fixing" {
		bugs, err := s.Store.ListTasks(c, t.ProjectID)
		if err != nil {
			return err
		}
		open := false
		for _, bug := range bugs {
			open = open || (bug.ParentRequirementID == t.ID && !domain.TaskFinished(bug.Status))
		}
		if !open {
			t.Status = "retest"
		}
	}
	t.Version++
	t.UpdatedAt = domain.Now()
	v.Mode, v.Status, v.Error, v.Source = "", "change_canceled", "", *t
	v.Generation++
	v.ExpireForms(nil, "本次变更已取消，原测试流程恢复；变更记录保留。", t.UpdatedAt)
	v.Messages = append(v.Messages, attributed(domain.RequirementReviewMessage{ID: domain.ID("rm_"), Role: "system", Action: "change_canceled", Body: fmt.Sprintf("已取消本次变更，恢复原流程（R%d）；尚未通过测试或发布。", t.Revision), At: t.UpdatedAt, Source: t}, reviewActor(a, nil)))
	return nil
}
