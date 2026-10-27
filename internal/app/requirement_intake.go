package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type RequirementIntakeInput struct {
	Title          string             `json:"title"`
	Kind           string             `json:"kind"`
	Description    string             `json:"description"`
	Acceptance     []string           `json:"acceptance"`
	AttachmentIDs  []string           `json:"attachment_ids"`
	Reason         string             `json:"reason"`
	BugDetails     *domain.BugDetails `json:"bug_details"`
	MissingDetails []string           `json:"missing_details"`
}

func validateIntakeDetails(kind string, details *domain.BugDetails, missing []string) error {
	if len(missing) > 12 {
		return errors.New("待补充细节最多 12 项")
	}
	for _, item := range missing {
		if !nonempty(item) || len(item) > 3000 {
			return errors.New("待补充细节不能为空，每项最多 3000 字节")
		}
	}
	if kind != "bug" {
		return nil
	}
	if len(missing) > 0 {
		return errors.New("Bug 仍有关键细节待确认，请先在当前对话补充，确认清楚后再整理创建建议")
	}
	if details == nil {
		return errors.New("Bug 缺少确认信息，请先补充发生位置/环境、触发场景、实际表现、预期行为和修复范围，再重新整理建议")
	}
	for _, field := range []struct{ label, value string }{
		{"发生位置/环境", details.Context}, {"触发场景或复现信息", details.Reproduction}, {"实际表现", details.Actual}, {"预期行为", details.Expected}, {"修复范围", details.Scope},
	} {
		if !nonempty(field.value) || len(field.value) > 10000 {
			return fmt.Errorf("Bug 的%s不能为空且最多 10000 字节；缺失时先向用户确认，不要编造", field.label)
		}
		if slices.Contains([]string{"未知", "待确认", "待补充", "不清楚", "未提供", "unknown", "tbd"}, strings.ToLower(strings.TrimSpace(field.value))) {
			return fmt.Errorf("Bug 的%s仍待确认，请先向用户补充询问", field.label)
		}
	}
	return nil
}

// Include structured facts in the document shown on the suggestion card and
// subsequently shared with developers/reviewers. The private intake is not their
// only source of the confirmed facts or outstanding questions.
func intakeDocument(in RequirementIntakeInput) string {
	doc := in.Description
	if in.Kind == "bug" && in.BugDetails != nil {
		v := in.BugDetails
		doc += "\n\n## Bug 确认信息\n\n### 发生位置与环境\n" + v.Context + "\n\n### 触发场景与复现信息\n" + v.Reproduction + "\n\n### 实际表现\n" + v.Actual + "\n\n### 预期行为\n" + v.Expected + "\n\n### 修复范围\n" + v.Scope
	}
	if len(in.MissingDetails) > 0 {
		doc += "\n\n## 创建时仍待补充的细节\n\n- " + strings.Join(in.MissingDetails, "\n- ")
	}
	return doc
}

type ResolveRequirementIntakeInput struct {
	Action    string `json:"action"`
	RequestID string `json:"request_id"`
}

func questionConversation(r domain.Run) string {
	if r.ConversationID != "" {
		return r.ConversationID
	}
	return r.ID
}

// The run supplies the human identity. Tool arguments cannot name another user,
// project or conversation, and automatic collaboration replies cannot authorize writes.
func (s *Service) requirementIntakeOwner(c context.Context, runID string) (domain.Run, domain.Actor, error) {
	r, err := s.Store.GetRun(c, runID)
	if err != nil {
		return r, domain.Actor{}, err
	}
	a := domain.Actor{ID: r.OwnerID, Kind: "human", Name: "问答用户"}
	if r.Kind != "question" || r.TriggerType != "" || r.OwnerKind != "human" || a.ID == "" {
		return r, a, domain.ErrForbidden
	}
	if err = s.CheckProject(c, a, r.ProjectID, false); err != nil {
		return r, a, err
	}
	if users, e := s.Store.ListUsers(c); e == nil {
		for _, u := range users {
			if u.ID == a.ID {
				a.Name = u.Name
			}
		}
	}
	if a.ID == Human().ID {
		a.Name = Human().Name
	}
	return r, a, nil
}

func (s *Service) RequirementIntakes(c context.Context, a domain.Actor, project string) ([]domain.RequirementIntake, error) {
	if a.Kind != "human" {
		return nil, domain.ErrForbidden
	}
	if err := s.CheckProject(c, a, project, false); err != nil {
		return nil, err
	}
	items, err := s.Store.ListRequirementIntakes(c, project)
	if err != nil {
		return nil, err
	}
	out := []domain.RequirementIntake{}
	for _, item := range items {
		if item.OwnerID != a.ID {
			continue
		}
		// Deleted private conversations must also disappear from this endpoint.
		if _, e := s.Store.GetRun(c, item.OriginRunID); errors.Is(e, domain.ErrNotFound) {
			continue
		} else if e != nil {
			return nil, e
		}
		out = append(out, item)
	}
	return out, nil
}

