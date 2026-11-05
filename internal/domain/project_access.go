package domain

import "slices"

// ProjectPages are stable capabilities. Labels and navigation groups may change
// without changing grants. Permission is intentionally at page granularity.
var ProjectPages = []string{"overview", "tasks", "reviews", "versions", "timeline", "chat", "code", "settings", "git", "access"}
var ProjectResponsibilities = []string{"product", "developer", "tester"}

const (
	ActionAskAgent            = "agent.ask"
	ActionRunAnalysis         = "analysis.run"
	ActionCreateRequirement   = "requirement.create"
	ActionCreateBug           = "bug.create"
	ActionCreateLinkedBug     = "bug.create.linked"
	ActionCreateCollaboration = "collaboration.create"
)

var ProjectActions = []string{ActionAskAgent, ActionRunAnalysis, ActionCreateRequirement, ActionCreateBug, ActionCreateLinkedBug, ActionCreateCollaboration}

type ProjectRole struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Pages       []string `json:"pages"`
	Actions     []string `json:"actions"`
	Version     int      `json:"version"`
}

// LegacyProjectActions preserves the actual pre-action operations. Reviews
// alone could file linked Bugs, never independent Bugs, including with chat.
func LegacyProjectActions(pages []string) []string {
	out := []string{}
	for _, action := range ProjectActions {
		grant := false
		switch action {
		case ActionAskAgent, ActionCreateCollaboration:
			grant = slices.Contains(pages, "chat")
		case ActionRunAnalysis, ActionCreateRequirement, ActionCreateBug:
			grant = slices.Contains(pages, "tasks")
		case ActionCreateLinkedBug:
			grant = !slices.Contains(pages, "tasks") && slices.Contains(pages, "reviews")
		}
		if grant {
			out = append(out, action)
		}
	}
	return out
}

func ProjectRoleActions(role ProjectRole) []string {
	if role.Actions == nil {
		return LegacyProjectActions(role.Pages)
	}
	return role.Actions
}

func DefaultProjectRoles() []ProjectRole {
	out := []ProjectRole{}
	for _, v := range []struct{ id, name string }{{"product", "产品"}, {"developer", "开发"}, {"tester", "测试"}, {"admin", "管理员"}} {
		pages := slices.Clone(ProjectPages)
		if v.id != "admin" {
			pages = pages[:len(pages)-1]
		}
		out = append(out, ProjectRole{ID: v.id, Name: v.name, Pages: pages, Actions: LegacyProjectActions(pages), Version: 1})
	}
	return out
}

// NormalizeProjectAccess upgrades the old membership representation once. All
// old members retain their business AND settings/git capabilities; notification
// responsibilities stop depending on role names after this migration.
func NormalizeProjectAccess(p Project) Project {
	if p.AccessVersion != 0 {
		return p
	}
	p.ProjectRoles = DefaultProjectRoles()
	p.Memberships = slices.Clone(ProjectMembers(p))
	for i := range p.Memberships {
		if p.Memberships[i].Responsibilities == nil {
			p.Memberships[i].Responsibilities = []string{}
			for _, duty := range ProjectResponsibilities {
				if p.Memberships[i].Has(duty) {
					p.Memberships[i].Responsibilities = append(p.Memberships[i].Responsibilities, duty)
				}
			}
		}
	}
	p.AccessVersion = 1
	return p
}

func (m ProjectMember) ResponsibleFor(duty string) bool {
	if m.Responsibilities == nil {
		return m.Has(duty)
	} // pre-migration values only
	return slices.Contains(m.Responsibilities, duty)
}

func MemberHasProjectPage(p Project, user string, pages ...string) bool {
	p = NormalizeProjectAccess(p)
	for _, m := range p.Memberships {
		if m.UserID != user {
			continue
		}
		for _, role := range p.ProjectRoles {
			if !slices.Contains(m.Roles, role.ID) {
				continue
			}
			for _, page := range pages {
				if slices.Contains(role.Pages, page) {
					return true
				}
			}
		}
	}
	return false
}
