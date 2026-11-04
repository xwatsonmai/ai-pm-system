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

func (p *versionPlan) ensureCurrent() (*domain.ProjectVersion, error) {
	if p.state.VersionID != "" {
		p.stateRead = true
		v := p.versions[p.state.VersionID]
		if v == nil || v.Status != "active" {
			return nil, domain.ErrConflict
		}
		p.check(v)
		return v, nil
	}
	active := p.otherActive("")
	if len(active) == 1 {
		p.state.VersionID = active[0].ID
		p.check(active[0])
		return active[0], nil
	}
	if len(active) > 1 {
		return nil, errors.New("存在多个进行中版本，请先选择默认版本或明确目标版本")
	}
	return p.createMilestone()
}
func (p *versionPlan) createMilestone() (*domain.ProjectVersion, error) {
	n := len(p.versions) + 1
	name := ""
	for {
		name = fmt.Sprintf("里程碑 %d", n)
		used := false
		for _, v := range p.versions {
			if strings.EqualFold(name, v.Name) {
				used = true
				break
			}
		}
		if !used {
			break
		}
		n++
	}
	v := &domain.ProjectVersion{ID: domain.ID("ver_"), ProjectID: p.p, Name: name, Status: "active", StartedAt: p.now, CreatedAt: p.now, Scope: []domain.VersionScopeItem{}, Releases: []domain.VersionRelease{}, History: []domain.VersionHistoryEntry{}}
	p.versions[v.ID] = v
	p.expected[v.ID] = 0
	p.state.VersionID = v.ID
	p.history(v, "created", "自动创建当前里程碑", nil, "")
	p.event(domain.Task{}, "project_version_created", "开始里程碑："+name, map[string]string{"project_version_id": v.ID})
	return v, nil
}
func (s *Service) CurrentMilestone(c context.Context, a domain.Actor, p string) (*ProjectVersionDetail, error) {
	if e := s.CheckProject(c, a, p, false); e != nil {
		return nil, e
	}
	state, e := s.Store.GetCurrentMilestone(c, p)
	if e != nil {
		return nil, e
	}
	if state.VersionID == "" {
		return nil, nil
	}
	v, e := s.ProjectVersionContext(c, a, state.VersionID)
	return &v, e
}
func (s *Service) needsCurrentMilestone(c context.Context, t domain.Task) (bool, error) {
	if t.ProjectVersionID == "" || t.Version <= 1 {
		return true, nil
	}
	v, e := s.Store.GetProjectVersion(c, t.ProjectVersionID)
	if e != nil {
		return false, e
	}
	return (v.Status == "closed" || v.Status == "completed") && domain.TaskActive(t.Status) && t.Status != "review", nil
}
func (p *versionPlan) bindTask(t *domain.Task, targets ...string) error {
	target := ""
	if len(targets) > 0 {
		target = targets[0]
	}
	var current *domain.ProjectVersion
	var e error
	if target != "" {
		current = p.versions[target]
		if current == nil {
			return domain.ErrNotFound
		}
		if current.Status != "active" {
			return fmt.Errorf("%w: 目标版本已结束或尚未开始", domain.ErrConflict)
		}
	} else {
		current, e = p.ensureCurrent()
	}
	if e != nil {
		return e
	}
	oldID := t.ProjectVersionID
	if oldID != "" && oldID != current.ID {
		old := p.touch(oldID)
		if old == nil {
			return domain.ErrNotFound
		}
		for i := range old.Scope {
			item := &old.Scope[i]
			if item.Task.ID != t.ID || item.Removed {
				continue
			}
			if item.Published || domain.TaskFinished(item.Task.Status) {
				item.DetachedRevision = t.Revision
				if item.Published && item.Task.Revision == t.Revision && t.Review != nil && !t.Review.Accepted {
					item.Task.Review = t.Review
					item.Task.Status = t.Status
					item.Reason = "验收退回，后续开发转入当前里程碑：" + t.Review.Note
				}
			} else {
				item.Removed = true
				item.Reason = "后续工作转入当前里程碑"
			}
		}
		p.history(old, "continued", "后续工作转入当前里程碑，原交付历史保留", []string{t.ID}, "")
	}
	t.ProjectVersionID = current.ID
	p.touch(current.ID)
	found := false
	for i := range current.Scope {
		if current.Scope[i].Task.ID == t.ID {
			current.Scope[i] = domain.VersionScopeItem{Task: *t}
			found = true
			break
		}
	}
	if !found {
		current.Scope = append(current.Scope, domain.VersionScopeItem{Task: *t})
	}
	p.history(current, "assigned", "归入当前里程碑", []string{t.ID}, "")
	if t.Version > 1 {
		p.event(*t, "milestone_assigned", "归入里程碑："+current.Name, map[string]string{"project_version_id": current.ID, "from_version_id": oldID})
	}
	return nil
}
func (s *Service) taskMilestoneBatch(c context.Context, a domain.Actor, t *domain.Task, targets ...string) (*versionPlan, error) {
	needed, e := s.needsCurrentMilestone(c, *t)
	if e != nil {
		return nil, e
	}
	if !needed {
		if len(targets) > 0 && targets[0] != "" && targets[0] != t.ProjectVersionID {
			prior, err := s.Store.GetTask(c, t.ID)
			if err != nil {
				return nil, err
			}
			if !domain.TaskFinished(prior.Status) && prior.Status != "review" {
				return nil, errors.New("进行中需求请通过安排目标版本调整归属")
			}
		} else {
			return nil, nil
		}
	}
	p, e := s.newVersionPlan(c, a, t.ProjectID)
	if e != nil {
		return nil, e
	}
	target := ""
	if len(targets) > 0 {
		target = targets[0]
	}
	if t.Version <= 1 && t.ProjectVersionID != "" {
		target = t.ProjectVersionID
	}
	if t.ParentRequirementID != "" && !domain.TaskFinished(t.Status) {
		parent, err := s.Store.GetTask(c, t.ParentRequirementID)
		if err != nil {
			return nil, err
		}
		parentVersion := p.versions[parent.ProjectVersionID]
		if parentVersion != nil && parentVersion.Status == "active" {
			if target != "" && target != parent.ProjectVersionID {
				return nil, errors.New("关联 Bug 必须跟随所属需求版本")
			}
			target = parent.ProjectVersionID
		} else {
			// Filing or reopening an acceptance Bug in an ended version starts
			// follow-up work for both parent and Bug, retaining the release.
			old := parent.Version
			parent.Version++
			parent.UpdatedAt = p.now
			if !parent.ReviewingChange() {
				parent.Status = "fixing"
			}
			parent.TestVerification = nil
			if err = p.bindTask(&parent, target); err != nil {
				return nil, err
			}
			target = parent.ProjectVersionID
			p.tasks = append(p.tasks, domain.VersionTaskWrite{Value: parent, Expected: old})
		}
	}
	if e = p.bindTask(t, target); e != nil {
		return nil, e
	}
	return p, nil
}
func (s *Service) commitWithMilestone(c context.Context, a domain.Actor, t domain.Task, old int, ev domain.Event, key, fp, claim string, targets ...string) (domain.Mutation, error) {
	var result domain.Mutation
	for attempt := 0; attempt < 32; attempt++ {
		if e := c.Err(); e != nil {
			return result, e
		}
		candidate := t
		plan, e := s.taskMilestoneBatch(c, a, &candidate, targets...)
		if e != nil {
			if errors.Is(e, domain.ErrConflict) {
				continue
			}
			return result, e
		}
		var data map[string]json.RawMessage
		if json.Unmarshal(ev.Data, &data) == nil && data != nil {
			data["project_version_id"], _ = json.Marshal(candidate.ProjectVersionID)
			ev.Data, _ = json.Marshal(data)
		}
		result = domain.Mutation{Task: candidate, ClaimID: claim}
		b, e := json.Marshal(result)
		if e != nil {
			return result, e
		}
		r := &domain.Receipt{Key: key, Fingerprint: fp, Response: b}
		if plan == nil {
			e = s.Store.CommitTask(c, candidate, old, ev, r)
		} else {
			batch := plan.batch()
			e = s.Store.CommitVersionPlan(c, nil, append(plan.tasks, domain.VersionTaskWrite{Value: candidate, Expected: old}), []domain.Event{ev}, r, batch)
		}
		if !errors.Is(e, domain.ErrConflict) {
			return result, e
		}
		time.Sleep(time.Duration(attempt+1) * time.Millisecond)
		if prior, ok, x := s.prior(c, key, fp); ok || x != nil {
			return prior, x
		}
		if old > 0 {
			current, x := s.Store.GetTask(c, t.ID)
			if x != nil {
				return result, x
			}
			if current.Version != old {
				return result, domain.ErrConflict
			}
		}
	}
	return result, domain.ErrConflict
}

func (p *versionPlan) otherActive(exclude string) []*domain.ProjectVersion {
	out := []*domain.ProjectVersion{}
	for id, v := range p.versions {
		if id != exclude && v.Status == "active" {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (p *versionPlan) replaceDefault(exclude, target string, expected int) error {
	if target != "" {
		v := p.versions[target]
		if v == nil || v.ID == exclude || v.Status != "active" || v.Version != expected {
			return fmt.Errorf("%w: 后继默认版本已变化", domain.ErrConflict)
		}
		p.state.VersionID = target
		p.check(v)
		return nil
	}
	active := p.otherActive(exclude)
	if len(active) > 1 {
		return errors.New("存在多个进行中版本，请明确选择后继默认版本")
	}
	p.state.VersionID = ""
	if len(active) == 1 {
		p.state.VersionID = active[0].ID
		p.check(active[0])
	}
	return nil
}
