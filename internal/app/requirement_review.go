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

type RequirementReviewInput struct {
	ExpectedTaskVersion int                                     `json:"expected_task_version,omitempty"`
	Action              string                                  `json:"action"`
	RequestID           string                                  `json:"request_id"`
	Body                string                                  `json:"body"`
	ReplyTo             string                                  `json:"reply_to"`
	Answers             map[string][]string                     `json:"answers"`
	QuestionNotes       map[string]RequirementQuestionNoteInput `json:"question_notes,omitempty"`
	AttachmentIDs       []string                                `json:"attachment_ids"`
	Generation          int                                     `json:"generation"`
}

type RequirementQuestionNoteInput struct {
	Body          string   `json:"body"`
	AttachmentIDs []string `json:"attachment_ids"`
}

func (s *Service) RequirementReview(c context.Context, a domain.Actor, id string) (domain.RequirementReview, error) {
	t, err := s.Store.GetTask(c, id)
	if err != nil {
		return domain.RequirementReview{}, err
	}
	if err = s.CheckProject(c, a, t.ProjectID, false); err != nil {
		return domain.RequirementReview{}, err
	}
	v, err := s.Store.GetRequirementReview(c, id)
	if errors.Is(err, domain.ErrNotFound) {
		status := "not_started"
		if domain.TaskTerminated(t.Status) {
			status = "closed"
		}
		creator, err := s.taskCreator(c, t)
		return domain.RequirementReview{TaskID: id, ProjectID: t.ProjectID, Source: t, Creator: creator, TaskRevision: t.Revision, Status: status, Messages: []domain.RequirementReviewMessage{}}, err
	}
	if err == nil && domain.TaskTerminated(t.Status) {
		v.Source = t
		v.Status = "closed"
	}
	if err == nil && t.Status == "clarify" && (v.Status == "needs_input" || v.Status == "ready_to_confirm") {
		v.RecheckReason, err = s.ReviewRecheckReason(c, v)
		v.NeedsRecheck = v.RecheckReason != ""
	}
	if err == nil && v.RunID != "" {
		if r, e := s.Store.GetRun(c, v.RunID); e == nil {
			v.Compaction = r.Compaction
			if v.Status == "running" {
				v.Progress = ReviewProgress(r)
			}
		}
	}
	if err == nil && v.TaskID != "" {
		v.IncomingRelations, _, err = s.ReviewIncomingRelations(c, v)
	}
	if err == nil {
		v.Creator, err = s.taskCreator(c, t)
	}
	return v, err
}

// Older tasks have no recorded creator; their first event names who created them.
func (s *Service) taskCreator(c context.Context, t domain.Task) (*domain.ActorRef, error) {
	if t.Creator != nil {
		return t.Creator, nil
	}
	events, err := s.Store.Events(c, t.ProjectID, t.ID, 0)
	if err != nil || len(events) == 0 || events[0].Actor == "" {
		return nil, err
	}
	return &domain.ActorRef{Name: events[0].Actor}, nil
}

// reviewActor attributes an action to the person when a personal MCP client
// acts for them, and to the client itself otherwise.
func reviewActor(a domain.Actor, authorization *domain.TaskActionAuthorization) domain.ActorRef {
	switch {
	case a.Kind == "human":
		return domain.ActorRef{ID: a.ID, Name: a.Name, Kind: "human"}
	case authorization != nil && a.UserID != "":
		return domain.ActorRef{ID: a.UserID, Name: strings.TrimSuffix(a.Name, " · MCP"), Kind: "human"}
	case a.Kind == "platform":
		return domain.ActorRef{ID: "platform", Name: a.Name, Kind: "platform"}
	}
	return domain.ActorRef{ID: a.ID, Name: a.Name, Kind: "dev_agent"}
}

func attributed(m domain.RequirementReviewMessage, actor domain.ActorRef) domain.RequirementReviewMessage {
	m.Actor, m.ActorID, m.ActorKind = actor.Name, actor.ID, actor.Kind
	return m
}

