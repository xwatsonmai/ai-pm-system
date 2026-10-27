package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"reflect"
	"slices"
)

func ProjectRole(a domain.Actor, p domain.Project) string {
	if a.ID == "local-owner" {
		return "admin"
	}
	for _, m := range p.Memberships {
		if m.UserID == a.ID {
			return m.Role
		}
	}
	if len(p.Memberships) == 0 && p.OwnerID == a.ID && a.ID != "" {
		return "admin"
	}
	if len(p.Memberships) == 0 && slices.Contains(p.AccessMembers, a.ID) {
		return "developer"
	}
	return ""
}
func CanRead(a domain.Actor, p domain.Project) bool {
	if a.Kind != "human" {
		if a.ProjectID != p.ID {
			return false
		}
		if a.Kind == "developer" && a.UserID != "" {
			return ProjectRole(domain.Actor{ID: a.UserID, Kind: "human", PlatformAdmin: a.PlatformAdmin}, p) != ""
		}
		return true
	}
	return a.ID != "" && ProjectRole(a, p) != ""
}
func CanManage(a domain.Actor, p domain.Project) bool {
	return a.Kind == "human" && (a.PlatformAdmin || ProjectRole(a, p) == "admin")
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
		p.Memberships = []domain.ProjectMember{{UserID: a.ID, Role: "admin"}}
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

func (s *Service) SetProjectMember(c context.Context, a domain.Actor, project, user, role string, remove bool) (domain.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.Store.GetProject(c, project)
	if e != nil {
		return p, e
	}
	if !CanManage(a, p) {
		return p, domain.ErrForbidden
	}
	if !remove && role != "admin" && role != "product" && role != "developer" {
		return p, errors.New("请选择开发者、产品或管理员")
	}
	users, e := s.Store.ListUsers(c)
	if e != nil {
		return p, e
	}
	if !remove && !slices.ContainsFunc(users, func(u domain.User) bool { return u.ID == user }) && user != "local-owner" {
		return p, errors.New("成员不存在，请刷新成员目录后重试")
	}
	if len(p.Memberships) == 0 {
		if p.OwnerID != "" {
			p.Memberships = append(p.Memberships, domain.ProjectMember{UserID: p.OwnerID, Role: "admin"})
		}
		for _, id := range p.AccessMembers {
			if id != p.OwnerID {
				p.Memberships = append(p.Memberships, domain.ProjectMember{UserID: id, Role: "developer"})
			}
		}
	}
	out := []domain.ProjectMember{}
	for _, m := range p.Memberships {
		if m.UserID != user {
			out = append(out, m)
		}
	}
	if !remove {
		out = append(out, domain.ProjectMember{UserID: user, Role: role})
	}
	if !slices.ContainsFunc(out, func(m domain.ProjectMember) bool { return m.Role == "admin" }) {
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
