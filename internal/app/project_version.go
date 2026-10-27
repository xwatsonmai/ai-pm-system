package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type ProjectVersionInput struct {
	Name       string `json:"name"`
	Goal       string `json:"goal"`
	Owner      string `json:"owner"`
	TargetDate string `json:"target_date"`
	Version    int    `json:"version"`
	RequestID  string `json:"request_id"`
}
type VersionTaskRef struct {
	ID       string `json:"id"`
	Version  int    `json:"version"`
	Revision int    `json:"revision,omitempty"`
	SHA      string `json:"sha,omitempty"`
}
type VersionAssignmentInput struct {
	ProjectVersionID string           `json:"project_version_id"`
	Version          int              `json:"version"`
	Tasks            []VersionTaskRef `json:"tasks"`
	Body             string           `json:"body"`
	RequestID        string           `json:"request_id"`
	UserConfirmed    bool             `json:"user_confirmed,omitempty"`
	UserInstruction  string           `json:"user_instruction,omitempty"`
}
type ProjectVersionAction struct {
	Action               string           `json:"action"`
	Version              int              `json:"version"`
	Body                 string           `json:"body"`
	DestinationVersionID string           `json:"destination_version_id,omitempty"`
	DestinationVersion   int              `json:"destination_version,omitempty"`
	Environment          string           `json:"environment,omitempty"`
	Items                []VersionTaskRef `json:"items,omitempty"`
	RequestID            string           `json:"request_id"`
}
type ProjectVersionMutation struct {
	ProjectVersion *domain.ProjectVersion `json:"project_version,omitempty"`
	Tasks          []domain.Task          `json:"tasks"`
}
type ProjectVersionCounts struct {
	Total           int            `json:"total"`
	Included        int            `json:"included"`
	Removed         int            `json:"removed"`
	Ended           int            `json:"ended"`
	DevelopmentDone int            `json:"development_done"`
	Released        int            `json:"released"`
	Accepted        int            `json:"accepted"`
	Blocked         int            `json:"blocked"`
	ByStatus        map[string]int `json:"by_status"`
}
type ProjectVersionSummary struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Goal        string               `json:"goal"`
	Owner       string               `json:"owner"`
	TargetDate  string               `json:"target_date"`
	Status      string               `json:"status"`
	Phase       string               `json:"phase"`
	Version     int                  `json:"version"`
	ScopeFrozen bool                 `json:"scope_frozen"`
	Current     bool                 `json:"current"`
	StartedAt   int64                `json:"started_at,omitempty"`
	EndedAt     int64                `json:"ended_at,omitempty"`
	Counts      ProjectVersionCounts `json:"counts"`
}
type ProjectVersionDetail struct {
	ProjectVersion domain.ProjectVersion `json:"project_version"`
	Summary        ProjectVersionSummary `json:"summary"`
}

