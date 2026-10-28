package app

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

type Service struct {
	ValidateTestingCode func(context.Context, string, string, string, []string) error
	Store               storage.Store
	mu                  sync.Mutex
	OnChange            func(string, string, string)
	workMu              sync.Mutex
	workListeners       map[string]map[chan struct{}]struct{}
}

func Hash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func Human() domain.Actor {
	return domain.Actor{ID: "local-owner", Name: "项目负责人", Kind: "human"}
}
func (s *Service) notify(p, t, k string) {
	s.NotifyWork(p)
	if s.OnChange != nil {
		s.OnChange(p, t, k)
	}
}
func nonempty(s string) bool { return strings.TrimSpace(s) != "" }

func (s *Service) SaveProject(ctx context.Context, p domain.Project) (domain.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveProject(ctx, p)
}
func (s *Service) saveProject(ctx context.Context, p domain.Project) (domain.Project, error) {
	if p.AgentReadMode != "" && p.AgentReadMode != "legacy" && p.AgentReadMode != "sandbox" {
		return p, errors.New("不支持的 Agent 代码阅读方式")
	}
	p.AgentRef = strings.TrimSpace(p.AgentRef)
	if strings.HasPrefix(p.AgentRef, "-") || strings.ContainsAny(p.AgentRef, "\x00\r\n") {
		return p, errors.New("无效的 Agent 代码版本")
	}
	if !nonempty(p.Name) {
		return p, errors.New("请填写项目名称")
	}
	if p.ID == "" {
		p.ID = domain.ID("prj_")
		p.CreatedAt = domain.Now()
	} else {
		old, e := s.Store.GetProject(ctx, p.ID)
		if e != nil {
			return p, e
		}
		if old.OwnerID != "" && p.OwnerID != old.OwnerID {
			return p, domain.ErrForbidden
		}
		p.CreatedAt = old.CreatedAt
		p.SHA = old.SHA
		p.SyncedAt = old.SyncedAt
		p.SyncError = old.SyncError
		if old.Repository != "" && p.Repository != old.Repository {
			return p, errors.New("首版不支持替换已有项目的仓库，请创建新项目")
		}
		if p.Branch != old.Branch {
			p.SHA = ""
			p.SyncedAt = 0
		}
	}
	if p.Branch == "" {
		p.Branch = "main"
	}
	if strings.HasPrefix(p.Repository, "-") || strings.ContainsAny(p.Repository, "\n\r\x00") {
		return p, errors.New("Git 地址不合法")
	}
	if strings.Contains(p.Repository, "://") {
		u, e := url.Parse(p.Repository)
		if e != nil || !(u.Scheme == "https" || u.Scheme == "http" || u.Scheme == "ssh" || u.Scheme == "file") {
			return p, errors.New("请使用 HTTPS、SSH 或本地 Git 地址")
		}
		if u.User != nil {
			if _, ok := u.User.Password(); ok {
				return p, errors.New("请勿在 Git 地址中保存密码；使用 Git 凭据助手或 SSH")
			}
		}
	}
	if p.ProfileID != "" {
		if _, e := s.Store.GetProfile(ctx, p.ProfileID); e != nil {
			return p, e
		}
	}
	return p, s.Store.PutProject(ctx, p)
}
func (s *Service) UpdateSync(ctx context.Context, id, sha, problem string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.Store.GetProject(ctx, id)
	if e != nil {
		return e
	}
	p.SyncError = problem
	if problem == "" {
		p.SHA = sha
		p.SyncedAt = domain.Now()
	}
	return s.Store.PutProject(ctx, p)
}
func (s *Service) SaveProfile(c context.Context, p domain.Profile, key string) (domain.Profile, error) {
	if p.ContextWindow < 0 || p.MaxTokens < 0 || (p.ContextWindow > 0 && p.ContextWindow < 4096) || (p.ContextWindow > 0 && p.MaxTokens >= p.ContextWindow) {
		return p, errors.New("上下文窗口至少 4096 tokens，最大输出须为正数且小于上下文窗口；留空使用默认值")
	}
	if !nonempty(p.Name) || !nonempty(p.Model) || !nonempty(p.Provider) {
		return p, errors.New("请填写配置名称、模型提供方和模型 ID")
	}
	if p.ID == "" {
		p.ID = domain.ID("mdl_")
	} else {
		old, e := s.Store.GetProfile(c, p.ID)
		if e != nil {
			return p, e
		}
		p.APIKey = old.APIKey
	}
	if key != "" {
		p.APIKey = key
	}
	p.HasKey = p.APIKey != ""
	if p.BaseURL != "" {
		u, e := url.Parse(p.BaseURL)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return p, errors.New("模型 API 地址不合法")
		}
	}
	if p.API == "" {
		p.API = "openai-completions"
	}
	switch p.API {
	case "openai-completions", "openai-responses", "anthropic-messages":
	default:
		return p, errors.New("不支持的模型 API 类型")
	}
	return p, s.Store.PutProfile(c, p)
}

