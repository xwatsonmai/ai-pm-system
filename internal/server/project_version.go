package server

import (
	"ai-pm-system/internal/app"
	"net/http"
)

func (s *Server) projectVersionRoutes(handle func(string, http.HandlerFunc)) {
	s.requirementIterationRoutes(handle)
	handle("GET /api/projects/{id}/timeline", s.projectTimeline)
	handle("GET /api/projects/{id}/milestone", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.App.CurrentMilestone(r.Context(), s.actor(r), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("GET /api/projects/{id}/versions", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.App.ProjectVersions(r.Context(), s.actor(r), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("POST /api/projects/{id}/versions", func(w http.ResponseWriter, r *http.Request) {
		var in app.ProjectVersionInput
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		v, e := s.App.SaveProjectVersion(r.Context(), s.actor(r), r.PathValue("id"), "", in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("POST /api/projects/{id}/version-assignments", func(w http.ResponseWriter, r *http.Request) {
		var in app.VersionAssignmentInput
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		v, e := s.App.AssignProjectVersion(r.Context(), s.actor(r), r.PathValue("id"), in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("GET /api/project-versions/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.App.ProjectVersionContext(r.Context(), s.actor(r), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("PUT /api/project-versions/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in app.ProjectVersionInput
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		old, e := s.App.Store.GetProjectVersion(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		v, e := s.App.SaveProjectVersion(r.Context(), s.actor(r), old.ProjectID, old.ID, in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("POST /api/project-versions/{id}/actions", func(w http.ResponseWriter, r *http.Request) {
		var in app.ProjectVersionAction
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		v, e := s.App.ActProjectVersion(r.Context(), s.actor(r), r.PathValue("id"), in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
}