// ConversationIntakeContext exposes only this user's current conversation's
// suggestions and attachment metadata to the model, including older-turn files.
func (s *Service) ConversationIntakeContext(c context.Context, r domain.Run) ([]domain.RequirementIntake, []domain.Attachment, error) {
	items, err := s.Store.ListRequirementIntakes(c, r.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	visible := []domain.RequirementIntake{}
	for _, item := range items {
		if item.ConversationID == questionConversation(r) && item.OwnerID == r.OwnerID && r.OwnerKind == "human" {
			visible = append(visible, item)
		}
	}
	runs, err := s.Store.ListRuns(c, r.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	files := []domain.Attachment{}
	seen := map[string]bool{}
	for _, turn := range runs {
		if turn.Kind != "question" || questionConversation(turn) != questionConversation(r) || turn.OwnerID != r.OwnerID || turn.OwnerKind != r.OwnerKind || turn.StartedAt > r.StartedAt {
			continue
		}
		for _, f := range turn.Attachments {
			if !seen[f.ID] {
				files = append(files, f)
				seen[f.ID] = true
			}
		}
	}
	return visible, files, nil
}

func (s *Service) ProposeRequirement(c context.Context, runID string, in RequirementIntakeInput) (domain.RequirementIntake, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _, err := s.requirementIntakeOwner(c, runID)
	if err != nil {
		return domain.RequirementIntake{}, err
	}
	if err = validateIntakeDetails(in.Kind, in.BugDetails, in.MissingDetails); err != nil {
		return domain.RequirementIntake{}, err
	}
	input := TaskInput{Title: in.Title, Kind: in.Kind, Description: intakeDocument(in), Acceptance: in.Acceptance}
	if err = validateInput(&input); err != nil {
		return domain.RequirementIntake{}, err
	}
	if !nonempty(in.Description) || !nonempty(in.Reason) || len(in.Reason) > 3000 {
		return domain.RequirementIntake{}, errors.New("请提供整理后的需求描述及建议创建的理由")
	}
	if _, err = agentCreatedTaskStatus(input.Kind, input.Description, input.Acceptance); err != nil {
		return domain.RequirementIntake{}, err
	}
	_, available, err := s.ConversationIntakeContext(c, r)
	if err != nil {
		return domain.RequirementIntake{}, err
	}
	allowed := map[string]bool{}
	for _, f := range available {
		allowed[f.ID] = true
	}
	for _, id := range in.AttachmentIDs {
		if !allowed[id] {
			return domain.RequirementIntake{}, errors.New("只能关联当前会话中用户已经发送的附件")
		}
	}
	files, err := s.taskAttachments(c, r.ProjectID, in.AttachmentIDs)
	if err != nil {
		return domain.RequirementIntake{}, err
	}
	// One immutable suggestion per user turn. A repeated tool call or lost reply
	// returns that suggestion; correcting it requires a new user turn.
	id := "intake_" + Hash(runID)[:24]
	v := domain.RequirementIntake{ID: id, ProjectID: r.ProjectID, ConversationID: questionConversation(r), OriginRunID: r.ID, OwnerID: r.OwnerID, Version: 1, Status: "suggested", Title: input.Title, Kind: input.Kind, Description: input.Description, Acceptance: input.Acceptance, Attachments: files, Reason: in.Reason, BugDetails: in.BugDetails, MissingDetails: in.MissingDetails, CreatedAt: domain.Now(), UpdatedAt: domain.Now()}
	if old, e := s.Store.GetRequirementIntake(c, id); e == nil {
		if old.Title != v.Title || old.Kind != v.Kind || old.Description != v.Description || fingerprint(old.Acceptance) != fingerprint(v.Acceptance) || fingerprint(old.Attachments) != fingerprint(v.Attachments) {
			return old, fmt.Errorf("%w: 本轮已有创建建议，请复用该建议；修改内容请根据用户下一条消息重新整理", domain.ErrConflict)
		}
		return old, nil
	} else if !errors.Is(e, domain.ErrNotFound) {
		return v, e
	}
	err = s.Store.CommitRequirementIntake(c, v, 0, nil, nil, nil)
	return v, err
}

// ResolveRequirementFromQuestion is only invoked by a human question's tool.
// The model interprets intent, but must cite the current human message verbatim;
// attachments, previous assistant messages and background results are not consent.
func (s *Service) ResolveRequirementFromQuestion(c context.Context, runID, id, action, confirmation string) (domain.RequirementIntake, error) {
	r, a, err := s.requirementIntakeOwner(c, runID)
	if err != nil {
		return domain.RequirementIntake{}, err
	}
	if strings.TrimSpace(confirmation) == "" || !strings.Contains(r.Prompt, confirmation) {
		return domain.RequirementIntake{}, errors.New("需要引用本轮用户明确创建或暂不创建的原话；仅识别到问题时先建议创建")
	}
	v, err := s.Store.GetRequirementIntake(c, id)
	if err != nil {
		return v, err
	}
	if v.ProjectID != r.ProjectID || v.ConversationID != questionConversation(r) {
		return v, domain.ErrForbidden
	}
	return s.resolveRequirementIntake(c, a, id, ResolveRequirementIntakeInput{Action: action, RequestID: "question:" + r.ID + ":" + id + ":" + action}, r.ID)
}

func (s *Service) ResolveRequirementIntake(c context.Context, a domain.Actor, id string, in ResolveRequirementIntakeInput) (domain.RequirementIntake, error) {
	return s.resolveRequirementIntake(c, a, id, in, "")
}

func (s *Service) resolveRequirementIntake(c context.Context, a domain.Actor, id string, in ResolveRequirementIntakeInput, runID string) (domain.RequirementIntake, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.Store.GetRequirementIntake(c, id)
	if err != nil {
		return v, err
	}
	if a.Kind != "human" || a.ID != v.OwnerID {
		return domain.RequirementIntake{}, domain.ErrForbidden
	}
	if err = s.CheckProject(c, a, v.ProjectID, false); err != nil {
		return v, err
	}
	if _, err = s.Store.GetRun(c, v.OriginRunID); err != nil {
		return v, err
	}
	if in.RequestID == "" {
		return v, errors.New("缺少 request_id")
	}
	if in.Action != "create" && in.Action != "dismiss" {
		return v, errors.New("请选择创建或暂不创建")
	}
	key := "requirement-intake:" + receiptKey(a, v.ProjectID, in.RequestID)
	fp := fingerprint([]string{id, in.Action, runID})
	if receipt, e := s.Store.GetReceipt(c, key); e == nil {
		if receipt.Fingerprint != fp {
			return v, domain.ErrRequestReused
		}
		return v, nil
	} else if !errors.Is(e, domain.ErrNotFound) {
		return v, e
	}
	if v.Status != "suggested" {
		if (v.Status == "created" && in.Action == "create") || (v.Status == "dismissed" && in.Action == "dismiss") {
			return v, nil
		}
		return v, fmt.Errorf("%w: 这条建议已经处理；需要调整时请在对话中说明", domain.ErrConflict)
	}
	expected := v.Version
	v.Version++
	v.UpdatedAt, v.ResolvedRunID = domain.Now(), runID
	v.Status = "dismissed"
	var task *domain.Task
	var ev *domain.Event
	if in.Action == "create" {
		if err := validateIntakeDetails(v.Kind, v.BugDetails, v.MissingDetails); err != nil {
			return v, err
		}
		status, e := agentCreatedTaskStatus(v.Kind, v.Description, v.Acceptance)
		if e != nil {
			return v, e
		}
		// Agent-prepared Bugs are ready for development once creation is confirmed.
		task = &domain.Task{ID: domain.ID("tsk_"), ProjectID: v.ProjectID, Title: v.Title, Kind: v.Kind, Description: v.Description, Acceptance: v.Acceptance, Attachments: v.Attachments, Priority: "next", Status: status, Revision: 1, Version: 1, CreatedAt: v.UpdatedAt, UpdatedAt: v.UpdatedAt}
		creator := reviewActor(a, nil)
		task.Creator = &creator
		v.TaskID, v.Status = task.ID, "created"
		// Private conversation IDs and unselected dialogue never enter project history.
		ev = &domain.Event{ProjectID: v.ProjectID, TaskID: task.ID, Kind: "created", Actor: a.Name, Body: "从项目问答创建需求", At: v.UpdatedAt}
	}
	var batches []domain.MilestoneBatch
	if task != nil {
		plan, e := s.taskMilestoneBatch(c, a, task)
		if e != nil {
			return v, e
		}
		if plan != nil {
			batches = append(batches, plan.batch())
		}
	}
	b, _ := json.Marshal(v)
	err = s.Store.CommitRequirementIntake(c, v, expected, task, ev, &domain.Receipt{Key: key, Fingerprint: fp, Response: b}, batches...)
	if err == nil && task != nil {
		s.notify(v.ProjectID, task.ID, "created")
	}
	return v, err
}