type TaskInput struct {
	ParentRequirementID string   `json:"parent_requirement_id,omitempty"`
	ParentVersion       int      `json:"parent_version,omitempty"`
	Title               string   `json:"title"`
	Kind                string   `json:"kind"`
	Description         string   `json:"description"`
	AttachmentIDs       []string `json:"attachment_ids"`
	Acceptance          []string `json:"acceptance"`
	Priority            string   `json:"priority"`
	Reviewer            string   `json:"reviewer"`
	Version             int      `json:"version"`
	RequestID           string   `json:"request_id"`
}

func validateInput(v *TaskInput) error {
	v.Title = strings.TrimSpace(v.Title)
	if v.Title == "" {
		return errors.New("请填写需求标题")
	}
	if len(v.Title) > 600 || len(v.Description) > 60000 {
		return errors.New("需求内容过长")
	}
	if v.Kind == "" {
		v.Kind = "requirement"
	}
	if v.Kind != "requirement" && v.Kind != "bug" {
		return errors.New("需求类型必须为迭代需求或 Bug 修复")
	}
	if v.Priority == "" {
		v.Priority = "next"
	}
	if v.Priority != "now" && v.Priority != "next" && v.Priority != "later" {
		return errors.New("优先级不合法")
	}
	clean := []string{}
	for _, a := range v.Acceptance {
		if nonempty(a) {
			clean = append(clean, strings.TrimSpace(a))
		}
	}
	v.Acceptance = clean
	return nil
}

