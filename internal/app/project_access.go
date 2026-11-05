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

type actionActorKey struct{}

// WithActionActor binds a trusted in-process platform write to its real owner.
// Request payloads cannot set this context value. The writer identity remains
// platform (e.g. triage still creates ready Bugs attributed to the Agent).
func WithActionActor(c context.Context, owner domain.Actor) context.Context {
	return context.WithValue(c, actionActorKey{}, owner)
}

func ActionActor(c context.Context, writer domain.Actor) domain.Actor {
	if writer.Kind != "platform" {
		return writer
	}
	owner, ok := c.Value(actionActorKey{}).(domain.Actor)
	if !ok {
		return writer
	}
	if owner.ID == "" || (owner.Kind != "human" && owner.Kind != "developer") || writer.ProjectID == "" || (owner.ProjectID != "" && owner.ProjectID != writer.ProjectID) || (owner.Kind == "developer" && owner.ProjectID != writer.ProjectID) {
		return domain.Actor{}
	}
	return owner
}

func CanPerformAction(a domain.Actor, p domain.Project, action string) bool {
	if !slices.Contains(domain.ProjectActions, action) {
		return false
	}
	if actorUser(a) == "local-owner" && (a.Kind == "human" || (a.Kind == "developer" && a.ProjectID == p.ID)) {
		return true
	}
	if a.ProjectID == p.ID && (a.Kind == "platform" || (a.Kind == "developer" && a.UserID == "")) {
		return true
	}
	if a.Kind != "human" && (a.Kind != "developer" || a.ProjectID != p.ID) {
		return false
	}
	p = domain.NormalizeProjectAccess(p)
	for _, member := range p.Memberships {
		if member.UserID != actorUser(a) {
			continue
		}
		for _, role := range p.ProjectRoles {
			if !slices.Contains(member.Roles, role.ID) {
				continue
			}
			actions := domain.ProjectRoleActions(role)
			if slices.Contains(actions, action) || (action == domain.ActionCreateLinkedBug && slices.Contains(actions, domain.ActionCreateBug)) {
				return true
			}
		}
	}
	return false
}

func (s *Service) RequireAction(c context.Context, a domain.Actor, project, action string) error {
	return s.RequireAnyAction(c, a, project, action)
}

func (s *Service) RequireAnyAction(c context.Context, a domain.Actor, project string, actions ...string) error {
	if a.Kind == "platform" && a.ProjectID != project {
		return domain.ErrForbidden
	}
	p, e := s.Store.GetProject(c, project)
	if e != nil {
		return e
	}
	a = ActionActor(c, a)
	for _, action := range actions {
		if CanPerformAction(a, p, action) {
			return nil
		}
	}
	return domain.ErrForbidden
}

func taskCreationAction(kind, parent string) string {
	if kind == "bug" {
		if parent != "" {
			return domain.ActionCreateLinkedBug
		}
		return domain.ActionCreateBug
	}
	return domain.ActionCreateRequirement
}

func (s *Service) requireTaskCreation(c context.Context, a domain.Actor, project, kind, parent string) error {
	if e := s.RequireAction(c, a, project, taskCreationAction(kind, parent)); e != nil {
		return e
	}
	if parent != "" {
		return s.RequirePages(c, ActionActor(c, a), project, "tasks", "reviews", "versions")
	}
	return nil
}

