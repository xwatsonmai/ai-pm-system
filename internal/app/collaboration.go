package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const CollaborationLease = 10 * time.Minute

// Reconstruct the conversation owner, including personal MCP membership. Older
// local token identities remain valid only while that exact token still exists.
func (s *Service) CollaborationOwner(ctx context.Context, id, kind, project string) (domain.Actor, error) {
	a := domain.Actor{ID: id, Kind: kind, ProjectID: project, Name: "会话发起人（平台 Agent 代办）"}
	if id == "" || (kind != "human" && kind != "developer") {
		return a, domain.ErrForbidden
	}
	if kind == "developer" {
		tokens, err := s.Store.ListTokens(ctx, project)
		if err != nil {
			return a, err
		}
		legacy := false
		for _, token := range tokens {
			if token.ID == id {
				legacy = true
			}
		}
		if !legacy {
			account, err := s.Store.GetMCPAccount(ctx, id)
			if err != nil {
				return a, err
			}
			if !account.Enabled {
				return a, domain.ErrForbidden
			}
			a.UserID = id
		}
	}
	return a, s.CheckProject(ctx, a, project, false)
}

// SubscribeWork is only a change hint. Clients reconcile durable state after
// subscribing/reconnecting; coalescing notifications cannot lose business work.
func (s *Service) SubscribeWork(project string) (<-chan struct{}, func()) {
	s.workMu.Lock()
	defer s.workMu.Unlock()
	if s.workListeners == nil {
		s.workListeners = map[string]map[chan struct{}]struct{}{}
	}
	if s.workListeners[project] == nil {
		s.workListeners[project] = map[chan struct{}]struct{}{}
	}
	ch := make(chan struct{}, 1)
	s.workListeners[project][ch] = struct{}{}
	return ch, func() {
		s.workMu.Lock()
		defer s.workMu.Unlock()
		delete(s.workListeners[project], ch)
		if len(s.workListeners[project]) == 0 {
			delete(s.workListeners, project)
		}
	}
}
func (s *Service) NotifyWork(project string) {
	s.workMu.Lock()
	defer s.workMu.Unlock()
	for ch := range s.workListeners[project] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

type CollaborationInput struct {
	RequestID   string `json:"request_id"`
	OriginRunID string `json:"origin_run_id"`
	Title       string `json:"title"`
	Brief       string `json:"brief"`
	Environment string `json:"environment"`
}
type CollaborationAction struct {
	Action     string                      `json:"action"`
	RequestID  string                      `json:"request_id"`
	ClaimID    string                      `json:"claim_id,omitempty"`
	Version    int                         `json:"version,omitempty"`
	Body       string                      `json:"body,omitempty"`
	ReportKind string                      `json:"report_kind,omitempty"`
	Result     *domain.InvestigationResult `json:"result,omitempty"`
}

func (s *Service) collaborationPrior(ctx context.Context, key, fp string) (domain.CollaborationMutation, bool, error) {
	r, err := s.Store.GetReceipt(ctx, key)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.CollaborationMutation{}, false, nil
	}
	if err != nil {
		return domain.CollaborationMutation{}, false, err
	}
	if r.Fingerprint != fp {
		return domain.CollaborationMutation{}, false, domain.ErrRequestReused
	}
	var v domain.CollaborationMutation
	err = json.Unmarshal(r.Response, &v)
	return v, true, err
}

func (s *Service) commitCollaboration(ctx context.Context, a domain.Actor, v domain.Collaboration, expected int, key, fp, claim string, job *domain.ReplyJob) (domain.CollaborationMutation, error) {
	v.Version, v.UpdatedAt = expected+1, domain.Now()
	result := domain.CollaborationMutation{Collaboration: v, ClaimID: claim}
	if a.Kind == "developer" {
		result.Collaboration = developerCollaboration(v)
	}
	body, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	err = s.Store.CommitCollaboration(ctx, v, expected, &domain.Receipt{Key: key, Fingerprint: fp, Response: body}, job)
	if errors.Is(err, domain.ErrConflict) {
		if prior, ok, e := s.collaborationPrior(ctx, key, fp); ok || e != nil {
			return prior, e
		}
	}
	if err == nil {
		s.NotifyWork(v.ProjectID)
	}
	return result, err
}