func hasSourceSnapshot(v domain.RequirementReview, revision int) bool {
	return slices.ContainsFunc(v.Messages, func(m domain.RequirementReviewMessage) bool {
		return m.Source != nil && m.Source.Revision == revision
	})
}

// SaveRequirementReview persists user input before any model admission or execution.
// Review/model failures are represented in the conversation, never creation errors.
func (s *Service) SaveRequirementReview(c context.Context, a domain.Actor, id string, in RequirementReviewInput) (domain.RequirementReview, error) {
	var v domain.RequirementReview
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		v, err = s.saveRequirementReview(c, a, id, in, nil)
		if !errors.Is(err, domain.ErrConflict) {
			return v, err
		}
	}
	return v, err
}
func (s *Service) saveRequirementReview(c context.Context, a domain.Actor, id string, in RequirementReviewInput, authorization *domain.TaskActionAuthorization) (domain.RequirementReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.Store.GetTask(c, id)
	if err != nil {
		return domain.RequirementReview{}, err
	}
	if err = s.CheckProject(c, a, t.ProjectID, false); err != nil {
		return domain.RequirementReview{}, err
	}
	if a.Kind != "human" && authorization == nil && !(a.Kind == "platform" && in.Action == "start") {
		return domain.RequirementReview{}, domain.ErrForbidden
	}
	if in.RequestID == "" {
		return domain.RequirementReview{}, errors.New("缺少 request_id")
	}
	key := "requirement-review:" + receiptKey(a, t.ProjectID, in.RequestID)
	fp := authorizedFingerprint(struct {
		ID    string
		Input RequirementReviewInput
	}{id, in}, authorization)
	prior, err := s.Store.GetReceipt(c, key)
	if err == nil {
		if prior.Fingerprint != fp {
			return domain.RequirementReview{}, domain.ErrRequestReused
		}
		return s.Store.GetRequirementReview(c, id)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.RequirementReview{}, err
	}
	if in.ExpectedTaskVersion > 0 && in.ExpectedTaskVersion != t.Version {
		return domain.RequirementReview{}, domain.ErrConflict
	}
	v, err := s.Store.GetRequirementReview(c, id)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return v, err
	}
	if v.TaskID == "" {
		v = domain.RequirementReview{TaskID: id, ProjectID: t.ProjectID, Messages: []domain.RequirementReviewMessage{}}
	}
	if domain.TaskTerminated(t.Status) {
		if in.Action == "start" {
			v.Source = t
			v.Status = "closed"
			return v, nil
		}
		return v, fmt.Errorf("%w: 需求已结束，评审记录只读；输入仍保留在草稿中，请先重新打开需求", domain.ErrConflict)
	}
	if v.Mode == "development_alignment" && t.Status != "clarify" {
		return s.replyDevelopmentAlignment(c, a, t, v, in, key, fp, authorization)
	}
	if t.Status == "clarify" {
		v.Mode = ""
	}
	if t.ParentRequirementID != "" && t.Status != "clarify" && t.Status != "ready" && in.Action != "start" {
		return v, errors.New("Bug 已进入修复流程，请在工作记录补充；评审历史只读")
	}
	expected := v.Version
	taskRevision := t.Revision
	if in.Action == "start" && v.Version > 0 && v.TaskRevision == t.Revision {
		return v, nil
	}
	var task *domain.Task
	var ev *domain.Event
	switch in.Action {
	case "start", "message", "retry":
		if in.Action == "message" {
			hasNote := false
			for _, note := range in.QuestionNotes {
				hasNote = hasNote || nonempty(note.Body) || len(note.AttachmentIDs) > 0
			}
			if !nonempty(in.Body) && len(in.Answers) == 0 && len(in.AttachmentIDs) == 0 && !hasNote {
				return v, errors.New("请输入补充说明或回答问题")
			}
			if len(in.Body) > 500000 {
				return v, errors.New("单条说明超过 50 万字节，请分段发送；输入仍保留在草稿中")
			}
			if len(in.Answers) > 20 {
				return v, errors.New("单次最多回答 20 个问题")
			}
			if in.ReplyTo != "" {
				found := false
				for _, m := range v.Messages {
					if m.ID == in.ReplyTo && m.Role == "assistant" && m.Result != nil && len(m.Result.Questions) > 0 {
						found = true
						if m.Generation != v.Generation || m.FormExpiredAt != 0 || v.TaskRevision != t.Revision || t.Status != "clarify" || (v.Status != "needs_input" && v.Status != "ready_to_confirm") {
							return v, fmt.Errorf("%w: 表单已过期，请回答最新问题或直接发送补充说明；旧草稿已保留", domain.ErrConflict)
						}
					}
				}
				if !found {
					return v, errors.New("问题消息不存在，请刷新后重试；草稿已保留")
				}
			}
		}
		if in.Action == "message" && (len(in.Answers) > 0 || len(in.QuestionNotes) > 0) && in.ReplyTo == "" {
			return v, errors.New("表单回答必须关联当前问题消息，请刷新后重试；草稿已保留")
		}
		files, e := s.taskAttachments(c, t.ProjectID, in.AttachmentIDs)
		if e != nil {
			return v, e
		}
		var notes map[string]domain.RequirementQuestionNote
		if in.Action == "message" && len(in.QuestionNotes) > 0 {
			notes, e = s.requirementQuestionNotes(c, t.ProjectID, v, in)
			if e != nil {
				return v, e
			}
		}
		// Edits and reopenings record their own snapshot when they happen.
		if v.TaskRevision != t.Revision && !hasSourceSnapshot(v, t.Revision) {
			source := t
			v.Messages = append(v.Messages, domain.RequirementReviewMessage{ID: domain.ID("rm_"), Role: "system", Body: "需求内容已保存，以下评审以此版本为基础。", Source: &source, At: domain.Now(), Generation: v.Generation + 1})
		}
		retry := ""
		if in.Action == "retry" {
			retry = "review_retried"
			if t.Status == "clarify" && (v.Status == "needs_input" || v.Status == "ready_to_confirm") {
				reason, e := s.ReviewRecheckReason(c, v)
				if e != nil {
					return v, e
				}
				if reason != "" {
					retry = "recheck_requested"
				}
			}
		}
		if t.Status == "ready" && in.Action != "start" {
			t.Status = "clarify"
			t.Version++
			t.UpdatedAt = domain.Now()
			task = &t
			ev = &domain.Event{ProjectID: t.ProjectID, TaskID: t.ID, Kind: "requirements_reopened", Actor: a.Name, Body: "产品补充需求，重新进入需求评审", At: domain.Now()}
		}
		reason := "已开始新一轮评审，此表单已过期。"
		if in.Action == "message" {
			reason = "已收到新的补充或纠正，请以最新评审为准。"
		}
		v.ExpireForms(nil, reason, domain.Now())
		v.Generation++
		if v.ConversationID == "" {
			v.ConversationID = domain.ID("conv_")
		}
		if v.Status != "queued" || v.QueuedAt == 0 {
			v.QueuedAt = domain.Now()
			queued, e := s.Store.ListRequirementReviews(c)
			if e != nil {
				return v, e
			}
			for _, item := range queued {
				if item.ProjectID == t.ProjectID && item.Status == "queued" && item.QueuedAt >= v.QueuedAt {
					v.QueuedAt = item.QueuedAt + 1
				}
			}
		}
		v.Evidence, v.NeedsRecheck, v.RecheckReason = nil, false, ""
		v.Source, v.TaskRevision = t, t.Revision
		v.Status, v.Error, v.RunID = "queued", "", ""
		actor := reviewActor(a, authorization)
		if in.Action == "message" {
			v.Messages = append(v.Messages, attributed(domain.RequirementReviewMessage{ID: domain.ID("rm_"), Role: "user", Authorization: authorization, Action: "message", Body: in.Body, At: domain.Now(), Generation: v.Generation, ReplyTo: in.ReplyTo, Answers: in.Answers, QuestionNotes: notes, Attachments: files}, actor))
		}
		if retry != "" {
			body := actor.Name + " 重新发起了评审"
			if retry == "recheck_requested" {
				body = actor.Name + " 请 Agent 重新核对关联需求"
			}
			v.Messages = append(v.Messages, attributed(domain.RequirementReviewMessage{ID: domain.ID("rm_"), Role: "system", Authorization: authorization, Action: retry, Body: body, At: domain.Now(), Generation: v.Generation}, actor))
		}
	case "confirm_rejection":
		if t.ParentRequirementID == "" || t.Status != "clarify" || v.Status != "rejection_pending" || in.Generation != v.Generation || v.TaskRevision != t.Revision {
			return v, domain.ErrConflict
		}
		reason := ""
		for _, msg := range v.Messages {
			if msg.Generation == v.Generation && msg.Result != nil && msg.Result.Decision == "reject_bug" {
				reason = msg.Body
			}
		}
		if reason == "" {
			return v, errors.New("缺少待确认的驳回依据")
		}
		t.Resolution = &domain.TaskResolution{FromStatus: t.Status, Reason: reason, Actor: a.Name, At: domain.Now()}
		t.Status, t.ClaimHash = "closed", ""
		t.Version++
		t.UpdatedAt = domain.Now()
		task = &t
		v.Status, v.Source = "closed", t
		v.ExpireForms(nil, "产品已确认驳回并关闭 Bug。", domain.Now())
		v.Messages = append(v.Messages, attributed(domain.RequirementReviewMessage{ID: domain.ID("rm_"), Role: "user", Authorization: authorization, Action: "confirm_rejection", Body: "产品确认驳回并关闭 Bug", At: domain.Now(), Generation: v.Generation}, reviewActor(a, authorization)))
		ev = &domain.Event{ProjectID: t.ProjectID, TaskID: t.ID, Kind: "bug_rejection_confirmed", Actor: a.Name, Body: reason, At: domain.Now()}
	case "confirm":
		if v.Status != "ready_to_confirm" || in.Generation != v.Generation || v.TaskRevision != t.Revision || t.Status != "clarify" {
			return v, fmt.Errorf("%w: 需求或评审已更新，请阅读最新评审后再确认", domain.ErrConflict)
		}
		if reason, e := s.ReviewRecheckReason(c, v); e != nil {
			return v, e
		} else if reason != "" {
			return v, fmt.Errorf("%w: %s", domain.ErrConflict, reason)
		}
		var proposal *domain.RequirementProposal
		for _, m := range v.Messages {
			if m.Generation == v.Generation && m.Result != nil && m.Result.Decision == "ready" {
				proposal = m.Result.Proposal
			}
		}
		if proposal == nil {
			return v, errors.New("尚无可确认的需求稿")
		}
		t.Description, t.Acceptance = proposal.Description, proposal.Acceptance
		t.Revision++
		t.Version++
		t.Status = "ready"
		t.UpdatedAt = domain.Now()
		task = &t
		v.TaskRevision = t.Revision
		v.Status, v.Error = "confirmed", ""
		v.ExpireForms(nil, "需求稿已确认，此表单已结束。", domain.Now())
		v.Messages = append(v.Messages, attributed(domain.RequirementReviewMessage{ID: domain.ID("rm_"), Role: "user", Authorization: authorization, Action: "confirm", Body: "已确认需求稿与验收条件，需求进入待认领。", At: domain.Now(), Generation: v.Generation}, reviewActor(a, authorization)))
		ev = &domain.Event{ProjectID: t.ProjectID, TaskID: t.ID, Kind: "requirements_confirmed", Actor: a.Name, Body: "产品确认评审需求稿与验收条件，进入待认领", At: domain.Now()}
	default:
		return v, errors.New("不支持的评审操作")
	}
	if ev != nil && authorization != nil {
		ev.Data, _ = json.Marshal(map[string]any{"authorization": authorization})
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
	v.Version++
	v.UpdatedAt = domain.Now()
	body, _ := json.Marshal(v)
	receipt := &domain.Receipt{Key: key, Fingerprint: fp, Response: body}
	err = s.Store.CommitRequirementReview(c, v, expected, taskRevision, task, ev, receipt, batches...)
	if err == nil && task != nil {
		s.NotifyWork(v.ProjectID)
	}
	return v, err
}

