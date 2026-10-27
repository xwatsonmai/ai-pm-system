package app

import (
	"ai-pm-system/internal/domain"
	"testing"
)

func TestTimelineLifecycleUsesRecordedCurrentRevisionTimes(t *testing.T) {
	base := domain.Task{ID: "lifecycle", Revision: 3, CreatedAt: 100, UpdatedAt: 9999,
		Submission: &domain.Submission{Revision: 3, At: 200},
		Deployment: &domain.Deployment{Revision: 3, At: 300},
		Review:     &domain.Review{Revision: 3, Accepted: true, At: 400},
		Resolution: &domain.TaskResolution{At: 500}}
	for _, tc := range []struct {
		status string
		ended  int64
	}{{"doing", 0}, {"pending_release", 0}, {"review", 0}, {"done", 400}, {"closed", 500}, {"rejected", 500}, {"canceled", 500}} {
		t.Run(tc.status, func(t *testing.T) {
			task := base
			task.Status = tc.status
			got := timelineTask(task)
			if got.CreatedAt != 100 || got.SubmittedAt != 200 || got.DeployedAt != 300 || got.EndedAt != tc.ended {
				t.Fatalf("lifecycle incorrectly derived from current status/update: %+v", got)
			}
		})
	}
	base.Status = "done"
	base.Revision++
	got := timelineTask(base)
	if got.SubmittedAt != 0 || got.DeployedAt != 0 || got.EndedAt != 0 {
		t.Fatalf("old revision was presented as current completion: %+v", got)
	}
	base.Review = nil
	got = timelineTask(base)
	if got.EndedAt != 0 {
		t.Fatal("missing completion was invented from UpdatedAt")
	}
}
