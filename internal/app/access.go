package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"reflect"
	"slices"
)

func ProjectRoles(a domain.Actor, p domain.Project) []string {
	if a.ID == "local-owner" {
		return []string{"admin"}
	}
	if a.ID == "" {
		return nil
	}
	for _, m := range domain.ProjectMembers(p) {
		if m.UserID == a.ID {
			return m.Roles
		}
	}
	return nil
}
func CanRead(a domain.Actor, p domain.Project) bool {
	if a.Kind != "human" {
		if a.ProjectID != p.ID {
			return false
		}
		if a.Kind == "developer" && a.UserID != "" {
			return len(ProjectRoles(domain.Actor{ID: a.UserID, Kind: "human", PlatformAdmin: a.PlatformAdmin}, p)) > 0
		}
		return true
	}
	return len(ProjectRoles(a, p)) > 0
}
func CanManage(a domain.Actor, p domain.Project) bool {
	return a.Kind == "human" && (a.PlatformAdmin || CanAccessPage(a, p, "access"))
}
func (s *Service) CheckProject(c context.Context, a domain.Actor, id string, manage bool) error {
	p, e := s.Store.GetProject(c, id)
	if e != nil {
		return e
	}
	if (manage && !CanManage(a, p)) || (!manage && !CanRead(a, p)) {
		return domain.ErrForbidden
	}
	return nil
}
func (s *Service) SaveProjectAs(c context.Context, a domain.Actor, p domain.Project) (domain.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.Kind != "human" || a.ID == "" {
		return p, domain.ErrForbidden
	}
	if p.ProfileID != "" {
		return p, errors.New("Agent 模型由平台设置统一管理")
	}
	p.OwnerID = a.ID
	p.OwnerName = a.Name
	if p.ID != "" {
		old, e := s.Store.GetProject(c, p.ID)
		if e != nil {
			return p, e
		}
		if !CanRead(a, old) {
			return p, domain.ErrForbidden
		}
		if (p.AccessMembers != nil && !reflect.DeepEqual(p.AccessMembers, old.AccessMembers)) || (p.Memberships != nil && !reflect.DeepEqual(p.Memberships, old.Memberships)) {
			return p, errors.New("请通过项目成员管理添加、移除员工或调整角色")
		}
		if (p.Name != old.Name || p.Description != old.Description) && !CanAccessPage(a, old, "settings") {
			return p, domain.ErrForbidden
		}
		if (p.Repository != old.Repository || p.Branch != old.Branch || p.AuthMode != old.AuthMode || p.CredentialID != old.CredentialID || p.AgentReadMode != old.AgentReadMode || p.AgentRef != old.AgentRef) && !CanAccessPage(a, old, "git") {
			return p, domain.ErrForbidden
		}
		if p.ProjectRoles != nil && !reflect.DeepEqual(p.ProjectRoles, old.ProjectRoles) {
			return p, errors.New("请通过角色与权限页面修改角色")
		}
		p.ProjectRoles = old.ProjectRoles
		if p.AccessVersion != 0 && p.AccessVersion != old.AccessVersion {
			return p, domain.ErrConflict
		}
		p.AccessVersion = old.AccessVersion + 1
		p.Memberships = old.Memberships
		p.AccessMembers = old.AccessMembers
		p.Members = old.Members
		if old.OwnerID != "" {
			p.OwnerID = old.OwnerID
			p.OwnerName = old.OwnerName
		}
	} else {
		if len(p.AccessMembers) > 0 || len(p.Memberships) > 0 {
			return p, errors.New("请先创建项目，再通过项目成员管理分配员工")
		}
		p.Memberships = []domain.ProjectMember{{UserID: a.ID, Roles: []string{"admin"}}}
		p.AccessMembers = []string{a.ID}
		p.Members = []string{a.Name}
	}
	return s.saveProject(c, domain.NormalizeProjectAccess(p))
}
func (s *Service) SaveProfileAs(c context.Context, a domain.Actor, p domain.Profile, key string) (domain.Profile, error) {
	if a.Kind != "human" || (!a.PlatformAdmin && a.ID != "local-owner") {
		return p, domain.ErrForbidden
	}
	p.OwnerID = ""
	return s.SaveProfile(c, p, key)
}

// SetProjectMember adds a member or replaces their roles; roles keep the
// order of domain.ProjectRoles and a member keeps their place in the list.
func (s *Service) SetProjectMember(c context.Context, a domain.Actor, project, user string, roles []string, remove bool) (domain.Project, error) {
	return s.SetProjectMemberAccess(c, a, project, user, roles, nil, remove, 0)
}
func (s *Service) SetProjectMemberAccess(c context.Context, a domain.Actor, project, user string, roles, responsibilities []string, remove bool, expected int) (domain.Project, error) {
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
	if expected != 0 && expected != p.AccessVersion {
		return p, domain.ErrConflict
	}
	picked := []string{}
	for _, role := range roles {
		if !slices.ContainsFunc(p.ProjectRoles, func(r domain.ProjectRole) bool { return r.ID == role }) {
			return p, errors.New("角色不存在")
		}
	}
	for _, role := range p.ProjectRoles {
		if slices.Contains(roles, role.ID) {
			picked = append(picked, role.ID)
		}
	}
	if !remove && len(picked) == 0 {
		return p, errors.New("请为成员选择至少一个角色")
	}
	if responsibilities != nil {
		seen := []string{}
		for _, d := range responsibilities {
			if !slices.Contains(domain.ProjectResponsibilities, d) || slices.Contains(seen, d) {
				return p, errors.New("待办职责无效或重复")
			}
			seen = append(seen, d)
		}
	}
	users, e := s.Store.ListUsers(c)
	if e != nil {
		return p, e
	}
	if !remove && !slices.ContainsFunc(users, func(u domain.User) bool { return u.ID == user }) && user != "local-owner" {
		return p, errors.New("成员不存在，请刷新成员目录后重试")
	}
	out := []domain.ProjectMember{}
	found := false
	for _, m := range p.Memberships {
		if m.UserID != user {
			out = append(out, m)
			continue
		}
		found = true
		if !remove {
			if responsibilities == nil {
				responsibilities = m.Responsibilities
			}
			out = append(out, domain.ProjectMember{UserID: user, Roles: picked, Responsibilities: responsibilities})
		}
	}
	if !remove && !found {
		if responsibilities == nil {
			responsibilities = []string{}
			for _, d := range domain.ProjectResponsibilities {
				if slices.Contains(roles, d) {
					responsibilities = append(responsibilities, d)
				}
			}
		}
		out = append(out, domain.ProjectMember{UserID: user, Roles: picked, Responsibilities: responsibilities})
	}
	p.Memberships = out
	if !hasProjectManager(p) {
		return p, errors.New("项目至少保留一位管理员（拥有角色与权限页面）")
	}
	p.AccessMembers = []string{}
	p.Members = []string{}
	for _, m := range out {
		p.AccessMembers = append(p.AccessMembers, m.UserID)
		for _, u := range users {
			if u.ID == m.UserID {
				p.Members = append(p.Members, u.Name)
			}
		}
	}
	old := p.AccessVersion
	p.AccessVersion++
	return p, s.Store.CommitProjectAccess(c, p, old)
}
