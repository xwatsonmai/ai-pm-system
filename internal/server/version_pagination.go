package server

import (
	"ai-pm-system/internal/domain"
	"errors"
	"net/http"
	"strconv"
)

func versionPageQuery(r *http.Request) (domain.PageQuery, error) {
	q := domain.PageQuery{Query: r.URL.Query().Get("q")}
	for key, dest := range map[string]*int{"page": &q.Page, "page_size": &q.PageSize} {
		if raw := r.URL.Query().Get(key); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 {
				return q, errors.New("分页参数必须为正整数")
			}
			*dest = n
		}
	}
	return q.Normalized()
}
func (s *Server) versionListRoutes(handle func(string, http.HandlerFunc)) {
	handle("GET /api/projects/{id}/version-options", func(w http.ResponseWriter, r *http.Request) {
		q, e := versionPageQuery(r)
		if e != nil {
			fail(w, e)
			return
		}
		v, e := s.App.ProjectVersionChoices(r.Context(), s.actor(r), r.PathValue("id"), domain.VersionListQuery{PageQuery: q, Status: r.URL.Query().Get("status")})
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("GET /api/projects/{id}/version-options/{version}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.App.ProjectVersionChoice(r.Context(), s.actor(r), r.PathValue("id"), r.PathValue("version"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("GET /api/project-versions/{id}/history", func(w http.ResponseWriter, r *http.Request) {
		q, e := versionPageQuery(r)
		if e != nil {
			fail(w, e)
			return
		}
		v, e := s.App.ProjectVersionHistoryPage(r.Context(), s.actor(r), r.PathValue("id"), q)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("GET /api/project-versions/{id}/scope", func(w http.ResponseWriter, r *http.Request) {
		q, e := versionPageQuery(r)
		if e != nil {
			fail(w, e)
			return
		}
		v, e := s.App.ProjectVersionScopePage(r.Context(), s.actor(r), r.PathValue("id"), domain.VersionScopeQuery{PageQuery: q, Status: r.URL.Query().Get("status"), IncludeRemoved: r.URL.Query().Get("include_removed") == "true"})
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("GET /api/project-versions/{id}/releases", func(w http.ResponseWriter, r *http.Request) {
		q, e := versionPageQuery(r)
		if e != nil {
			fail(w, e)
			return
		}
		v, e := s.App.ProjectVersionReleasesPage(r.Context(), s.actor(r), r.PathValue("id"), q)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("GET /api/project-versions/{id}/releases/{release}/tasks", func(w http.ResponseWriter, r *http.Request) {
		q, e := versionPageQuery(r)
		if e != nil {
			fail(w, e)
			return
		}
		v, e := s.App.ProjectVersionReleaseTasksPage(r.Context(), s.actor(r), r.PathValue("id"), r.PathValue("release"), q)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
}