func (s *Service) requirementQuestionNotes(c context.Context, project string, v domain.RequirementReview, in RequirementReviewInput) (map[string]domain.RequirementQuestionNote, error) {
	if len(in.QuestionNotes) > 20 {
		return nil, errors.New("单次最多补充 20 个问题")
	}
	questions := map[string]domain.RequirementQuestion{}
	for _, m := range v.Messages {
		if m.ID == in.ReplyTo && m.Result != nil {
			for _, q := range m.Result.Questions {
				questions[q.ID] = q
			}
		}
	}
	total := len(in.Body)
	ids := append([]string{}, in.AttachmentIDs...)
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	for id, note := range in.QuestionNotes {
		q, ok := questions[id]
		if !ok || (q.Type != "radio" && q.Type != "multiselect" && q.Type != "select") {
			return nil, errors.New("补充说明必须关联原消息中的选择题，请刷新后重试；草稿仍保留")
		}
		total += len(note.Body)
		for _, file := range note.AttachmentIDs {
			if !seen[file] {
				ids = append(ids, file)
				seen[file] = true
			}
		}
	}
	if total > 500000 {
		return nil, errors.New("单条消息及逐题说明合计超过 50 万字节，请分段发送；草稿仍保留")
	}
	files, err := s.taskAttachments(c, project, ids)
	if err != nil {
		return nil, err
	}
	byID := map[string]domain.Attachment{}
	for _, file := range files {
		byID[file.ID] = file
	}
	out := map[string]domain.RequirementQuestionNote{}
	for id, note := range in.QuestionNotes {
		if !nonempty(note.Body) && len(note.AttachmentIDs) == 0 {
			continue
		}
		n := domain.RequirementQuestionNote{Body: note.Body}
		seenNote := map[string]bool{}
		for _, fileID := range note.AttachmentIDs {
			file := byID[fileID]
			if file.Kind != "image" || seenNote[fileID] {
				return nil, errors.New("逐题说明只能添加图片，且同一题内不能重复添加同一图片")
			}
			seenNote[fileID] = true
			n.Attachments = append(n.Attachments, file)
		}
		out[id] = n
	}
	return out, nil
}

