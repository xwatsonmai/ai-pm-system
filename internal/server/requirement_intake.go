package server

import (
	"ai-pm-system/internal/app"
	"context"
	"net/http"
)

func (s *Server) listRequirementIntakes(w http.ResponseWriter, r *http.Request) {
	v, err := s.App.RequirementIntakes(r.Context(), s.actor(r), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, v)
}

func (s *Server) resolveRequirementIntake(w http.ResponseWriter, r *http.Request) {
	var in app.ResolveRequirementIntakeInput
	if err := read(r, &in); err != nil {
		fail(w, err)
		return
	}
	v, err := s.App.ResolveRequirementIntake(r.Context(), s.actor(r), r.PathValue("id"), in)
	if err != nil {
		fail(w, err)
		return
	}
	if v.Status == "created" && v.Kind != "bug" {
		if s.Agent != nil {
			s.Agent.ReviewCreatedRequirement(v.TaskID)
		} else {
			c := context.Background()
			review, e := s.App.SaveRequirementReview(c, s.actor(r), v.TaskID, app.RequirementReviewInput{Action: "start", RequestID: "intake-review:" + v.TaskID})
			if e == nil && review.Status == "queued" {
				_ = s.App.FinishRequirementReview(c, v.TaskID, review.Generation, "", nil, "评审服务暂不可用，需求与附件已保存，请稍后重试。")
			}
		}
	}
	// A successful creation always returns the durable task identity, even if
	// review admission failed or this request was a lost-response retry.
	respond(w, v)
}
