package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/notify"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

func (s *Server) projectRoleRoutes(mux *http.ServeMux) {
	handle := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.projectAccess(h)) }
	handle("GET /api/projects/{id}/access", func(w http.ResponseWriter, r *http.Request) {
		p, e := s.App.Store.GetProject(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		p = domain.NormalizeProjectAccess(p)
		a := s.actor(r)
		pages := []string{}
		for _, page := range domain.ProjectPages {
			if app.CanAccessPage(a, p, page) {
				pages = append(pages, page)
			}
		}
		roles := []domain.ProjectRole{}
		duties := []string{}
		mine := []string{}
		for _, m := range p.Memberships {
			if m.UserID == a.ID {
				mine = m.Roles
				duties = m.Responsibilities
			}
		}
		for _, role := range p.ProjectRoles {
			if app.CanManage(a, p) || slices.Contains(mine, role.ID) {
				roles = append(roles, role)
			}
		}
		respond(w, map[string]any{"pages": pages, "roles": roles, "responsibilities": duties, "version": p.AccessVersion, "can_manage": app.CanManage(a, p), "recovery": a.PlatformAdmin && !app.CanAccessPage(a, p, "access")})
	})
	respondRoles := func(w http.ResponseWriter, p domain.Project) {
		respond(w, map[string]any{"items": p.ProjectRoles, "version": p.AccessVersion})
	}
	handle("GET /api/projects/{id}/roles", func(w http.ResponseWriter, r *http.Request) {
		p, e := s.App.Store.GetProject(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		q := r.URL.Query()
		page, size := 1, 20
		for key, target := range map[string]*int{"page": &page, "page_size": &size} {
			if raw := q.Get(key); raw != "" {
				n, e := strconv.Atoi(raw)
				if e != nil || n < 1 || (key == "page_size" && n > 100) {
					fail(w, errInvalidPage)
					return
				}
				*target = n
			}
		}
		items, total, e := s.App.Store.ListProjectRolesPage(r.Context(), p.ID, page, size, strings.TrimSpace(q.Get("q")))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, map[string]any{"items": items, "total": total, "page": page, "page_size": size, "version": p.AccessVersion})
	})
	save := func(w http.ResponseWriter, r *http.Request) {
		var in app.RoleInput
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		p, e := s.App.SaveProjectRole(r.Context(), s.actor(r), r.PathValue("id"), r.PathValue("role"), in)
		if e != nil {
			fail(w, e)
			return
		}
		respondRoles(w, p)
	}
	handle("POST /api/projects/{id}/roles", save)
	handle("PUT /api/projects/{id}/roles/{role}", save)
	handle("DELETE /api/projects/{id}/roles/{role}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Version     int    `json:"version"`
			Replacement string `json:"replacement_role_id"`
		}
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		p, e := s.App.DeleteProjectRole(r.Context(), s.actor(r), r.PathValue("id"), r.PathValue("role"), in.Replacement, in.Version)
		if e != nil {
			fail(w, e)
			return
		}
		respondRoles(w, p)
	})
	handle("PATCH /api/projects/{id}/settings", s.patchProjectSettings)
	handle("PATCH /api/projects/{id}/git", s.patchProjectGit)
}

var errInvalidPage = &pageParameterError{}

type pageParameterError struct{}

func (*pageParameterError) Error() string { return "page须为正整数，page_size为1–100" }

func (s *Server) patchProjectSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Version     int     `json:"version"`
	}
	if e := read(r, &in); e != nil {
		fail(w, e)
		return
	}
	p, e := s.App.Store.GetProject(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, e)
		return
	}
	if in.Version != 0 && in.Version != p.AccessVersion {
		fail(w, domain.ErrConflict)
		return
	}
	if in.Name != nil {
		p.Name = *in.Name
	}
	if in.Description != nil {
		p.Description = *in.Description
	}
	p, e = s.App.SaveProjectAs(r.Context(), s.actor(r), p)
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, s.repositoryProjectFor(r, p))
}
func (s *Server) patchProjectGit(w http.ResponseWriter, r *http.Request) {
	// Decode only Git-owned fields. Build the legacy DTO from current stored data
	// so this page cannot blank unrelated project settings.
	var fields map[string]json.RawMessage
	if e := read(r, &fields); e != nil {
		fail(w, e)
		return
	}
	allowed := []string{"repository", "branch", "agent_read_mode", "agent_ref", "repository_auth", "generated_credential_id", "version"}
	for key := range fields {
		if !slices.Contains(allowed, key) {
			fail(w, domain.ErrForbidden)
			return
		}
	}
	p, e := s.App.Store.GetProject(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, e)
		return
	}
	if raw := fields["version"]; raw != nil {
		var version int
		if e = json.Unmarshal(raw, &version); e != nil {
			fail(w, e)
			return
		}
		if version != 0 && version != p.AccessVersion {
			fail(w, domain.ErrConflict)
			return
		}
	}
	raw, _ := json.Marshal(p)
	merged := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &merged)
	for k, v := range fields {
		if k != "version" {
			merged[k] = v
		}
	}
	raw, _ = json.Marshal(merged)
	r.Body = http.NoBody
	r.Body = readCloser{bytes.NewReader(raw)}
	s.saveProject(w, r)
}

