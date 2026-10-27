package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"net/http"
	"strings"
)

// Wrap the matched mux handler so PathValue is populated. All object routes
// authorize using their stored project, never a client-supplied project ID.
func (s *Server) projectAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(path) < 3 || path[0] != "api" {
			next.ServeHTTP(w, r)
			return
		}
		var id string
		var err error
		manage := false
		switch path[1] {
		case "projects":
			id = r.PathValue("id")
			manage = len(path) > 3 && (path[3] == "tokens" || (path[3] == "members" && (r.Method != "GET" || platformAdmin(s.actor(r)))) || path[3] == "directory")
		case "tasks":
			var t domain.Task
			t, err = s.App.Store.GetTask(r.Context(), r.PathValue("id"))
			id = t.ProjectID
		case "project-versions":
			var v domain.ProjectVersion
			v, err = s.App.Store.GetProjectVersion(r.Context(), r.PathValue("id"))
			id = v.ProjectID
		case "runs":
			var run domain.Run
			run, err = s.App.Store.GetRun(r.Context(), r.PathValue("id"))
			id = run.ProjectID
			if err == nil {
				var p domain.Project
				p, err = s.App.Store.GetProject(r.Context(), id)
				if err == nil && !app.CanReadRun(s.actor(r), p, run) {
					err = domain.ErrForbidden
				}
			}
		}
		if err != nil {
			fail(w, err)
			return
		}
		if id != "" {
			if err = s.App.CheckProject(r.Context(), s.actor(r), id, manage); err != nil {
				fail(w, err)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) visibleProjects(r *http.Request) ([]domain.Project, error) {
	all, e := s.App.Store.ListProjects(r.Context())
	if e != nil {
		return nil, e
	}
	out := []domain.Project{}
	for _, p := range all {
		if app.CanRead(s.actor(r), p) {
			out = append(out, p)
		}
	}
	return out, nil
}
