package server

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"context"
	"errors"
	"net/http"
	"time"
)

func (s *Server) lineageRoutes(handle func(string, http.HandlerFunc)) {
	handle("GET /api/projects/{id}/lineage/versions", func(w http.ResponseWriter, r *http.Request) {
		q, _, e := pageQuery(r)
		if e != nil {
			fail(w, e)
			return
		}
		adapter, err := s.lineageStore()
		if err != nil {
			fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		out, e := adapter.LineageVersions(ctx, r.PathValue("id"), q)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, out)
	})
	handle("GET /api/projects/{id}/lineage", func(w http.ResponseWriter, r *http.Request) {
		q, _, e := pageQuery(r)
		if e != nil {
			fail(w, e)
			return
		}
		version := r.URL.Query().Get("version_id")
		if version == "" {
			fail(w, errors.New("请选择项目版本"))
			return
		}
		adapter, err := s.lineageStore()
		if err != nil {
			fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		out, e := adapter.QueryLineage(ctx, r.PathValue("id"), version, domain.LineageQuery{PageQuery: q, ParentID: r.URL.Query().Get("parent_id")})
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, out)
	})
}

func (s *Server) lineageStore() (storage.LineageQueries, error) {
	store, e := s.queryStore()
	if e != nil {
		return nil, e
	}
	adapter, ok := store.(storage.LineageQueries)
	if !ok {
		return nil, errors.New("存储暂不支持版本谱系")
	}
	return adapter, nil
}