func projectVersionSummary(v domain.ProjectVersion) ProjectVersionSummary {
	c := ProjectVersionCounts{ByStatus: map[string]int{}}
	for _, i := range v.Scope {
		c.Total++
		if i.Removed {
			c.Removed++
			continue
		}
		t := i.Task
		c.ByStatus[t.Status]++
		if domain.TaskTerminated(t.Status) || t.Status == "resolved" {
			c.Ended++
			continue
		}
		c.Included++
		if t.Blocker != "" {
			c.Blocked++
		}
		if t.Status == "testing" || t.Status == "fixing" || t.Status == "retest" || t.Status == "verifying" || t.Status == "resolved" || t.Status == "pending_release" || t.Status == "review" || t.Status == "done" {
			c.DevelopmentDone++
		}
		if t.Deployment != nil && t.Deployment.Revision == t.Revision && (t.Status == "review" || t.Status == "done") {
			c.Released++
		}
		if t.Status == "done" {
			c.Accepted++
		}
	}
	phase := "planning"
	if v.Status == "active" || c.ByStatus["testing"]+c.ByStatus["fixing"]+c.ByStatus["retest"]+c.ByStatus["verifying"]+c.ByStatus["doing"]+c.ByStatus["aligning"]+c.ByStatus["pending_release"]+c.ByStatus["review"]+c.ByStatus["done"] > 0 {
		phase = "in_progress"
	}
	if c.Included > 0 && c.DevelopmentDone == c.Included {
		switch {
		case c.ByStatus["testing"]+c.ByStatus["fixing"]+c.ByStatus["retest"]+c.ByStatus["verifying"] > 0:
			phase = "testing"
		case c.ByStatus["pending_release"] > 0:
			phase = "pending_release"
		case c.ByStatus["review"] > 0:
			phase = "acceptance"
		case c.Accepted == c.Included:
			phase = "awaiting_completion"
		}
	}
	if v.Status == "planned" {
		phase = "planning"
	}
	if v.Status == "completed" {
		phase = "completed"
	}
	if v.Status == "closed" {
		phase = "closed"
	}
	return ProjectVersionSummary{ID: v.ID, Name: v.Name, Goal: v.Goal, Owner: v.Owner, TargetDate: v.TargetDate, Status: v.Status, Phase: phase, Version: v.Version, ScopeFrozen: false, Current: v.Status == "active", StartedAt: v.StartedAt, EndedAt: v.EndedAt, Counts: c}
}
func (s *Service) ProjectVersions(c context.Context, a domain.Actor, p string) ([]ProjectVersionSummary, error) {
	if e := s.CheckProject(c, a, p, false); e != nil {
		return nil, e
	}
	vs, e := s.Store.ListProjectVersions(c, p)
	if e != nil {
		return nil, e
	}
	out := []ProjectVersionSummary{}
	for _, v := range vs {
		out = append(out, projectVersionSummary(v))
	}
	return out, nil
}
func (s *Service) ProjectVersionContext(c context.Context, a domain.Actor, id string) (ProjectVersionDetail, error) {
	v, e := s.Store.GetProjectVersion(c, id)
	if e != nil {
		return ProjectVersionDetail{}, e
	}
	if e = s.CheckProject(c, a, v.ProjectID, false); e != nil {
		return ProjectVersionDetail{}, e
	}
	return ProjectVersionDetail{v, projectVersionSummary(v)}, nil
}
func (s *Service) versionPrior(c context.Context, key, fp string) (ProjectVersionMutation, bool, error) {
	r, e := s.Store.GetReceipt(c, key)
	if errors.Is(e, domain.ErrNotFound) {
		return ProjectVersionMutation{}, false, nil
	}
	if e != nil {
		return ProjectVersionMutation{}, false, e
	}
	if r.Fingerprint != fp {
		return ProjectVersionMutation{}, false, domain.ErrRequestReused
	}
	var m ProjectVersionMutation
	e = json.Unmarshal(r.Response, &m)
	return m, true, e
}
func validateProjectVersion(in *ProjectVersionInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Goal = strings.TrimSpace(in.Goal)
	in.Owner = strings.TrimSpace(in.Owner)
	if in.Name == "" || len(in.Name) > 180 || len(in.Goal) > 6000 || len(in.Owner) > 200 {
		return errors.New("请填写版本名称（最多180字节），目标最多6000字节，负责人最多200字节")
	}
	if in.TargetDate != "" {
		if _, e := time.Parse("2006-01-02", in.TargetDate); e != nil {
			return errors.New("计划完成日期格式应为 YYYY-MM-DD")
		}
	}
	return nil
}

type versionPlan struct {
	s             *Service
	c             context.Context
	a             domain.Actor
	p             string
	versions      map[string]*domain.ProjectVersion
	expected      map[string]int
	tasks         []domain.VersionTaskWrite
	events        []domain.Event
	now           int64
	state         domain.CurrentMilestone
	stateExpected int
}

