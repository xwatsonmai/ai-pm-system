package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"net/http"
	"strconv"
)

func (s *Server) timeline(c context.Context, a domain.Actor, p string, q app.TimelineQuery) (app.ProjectTimeline, error) {
	if e := app.NormalizeTimeline(&q); e != nil {
		return app.ProjectTimeline{}, e
	}
	v, e := s.App.Timeline(c, a, p, q)
	if e != nil {
		return v, e
	}
	if s.Git != nil {
		v.Git, e = s.Git.Timeline(c, p, q.From, q.Until, q.GitOffset, q.Limit, q.GitAnchor)
		if e != nil {
			v.Git.Warning = "提交暂时读取失败，请重试；这不代表没有提交"
		}
	} else {
		v.Git.Warning = "代码仓库服务暂不可用"
	}
	app.LinkTimelineCommits(&v)
	return v, nil
}
func (s *Server) projectTimeline(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	q := app.TimelineQuery{GitAnchor: values.Get("git_anchor")}
	var e error
	for key, dest := range map[string]*int64{"from": &q.From, "until": &q.Until} {
		if raw := values.Get(key); raw != "" {
			*dest, e = strconv.ParseInt(raw, 10, 64)
			if e != nil {
				fail(w, e)
				return
			}
		}
	}
	for key, dest := range map[string]*int{"offset": &q.Offset, "git_offset": &q.GitOffset, "limit": &q.Limit} {
		if raw := values.Get(key); raw != "" {
			*dest, e = strconv.Atoi(raw)
			if e != nil {
				fail(w, e)
				return
			}
		}
	}
	v, e := s.timeline(r.Context(), s.actor(r), r.PathValue("id"), q)
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, v)
}