func (s *Service) CreateCollaboration(ctx context.Context, a domain.Actor, in CollaborationInput) (domain.CollaborationMutation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	origin, err := s.Store.GetRun(ctx, in.OriginRunID)
	if err != nil {
		return domain.CollaborationMutation{}, err
	}
	p, err := s.Store.GetProject(ctx, origin.ProjectID)
	if err != nil {
		return domain.CollaborationMutation{}, err
	}
	if origin.Kind != "question" || origin.TriggerType == "collaboration_result" || !CanRead(a, p) || (a.Kind != "platform" && !CanReadRun(a, p, origin)) {
		return domain.CollaborationMutation{}, domain.ErrForbidden
	}
	if _, err = s.CollaborationOwner(ctx, origin.OwnerID, origin.OwnerKind, p.ID); err != nil {
		return domain.CollaborationMutation{}, err
	}
	if !nonempty(in.RequestID) || !nonempty(in.Title) || !nonempty(in.Brief) || !nonempty(in.Environment) || origin.OwnerID == "" {
		return domain.CollaborationMutation{}, errors.New("需要请求 ID、排查标题、问题摘要和环境，且来源必须是有归属的对话")
	}
	if len(in.Title) > 600 || len(in.Brief) > 30000 || len(in.Environment) > 200 {
		return domain.CollaborationMutation{}, errors.New("排查内容过长")
	}
	key, fp := "collaboration:"+receiptKey(a, p.ID, in.RequestID), fingerprint(in)
	if old, ok, e := s.collaborationPrior(ctx, key, fp); ok || e != nil {
		return old, e
	}
	conversation := origin.ConversationID
	if conversation == "" {
		conversation = origin.ID
	}
	all, err := s.Store.ListCollaborations(ctx, p.ID)
	if err != nil {
		return domain.CollaborationMutation{}, err
	}
	for _, v := range all {
		if v.ConversationID == conversation && v.Title == strings.TrimSpace(in.Title) && v.Brief == in.Brief && v.Environment == strings.TrimSpace(in.Environment) && v.Status != "completed" && v.Status != "canceled" {
			return s.commitCollaboration(ctx, a, v, v.Version, key, fp, "", nil)
		}
	}
	v := domain.Collaboration{ID: "collab_" + Hash(key)[:24], ProjectID: p.ID, TaskID: origin.TaskID, ConversationID: conversation, OriginRunID: origin.ID, OwnerID: origin.OwnerID, OwnerKind: origin.OwnerKind, Title: strings.TrimSpace(in.Title), Brief: in.Brief, Environment: strings.TrimSpace(in.Environment), Scope: "只读调查；不授权重启、部署、修改配置或业务数据。凭据留在本地，回传证据须脱敏。", Status: "pending", Updates: []domain.CollaborationUpdate{}, CreatedAt: domain.Now()}
	return s.commitCollaboration(ctx, a, v, 0, key, fp, "", nil)
}

func developerCollaboration(v domain.Collaboration) domain.Collaboration {
	v.ConversationID, v.OriginRunID, v.OwnerID, v.OwnerKind, v.ClaimHash = "", "", "", "", ""
	return v
}

func (s *Service) GetCollaboration(ctx context.Context, a domain.Actor, id string) (domain.CollaborationView, error) {
	v, err := s.Store.GetCollaboration(ctx, id)
	if err != nil {
		return domain.CollaborationView{}, err
	}
	if err = s.CheckProject(ctx, a, v.ProjectID, false); err != nil {
		return domain.CollaborationView{}, err
	}
	if a.Kind != "developer" && a.Kind != "platform" && (a.ID != v.OwnerID || a.Kind != v.OwnerKind) {
		return domain.CollaborationView{}, domain.ErrForbidden
	}
	out := domain.CollaborationView{Collaboration: v}
	if a.Kind == "developer" {
		out.Collaboration = developerCollaboration(v)
		return out, nil
	}
	if _, err = s.Store.GetRun(ctx, v.OriginRunID); err != nil {
		return domain.CollaborationView{}, err
	}
	if v.Result != nil {
		job, e := s.Store.GetReplyJob(ctx, v.Result.ID)
		if e != nil {
			return out, e
		}
		out.Reply = &job
	}
	return out, nil
}

