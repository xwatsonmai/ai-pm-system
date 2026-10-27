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

func taskClientID(a domain.Actor) string {
	if a.ClientID != "" {
		return a.ClientID
	}
	return a.ID // legacy project tokens each have their own authenticated identity
}

func ownsTaskClient(t domain.Task, a domain.Actor) bool {
	return t.Claimant == a.ID && (t.ClaimClientID == "" || t.ClaimClientID == taskClientID(a))
}

func alignmentReserved(t domain.Task) bool {
	return t.Alignment != nil && (t.Status == "aligning" || t.Status == "ready") && t.Alignment.Status != "completed"
}

// Task, conversation, event and idempotency receipt always change together.
func (s *Service) commitAlignment(c context.Context, a domain.Actor, t domain.Task, v domain.RequirementReview, taskRevision int, kind, body, key, fp, claim string) (domain.Mutation, error) {
	expected := v.Version
	v.Version++
	t.Version++
	t.UpdatedAt = domain.Now()
	v.Source, v.TaskRevision, v.UpdatedAt = t, t.Revision, t.UpdatedAt
	m := domain.Mutation{Task: t, ClaimID: claim}
	response, err := json.Marshal(m)
	if err != nil {
		return m, err
	}
	payload, err := json.Marshal(map[string]any{"project_version_id": t.ProjectVersionID, "generation": v.Generation})
	if err != nil {
		return m, err
	}
	ev := &domain.Event{ProjectID: t.ProjectID, TaskID: t.ID, Kind: kind, Actor: a.Name, Body: body, Data: payload, At: t.UpdatedAt}
	r := &domain.Receipt{Key: key, Fingerprint: fp, Response: response}
	// The update does not change the target milestone. Its scope snapshot is
	// refreshed by CommitRequirementReview in this same transaction.
	err = s.Store.CommitRequirementReview(c, v, expected, taskRevision, &t, ev, r)
	if err == nil {
		s.NotifyWork(t.ProjectID)
	}
	return m, err
}

