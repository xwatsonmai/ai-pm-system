package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (p *versionPlan) ensureCurrent() (*domain.ProjectVersion, error) {
	if p.state.VersionID != "" {
		v := p.versions[p.state.VersionID]
		if v == nil || v.Status != "active" {
			return nil, domain.ErrConflict
		}
		return v, nil
	}
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
	if t.ProjectVersionID == "" {
		return true, nil
	}
	v, e := s.Store.GetProjectVersion(c, t.ProjectVersionID)
	if e != nil {
		return false, e
	}
	return (v.Status == "closed" || v.Status == "completed") && domain.TaskActive(t.Status) && t.Status != "review", nil
}
func (p *versionPlan) bindTask(t *domain.Task) error {
	current, e := p.ensureCurrent()
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
func (s *Service) taskMilestoneBatch(c context.Context, a domain.Actor, t *domain.Task) (*versionPlan, error) {
	needed, e := s.needsCurrentMilestone(c, *t)
	if e != nil || !needed {
		return nil, e
	}
	p, e := s.newVersionPlan(c, a, t.ProjectID)
	if e != nil {
		return nil, e
	}
	if e = p.bindTask(t); e != nil {
		return nil, e
	}
	return p, nil
}
func (s *Service) commitWithMilestone(c context.Context, a domain.Actor, t domain.Task, old int, ev domain.Event, key, fp, claim string) (domain.Mutation, error) {
	var result domain.Mutation
	for attempt := 0; attempt < 32; attempt++ {
		if e := c.Err(); e != nil {
			return result, e
		}
		candidate := t
		plan, e := s.taskMilestoneBatch(c, a, &candidate)
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
			e = s.Store.CommitVersionPlan(c, nil, []domain.VersionTaskWrite{{Value: candidate, Expected: old}}, []domain.Event{ev}, r, batch)
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
