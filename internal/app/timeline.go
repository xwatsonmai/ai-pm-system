package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"errors"
)

type TimelineQuery struct {
	From      int64  `json:"from"`
	Until     int64  `json:"until"`
	Offset    int    `json:"offset"`
	Limit     int    `json:"limit"`
	GitOffset int    `json:"git_offset"`
	GitAnchor string `json:"git_anchor"`
}
type TimelineEvent struct {
	Seq              int64  `json:"seq"`
	TaskID           string `json:"task_id"`
	ProjectVersionID string `json:"project_version_id"`
	Kind             string `json:"kind"`
	Actor            string `json:"actor"`
	Body             string `json:"body"`
	At               int64  `json:"at"`
}
type TimelinePage struct {
	Events     []TimelineEvent `json:"events"`
	Total      int             `json:"total"`
	HasMore    bool            `json:"has_more"`
	NextOffset int             `json:"next_offset"`
}

// TimelineTask exposes recorded lifecycle times independently of the event page.
// UpdatedAt is deliberately not used as a completion time.
type TimelineTask struct {
	RequirementSummary
	CreatedAt   int64 `json:"created_at"`
	SubmittedAt int64 `json:"submitted_at,omitempty"`
	DeployedAt  int64 `json:"deployed_at,omitempty"`
	EndedAt     int64 `json:"ended_at,omitempty"`
}

func timelineTask(t domain.Task) TimelineTask {
	out := TimelineTask{RequirementSummary: requirementSummary(t), CreatedAt: t.CreatedAt}
	if t.Submission != nil && t.Submission.Revision == t.Revision {
		out.SubmittedAt = t.Submission.At
	}
	if t.Deployment != nil && t.Deployment.Revision == t.Revision {
		out.DeployedAt = t.Deployment.At
	}
	if t.Status == "resolved" && t.TestVerification != nil && t.TestVerification.Revision == t.Revision {
		out.EndedAt = t.TestVerification.At
	}
	if t.Status == "done" && t.Review != nil && t.Review.Accepted && t.Review.Revision == t.Revision {
		out.EndedAt = t.Review.At
	} else if domain.TaskTerminated(t.Status) && t.Resolution != nil {
		out.EndedAt = t.Resolution.At
	}
	return out
}

type ProjectTimeline struct {
	From        int64                   `json:"from"`
	Until       int64                   `json:"until"`
	Versions    []ProjectVersionSummary `json:"versions"`
	Tasks       []TimelineTask          `json:"tasks"`
	Activity    TimelinePage            `json:"activity"`
	Git         domain.GitTimeline      `json:"git"`
	CommitTasks map[string][]string     `json:"-"`
}

func NormalizeTimeline(q *TimelineQuery) error {
	if q.Until == 0 {
		q.Until = domain.Now() + 1
	}
	if q.From == 0 {
		q.From = q.Until - 30*86400000
	}
	if q.Limit == 0 {
		q.Limit = 100
	}
	if q.From < 0 || q.Until <= q.From || q.Until-q.From > 93*86400000 || q.Offset < 0 || q.GitOffset < 0 || q.Limit < 1 || q.Limit > 200 {
		return errors.New("请选择最多90天的时间范围，每页1–200项")
	}
	return nil
}
func (s *Service) Timeline(c context.Context, a domain.Actor, p string, q TimelineQuery) (ProjectTimeline, error) {
	out := ProjectTimeline{Git: domain.GitTimeline{Commits: []domain.GitCommit{}}}
	if e := s.CheckProject(c, a, p, false); e != nil {
		return out, e
	}
	if e := NormalizeTimeline(&q); e != nil {
		return out, e
	}
	out.From, out.Until = q.From, q.Until

	tasks, e := s.Store.ListTasks(c, p)
	if e != nil {
		return out, e
	}
	versions, e := s.Store.ListProjectVersions(c, p)
	if e != nil {
		return out, e
	}
	out.Tasks = []TimelineTask{}
	out.Versions = []ProjectVersionSummary{}
	out.CommitTasks = map[string][]string{}
	link := func(t domain.Task) {
		if t.Submission != nil {
			sha := t.Submission.SHA
			for _, id := range out.CommitTasks[sha] {
				if id == t.ID {
					return
				}
			}
			out.CommitTasks[sha] = append(out.CommitTasks[sha], t.ID)
		}
	}
	for _, task := range tasks {
		out.Tasks = append(out.Tasks, timelineTask(task))
		link(task)
	}
	for _, v := range versions {
		out.Versions = append(out.Versions, projectVersionSummary(v))
		for _, r := range v.Releases {
			for _, t := range r.Tasks {
				link(t)
			}
		}
	}

	rows, total, e := s.Store.ProjectTimelineEvents(c, p, q.From, q.Until, q.Offset, q.Limit)
	if e != nil {
		return out, e
	}
	events := []TimelineEvent{}
	for _, row := range rows {
		var data struct {
			ProjectVersionID string `json:"project_version_id"`
		}
		_ = json.Unmarshal(row.Data, &data)
		body := []rune(row.Body)
		if len(body) > 300 {
			body = append(body[:300], '…')
		}
		events = append(events, TimelineEvent{Seq: row.Seq, TaskID: row.TaskID, ProjectVersionID: data.ProjectVersionID, Kind: row.Kind, Actor: row.Actor, Body: string(body), At: row.At})
	}
	out.Activity = TimelinePage{Events: events, Total: total, NextOffset: q.Offset + len(rows), HasMore: q.Offset+len(rows) < total}
	return out, nil
}
func LinkTimelineCommits(out *ProjectTimeline) {
	for i := range out.Git.Commits {
		if ids := out.CommitTasks[out.Git.Commits[i].SHA]; len(ids) > 0 {
			out.Git.Commits[i].TaskIDs = ids
		}
	}
}