// The caller holds Service.mu through the corresponding storage commit.
func (s *Service) authorizeTaskWrite(c context.Context, a domain.Actor, t domain.Task, old int) error {
	if old == 0 && t.ParentTaskID != "" {
		if e := s.RequirePages(c, ActionActor(c, a), t.ProjectID, "tasks", "reviews", "versions"); e != nil {
			return e
		}
		parent, e := s.Store.GetTask(c, t.ParentTaskID)
		if e != nil {
			return e
		}
		if parent.ProjectID != t.ProjectID || parent.Kind != "requirement" || t.Kind != "requirement" || t.ParentRequirementID != "" || t.Iteration != nil {
			return domain.ErrForbidden
		}
	}
	if old == 0 {
		if e := s.requireTaskCreation(c, a, t.ProjectID, t.Kind, t.ParentRequirementID); e != nil {
			return e
		}
	} else {
		current, e := s.Store.GetTask(c, t.ID)
		if e != nil {
			return e
		}
		pages := []string{"tasks"}
		if current.ParentRequirementID != "" {
			pages = append(pages, "reviews")
		}
		if e = s.RequirePages(c, ActionActor(c, a), current.ProjectID, pages...); e != nil {
			return e
		}
		if t.Kind != current.Kind {
			if e = s.requireTaskCreation(c, a, current.ProjectID, t.Kind, current.ParentRequirementID); e != nil {
				return e
			}
			if current.Kind == "requirement" {
				children, err := s.Store.ListTasks(c, current.ProjectID)
				if err != nil {
					return err
				}
				for _, child := range children {
					if child.ParentTaskID == current.ID {
						return errors.New("已有子需求的上级不能转换为 Bug")
					}
				}
			}
		}
	}
	ids := []string{}
	for _, f := range t.Attachments {
		ids = append(ids, f.ID)
	}
	_, e := s.taskAttachments(c, a, t.ProjectID, ids)
	return e
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
	if artifacts, ok := out["artifacts"].([]domain.Artifact); ok {
		visible := []domain.Artifact{}
		for _, v := range artifacts {
			if VisibleArtifact(a, p, v) {
				visible = append(visible, v)
			}
		}
		out["artifacts"] = visible
	}
	if !CanAccessAnyPage(a, p, "tasks", "reviews", "versions", "timeline") {
		out["events"] = []domain.Event{}
	}
	if !CanAccessAnyPage(a, p, "code", "git", "timeline") {
		if events, ok := out["events"].([]domain.Event); ok {
			visible := []domain.Event{}
			for _, ev := range events {
				if ev.Kind != "git_sync" {
					visible = append(visible, ev)
				}
			}
			out["events"] = visible
		}
	}
	out["project"] = ProjectForActor(a, p)
	return out
}

type RoleInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Pages       []string `json:"pages"`
	Actions     []string `json:"actions"`
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
	actions := in.Actions
	if actions == nil {
		if index < 0 {
			actions = domain.LegacyProjectActions(pages)
		} else {
			actions = slices.Clone(domain.ProjectRoleActions(p.ProjectRoles[index]))
		}
	}
	checked := []string{}
	for _, action := range actions {
		if !slices.Contains(domain.ProjectActions, action) || slices.Contains(checked, action) {
			return p, errors.New("操作权限无效或重复")
		}
		checked = append(checked, action)
	}
	if index < 0 {
		p.ProjectRoles = append(p.ProjectRoles, domain.ProjectRole{ID: domain.ID("role_"), Name: name, Description: in.Description, Pages: pages, Actions: checked, Version: 1})
	} else {
		r := p.ProjectRoles[index]
		r.Name = name
		r.Description = in.Description
		r.Pages = pages
		r.Actions = checked
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
	case "accept", "reject", "test_pass", "verify_bug", "return_bug", "start_retest":
		return []string{"reviews"}
	case "mark_deployed":
		return []string{"reviews", "versions"}
	default:
		return []string{"tasks"}
	}
}
func (s *Service) RequireTaskAction(c context.Context, a domain.Actor, t domain.Task, action string) error {
	if t.ParentRequirementID != "" && (action == "close" || action == "decline" || action == "reopen") {
		return s.RequirePages(c, a, t.ProjectID, "tasks", "reviews")
	}
	return s.RequirePages(c, a, t.ProjectID, TaskActionPages(action)...)
}

// ArtifactAccess describes the existing page ownership of evidence lists.
// Overview shows project analyses; historical task/delivery evidence belongs to
// reviews. Raw Git observations additionally require a code-related page.
func ArtifactAccess(a domain.Actor, p domain.Project) (string, bool) {
	mode := "none"
	if CanAccessPage(a, p, "reviews") {
		mode = "all"
	} else if CanAccessPage(a, p, "overview") {
		mode = "overview"
	}
	return mode, CanAccessAnyPage(a, p, "code", "git", "timeline")
}
func VisibleArtifact(a domain.Actor, p domain.Project, v domain.Artifact) bool {
	if v.OriginUnavailable {
		return false
	}
	if v.OriginOwnerID != "" && !CanReadRun(a, p, domain.Run{ProjectID: p.ID, OwnerID: v.OriginOwnerID, OwnerKind: v.OriginOwnerKind, Kind: v.OriginRunKind}) {
		return false
	}
	mode, git := ArtifactAccess(a, p)
	if v.Kind == "git" && !git {
		return false
	}
	if mode == "all" {
		return true
	}
	return mode == "overview" && v.TaskID == "" && (v.Kind == "analysis" || v.Kind == "knowledge" || v.Kind == "question")
}