func (s *Service) BeginRequirementReview(c context.Context, id string, generation int, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.Store.GetRequirementReview(c, id)
	if err != nil {
		return err
	}
	if v.Generation != generation || v.Status != "queued" {
		return domain.ErrConflict
	}
	expected := v.Version
	v.Version++
	v.Status = "running"
	v.RunID = runID
	v.UpdatedAt = domain.Now()
	return s.Store.CommitRequirementReview(c, v, expected, v.TaskRevision, nil, nil, nil)
}

func (s *Service) FinishRequirementReview(c context.Context, id string, generation int, runID string, result *domain.RequirementReviewResult, problem string, evidence ...*domain.RequirementReviewEvidence) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = s.finishRequirementReview(c, id, generation, runID, result, problem, evidence...)
		if !errors.Is(err, domain.ErrConflict) {
			return err
		}
	}
	return err
}
func (s *Service) finishRequirementReview(c context.Context, id string, generation int, runID string, result *domain.RequirementReviewResult, problem string, evidence ...*domain.RequirementReviewEvidence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.Store.GetRequirementReview(c, id)
	if err != nil {
		return err
	}
	// Superseded attempts never replace the newest round or append duplicate messages.
	if v.Generation != generation || (v.Status != "running" && v.Status != "queued") || (v.RunID != "" && v.RunID != runID) {
		return nil
	}
	t, err := s.Store.GetTask(c, id)
	if err != nil {
		return err
	}
	if domain.TaskTerminated(t.Status) {
		expected := v.Version
		v.Status = "closed"
		v.Source = t
		v.TaskRevision = t.Revision
		v.Version++
		v.Generation++
		v.UpdatedAt = domain.Now()
		return s.Store.CommitRequirementReview(c, v, expected, t.Revision, nil, nil, nil)
	}
	if t.ParentRequirementID != "" && t.Status != "clarify" {
		problem = "Bug 已进入后续处理，旧评审不能改变其状态"
	}
	if t.Revision != v.TaskRevision {
		// The next start/message rebases on the new source; preserve this attempt as failed.
		result = nil
		problem = "需求内容已更新，请基于新版本重新评审。"
		v.TaskRevision = t.Revision
		v.Source = t
	}
	expected := v.Version
	v.Version++
	v.UpdatedAt = domain.Now()
	v.Error = problem
	if problem == "" {
		if result == nil {
			problem = "Agent 未返回完整评审，请重试。"
		} else if err = ValidateRequirementReviewResult(*result); err != nil {
			problem = err.Error()
		}
		if problem == "" {
			for _, formID := range result.ExpireFormIDs {
				if !slices.ContainsFunc(v.Messages, func(m domain.RequirementReviewMessage) bool {
					return m.ID == formID && m.Role == "assistant" && m.Result != nil && len(m.Result.Questions) > 0
				}) {
					problem = "待过期的表单不属于本次评审"
					break
				}
			}
		}
	}
	if problem == "" && result != nil && result.Decision == "reject_bug" && t.ParentRequirementID == "" {
		problem = "仅关联测试 Bug 可以提出驳回建议"
	}
	if problem != "" {
		v.Status, v.Error = "failed", problem
		v.Messages = append(v.Messages, domain.RequirementReviewMessage{ID: domain.ID("rm_"), Role: "system", Action: "review_failed", Body: "评审没有完成：" + problem, Event: &domain.RequirementReviewEvent{Reason: problem}, Generation: generation, At: domain.Now()})
	} else {
		if len(evidence) > 0 {
			v.Evidence = evidence[0]
		}
		now := domain.Now()
		if len(result.ExpireFormIDs) > 0 {
			v.ExpireForms(result.ExpireFormIDs, "Agent 已根据最新需求将此表单设为过期。", now)
		}
		if len(result.Questions) > 0 || result.Ask != nil {
			v.ExpireForms(nil, "已有新的问题，请以最新问题为准。", now)
		}
		if result.Agenda != nil {
			v.Agenda = result.Agenda
		}
		v.Status = "needs_input"
		if result.Decision == "ready" {
			v.Status = "ready_to_confirm"
		}
		v.Messages = append(v.Messages, domain.RequirementReviewMessage{ID: "rm_" + runID, Role: "assistant", ActorID: "platform", ActorKind: "platform", Body: result.Body, Generation: generation, At: now, Result: result})
	}
	var task *domain.Task
	var ev *domain.Event
	if problem == "" && result != nil && t.ParentRequirementID != "" {
		if result.Decision == "reject_bug" {
			v.Status = "rejection_pending"
		} else if result.Decision == "ready" {
			t.Description, t.Acceptance = result.Proposal.Description, result.Proposal.Acceptance
			t.Revision++
			t.Version++
			t.Status = "ready"
			t.UpdatedAt = domain.Now()
			v.Status, v.TaskRevision, v.Source = "confirmed", t.Revision, t
			task = &t
			ev = &domain.Event{ProjectID: t.ProjectID, TaskID: t.ID, Kind: "bug_confirmed", Actor: "平台 Agent", Body: "Bug 评审确认存在，进入待认领", At: domain.Now()}
		}
	}
	taskRevision := v.TaskRevision
	if task != nil {
		taskRevision = t.Revision - 1
	}
	err = s.Store.CommitRequirementReview(c, v, expected, taskRevision, task, ev, nil)
	if err == nil && task != nil {
		s.NotifyWork(t.ProjectID)
	}
	return err
}