func (s *Service) newVersionPlan(c context.Context, a domain.Actor, p string) (*versionPlan, error) {
	if e := s.CheckProject(c, a, p, false); e != nil {
		return nil, e
	}
	vs, e := s.Store.ListProjectVersions(c, p)
	if e != nil {
		return nil, e
	}
	state, e := s.Store.GetCurrentMilestone(c, p)
	if e != nil {
		return nil, e
	}
	plan := &versionPlan{state: state, stateExpected: state.Version, s: s, c: c, a: a, p: p, versions: map[string]*domain.ProjectVersion{}, expected: map[string]int{}, tasks: []domain.VersionTaskWrite{}, events: []domain.Event{}, now: domain.Now()}
	for _, v := range vs {
		copy := v
		plan.versions[v.ID] = &copy
	}
	return plan, nil
}
func (p *versionPlan) touch(id string) *domain.ProjectVersion {
	v := p.versions[id]
	if v != nil {
		if _, ok := p.expected[id]; !ok {
			p.expected[id] = v.Version
		}
	}
	return v
}
func (p *versionPlan) history(v *domain.ProjectVersion, action, body string, ids []string, instruction string) {
	userID := p.a.UserID
	if userID == "" {
		userID = p.a.ID
	}
	v.History = append(v.History, domain.VersionHistoryEntry{Action: action, Body: body, Actor: p.a.Name, UserID: userID, UserInstruction: instruction, TaskIDs: ids, At: p.now})
}
func (p *versionPlan) event(t domain.Task, kind, body string, data any) {
	b, _ := json.Marshal(data)
	p.events = append(p.events, domain.Event{ProjectID: p.p, TaskID: t.ID, Kind: kind, Body: body, Actor: p.a.Name, Data: b, At: p.now})
}
func (p *versionPlan) batch() domain.MilestoneBatch {
	writes := []domain.VersionWrite{}
	keys := []string{}
	for id := range p.expected {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		v := p.versions[id]
		v.Version = p.expected[id] + 1
		v.UpdatedAt = p.now
		writes = append(writes, domain.VersionWrite{Value: *v, Expected: p.expected[id]})
	}
	state := p.state
	state.Version = p.stateExpected + 1
	return domain.MilestoneBatch{State: &state, Expected: p.stateExpected, Versions: writes, Events: p.events}
}
func (p *versionPlan) finish(key, fp, target string) (ProjectVersionMutation, error) {
	batch := p.batch()
	out := ProjectVersionMutation{ProjectVersion: p.versions[target], Tasks: []domain.Task{}}
	for _, t := range p.tasks {
		out.Tasks = append(out.Tasks, t.Value)
	}
	b, e := json.Marshal(out)
	if e != nil {
		return out, e
	}
	r := &domain.Receipt{Key: key, Fingerprint: fp, Response: b}
	if e = p.s.Store.CommitVersionPlan(p.c, nil, p.tasks, nil, r, batch); e != nil {
		if errors.Is(e, domain.ErrConflict) {
			if old, ok, x := p.s.versionPrior(p.c, key, fp); ok || x != nil {
				return old, x
			}
		}
		return out, e
	}
	p.s.NotifyWork(p.p)
	return out, nil
}
func (s *Service) SaveProjectVersion(c context.Context, a domain.Actor, p, id string, in ProjectVersionInput) (ProjectVersionMutation, error) {
	return s.saveProjectVersion(c, a, p, id, in, nil)
}
func (s *Service) saveProjectVersion(c context.Context, a domain.Actor, p, id string, in ProjectVersionInput, authorization *domain.TaskActionAuthorization) (ProjectVersionMutation, error) {
	if a.Kind != "human" && authorization == nil {
		return ProjectVersionMutation{}, domain.ErrForbidden
	}
	plan, e := s.newVersionPlan(c, a, p)
	if e != nil {
		return ProjectVersionMutation{}, e
	}
	if !nonempty(in.RequestID) {
		return ProjectVersionMutation{}, errors.New("缺少 request_id")
	}
	key := receiptKey(a, p, in.RequestID)
	fp := authorizedFingerprint(struct {
		Operation, ID string
		Input         ProjectVersionInput
	}{"save_project_version", id, in}, authorization)
	if old, ok, e := s.versionPrior(c, key, fp); ok || e != nil {
		return old, e
	}
	if e = validateProjectVersion(&in); e != nil {
		return ProjectVersionMutation{}, e
	}
	creating := id == ""
	var v *domain.ProjectVersion
	if id == "" {
		if plan.state.VersionID != "" {
			return ProjectVersionMutation{}, fmt.Errorf("%w: 当前已有进行中的里程碑，请先结束它", domain.ErrConflict)
		}
		id = domain.ID("ver_")
		v = &domain.ProjectVersion{ID: id, ProjectID: p, Status: "active", StartedAt: plan.now, Scope: []domain.VersionScopeItem{}, Releases: []domain.VersionRelease{}, History: []domain.VersionHistoryEntry{}, CreatedAt: plan.now}
		plan.versions[id] = v
		plan.expected[id] = 0
		plan.state.VersionID = id
	} else {
		v = plan.touch(id)
		if v == nil {
			return ProjectVersionMutation{}, domain.ErrNotFound
		}
		if v.Version != in.Version || (v.Status == "closed" || v.Status == "completed") {
			return ProjectVersionMutation{}, fmt.Errorf("%w: 里程碑已变化或已结束，不能修改计划", domain.ErrConflict)
		}
	}
	for _, other := range plan.versions {
		if other.ID != id && strings.EqualFold(other.Name, in.Name) {
			return ProjectVersionMutation{}, fmt.Errorf("%w: 同一项目版本名称不能重复", domain.ErrConflict)
		}
	}
	v.Name, v.Goal, v.Owner, v.TargetDate = in.Name, in.Goal, in.Owner, in.TargetDate
	instruction := ""
	if authorization != nil {
		instruction = authorization.UserInstruction
	}
	action, body := "saved", "更新里程碑计划："+v.Name
	if creating {
		action, body = "created", "开始里程碑："+v.Name
	}
	plan.history(v, action, body, nil, instruction)
	plan.event(domain.Task{}, "project_version_"+action, body, map[string]string{"project_version_id": id})
	return plan.finish(key, fp, id)
}
func (p *versionPlan) move(refs []VersionTaskRef, target, reason, instruction string) error {
	if target == "" {
		return errors.New("需求必须属于里程碑，不能移回未排期")
	}
	var destination *domain.ProjectVersion
	if target != "" {
		destination = p.versions[target]
		if destination == nil {
			return domain.ErrNotFound
		}
		if destination.Status != "active" || target != p.state.VersionID {
			return fmt.Errorf("%w: 只能归入当前进行中的里程碑", domain.ErrConflict)
		}
	}
	if len(refs) == 0 || len(refs) > 100 || !nonempty(reason) {
		return errors.New("请选择1–100个需求并填写调整原因")
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref.ID == "" || ref.Version <= 0 || seen[ref.ID] {
			return errors.New("需求编号/最新版本缺失或重复")
		}
		seen[ref.ID] = true
		t, e := p.s.Store.GetTask(p.c, ref.ID)
		if e != nil {
			return e
		}
		if t.ProjectID != p.p {
			return domain.ErrForbidden
		}
		if t.Version != ref.Version {
			return domain.ErrConflict
		}
		if domain.TaskFinished(t.Status) {
			return errors.New("只能调整尚未结束的需求；需要继续时先重新打开")
		}
		if t.Status == "review" {
			return errors.New("已上线需求保留原版本进行验收；新工作请先明确退回或重新打开")
		}
		if t.ProjectVersionID == target {
			continue
		}
		previous := t.ProjectVersionID
		if previous != "" {
			v := p.touch(previous)
			if v == nil {
				return domain.ErrNotFound
			}
			for i := range v.Scope {
				if v.Scope[i].Task.ID == t.ID && !v.Scope[i].Removed {
					v.Scope[i].Removed = true
					v.Scope[i].Reason = reason
					v.Scope[i].Task = t
				}
			}
			p.history(v, "removed", reason, []string{t.ID}, instruction)
		}
		expected := t.Version
		t.ProjectVersionID = target
		t.Version++
		t.UpdatedAt = p.now
		if destination != nil {
			p.touch(target)
			found := false
			for i := range destination.Scope {
				if destination.Scope[i].Task.ID == t.ID {
					destination.Scope[i] = domain.VersionScopeItem{Task: t}
					found = true
					break
				}
			}
			if !found {
				destination.Scope = append(destination.Scope, domain.VersionScopeItem{Task: t})
			}
			p.history(destination, "assigned", reason, []string{t.ID}, instruction)
		}
		p.tasks = append(p.tasks, domain.VersionTaskWrite{Value: t, Expected: expected})
		label := "未排期"
		if destination != nil {
			label = destination.Name
		}
		p.event(t, "version_assigned", "调整至 "+label+"："+reason, map[string]any{"from_version_id": previous, "project_version_id": target, "user_instruction": instruction, "user_id": p.a.UserID})
	}
	return nil
}
func (s *Service) AssignProjectVersion(c context.Context, a domain.Actor, p string, in VersionAssignmentInput) (ProjectVersionMutation, error) {
	if a.Kind != "human" && !(a.Kind == "developer" && a.ID == a.UserID && a.UserID != "" && in.UserConfirmed && nonempty(in.UserInstruction)) {
		return ProjectVersionMutation{}, fmt.Errorf("%w: 排期须由用户明确要求，MCP需提供用户确认与原话", domain.ErrForbidden)
	}
	plan, e := s.newVersionPlan(c, a, p)
	if e != nil {
		return ProjectVersionMutation{}, e
	}
	if !nonempty(in.RequestID) {
		return ProjectVersionMutation{}, errors.New("缺少 request_id")
	}
	key := receiptKey(a, p, in.RequestID)
	fp := fingerprint(struct {
		Operation string
		Input     VersionAssignmentInput
	}{"assign_project_version", in})
	if old, ok, e := s.versionPrior(c, key, fp); ok || e != nil {
		return old, e
	}
	if in.ProjectVersionID == "" && in.Version != 0 {
		return ProjectVersionMutation{}, errors.New("未排期的目标版本编号应填0")
	}
	if in.ProjectVersionID != "" {
		v := plan.versions[in.ProjectVersionID]
		if v == nil {
			return ProjectVersionMutation{}, domain.ErrNotFound
		}
		if in.Version <= 0 || v.Version != in.Version {
			return ProjectVersionMutation{}, domain.ErrConflict
		}
	}
	if e = plan.move(in.Tasks, in.ProjectVersionID, strings.TrimSpace(in.Body), strings.TrimSpace(in.UserInstruction)); e != nil {
		return ProjectVersionMutation{}, e
	}
	if len(plan.tasks) == 0 {
		return ProjectVersionMutation{}, errors.New("所选需求已经属于该版本，没有需要调整的内容")
	}
	return plan.finish(key, fp, in.ProjectVersionID)
}
func (s *Service) ActProjectVersion(c context.Context, a domain.Actor, id string, in ProjectVersionAction) (ProjectVersionMutation, error) {
	return s.actProjectVersion(c, a, id, in, nil)
}
func (s *Service) actProjectVersion(c context.Context, a domain.Actor, id string, in ProjectVersionAction, authorization *domain.TaskActionAuthorization) (ProjectVersionMutation, error) {
	if a.Kind != "human" && authorization == nil {
		return ProjectVersionMutation{}, domain.ErrForbidden
	}
	v, e := s.Store.GetProjectVersion(c, id)
	if e != nil {
		return ProjectVersionMutation{}, e
	}
	plan, e := s.newVersionPlan(c, a, v.ProjectID)
	if e != nil {
		return ProjectVersionMutation{}, e
	}
	if !nonempty(in.RequestID) {
		return ProjectVersionMutation{}, errors.New("缺少 request_id")
	}
	key := receiptKey(a, v.ProjectID, in.RequestID)
	fp := authorizedFingerprint(struct {
		ID    string
		Input ProjectVersionAction
	}{id, in}, authorization)
	if old, ok, e := s.versionPrior(c, key, fp); ok || e != nil {
		return old, e
	}
	current := plan.touch(id)
	if in.Version <= 0 || current.Version != in.Version {
		return ProjectVersionMutation{}, domain.ErrConflict
	}
	reason := strings.TrimSpace(in.Body)
	switch in.Action {
	case "start":
		if current.Status != "planned" || plan.state.VersionID != "" {
			return ProjectVersionMutation{}, domain.ErrConflict
		}
		current.Status = "active"
		current.StartedAt = plan.now
		current.EndedAt = 0
		plan.state.VersionID = id
		if reason == "" {
			reason = "开始推进版本"
		}
	case "reopen":
		if (current.Status != "closed" && current.Status != "completed") || reason == "" || plan.state.VersionID != "" {
			return ProjectVersionMutation{}, errors.New("当前不能有其他进行中里程碑，重新打开需填写原因")
		}
		current.Status = "active"
		current.EndedAt = 0
		plan.state.VersionID = id
	case "complete":
		if current.Status != "active" || plan.state.VersionID != id || reason == "" {
			return ProjectVersionMutation{}, errors.New("只能结束当前里程碑，请填写结论")
		}
		for _, item := range current.Scope {
			if !item.Removed && !domain.TaskFinished(item.Task.Status) {
				return ProjectVersionMutation{}, errors.New("仍有未结束的需求，请先验收/关闭，或选择关闭里程碑并转移剩余工作")
			}
		}
		current.Status = "completed"
		current.EndedAt = plan.now
		plan.state.VersionID = ""
	case "close":
		if current.Status == "closed" || current.Status == "completed" || reason == "" {
			return ProjectVersionMutation{}, errors.New("请填写关闭原因，已关闭版本不能重复关闭")
		}
		if in.DestinationVersionID == id {
			return ProjectVersionMutation{}, errors.New("剩余需求不能转入正在关闭的版本")
		}
		refs := []VersionTaskRef{}
		for _, item := range current.Scope {
			if item.Removed || item.Task.Status == "review" || domain.TaskFinished(item.Task.Status) {
				continue
			}
			t, e := s.Store.GetTask(c, item.Task.ID)
			if e != nil {
				return ProjectVersionMutation{}, e
			}
			if t.ProjectVersionID == id {
				refs = append(refs, VersionTaskRef{ID: t.ID, Version: t.Version})
			}
		}
		if plan.state.VersionID == id {
			plan.state.VersionID = ""
		}
		destination := ""
		if len(refs) > 0 {
			if in.DestinationVersionID != "" {
				return ProjectVersionMutation{}, errors.New("当前仅开放一个里程碑，剩余工作将自动转入下一里程碑")
			}
			successor, err := plan.ensureCurrent()
			if err != nil {
				return ProjectVersionMutation{}, err
			}
			destination = successor.ID
		}
		for start := 0; start < len(refs); start += 100 {
			end := min(start+100, len(refs))
			if e = plan.move(refs[start:end], destination, "里程碑关闭，转入下一里程碑："+reason, ""); e != nil {
				return ProjectVersionMutation{}, e
			}
		}

		current.Status = "closed"
		current.EndedAt = plan.now
	case "publish":
		if current.Status != "active" || current.ID != plan.state.VersionID || reason == "" || !nonempty(in.Environment) || len(in.Items) == 0 || len(in.Items) > 100 {
			return ProjectVersionMutation{}, errors.New("请选择本次实际上线的需求，填写环境和发布结果；已关闭版本不能上线")
		}
		release := domain.VersionRelease{ID: domain.ID("pub_"), Environment: strings.TrimSpace(in.Environment), Body: reason, Actor: a.Name, Source: "human", At: plan.now, Tasks: []domain.Task{}}
		seen := map[string]bool{}
		for _, ref := range in.Items {
			if seen[ref.ID] {
				return ProjectVersionMutation{}, errors.New("本次发布清单有重复需求")
			}
			seen[ref.ID] = true
			t, e := s.Store.GetTask(c, ref.ID)
			if e != nil {
				return ProjectVersionMutation{}, e
			}
			if t.ProjectID != v.ProjectID {
				return ProjectVersionMutation{}, domain.ErrForbidden
			}
			if t.ProjectVersionID != id || t.Status != "pending_release" || t.Version != ref.Version || t.Revision != ref.Revision || t.Submission == nil || t.Submission.Revision != t.Revision || t.Submission.SHA != ref.SHA || !nonempty(ref.SHA) {
				return ProjectVersionMutation{}, fmt.Errorf("%w: 只可登记本版本中当前已开发完成的确定提交，请刷新清单", domain.ErrConflict)
			}
			expected := t.Version
			t.Version++
			t.UpdatedAt = plan.now
			t.Status = "review"
			t.Deployment = &domain.Deployment{Revision: t.Revision, SHA: ref.SHA, Environment: release.Environment, Body: reason, Actor: a.Name, Source: "human", At: plan.now, ProjectVersionID: id, ReleaseID: release.ID}
			found := false
			for i := range current.Scope {
				if current.Scope[i].Task.ID == t.ID && !current.Scope[i].Removed {
					current.Scope[i].Task = t
					current.Scope[i].Published = true
					found = true
				}
			}
			if !found {
				return ProjectVersionMutation{}, domain.ErrConflict
			}
			release.Tasks = append(release.Tasks, t)
			plan.tasks = append(plan.tasks, domain.VersionTaskWrite{Value: t, Expected: expected})
			plan.event(t, "mark_deployed", reason, map[string]any{"deployment": t.Deployment, "project_version_id": id, "release_id": release.ID})
		}
		current.Releases = append(current.Releases, release)
		current.ScopeFrozen = false
		current.Status = "active"
	default:
		return ProjectVersionMutation{}, errors.New("不支持的版本操作")
	}
	instruction := ""
	if authorization != nil {
		instruction = authorization.UserInstruction
	}
	plan.history(current, in.Action, reason, nil, instruction)
	plan.event(domain.Task{}, "project_version_"+in.Action, current.Name+"："+reason, map[string]string{"project_version_id": id})
	return plan.finish(key, fp, id)
}

