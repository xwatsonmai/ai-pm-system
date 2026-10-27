package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"net/http"
)

func (s *Server) collaborationRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/projects/{id}/collaborations", s.projectAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, err := s.App.ListCollaborations(r.Context(), s.actor(r), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, v)
	})))
	mux.Handle("POST /api/projects/{id}/collaborations", s.projectAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in app.CollaborationInput
		if err := read(r, &in); err != nil {
			fail(w, err)
			return
		}
		origin, err := s.App.Store.GetRun(r.Context(), in.OriginRunID)
		if err != nil {
			fail(w, err)
			return
		}
		if origin.ProjectID != r.PathValue("id") {
			fail(w, domain.ErrForbidden)
			return
		}
		v, err := s.App.CreateCollaboration(r.Context(), s.actor(r), in)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, v)
	})))
	mux.HandleFunc("GET /api/collaborations/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.App.GetCollaboration(r.Context(), s.actor(r), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, v)
	})
	mux.HandleFunc("POST /api/collaborations/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RequestID string `json:"request_id"`
		}
		if err := read(r, &in); err != nil {
			fail(w, err)
			return
		}
		v, err := s.Agent.CancelCollaboration(r.Context(), s.actor(r), r.PathValue("id"), in.RequestID)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, v)
	})
	mux.HandleFunc("POST /api/collaborations/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Agent.RetryCollaboration(r.Context(), s.actor(r), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, v)
	})
}
