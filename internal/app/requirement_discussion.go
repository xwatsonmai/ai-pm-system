package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
)

// Discussion uses the requirement's existing transcript and Pi session. It can
// append messages, but never writes the task or authorizes a workflow action.
func (s *Service) queueRequirementDiscussion(c context.Context, a domain.Actor, t domain.Task, v domain.RequirementReview, in RequirementReviewInput, key, fp string, authorization *domain.TaskActionAuthorization) (domain.RequirementReview, error) {
	if t.Status == "clarify" || alignmentReserved(t) || t.ChangeRequestID != "" {
		return v, domain.ErrConflict
	}
	if in.Action == "retry" && (v.Mode != "discussion" || v.Status != "failed") {
		return v, domain.ErrConflict
	}
	if len(in.Body) > 500000 || len(in.Answers) > 0 || len(in.QuestionNotes) > 0 || in.ReplyTo != "" {
		return v, errors.New("请直接发送问题或截图；评审表单只能在当前评审中回答")
	}
	if in.Action != "retry" && !nonempty(in.Body) && len(in.AttachmentIDs) == 0 {
		return v, errors.New("请输入问题或添加截图")
	}
	files, err := s.taskAttachments(c, t.ProjectID, in.AttachmentIDs)
	if err != nil {
		return v, err
	}
	expected := v.Version
	v.Mode, v.Source, v.TaskRevision = "discussion", t, t.Revision
	v.Generation++
	v.Status, v.Error, v.RunID = "queued", "", ""
	v.QueuedAt, v.UpdatedAt = domain.Now(), domain.Now()
	v.Evidence, v.NeedsRecheck, v.RecheckReason = nil, false, ""
	message := attributed(domain.RequirementReviewMessage{ID: domain.ID("rm_"), Role: "user", Action: "discuss", Body: in.Body, Attachments: files, Mentions: in.Mentions, Authorization: authorization, Generation: v.Generation, At: v.UpdatedAt}, reviewActor(a, authorization))
	if in.Action == "retry" {
		message.Role, message.Action, message.Body = "system", "discussion_retried", a.Name+" 重试了 Agent 回复"
	}
	v.Messages = append(v.Messages, message)
	v.Version++
	body, _ := json.Marshal(v)
	err = s.Store.CommitRequirementReview(c, v, expected, t.Revision, nil, nil, &domain.Receipt{Key: key, Fingerprint: fp, Response: body})
	return v, err
}

func (s *Service) finishRequirementDiscussion(c context.Context, t domain.Task, v domain.RequirementReview, generation int, runID string, result *domain.RequirementReviewResult, problem string) error {
	if t.Revision != v.TaskRevision || submissionSHA(t) != submissionSHA(v.Source) {
		problem = "需求或提测版本已更新，请基于最新记录重试。"
	}
	if problem == "" {
		if result == nil {
			problem = "Agent 未返回回复，请重试。"
		} else if err := ValidateRequirementDiscussionSubmission(RequirementReviewSubmission{Reply: result.Body, Analysis: result.Analysis, Decision: result.Decision, Agenda: result.Agenda, Proposal: result.Proposal, Relations: result.Relations, ExpireFormIDs: result.ExpireFormIDs}); err != nil {
			problem = err.Error()
		} else if result.Ask != nil || len(result.Questions) > 0 {
			problem = "答疑不能发起正式评审表单"
		}
	}
	expected := v.Version
	v.Version++
	v.Source, v.TaskRevision, v.UpdatedAt = t, t.Revision, domain.Now()
	v.Status, v.Error = "answered", problem
	m := domain.RequirementReviewMessage{ID: "rm_" + runID, Role: "assistant", ActorID: "platform", ActorKind: "platform", Action: "discussion_answer", Generation: generation, At: v.UpdatedAt}
	if problem != "" {
		v.Status = "failed"
		m.ID, m.Role, m.Action, m.Body = domain.ID("rm_"), "system", "review_failed", "Agent 回复未完成："+problem
	} else {
		m.Body, m.Result = result.Body, result
	}
	v.Messages = append(v.Messages, m)
	return s.Store.CommitRequirementReview(c, v, expected, t.Revision, nil, nil, nil)
}

func submissionSHA(t domain.Task) string {
	if t.Submission != nil {
		return t.Submission.SHA
	}
	return ""
}

func ValidateRequirementDiscussionSubmission(in RequirementReviewSubmission) error {
	if in.Decision != "answer" || in.Ask != nil || in.Proposal != nil || len(in.Agenda) > 0 || len(in.Relations) > 0 || len(in.ExpireFormIDs) > 0 {
		return errors.New("当前是需求答疑，请使用 decision=answer，仅提交 reply 和 analysis；不能确认需求、驳回 Bug 或发起变更")
	}
	if !nonempty(in.Reply) || len([]rune(in.Reply)) > maxReplyRunes || len([]rune(in.Analysis)) > maxAnalysisRunes {
		return errors.New("请用不超过 400 字的 reply 回答问题，较长依据放入 analysis")
	}
	if internalID.MatchString(in.Reply + "\n" + in.Analysis) {
		return errors.New("面向产品的文字不能包含内部编号，请使用需求标题")
	}
	return nil
}