func (s *Service) TasksForProjectVersion(c context.Context, a domain.Actor, p, filter string) ([]domain.Task, error) {
	if e := s.CheckProject(c, a, p, false); e != nil {
		return nil, e
	}
	if filter != "" && filter != "unplanned" {
		v, e := s.Store.GetProjectVersion(c, filter)
		if e != nil {
			return nil, e
		}
		if v.ProjectID != p {
			return nil, domain.ErrForbidden
		}
	}
	tasks, e := s.Store.ListTasks(c, p)
	if e != nil {
		return nil, e
	}
	if filter == "" {
		return tasks, nil
	}
	target := filter
	if filter == "unplanned" {
		target = ""
	}
	out := []domain.Task{}
	for _, t := range tasks {
		if t.ProjectVersionID == target {
			out = append(out, t)
		}
	}
	return out, nil
}
func (s *Service) WorkListForProjectVersion(c context.Context, a domain.Actor, filter string) (map[string]any, error) {
	if filter == "" {
		return s.WorkList(c, a)
	}
	_, e := s.TasksForProjectVersion(c, a, a.ProjectID, filter)
	if e != nil {
		return nil, e
	}
	work, e := s.WorkList(c, a)
	if e != nil {
		return nil, e
	}
	target := filter
	if filter == "unplanned" {
		target = ""
	}
	tasks := []domain.Task{}
	for _, t := range work["tasks"].([]domain.Task) {
		if t.ProjectVersionID == target {
			tasks = append(tasks, t)
		}
	}
	work["tasks"] = tasks
	return work, nil
}