func ValidateRequirementReviewResult(v domain.RequirementReviewResult) error {
	if len(v.Relations) > 20 {
		return errors.New("关联需求一次最多 20 条")
	}
	for _, rel := range v.Relations {
		if rel.TaskID == "" || !nonempty(rel.Reason) || !slices.Contains([]string{"overlap", "dependency", "conflict"}, rel.Kind) {
			return errors.New("关联需求需要编号、关系类型和具体依据")
		}
	}
	if !nonempty(v.Body) || len(v.Body) > 100000 {
		return errors.New("评审说明为空或过长")
	}
	if !slices.Contains([]string{"clarify", "ready", "infeasible", "reject_bug"}, v.Decision) {
		return errors.New("评审结论必须为 clarify、ready、infeasible 或关联 Bug 的 reject_bug")
	}
	if len(v.Questions) > 8 {
		return errors.New("一轮最多提出 8 个关键问题")
	}
	ids := map[string]bool{}
	for _, q := range v.Questions {
		if q.ID == "" || ids[q.ID] || !nonempty(q.Label) {
			return errors.New("问题需要唯一 ID 与标题")
		}
		ids[q.ID] = true
		if !slices.Contains([]string{"text", "textarea", "select", "radio", "multiselect"}, q.Type) {
			return errors.New("不支持的问题控件类型")
		}
		choice := q.Type == "select" || q.Type == "radio" || q.Type == "multiselect"
		if choice && (len(q.Options) < 2 || len(q.Options) > 20) {
			return errors.New("选择题需要 2 至 20 个选项")
		}
		seen := map[string]bool{}
		for _, option := range q.Options {
			if !nonempty(option) || seen[option] {
				return errors.New("选项不能为空或重复")
			}
			seen[option] = true
		}
		if q.Type != "multiselect" && len(q.Recommended) > 1 {
			return errors.New("此问题只能推荐一个答案")
		}
		for _, r := range q.Recommended {
			if choice && !slices.Contains(q.Options, r) {
				return errors.New("推荐答案必须来自选项")
			}
		}
	}
	if v.Proposal != nil {
		if !nonempty(v.Proposal.Description) || len(v.Proposal.Acceptance) == 0 {
			return errors.New("建议需求稿需要正文和验收条件")
		}
		for _, condition := range v.Proposal.Acceptance {
			if !nonempty(condition) {
				return errors.New("验收条件不能为空")
			}
		}
	}
	if v.Decision == "reject_bug" && len(v.Questions) != 0 {
		return errors.New("驳回建议不能同时包含未解问题")
	}
	if v.Decision == "ready" {
		if len(v.Questions) > 0 || v.Proposal == nil || !nonempty(v.Proposal.Description) || len(v.Proposal.Acceptance) == 0 {
			return errors.New("待确认评审需要完整需求稿、验收条件且无待解问题")
		}
		for _, condition := range v.Proposal.Acceptance {
			if !nonempty(condition) {
				return errors.New("验收条件不能为空")
			}
		}
	}
	return nil
}

// ReviewPrompt is an immutable snapshot. All input is data, never tool authority.
func ReviewPrompt(v domain.RequirementReview) string {
	messages := append([]domain.RequirementReviewMessage{}, v.Messages...)
	for i := range messages {
		// People are addressed by name; account IDs are not conversation content.
		messages[i].Authorization, messages[i].ActorID = nil, ""
		if messages[i].Source != nil {
			t := ReviewSource(*messages[i].Source)
			messages[i].Source = &t
		}
	}
	agenda := v.Agenda
	if agenda == nil {
		agenda = []domain.RequirementTopic{}
	}
	b, _ := json.Marshal(struct {
		Source   domain.Task                       `json:"requirement"`
		Agenda   []domain.RequirementTopic         `json:"agenda"`
		Messages []domain.RequirementReviewMessage `json:"conversation"`
	}{ReviewSource(v.Source), agenda, messages})
	return strings.TrimSpace(string(b))
}
