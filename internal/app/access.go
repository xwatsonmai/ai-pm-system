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
	return a.Kind == "human" && (a.PlatformAdmin || slices.Contains(ProjectRoles(a, p), "admin"))
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
	return s.saveProject(c, p)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.Store.GetProject(c, project)
	if e != nil {
		return p, e
	}
	if !CanManage(a, p) {
		return p, domain.ErrForbidden
	}
	picked := []string{}
	for _, r := range domain.ProjectRoles {
		if slices.Contains(roles, r) {
			picked = append(picked, r)
		}
	}
	if !remove && (len(picked) == 0 || len(picked) != len(slices.Compact(slices.Sorted(slices.Values(roles))))) {
		return p, errors.New("请为成员选择至少一个角色：产品、开发、测试或管理员")
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
	for _, m := range domain.ProjectMembers(p) {
		if m.UserID != user {
			out = append(out, m)
		} else if !remove {
			out = append(out, domain.ProjectMember{UserID: user, Roles: picked})
			found = true
		}
	}
	if !remove && !found {
		out = append(out, domain.ProjectMember{UserID: user, Roles: picked})
	}
	if !slices.ContainsFunc(out, func(m domain.ProjectMember) bool { return m.Has("admin") }) {
		return p, errors.New("项目至少保留一位管理员")
	}
	// Owner is retained as the project creator, not an implicit membership after migration.
	p.Memberships = out
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
	if e = s.Store.PutProject(c, p); e != nil {
		return p, e
	}
	return p, nil
}
