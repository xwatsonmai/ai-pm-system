package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestTimelinePagingPrivacyAndExactCommitRelations(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	task := create(t, s, p.ID)
	task, _, _ = taskAtStage(t, s, p.ID, "pending_release")
	published := publishVersion(t, s, task.ProjectVersionID, task).Tasks[0]
	from := domain.Now() - 86400000
	until := domain.Now() + 1
	q := app.TimelineQuery{From: from, Until: until, Limit: 1}
	view, e := s.Timeline(c, app.Human(), p.ID, q)
	if e != nil || len(view.Activity.Events) != 1 || !view.Activity.HasMore {
		t.Fatal(view, e)
	}
	q.Offset = view.Activity.NextOffset
	next, e := s.Timeline(c, app.Human(), p.ID, q)
	if e != nil || next.Activity.Events[0].Seq == view.Activity.Events[0].Seq {
		t.Fatal(next, e)
	}
	view.Git.Commits = []domain.GitCommit{{SHA: published.Submission.SHA}, {SHA: "similar-but-unrelated"}}
	app.LinkTimelineCommits(&view)
	if len(view.Git.Commits[0].TaskIDs) != 1 || len(view.Git.Commits[1].TaskIDs) != 0 {
		t.Fatal("guessed commit association")
	}
	b, _ := json.Marshal(view)
	var raw map[string]any
	json.Unmarshal(b, &raw)
	if _, exists := raw["CommitTasks"]; exists {
		t.Fatal("internal index exposed")
	}
	for _, item := range raw["tasks"].([]any) {
		if _, has := item.(map[string]any)["description"]; has {
			t.Fatal("timeline duplicated entire requirements")
		}
	}
	a := domain.Actor{ID: "other", Kind: "developer", ProjectID: "other"}
	if _, e = s.Timeline(c, a, p.ID, q); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("foreign timeline leaked", e)
	}
}