type readCloser struct{ *bytes.Reader }

func (readCloser) Close() error { return nil }
func (s *Server) repositoryProjectFor(r *http.Request, p domain.Project) repositoryProject {
	if full, e := s.App.Store.GetProject(r.Context(), p.ID); e == nil {
		p = full
	}
	v := s.repositoryProject(r.Context(), p)
	v.Project = app.ProjectForActor(s.actor(r), v.Project)
	return v
}

func (s *Server) authorizeProjectRoute(r *http.Request, id string, manage bool, path []string) error {
	if manage {
		return nil
	} // management is checked by CheckProject, including recovery
	var pages []string
	switch path[1] {
	case "project-versions":
		pages = []string{"versions"}
	case "tasks":
		pages = []string{"tasks", "reviews", "versions"}
		if r.Method != "GET" {
			pages = []string{"tasks"}
			if len(path) > 3 && path[3] == "actions" {
				pages = []string{"tasks", "reviews", "versions"}
			}
		}
	case "requirement-intakes":
		pages = []string{"tasks"}
	case "runs":
		return nil // CanReadRun checks page + private conversation ownership
	case "projects":
		if len(path) < 4 {
			return nil
		}
		switch path[3] {
		case "access", "summary", "context":
			return nil // aggregate outputs are filtered
		case "settings":
			pages = []string{"settings"}
		case "git", "repository-auth":
			pages = []string{"git"}
		case "repository-status":
			pages = []string{"code", "git", "timeline", "chat", "tasks", "overview", "reviews", "versions"}
		case "sync":
			pages = []string{"code", "git"}
		case "code":
			pages = []string{"code"}
		case "tasks":
			pages = []string{"tasks", "reviews", "versions"}
			if r.Method != "GET" {
				pages = []string{"tasks"}
			}
		case "requirement-intakes":
			pages = []string{"tasks"}
		case "versions", "version-assignments":
			pages = []string{"versions"}
		case "milestone":
			pages = []string{"tasks", "reviews", "versions", "overview"}
		case "timeline":
			pages = []string{"timeline"}
		case "runs", "conversations", "collaborations":
			pages = []string{"chat"}
			if path[3] == "runs" && r.Method == "GET" {
				pages = []string{"chat", "tasks", "reviews", "versions"}
			}
		case "artifacts":
			pages = []string{"reviews"}
		case "events":
			pages = []string{"timeline", "tasks", "reviews", "versions"}
		case "attachments":
			pages = []string{"tasks", "reviews", "versions", "chat"}
		default:
			return domain.ErrForbidden
		}
	}
	if len(pages) == 0 {
		return nil
	}
	return s.App.RequirePages(r.Context(), s.actor(r), id, pages...)
}

func (s *Server) authorizeMCPPage(c context.Context, a domain.Actor, name string) error {
	pages := []string{"tasks"}
	switch {
	case name == "project_get_context":
		return nil // filtered by app.Context
	case name == "work_list":
		pages = []string{"tasks", "chat"}
	case strings.HasPrefix(name, "project_version_") || name == "project_milestone_get":
		pages = []string{"versions"}
	case name == "project_timeline":
		pages = []string{"timeline"}
	case name == "task_mark_deployed" || name == "delivery_record":
		pages = []string{"reviews", "versions"}
	case strings.HasPrefix(name, "collaboration_") || name == "project_ask" || name == "analysis_get":
		pages = []string{"chat"}
	case name == "task_get_context" || name == "task_get_updates" || name == "task_list":
		pages = []string{"tasks", "reviews", "versions"}
	}
	return s.App.RequirePages(c, a, a.ProjectID, pages...)
}

func (s *Server) canReadTodo(c context.Context, a domain.Actor, t notify.Todo) bool {
	pages := []string{"tasks"}
	switch t.Kind {
	case "acceptance":
		pages = []string{"reviews"}
	case "release":
		pages = []string{"reviews", "versions"}
	}
	return s.App.RequirePages(c, a, t.ProjectID, pages...) == nil
}