func agentCreatedTaskStatus(kind, description string, acceptance []string) (string, error) {
	if kind != "bug" {
		return "clarify", nil
	}
	if nonempty(description) {
		for _, item := range acceptance {
			if nonempty(item) {
				return "ready", nil
			}
		}
	}
	return "", errors.New("Agent 创建 Bug 前需整理问题描述和至少一条验收条件，创建后直接进入待认领")
}
func receiptKey(a domain.Actor, p, request string) string {
	return a.ID + ":" + p + ":" + request
}
func fingerprint(v any) string { b, _ := json.Marshal(v); return Hash(string(b)) }
func (s *Service) prior(c context.Context, key, fp string) (domain.Mutation, bool, error) {
	r, e := s.Store.GetReceipt(c, key)
	if errors.Is(e, domain.ErrNotFound) {
		return domain.Mutation{}, false, nil
	}
	if e != nil {
		return domain.Mutation{}, false, e
	}
	if r.Fingerprint != fp {
		return domain.Mutation{}, false, domain.ErrRequestReused
	}
	var m domain.Mutation
	e = json.Unmarshal(r.Response, &m)
	return m, true, e
}
func (s *Service) commit(c context.Context, a domain.Actor, t domain.Task, old int, kind, body string, data any, key, fp, claim string, note ...*domain.RequirementReviewNote) (domain.Mutation, error) {
	t.UpdatedAt = domain.Now()
	t.Version = old + 1
	// Claim credentials belong to the caller's receipt, not shared project history.
	if action, ok := data.(Action); ok {
		action.ClaimID = ""
		data = action
	}
	payload, e := json.Marshal(data)
	if e != nil {
		return domain.Mutation{}, e
	}
	ev := domain.Event{ProjectID: t.ProjectID, TaskID: t.ID, Kind: kind, Actor: a.Name, Body: body, Data: payload, At: t.UpdatedAt}
	if len(note) > 0 {
		ev.ReviewNote = note[0]
	}
	m, e := s.commitWithMilestone(c, a, t, old, ev, key, fp, claim)
	if e == nil {
		if a.Kind != "platform" {
			s.notify(t.ProjectID, t.ID, kind)
		} else {
			s.NotifyWork(t.ProjectID)
		}
	}
	return m, e
}
func (s *Service) CreateTask(c context.Context, a domain.Actor, p string, v TaskInput) (domain.Mutation, error) {
	return s.createTask(c, a, p, v, nil)
}
func (s *Service) createTask(c context.Context, a domain.Actor, p string, v TaskInput, authorization *domain.TaskActionAuthorization) (domain.Mutation, error) {
	if e := s.CheckProject(c, a, p, false); e != nil {
		return domain.Mutation{}, e
	}
	if _, e := s.Store.GetProject(c, p); e != nil {
		return domain.Mutation{}, e
	}
	if a.Kind == "developer" && authorization == nil {
		return domain.Mutation{}, domain.ErrForbidden
	}
	if e := validateInput(&v); e != nil {
		return domain.Mutation{}, e
	}
	if a.Kind == "platform" && v.Kind != "bug" {
		return domain.Mutation{}, domain.ErrForbidden
	}
	if v.RequestID == "" {
		return domain.Mutation{}, errors.New("缺少 request_id")
	}
	key := receiptKey(a, p, v.RequestID)
	fp := authorizedFingerprint(v, authorization)
	if m, ok, e := s.prior(c, key, fp); ok || e != nil {
		return m, e
	}
	attachments, e := s.taskAttachments(c, p, v.AttachmentIDs)
	if e != nil {
		return domain.Mutation{}, e
	}
	t := domain.Task{ID: domain.ID("tsk_"), ProjectID: p, Title: v.Title, Kind: v.Kind, Description: v.Description, Attachments: attachments, Acceptance: v.Acceptance, Priority: v.Priority, Reviewer: v.Reviewer, Status: "clarify", Revision: 1, CreatedAt: domain.Now()}
	creator := reviewActor(a, authorization)
	t.Creator = &creator
	if v.ParentRequirementID != "" {
		if a.Kind != "human" || v.Kind != "bug" {
			return domain.Mutation{}, domain.ErrForbidden
		}
		parent, err := s.Store.GetTask(c, v.ParentRequirementID)
		if err != nil {
			return domain.Mutation{}, err
		}
		if parent.ProjectID != p || parent.Kind != "requirement" {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if parent.Version != v.ParentVersion {
			return domain.Mutation{}, domain.ErrConflict
		}
		if parent.Status != "testing" && parent.Status != "fixing" && parent.Status != "retest" {
			return domain.Mutation{}, errors.New("只能在需求测试或修复期间提起关联 Bug")
		}
		if parent.Submission == nil || parent.Submission.Revision != parent.Revision || !nonempty(parent.Branch) {
			return domain.Mutation{}, errors.New("需求缺少当前提测版本或开发分支，请由开发方补全后提起 Bug")
		}
		t.ParentRequirementID = parent.ID
		t.BugContext = &domain.BugContext{TaskID: parent.ID, Revision: parent.Revision, Version: parent.Version, Branch: parent.Branch, SHA: parent.Submission.SHA, Description: parent.Description, Acceptance: append([]string{}, parent.Acceptance...)}
	}
	if a.Kind == "platform" {
		t.Status, e = agentCreatedTaskStatus(v.Kind, v.Description, v.Acceptance)
		if e != nil {
			return domain.Mutation{}, e
		}
	}
	return s.commit(c, a, t, 0, "created", "创建需求", authorizedData(v, authorization), key, fp, "")
}
func (s *Service) EditTask(c context.Context, a domain.Actor, id string, v TaskInput) (domain.Mutation, error) {
	return s.editTask(c, a, id, v, nil)
}
func (s *Service) editTask(c context.Context, a domain.Actor, id string, v TaskInput, authorization *domain.TaskActionAuthorization, fingerprints ...string) (domain.Mutation, error) {
	if a.Kind != "human" && authorization == nil {
		return domain.Mutation{}, domain.ErrForbidden
	}
	if e := validateInput(&v); e != nil {
		return domain.Mutation{}, e
	}
	t, e := s.Store.GetTask(c, id)
	if e != nil {
		return domain.Mutation{}, e
	}
	if e = s.CheckProject(c, a, t.ProjectID, false); e != nil {
		return domain.Mutation{}, e
	}
	if v.RequestID == "" {
		return domain.Mutation{}, errors.New("缺少 request_id")
	}
	key := receiptKey(a, t.ProjectID, v.RequestID)
	fp := authorizedFingerprint(struct {
		ID    string
		Input TaskInput
	}{id, v}, authorization)
	if len(fingerprints) > 0 {
		fp = fingerprints[0]
	}
	if m, ok, e := s.prior(c, key, fp); ok || e != nil {
		return m, e
	}
	if v.Version != t.Version {
		return domain.Mutation{}, domain.ErrConflict
	}
	if domain.TaskTerminated(t.Status) {
		return domain.Mutation{}, fmt.Errorf("%w: 需求已结束，请先重新打开再编辑；原内容仍保留", domain.ErrConflict)
	}
	if alignmentReserved(t) {
		return domain.Mutation{}, fmt.Errorf("%w: 请在开发对齐中补充或纠正需求，或先重新开放领取", domain.ErrConflict)
	}
	// An omitted attachment_ids preserves legacy clients' attachment associations;
	// an explicit empty array removes them.
	attachments := t.Attachments
	if v.AttachmentIDs != nil {
		attachments, e = s.taskAttachments(c, t.ProjectID, v.AttachmentIDs)
		if e != nil {
			return domain.Mutation{}, e
		}
	}
	changed := t.Title != v.Title || t.Kind != v.Kind || t.Description != v.Description || fingerprint(t.Acceptance) != fingerprint(v.Acceptance) || fingerprint(t.Attachments) != fingerprint(attachments)
	t.Title = v.Title
	t.Description = v.Description
	t.Attachments = attachments
	t.Acceptance = v.Acceptance
	t.Priority = v.Priority
	t.Reviewer = v.Reviewer
	t.Kind = v.Kind
	if changed && t.Status == "ready" {
		if _, err := s.Store.GetRequirementReview(c, t.ID); err == nil {
			t.Status = "clarify"
		} else if !errors.Is(err, domain.ErrNotFound) {
			return domain.Mutation{}, err
		}
	}
	if t.ParentRequirementID != "" && v.Kind != "bug" {
		return domain.Mutation{}, errors.New("关联 Bug 不能更改为需求")
	}
	if v.ParentRequirementID != "" && v.ParentRequirementID != t.ParentRequirementID {
		return domain.Mutation{}, errors.New("不能更换 Bug 所属需求")
	}
	if changed {
		t.TestVerification = nil
		if t.Status == "testing" || t.Status == "retest" || t.Status == "fixing" {
			return domain.Mutation{}, errors.New("测试期间请通过关联 Bug 跟进问题，正式范围变化需另提需求")
		}
		if t.Status == "verifying" || t.Status == "resolved" {
			t.Status = "doing"
		}
		t.Revision++
		if t.Status == "pending_release" || t.Status == "review" {
			t.Status = "doing"
		}
		if t.Status == "done" || t.Status == "canceled" {
			t.Status = "clarify"
			t.ClaimHash = ""
			t.Claimant = ""
			t.ClaimClientID = ""
			t.Alignment = nil
			t.Owner = ""
		}
	}
	var note *domain.RequirementReviewNote
	if changed {
		actor := reviewActor(a, authorization)
		note = &domain.RequirementReviewNote{ExpireForms: "需求已编辑，请以新版本的评审为准。", Message: attributed(domain.RequirementReviewMessage{
			ID: domain.ID("rm_"), Role: "system", Action: "task_edited", Authorization: authorization, At: domain.Now(), Source: &t,
			Body:  fmt.Sprintf("%s 编辑了需求（R%d → R%d）", actor.Name, t.Revision-1, t.Revision),
			Event: &domain.RequirementReviewEvent{FromRevision: t.Revision - 1, ToRevision: t.Revision},
		}, actor)}
	}
	return s.commit(c, a, t, t.Version, "revised", "更新需求；提交成果前请读取最新要求", authorizedData(v, authorization), key, fp, "", note)
}

type Action struct {
	SubmissionSHA    string                       `json:"submission_sha,omitempty"`
	UserConfirmed    bool                         `json:"user_confirmed,omitempty"`
	UserInstruction  string                       `json:"user_instruction,omitempty"`
	TestVerification *domain.TestVerification     `json:"test_verification,omitempty"`
	Questions        []domain.RequirementQuestion `json:"questions,omitempty"`
	ProjectVersionID string                       `json:"project_version_id,omitempty"`
	Action           string                       `json:"action"`
	RequestID        string                       `json:"request_id"`
	ClaimID          string                       `json:"claim_id,omitempty"`
	Revision         int                          `json:"revision,omitempty"`
	Version          int                          `json:"version,omitempty"`
	Body             string                       `json:"body,omitempty"`
	Branch           string                       `json:"branch,omitempty"`
	ReportKind       string                       `json:"report_kind,omitempty"`
	Submission       *domain.Submission           `json:"submission,omitempty"`
	Deployment       *domain.Deployment           `json:"deployment,omitempty"`
	Review           *domain.Review               `json:"review,omitempty"`
}

func (s *Service) Act(c context.Context, a domain.Actor, id string, in Action) (domain.Mutation, error) {
	if in.Action == "request_alignment" || in.Action == "resume_development" || in.Action == "reopen_claim" {
		return s.actDevelopmentAlignment(c, a, id, in)
	}
	return s.actTask(c, a, id, in, nil)
}

func (s *Service) actTask(c context.Context, a domain.Actor, id string, in Action, authorization *domain.TaskActionAuthorization) (domain.Mutation, error) {
	t, e := s.Store.GetTask(c, id)
	if e != nil {
		return domain.Mutation{}, e
	}
	if e = s.CheckProject(c, a, t.ProjectID, false); e != nil {
		return domain.Mutation{}, e
	}
	if in.RequestID == "" {
		return domain.Mutation{}, errors.New("缺少 request_id")
	}
	key := receiptKey(a, t.ProjectID, in.RequestID)
	fp := fingerprint(struct {
		ID            string
		Action        Action
		Authorization *domain.TaskActionAuthorization `json:",omitempty"`
	}{id, in, authorization})
	if m, ok, e := s.prior(c, key, fp); ok || e != nil {
		// Keep pre-upgrade receipt keys retryable while preventing another OAuth
		// client for the same employee from retrieving a bound claim credential.
		if ok && m.ClaimID != "" && ((m.Task.ClaimClientID != "" && m.Task.ClaimClientID != taskClientID(a)) || (t.ClaimClientID != "" && t.ClaimClientID != taskClientID(a))) {
			return domain.Mutation{}, domain.ErrForbidden
		}
		return m, e
	}
	if in.Version != 0 && in.Version != t.Version {
		return domain.Mutation{}, domain.ErrConflict
	}
	if in.ProjectVersionID != "" {
		expected := in.ProjectVersionID
		if expected == "unplanned" {
			expected = ""
		}
		if t.ProjectVersionID != expected {
			return domain.Mutation{}, fmt.Errorf("%w: 目标发布版本已调整，请重新读取需求", domain.ErrConflict)
		}
	}
	human := a.Kind == "human"
	if domain.TaskTerminated(t.Status) && in.Action != "reopen" {
		return domain.Mutation{}, fmt.Errorf("%w: 需求已拒绝或关闭，不能继续处理；请读取最新状态", domain.ErrConflict)
	}
	claimed := a.Kind == "developer" && ownsTaskClient(t, a) && nonempty(in.ClaimID) && Hash(in.ClaimID) == t.ClaimHash
	claim := ""
	kind := in.Action
	body := strings.TrimSpace(in.Body)
	reason := body
	switch in.Action {
	case "ready":
		if t.ParentRequirementID != "" {
			return domain.Mutation{}, errors.New("关联 Bug 必须经 Agent 评审确认存在后进入待认领")
		}
		if !human {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if t.Status != "clarify" {
			return domain.Mutation{}, errors.New("只有待澄清需求可以进入待认领")
		}
		if !nonempty(t.Description) || len(t.Acceptance) == 0 {
			return domain.Mutation{}, errors.New("请先补充需求描述和至少一条验收条件")
		}
		t.Status = "ready"
		body = "确认范围，进入待认领"
	case "claim":
		// Development happens in an MCP client; the web confirms requirements and
		// can hand work back, but never claims it.
		if a.Kind != "developer" {
			return domain.Mutation{}, fmt.Errorf("%w: 需求只能由开发代理通过 MCP 领取", domain.ErrForbidden)
		}
		if alignmentReserved(t) {
			return domain.Mutation{}, fmt.Errorf("%w: 仅原 MCP 客户端可阅读对齐回复后调用 task_resume_development；无法继续时由产品重新开放领取", domain.ErrConflict)
		}
		if t.Status == "doing" && !ownsTaskClient(t, a) {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if t.Status != "ready" && !(t.Status == "doing" && t.Claimant == a.ID) {
			return domain.Mutation{}, fmt.Errorf("%w: 需求已被领取或尚未就绪", domain.ErrConflict)
		}
		claim = domain.ID("claim_")
		t.ClaimHash = Hash(claim)
		t.Claimant = a.ID
		t.ClaimClientID = taskClientID(a)
		t.Owner = a.Name
		t.Branch = in.Branch
		t.Status = "doing"
		body = "领取任务"
	case "report":
		if !human && !claimed {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if !nonempty(body) {
			return domain.Mutation{}, errors.New("请填写工作记录")
		}
		switch in.ReportKind {
		case "", "progress", "blocked", "unblocked", "decision":
		default:
			return domain.Mutation{}, errors.New("不支持的工作记录类型")
		}
		if in.ReportKind == "blocked" {
			t.Blocker = body
		}
		if in.ReportKind == "unblocked" {
			t.Blocker = ""
		}
		if in.ReportKind == "decision" && !human {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if in.ReportKind != "" {
			kind = in.ReportKind
		}
	case "release":
		if alignmentReserved(t) {
			return domain.Mutation{}, domain.ErrConflict
		}
		if !human && !claimed {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if t.Status != "doing" {
			return domain.Mutation{}, errors.New("只有处理中需求可以交接")
		}
		if body == "" {
			return domain.Mutation{}, errors.New("请填写已有成果和交接说明")
		}
		t.ClaimHash = ""
		t.Claimant = ""
		t.ClaimClientID = ""
		t.Alignment = nil
		t.Owner = ""
		t.Status = "ready"
	case "submit":
		if !human && !claimed {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if t.Status != "doing" {
			return domain.Mutation{}, errors.New("只有处理中需求可以提交开发结果")
		}
		if in.Revision != t.Revision {
			return domain.Mutation{}, fmt.Errorf("%w: 需求修订已更新，请重新读取任务", domain.ErrConflict)
		}
		if in.Submission == nil || !nonempty(in.Submission.Summary) || !nonempty(in.Submission.SHA) || !nonempty(in.Submission.Tests) {
			return domain.Mutation{}, errors.New("请填写实现说明、代码版本和验证情况")
		}
		v := *in.Submission
		if nonempty(v.Branch) {
			t.Branch = strings.TrimSpace(v.Branch)
		}
		v.Revision = t.Revision
		v.At = domain.Now()
		t.Submission = &v
		t.Deployment = nil
		t.Review = nil
		t.TestVerification = nil
		t.Status = "testing"
		if t.ParentRequirementID != "" {
			t.Status = "verifying"
		}
		t.Blocker = ""
		body = "开发已完成，等待测试：" + v.Summary
	case "test_pass", "verify_bug", "return_bug", "start_retest":
		if !human {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if err := s.applyTestingAction(c, &t, in, a); err != nil {
			return domain.Mutation{}, err
		}
		body = in.Body
		if in.TestVerification != nil {
			body = in.TestVerification.Note
		}
	case "mark_deployed":
		if !human && !claimed {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if t.Status != "pending_release" || t.Submission == nil {
			return domain.Mutation{}, errors.New("只有待上线需求可以确认上线")
		}
		if in.Deployment == nil || in.Deployment.Revision != t.Revision || t.Submission.Revision != t.Revision || in.Deployment.SHA != t.Submission.SHA {
			return domain.Mutation{}, fmt.Errorf("%w: 上线版本与当前开发提交不一致，请重新读取需求", domain.ErrConflict)
		}
		d := *in.Deployment
		d.Environment, d.Body = strings.TrimSpace(d.Environment), strings.TrimSpace(d.Body)
		if d.Environment == "" || d.Body == "" {
			return domain.Mutation{}, errors.New("请填写实际上线环境和上线说明（含结果或证据链接）")
		}
		d.Actor, d.Source, d.At = a.Name, a.Kind, domain.Now()
		d.ProjectVersionID, d.ReleaseID = t.ProjectVersionID, ""
		if t.ProjectVersionID != "" {
			d.ReleaseID = domain.ID("pub_")
		}
		t.Deployment = &d
		in.Deployment = &d
		t.Status = "review"
		t.Blocker = ""
		body = "已上线，进入待验收（" + d.Environment + " · " + d.SHA + "）：" + d.Body
	case "withdraw_submission":
		if !human && !claimed {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if !in.UserConfirmed || !nonempty(in.UserInstruction) || body == "" {
			return domain.Mutation{}, errors.New("撤回提前提测需要用户明确确认、原话和原因")
		}
		if in.Version <= 0 || in.Revision != t.Revision || t.Submission == nil ||
			t.Submission.Revision != t.Revision || in.SubmissionSHA != t.Submission.SHA {
			return domain.Mutation{}, fmt.Errorf("%w: 请核对最新 version、revision 和提交版本", domain.ErrConflict)
		}
		if t.Status != "testing" || t.ParentRequirementID != "" || t.TestVerification != nil || t.Deployment != nil || t.Review != nil {
			return domain.Mutation{}, errors.New("仅尚无测试结果、关联测试 Bug 或发布记录的提前提测可以撤回")
		}
		// Storage also checks all linked Bugs in the same write transaction.
		// Preserve the original claim, source submission, revision and history.
		t.Status = "doing"
		body = "撤回提前提测，等待评审和测试发布：" + body
	case "rework":
		if !human && !claimed {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if t.Status != "pending_release" || body == "" {
			return domain.Mutation{}, errors.New("仅待上线需求可退回开发，请填写原因")
		}
		t.Status = "doing"
		t.TestVerification = nil
		body = "退回开发：" + body
	case "accept", "reject":
		if !human {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if t.Status != "review" || t.Submission == nil {
			return domain.Mutation{}, errors.New("需求尚未提交验收")
		}
		if t.Deployment == nil || t.Deployment.Revision != t.Revision || t.Deployment.SHA != t.Submission.SHA {
			return domain.Mutation{}, errors.New("请先确认当前版本已上线，再进行业务验收")
		}
		if in.Review == nil || in.Review.Revision != t.Revision || in.Review.SHA != t.Submission.SHA {
			return domain.Mutation{}, fmt.Errorf("%w: 验收版本与当前提交不一致", domain.ErrConflict)
		}
		r := *in.Review
		r.Accepted = in.Action == "accept"
		r.Actor = a.Name
		r.At = domain.Now()
		if r.Environment != t.Deployment.Environment {
			return domain.Mutation{}, errors.New("验收环境必须与当前上线环境一致")
		}
		if r.Accepted {
			if len(t.Acceptance) == 0 || len(r.Checks) != len(t.Acceptance) {
				return domain.Mutation{}, errors.New("请逐项检查验收条件")
			}
			for _, v := range r.Checks {
				if v != "passed" {
					return domain.Mutation{}, errors.New("所有条件通过后才能完成验收")
				}
			}
			t.Status = "done"
			t.ClaimHash = ""
		} else {
			if !nonempty(r.Note) {
				return domain.Mutation{}, errors.New("请填写退回原因")
			}
			t.Status = "doing"
		}
		t.Review = &r
		body = r.Note
	case "close", "decline", "cancel":
		if !human && authorization == nil {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if !domain.TaskActive(t.Status) {
			return domain.Mutation{}, fmt.Errorf("%w: 只有尚未结束的需求可以拒绝或关闭", domain.ErrConflict)
		}
		if body == "" {
			return domain.Mutation{}, errors.New("请填写拒绝或关闭的原因")
		}
		t.Resolution = &domain.TaskResolution{FromStatus: t.Status, Reason: body, Actor: a.Name, At: domain.Now()}
		t.Status = map[string]string{"close": "closed", "decline": "rejected", "cancel": "canceled"}[in.Action]
		body = map[string]string{"close": "关闭需求：", "decline": "拒绝需求：", "cancel": "取消需求："}[in.Action] + body
		t.Blocker = ""
		t.ClaimHash = ""
		t.Claimant = ""
		t.ClaimClientID = ""
		t.Alignment = nil
	case "reopen":
		t.TestVerification = nil
		if !human && authorization == nil {
			return domain.Mutation{}, domain.ErrForbidden
		}
		if !domain.TaskTerminated(t.Status) {
			return domain.Mutation{}, fmt.Errorf("%w: 只有已拒绝或关闭的需求可以重新打开", domain.ErrConflict)
		}
		if body == "" {
			return domain.Mutation{}, errors.New("请填写重新打开的原因")
		}
		t.Status = "clarify"
		t.Revision++
		t.Owner = ""
		t.Claimant = ""
		t.ClaimClientID = ""
		t.Alignment = nil
		t.ClaimHash = ""
		t.Blocker = ""
		t.Resolution = nil
		body = "重新打开需求，进入需求评审：" + body
	default:
		return domain.Mutation{}, errors.New("不支持的操作")
	}
	var data any = in
	if authorization != nil {
		public := in
		public.ClaimID = ""
		data = struct {
			Action
			Resolution    *domain.TaskResolution          `json:"resolution,omitempty"`
			Authorization *domain.TaskActionAuthorization `json:"authorization"`
		}{public, t.Resolution, authorization}
		body += "\n用户授权原话（MCP 客户端提供）：" + authorization.UserInstruction
	} else if t.Resolution != nil && (in.Action == "close" || in.Action == "decline" || in.Action == "cancel") {
		public := in
		public.ClaimID = ""
		data = struct {
			Action
			Resolution domain.TaskResolution `json:"resolution"`
		}{public, *t.Resolution}
	}
	var note *domain.RequirementReviewNote
	if verb, ok := resolutionVerbs[in.Action]; ok {
		actor := reviewActor(a, authorization)
		message := attributed(domain.RequirementReviewMessage{
			ID: domain.ID("rm_"), Role: "system", Action: verb[1], Authorization: authorization, At: domain.Now(),
			Body:  fmt.Sprintf("%s %s了需求：%s", actor.Name, verb[0], reason),
			Event: &domain.RequirementReviewEvent{Reason: reason},
		}, actor)
		note = &domain.RequirementReviewNote{Message: message, ExpireForms: "需求已结束，此表单仅供查看。"}
		if in.Action == "reopen" {
			// The new revision's snapshot travels with the reopening itself.
			message.Source = &t
			message.Event.FromRevision, message.Event.ToRevision = t.Revision-1, t.Revision
			note = &domain.RequirementReviewNote{Message: message}
		}
	}
	return s.commit(c, a, t, t.Version, kind, body, data, key, fp, claim, note)
}

// Lifecycle decisions that leave a trace in the requirement review conversation.
var resolutionVerbs = map[string][2]string{"close": {"关闭", "task_closed"}, "decline": {"拒绝", "task_declined"}, "cancel": {"取消", "task_canceled"}, "reopen": {"重新打开", "task_reopened"}}

func (s *Service) Context(c context.Context, a domain.Actor, p, id string) (map[string]any, error) {
	if e := s.CheckProject(c, a, p, false); e != nil {
		return nil, e
	}
	project, e := s.Store.GetProject(c, p)
	if e != nil {
		return nil, e
	}
	tasks, e := s.Store.ListTasks(c, p)
	if e != nil {
		return nil, e
	}
	artifacts, e := s.Store.ListArtifacts(c, p)
	if e != nil {
		return nil, e
	}
	events, e := s.Store.Events(c, p, id, -1)
	if e != nil {
		return nil, e
	}
	if a.Kind != "human" {
		project.CredentialID = ""
	}
	out := map[string]any{"project": project, "tasks": tasks, "artifacts": artifacts, "events": events, "as_of": domain.Now()}
	versions, err := s.ProjectVersions(c, a, p)
	if err != nil {
		return nil, err
	}
	out["project_versions"] = versions
	current, e := s.CurrentMilestone(c, a, p)
	if e != nil {
		return nil, e
	}
	if current == nil {
		out["current_milestone"] = nil
	} else {
		out["current_milestone"] = &current.Summary
	}
	if a.Kind == "human" {
		reviews, err := s.Store.ListRequirementReviewSummaries(c, p)
		if err != nil {
			return nil, err
		}
		out["requirement_reviews"] = reviews
	}
	if id != "" {
		t, e := s.Store.GetTask(c, id)
		if e != nil {
			return nil, e
		}
		if t.ProjectID != p {
			return nil, domain.ErrForbidden
		}
		out["task"] = t
		if review, err := s.Store.GetRequirementReview(c, id); err == nil {
			out["requirement_review"] = review
		} else if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		history, err := s.RequirementIterations(c, a, id, 0, 10)
		if err != nil {
			return nil, err
		}
		out["iteration_history"] = history
	}
	return out, nil
}
func (s *Service) CreateToken(c context.Context, p, name string) (domain.Token, string, error) {
	if _, e := s.Store.GetProject(c, p); e != nil {
		return domain.Token{}, "", e
	}
	if !nonempty(name) {
		return domain.Token{}, "", errors.New("请填写开发者或客户端名称")
	}
	raw := domain.ID("pm_")
	t := domain.Token{ID: domain.ID("tok_"), ProjectID: p, Name: name, Hash: Hash(raw), At: domain.Now()}
	return t, raw, s.Store.PutToken(c, t)
}
func (s *Service) Artifact(c context.Context, a domain.Actor, v domain.Artifact) (domain.Artifact, error) {
	if e := s.CheckProject(c, a, v.ProjectID, false); e != nil {
		return v, e
	}
	if _, e := s.Store.GetProject(c, v.ProjectID); e != nil {
		return v, e
	}
	if v.TaskID != "" {
		t, e := s.Store.GetTask(c, v.TaskID)
		if e != nil {
			return v, e
		}
		if t.ProjectID != v.ProjectID {
			return v, domain.ErrForbidden
		}
	}
	if v.Kind == "" || !nonempty(v.Body) {
		return v, errors.New("请填写记录类型和内容")
	}
	if a.Kind == "developer" && v.Kind != "test" && v.Kind != "deployment" {
		return v, domain.ErrForbidden
	}
	if a.Kind == "platform" && v.Kind != "analysis" && v.Kind != "question" && v.Kind != "knowledge" {
		return v, domain.ErrForbidden
	}
	if v.Kind == "deployment" && (v.SHA == "" || v.Environment == "") {
		return v, errors.New("部署证据需要版本和环境")
	}
	if v.ID == "" {
		v.ID = domain.ID("ev_")
	}
	v.At = domain.Now()
	v.Source = a.Kind + ":" + a.Name
	_, previousErr := s.Store.GetArtifact(c, v.ID)
	if previousErr != nil && !errors.Is(previousErr, domain.ErrNotFound) {
		return v, previousErr
	}
	if err := s.Store.PutArtifact(c, v); err != nil {
		return v, err
	}
	stored, err := s.Store.GetArtifact(c, v.ID)
	if err == nil && errors.Is(previousErr, domain.ErrNotFound) && a.Kind != "platform" && (v.Kind == "test" || v.Kind == "deployment") {
		s.notify(v.ProjectID, v.TaskID, "evidence")
	}
	return stored, err
}