func (s *Service) actDevelopmentAlignment(c context.Context, a domain.Actor, id string, in Action) (domain.Mutation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.Store.GetTask(c, id)
	if err != nil {
		return domain.Mutation{}, err
	}
	if err = s.CheckProject(c, a, t.ProjectID, false); err != nil {
		return domain.Mutation{}, err
	}
	if !nonempty(in.RequestID) {
		return domain.Mutation{}, errors.New("缺少 request_id")
	}
	key := "development-alignment:" + taskClientID(a) + ":" + receiptKey(a, t.ProjectID, in.RequestID)
	fp := fingerprint(struct {
		ID    string
		Input Action
	}{id, in})
	if m, ok, e := s.prior(c, key, fp); ok || e != nil {
		return m, e
	}
	if in.Version <= 0 || in.Version != t.Version || in.Revision != t.Revision {
		return domain.Mutation{}, fmt.Errorf("%w: 请读取最新需求 version、revision 及对齐回复后操作", domain.ErrConflict)
	}
	if domain.TaskFinished(t.Status) {
		return domain.Mutation{}, domain.ErrConflict
	}
	v, err := s.Store.GetRequirementReview(c, id)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.Mutation{}, err
	}
	if v.TaskID == "" {
		v = domain.RequirementReview{TaskID: id, ProjectID: t.ProjectID, Messages: []domain.RequirementReviewMessage{}}
	}
	body := strings.TrimSpace(in.Body)
	oldRevision := t.Revision
	claim := ""
	switch in.Action {
	case "request_alignment":
		if a.Kind != "developer" || !ownsTaskClient(t, a) || !nonempty(in.ClaimID) || Hash(in.ClaimID) != t.ClaimHash {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if t.Status != "doing" && !(t.Status == "ready" && alignmentReserved(t)) {
			return domain.Mutation{}, domain.ErrConflict
		}
		result := domain.RequirementReviewResult{Body: body, Decision: "clarify", Questions: in.Questions}
		if err = ValidateRequirementReviewResult(result); err != nil {
			return domain.Mutation{}, err
		}
		v.ExpireForms(nil, "开发代理已提出新一轮对齐问题，请以最新问题为准。", domain.Now())
		v.Generation++
		v.Mode, v.Status, v.RunID, v.Error = "development_alignment", "needs_input", "", ""
		v.Evidence, v.NeedsRecheck, v.RecheckReason = nil, false, ""
		source := t
		v.Messages = append(v.Messages, attributed(domain.RequirementReviewMessage{ID: domain.ID("rm_"), Role: "assistant", Action: "request_alignment", Body: body, Result: &result, Source: &source, Generation: v.Generation, At: domain.Now()}, reviewActor(a, nil)))
		t.Status, t.Blocker = "aligning", body
		t.ClaimClientID = taskClientID(a)
		t.Alignment = &domain.DevelopmentAlignment{Status: "waiting_product"}
	case "resume_development":
		if a.Kind != "developer" || !ownsTaskClient(t, a) || !nonempty(in.ClaimID) || Hash(in.ClaimID) != t.ClaimHash {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if t.Status != "ready" || t.Alignment == nil || t.Alignment.Status != "replied" || v.Mode != "development_alignment" || v.Status != "alignment_replied" {
			return domain.Mutation{}, domain.ErrConflict
		}
		if body == "" {
			return domain.Mutation{}, errors.New("请说明已理解的对齐结论与继续开发的依据")
		}
		t.Alignment = &domain.DevelopmentAlignment{Status: "completed"}
		t.Status, t.Blocker = "doing", ""
		v.Status = "alignment_completed"
		claim = domain.ID("claim_")
		t.ClaimHash = Hash(claim)
		v.Messages = append(v.Messages, attributed(domain.RequirementReviewMessage{ID: domain.ID("rm_"), Role: "system", Action: "alignment_completed", Body: "开发代理确认对齐完成，继续开发：" + body, Generation: v.Generation, At: domain.Now()}, reviewActor(a, nil)))
	case "reopen_claim":
		if a.Kind != "human" {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if t.Status != "ready" || t.Alignment == nil || t.Alignment.Status != "replied" {
			return domain.Mutation{}, domain.ErrConflict
		}
		if body == "" {
			body = "产品重新开放领取"
		}
		appendix := alignmentAppendix(t, v)
		t.Description += appendix
		t.Revision++
		t.ClaimHash, t.Claimant, t.ClaimClientID, t.Owner, t.Branch, t.Blocker = "", "", "", "", "", ""
		t.Alignment = nil
		v.Mode, v.Status = "", "confirmed"
		v.ArchivedAlignmentGeneration = v.Generation
		v.Messages = append(v.Messages, attributed(domain.RequirementReviewMessage{ID: domain.ID("rm_"), Role: "system", Action: "reopen_claim", Body: "产品重新开放领取；开发对齐记录已追加进需求正文。原因：" + body, Generation: v.Generation, At: domain.Now()}, reviewActor(a, nil)))
	default:
		return domain.Mutation{}, domain.ErrForbidden
	}
	return s.commitAlignment(c, a, t, v, oldRevision, in.Action, body, key, fp, claim)
}

// Product replies go directly to the development client. No platform model runs,
// and no amount of form completion declares the alignment completed.
func (s *Service) replyDevelopmentAlignment(c context.Context, a domain.Actor, t domain.Task, v domain.RequirementReview, in RequirementReviewInput, key, fp string, authorization *domain.TaskActionAuthorization) (domain.RequirementReview, error) {
	if in.Action == "start" {
		v.Source = t
		return v, nil
	}
	if in.Action != "message" || !alignmentReserved(t) {
		return v, fmt.Errorf("%w: 本轮由开发客户端确认对齐完成；新问题由开发代理发起", domain.ErrConflict)
	}
	if in.Generation != v.Generation || v.TaskRevision != t.Revision {
		return v, domain.ErrConflict
	}
	if len(in.Body) > 500000 {
		return v, errors.New("单条说明超过 50 万字节，请分段发送")
	}
	if len(in.Answers) > 8 {
		return v, errors.New("单次最多回答 8 个问题")
	}
	questions := map[string]domain.RequirementQuestion{}
	for _, m := range v.Messages {
		if m.ID == in.ReplyTo && m.Action == "request_alignment" && m.Generation == v.Generation && m.FormExpiredAt == 0 && m.Result != nil {
			for _, q := range m.Result.Questions {
				questions[q.ID] = q
			}
		}
	}
	if in.ReplyTo != "" && len(questions) == 0 {
		return v, fmt.Errorf("%w: 表单已过期，请刷新或直接补充说明", domain.ErrConflict)
	}
	for id, values := range in.Answers {
		q, ok := questions[id]
		if !ok {
			return v, errors.New("回答必须关联当前问题")
		}
		if q.Type != "multiselect" && len(values) > 1 {
			return v, errors.New("此问题只能填写一个答案")
		}
		for _, value := range values {
			if !nonempty(value) || len(value) > 60000 {
				return v, errors.New("问题答案为空或过长")
			}
			if slices.Contains([]string{"select", "radio", "multiselect"}, q.Type) && !slices.Contains(q.Options, value) {
				return v, errors.New("回答必须来自原问题的选项，也可直接发送文字纠正")
			}
		}
	}
	files, err := s.taskAttachments(c, t.ProjectID, in.AttachmentIDs)
	if err != nil {
		return v, err
	}
	notes, err := s.requirementQuestionNotes(c, t.ProjectID, v, in)
	if err != nil {
		return v, err
	}
	hasAnswer := false
	for _, values := range in.Answers {
		hasAnswer = hasAnswer || len(values) > 0
	}
	if !nonempty(in.Body) && !hasAnswer && len(files) == 0 && len(notes) == 0 {
		return v, errors.New("请填写部分回答或直接输入你的看法")
	}
	v.Messages = append(v.Messages, attributed(domain.RequirementReviewMessage{ID: domain.ID("rm_"), Role: "user", Action: "alignment_reply", Authorization: authorization, Body: in.Body, ReplyTo: in.ReplyTo, Answers: in.Answers, QuestionNotes: notes, Attachments: files, Generation: v.Generation, At: domain.Now()}, reviewActor(a, authorization)))
	v.ExpireForms(nil, "产品已提交回复，等待原开发客户端判断；仍可继续补充文字。", domain.Now())
	t.Status, t.Blocker = "ready", ""
	t.Alignment = &domain.DevelopmentAlignment{Status: "replied"}
	// Reply content is part of the requirement revision even before it is folded
	// into the document by the product's explicit reopen-claim action.
	oldRevision := t.Revision
	t.Revision++
	expected := v.Version
	v.Status = "alignment_replied"
	v.Version++
	t.Version++
	t.UpdatedAt = domain.Now()
	v.Source, v.TaskRevision, v.UpdatedAt = t, t.Revision, t.UpdatedAt
	response, _ := json.Marshal(v)
	payload, _ := json.Marshal(map[string]any{"authorization": authorization, "project_version_id": t.ProjectVersionID, "generation": v.Generation})
	ev := &domain.Event{ProjectID: t.ProjectID, TaskID: t.ID, Kind: "alignment_reply", Actor: a.Name, Body: "产品已提交开发对齐回复，待原 MCP 客户端确认是否继续开发", Data: payload, At: t.UpdatedAt}
	err = s.Store.CommitRequirementReview(c, v, expected, oldRevision, &t, ev, &domain.Receipt{Key: key, Fingerprint: fp, Response: response})
	if err == nil {
		s.NotifyWork(t.ProjectID)
	}
	return v, err
}

func alignmentAppendix(t domain.Task, v domain.RequirementReview) string {
	var out strings.Builder
	out.WriteString("\n\n## 需求评审对齐补充\n\n以下为开发过程中与产品对齐的原始记录，供重新领取时理解需求。\n")
	for _, m := range v.Messages {
		if m.Generation <= v.ArchivedAlignmentGeneration {
			continue
		}
		if m.Action != "request_alignment" && m.Action != "alignment_reply" {
			continue
		}
		label := "开发提出的问题"
		if m.Action == "alignment_reply" {
			label = "产品回复与纠正"
		}
		fmt.Fprintf(&out, "\n### %s\n\n%s\n", label, m.Body)
		if m.Result != nil {
			for _, q := range m.Result.Questions {
				fmt.Fprintf(&out, "\n- %s", q.Label)
				if len(q.Options) > 0 {
					fmt.Fprintf(&out, "（选项：%s）", strings.Join(q.Options, "；"))
				}
				out.WriteString("\n")
			}
		}
		for _, question := range v.Messages {
			if question.ID != m.ReplyTo || question.Result == nil {
				continue
			}
			for _, q := range question.Result.Questions {
				if values := m.Answers[q.ID]; len(values) > 0 {
					fmt.Fprintf(&out, "\n- %s：%s\n", q.Label, strings.Join(values, "；"))
				}
				if note, ok := m.QuestionNotes[q.ID]; ok {
					fmt.Fprintf(&out, "\n- %s的补充：%s\n", q.Label, note.Body)
					appendAlignmentFiles(&out, t.ProjectID, note.Attachments)
				}
			}
		}
		appendAlignmentFiles(&out, t.ProjectID, m.Attachments)
	}
	return out.String()
}

func appendAlignmentFiles(out *strings.Builder, project string, files []domain.Attachment) {
	for _, f := range files {
		fmt.Fprintf(out, "\n附件 [%s](/api/projects/%s/attachments/%s)\n", strings.NewReplacer("[", "\\[", "]", "\\]", "\n", " ", "\r", " ").Replace(f.Name), project, f.ID)
	}
}
