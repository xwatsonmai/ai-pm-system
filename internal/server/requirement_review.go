package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"net/http"
)

func (s *Server) getRequirementReview(w http.ResponseWriter, r *http.Request) {
	v, err := s.App.RequirementReview(r.Context(), s.actor(r), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	s.reviewMentionWarnings(r, &v)
	response, e := reviewMessagePage(r, v)
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, response)
}
func (s *Server) saveRequirementReview(w http.ResponseWriter, r *http.Request) {
	var in app.RequirementReviewInput
	if err := read(r, &in); err != nil {
		fail(w, err)
		return
	}
	v, err := s.App.SaveRequirementReview(r.Context(), s.actor(r), r.PathValue("id"), in)
	if err != nil {
		fail(w, err)
		return
	}
	// Return durable input independently of model availability. Admission failures are
	// persisted as a failed review and can be retried without resending the message.
	if v.Status == "queued" {
		if s.Agent != nil {
			s.Agent.KickRequirementReview(v.TaskID)
		} else {
			_ = s.App.FinishRequirementReview(context.Background(), v.TaskID, v.Generation, "", nil, "评审服务暂不可用，消息已保存，请稍后重试。")
		}
	}
	latest, err := s.App.RequirementReview(r.Context(), s.actor(r), v.TaskID)
	if err == nil {
		v = latest
	}
	s.reviewMentionWarnings(r, &v)
	response, e := reviewMessagePage(r, v)
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, response)
}

func (s *Server) reviewMentionWarnings(r *http.Request, v *domain.RequirementReview) {
	for i := range v.Messages {
		m := &v.Messages[i]
		if m.ActorID == s.actor(r).ID {
			m.MentionWarnings = s.Notify.MentionWarnings(r.Context(), *m)
		}
	}
}
