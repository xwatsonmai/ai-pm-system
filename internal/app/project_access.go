package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"slices"
	"strings"
)

func actorUser(a domain.Actor) string {
	if a.Kind == "developer" && a.UserID != "" {
		return a.UserID
	}
	return a.ID
}

func CanAccessPage(a domain.Actor, p domain.Project, page string) bool {
	if !slices.Contains(domain.ProjectPages, page) {
		return false
	}
	if actorUser(a) == "local-owner" && (a.Kind == "human" || (a.Kind == "developer" && a.ProjectID == p.ID)) {
		return true
	}
	// System work and local legacy tokens are scoped to their originating project.
	if a.Kind != "human" && a.ProjectID == p.ID && (a.Kind == "platform" || a.UserID == "") {
		return page != "access"
	}
	if a.Kind != "human" && a.ProjectID != p.ID {
		return false
	}
	p = domain.NormalizeProjectAccess(p)
	user := actorUser(a)
	for _, m := range p.Memberships {
		if m.UserID != user {
			continue
		}
		for _, r := range p.ProjectRoles {
			if slices.Contains(m.Roles, r.ID) && slices.Contains(r.Pages, page) {
				return true
			}
		}
	}
	return false
}
func CanAccessAnyPage(a domain.Actor, p domain.Project, pages ...string) bool {
	for _, page := range pages {
		if CanAccessPage(a, p, page) {
			return true
		}
	}
	return false
}
func (s *Service) RequirePages(c context.Context, a domain.Actor, id string, pages ...string) error {
	p, e := s.Store.GetProject(c, id)
	if e != nil {
		return e
	}
	if !CanAccessAnyPage(a, p, pages...) {
		return domain.ErrForbidden
	}
	return nil
}

func ProjectForActor(a domain.Actor, p domain.Project) domain.Project {
	p = domain.NormalizeProjectAccess(p)
	if !CanAccessAnyPage(a, p, "code", "git", "timeline") {
		p.Repository = ""
		p.Branch = ""
		p.SHA = ""
		p.SyncedAt = 0
		p.SyncError = ""
		p.AgentRef = ""
	}
	if !CanAccessPage(a, p, "git") {
		p.CredentialID = ""
		p.AuthMode = ""
		p.AgentReadMode = ""
	}
	if !CanManage(a, p) {
		own := []domain.ProjectMember{}
		for _, m := range p.Memberships {
			if m.UserID == actorUser(a) {
				own = append(own, m)
			}
		}
		p.Memberships = own
		p.AccessMembers = nil
		p.Members = nil
		p.ProjectRoles = nil
	}
	return p
}

// FilterContext is shared by ordinary pages and task_get_context. A membership
// alone must never expose all page data through a bootstrap/aggregate endpoint.
func FilterContext(a domain.Actor, p domain.Project, out map[string]any) map[string]any {
	if !CanAccessAnyPage(a, p, "tasks", "reviews", "versions") {
		out["tasks"] = []domain.Task{}
		out["requirement_reviews"] = []domain.RequirementReviewSummary{}
		delete(out, "task")
		delete(out, "requirement_review")
		delete(out, "iteration_history")
	}
	if !CanAccessPage(a, p, "versions") {
		out["project_versions"] = []ProjectVersionSummary{}
		out["current_milestone"] = nil
	}
	if !CanAccessPage(a, p, "reviews") {
		out["artifacts"] = []domain.Artifact{}
	}
	if !CanAccessAnyPage(a, p, "tasks", "reviews", "versions", "timeline") {
		out["events"] = []domain.Event{}
	}
	out["project"] = ProjectForActor(a, p)
	return out
}

type RoleInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Pages       []string `json:"pages"`
	Version     int      `json:"version"`
}