func (s *Service) ListCollaborations(ctx context.Context, a domain.Actor, project string) ([]domain.CollaborationView, error) {
	if err := s.CheckProject(ctx, a, project, false); err != nil {
		return nil, err
	}
	all, err := s.Store.ListCollaborations(ctx, project)
	if err != nil {
		return nil, err
	}
	out := []domain.CollaborationView{}
	for _, v := range all {
		view, e := s.GetCollaboration(ctx, a, v.ID)
		if errors.Is(e, domain.ErrForbidden) || errors.Is(e, domain.ErrNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		out = append(out, view)
	}
	return out, nil
}

func (s *Service) ActCollaboration(ctx context.Context, a domain.Actor, id string, in CollaborationAction) (domain.CollaborationMutation, error) {
	v, err := s.Store.GetCollaboration(ctx, id)
	if err != nil {
		return domain.CollaborationMutation{}, err
	}
	if err = s.CheckProject(ctx, a, v.ProjectID, false); err != nil {
		return domain.CollaborationMutation{}, err
	}
	owner := a.ID == v.OwnerID && a.Kind == v.OwnerKind
	if a.Kind != "developer" && !owner {
		return domain.CollaborationMutation{}, domain.ErrForbidden
	}
	if !nonempty(in.RequestID) {
		return domain.CollaborationMutation{}, errors.New("缺少 request_id")
	}
	key, fp := "collaboration:"+receiptKey(a, v.ProjectID, in.RequestID), fingerprint(struct {
		ID    string
		Input CollaborationAction
	}{id, in})
	if prior, ok, e := s.collaborationPrior(ctx, key, fp); ok || e != nil {
		return prior, e
	}
	if in.Version != 0 && in.Version != v.Version {
		return domain.CollaborationMutation{}, domain.ErrConflict
	}
	if len(in.Body) > 30000 {
		return domain.CollaborationMutation{}, errors.New("进展内容过长")
	}
	if _, err = s.Store.GetRun(ctx, v.OriginRunID); err != nil {
		return domain.CollaborationMutation{}, errors.New("来源会话已删除，停止排查")
	}
	if v.Status == "canceled" {
		return domain.CollaborationMutation{}, domain.ErrConflict
	}
	active := v.Status == "investigating" || v.Status == "blocked"
	claimed := a.Kind == "developer" && active && a.ID == v.Claimant && nonempty(in.ClaimID) && Hash(in.ClaimID) == v.ClaimHash && v.LeaseUntil > domain.Now()
	claim := ""
	var job *domain.ReplyJob
	switch in.Action {
	case "claim":
		if a.Kind != "developer" {
			return domain.CollaborationMutation{}, domain.ErrForbidden
		}
		if v.Status != "pending" && !(active && v.LeaseUntil <= domain.Now()) {
			return domain.CollaborationMutation{}, domain.ErrConflict
		}
		claim = domain.ID("claim_")
		v.ClaimHash, v.Claimant, v.ClaimantName = Hash(claim), a.ID, a.Name
		v.Status, v.LeaseUntil = "investigating", domain.Now()+CollaborationLease.Milliseconds()
		in.Body = "认领排查任务"
	case "report", "release", "submit":
		if !claimed {
			return domain.CollaborationMutation{}, domain.ErrConflict
		}
		if in.Action == "report" {
			if !nonempty(in.Body) {
				return domain.CollaborationMutation{}, errors.New("请填写进展或阻塞说明")
			}
			switch in.ReportKind {
			case "", "progress", "unblocked":
				v.Status = "investigating"
			case "blocked":
				v.Status = "blocked"
			default:
				return domain.CollaborationMutation{}, errors.New("无效进展类型")
			}
			v.LeaseUntil = domain.Now() + CollaborationLease.Milliseconds()
		}
		if in.Action == "release" {
			if !nonempty(in.Body) {
				return domain.CollaborationMutation{}, errors.New("请填写交接说明")
			}
			v.Status, v.Claimant, v.ClaimantName, v.ClaimHash, v.LeaseUntil = "pending", "", "", "", 0
		}
		if in.Action == "submit" {
			if in.Version == 0 {
				return domain.CollaborationMutation{}, errors.New("提交前读取最新 version")
			}
			if in.Result == nil {
				return domain.CollaborationMutation{}, errors.New("缺少调查结果")
			}
			r := *in.Result
			if r.Outcome != "confirmed" && r.Outcome != "inconclusive" && r.Outcome != "cannot_access" {
				return domain.CollaborationMutation{}, errors.New("结论类型必须为 confirmed/inconclusive/cannot_access")
			}
			if !nonempty(r.Summary) || !nonempty(r.Environment) || !nonempty(r.ObservedAt) || !nonempty(r.DeploymentVersion) || !nonempty(r.Limitations) || (r.Outcome == "confirmed" && !nonempty(r.Evidence)) {
				return domain.CollaborationMutation{}, errors.New("请填写结论、环境、取证时间、未确认项；已确认结论必须附证据")
			}
			b, _ := json.Marshal(r)
			if len(b) > 60000 {
				return domain.CollaborationMutation{}, errors.New("调查结果过长")
			}
			r.ID, r.Actor, r.At = "result_"+v.ID, a.Name, domain.Now()
			v.Result, v.Status, v.ClaimHash, v.LeaseUntil = &r, "completed", "", 0
			job = &domain.ReplyJob{ID: r.ID, CollaborationID: v.ID, ProjectID: v.ProjectID, RunID: "run_" + r.ID, Status: "pending", Version: 1, NextAt: domain.Now()}
			in.Body = "已返回调查结果"
		}
	case "cancel":
		if !owner {
			return domain.CollaborationMutation{}, domain.ErrForbidden
		}
		v.Status, v.ClaimHash, v.LeaseUntil = "canceled", "", 0
		in.Body = "发起人取消协同"
	default:
		return domain.CollaborationMutation{}, errors.New("不支持的协同操作")
	}
	v.Updates = append(v.Updates, domain.CollaborationUpdate{Kind: in.Action, Body: in.Body, Actor: a.Name, At: domain.Now()})
	return s.commitCollaboration(ctx, a, v, v.Version, key, fp, claim, job)
}

func (s *Service) WorkList(ctx context.Context, a domain.Actor) (map[string]any, error) {
	if a.Kind != "developer" {
		return nil, domain.ErrForbidden
	}
	if err := s.CheckProject(ctx, a, a.ProjectID, false); err != nil {
		return nil, err
	}
	tasks, err := s.Store.ListTasks(ctx, a.ProjectID)
	if err != nil {
		return nil, err
	}
	ready := []domain.Task{}
	for _, t := range tasks {
		if (t.Status == "ready" && (!alignmentReserved(t) || ownsTaskClient(t, a))) || ((t.Status == "doing" || t.Status == "aligning" || t.Status == "testing" || t.Status == "fixing" || t.Status == "retest" || t.Status == "verifying" || t.Status == "pending_release" || t.Status == "review") && ownsTaskClient(t, a)) {
			ready = append(ready, t)
		}
	}
	all, err := s.Store.ListCollaborations(ctx, a.ProjectID)
	if err != nil {
		return nil, err
	}
	items := []domain.Collaboration{}
	for _, v := range all {
		if v.Status != "pending" && v.Status != "investigating" && v.Status != "blocked" {
			continue
		}
		if _, e := s.Store.GetRun(ctx, v.OriginRunID); errors.Is(e, domain.ErrNotFound) {
			continue
		} else if e != nil {
			return nil, e
		}
		if v.Status == "pending" || v.LeaseUntil <= domain.Now() || v.Claimant == a.ID {
			items = append(items, developerCollaboration(v))
		}
	}
	return map[string]any{"tasks": ready, "collaborations": items, "as_of": domain.Now(), "lease_seconds": int(CollaborationLease.Seconds())}, nil
}
