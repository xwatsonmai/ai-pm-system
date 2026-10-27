package server

import (
	"ai-pm-system/internal/app"
	"net/http"
	"strconv"
)

func (s *Server) requirementIterationRoutes(handle func(string, http.HandlerFunc)) {
	handle("POST /api/tasks/{id}/iterations", func(w http.ResponseWriter, r *http.Request) {
		var input app.RequirementIterationInput
		if e := read(r, &input); e != nil {
			fail(w, e)
			return
		}
		v, e := s.App.CreateRequirementIteration(r.Context(), s.actor(r), r.PathValue("id"), input)
		if e != nil {
			fail(w, e)
			return
		}
		s.queueProductReview(r.Context(), s.actor(r), v.Task.ID)
		respond(w, v)
	})
	handle("GET /api/tasks/{id}/iterations", func(w http.ResponseWriter, r *http.Request) {
		offset, limit := 0, 0
		for key, dest := range map[string]*int{"offset": &offset, "limit": &limit} {
			if raw := r.URL.Query().Get(key); raw != "" {
				n, e := strconv.Atoi(raw)
				if e != nil {
					fail(w, e)
					return
				}
				*dest = n
			}
		}
		v, e := s.App.RequirementIterations(r.Context(), s.actor(r), r.PathValue("id"), offset, limit)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
}