func (s *Service) SaveProjectRole(c context.Context, a domain.Actor, project, id string, in RoleInput) (domain.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.Store.GetProject(c, project)
	if e != nil {
		return p, e
	}
	if !CanManage(a, p) {
		return p, domain.ErrForbidden
	}
	p = domain.NormalizeProjectAccess(p)
	if in.Version != p.AccessVersion {
		return p, domain.ErrConflict
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len([]rune(name)) > 80 || len(in.Description) > 2000 {
		return p, errors.New("请填写80字以内的角色名称，说明最多2000字节")
	}
	pages := []string{}
	for _, v := range in.Pages {
		if !slices.Contains(domain.ProjectPages, v) || slices.Contains(pages, v) {
			return p, errors.New("页面权限无效或重复")
		}
		pages = append(pages, v)
	}
	index := -1
	for i, r := range p.ProjectRoles {
		if strings.EqualFold(r.Name, name) && r.ID != id {
			return p, errors.New("角色名称已存在")
		}
		if r.ID == id {
			index = i
		}
	}
	if id != "" && index < 0 {
		return p, domain.ErrNotFound
	}
	if index < 0 {
		p.ProjectRoles = append(p.ProjectRoles, domain.ProjectRole{ID: domain.ID("role_"), Name: name, Description: in.Description, Pages: pages, Version: 1})
	} else {
		r := p.ProjectRoles[index]
		r.Name = name
		r.Description = in.Description
		r.Pages = pages
		r.Version++
		p.ProjectRoles[index] = r
	}
	if !hasProjectManager(p) {
		return p, errors.New("项目至少保留一位拥有角色与权限页面的管理员")
	}
	old := p.AccessVersion
	p.AccessVersion++
	return p, s.Store.CommitProjectAccess(c, p, old)
}
func (s *Service) DeleteProjectRole(c context.Context, a domain.Actor, project, id, replacement string, expected int) (domain.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.Store.GetProject(c, project)
	if e != nil {
		return p, e
	}
	if !CanManage(a, p) {
		return p, domain.ErrForbidden
	}
	p = domain.NormalizeProjectAccess(p)
	if expected != p.AccessVersion {
		return p, domain.ErrConflict
	}
	if !slices.ContainsFunc(p.ProjectRoles, func(r domain.ProjectRole) bool { return r.ID == id }) {
		return p, domain.ErrNotFound
	}
	if replacement != "" && (replacement == id || !slices.ContainsFunc(p.ProjectRoles, func(r domain.ProjectRole) bool { return r.ID == replacement })) {
		return p, errors.New("替代角色不存在")
	}
	for i, m := range p.Memberships {
		if !slices.Contains(m.Roles, id) {
			continue
		}
		if replacement == "" {
			return p, errors.New("角色仍分配给成员，请指定替代角色")
		}
		m.Roles = slices.DeleteFunc(slices.Clone(m.Roles), func(r string) bool { return r == id })
		if !slices.Contains(m.Roles, replacement) {
			m.Roles = append(m.Roles, replacement)
		}
		p.Memberships[i] = m
	}
	p.ProjectRoles = slices.DeleteFunc(slices.Clone(p.ProjectRoles), func(r domain.ProjectRole) bool { return r.ID == id })
	if !hasProjectManager(p) {
		return p, errors.New("项目至少保留一位拥有角色与权限页面的管理员")
	}
	old := p.AccessVersion
	p.AccessVersion++
	return p, s.Store.CommitProjectAccess(c, p, old)
}
func hasProjectManager(p domain.Project) bool {
	for _, m := range domain.ProjectMembers(p) {
		if CanAccessPage(domain.Actor{ID: m.UserID, Kind: "human"}, p, "access") {
			return true
		}
	}
	return false
}

func TaskActionPages(action string) []string {
	switch action {
	case "accept", "reject":
		return []string{"reviews"}
	case "mark_deployed":
		return []string{"reviews", "versions"}
	default:
		return []string{"tasks"}
	}
}
func (s *Service) RequireTaskAction(c context.Context, a domain.Actor, t domain.Task, action string) error {
	return s.RequirePages(c, a, t.ProjectID, TaskActionPages(action)...)
}
