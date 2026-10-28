package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"strconv"
	"strings"
)

// ReviewSource keeps product scope and accepted iteration history, without
// copying development receipts and unrelated progress reports into model input.
func ReviewSource(t domain.Task) domain.Task {
	t.Submission, t.Deployment = nil, nil
	if t.Status != "done" {
		t.Review = nil
	}
	t.ClaimHash, t.Claimant, t.ClaimClientID, t.Branch = "", "", "", ""
	t.Alignment = nil
	if t.Creator != nil {
		creator := *t.Creator
		creator.ID = "" // people are addressed by name, not account ID
		t.Creator = &creator
	}
	if t.Iteration != nil {
		i := *t.Iteration
		i.Source = ReviewSource(i.Source)
		if i.Baseline != nil {
			b := ReviewSource(*i.Baseline)
			i.Baseline = &b
		}
		t.Iteration = &i
	}
	return t
}

// Reviews run one at a time per project, and each review checks itself against
// the requirements before it. An earlier review therefore stays fixed when
// requirements are added or changed later; it is only rechecked when a
// requirement it actually read changes or ends, or a later review reports a
// conflict with it.
func (s *Service) ReviewRecheckReason(c context.Context, v domain.RequirementReview) (string, error) {
	_, conflicts, err := s.ReviewIncomingRelations(c, v)
	if err != nil {
		return "", err
	}
	if len(conflicts) > 0 {
		titles := []string{}
		for _, r := range conflicts {
			titles = append(titles, r.Title)
		}
		return "需求" + quoteTitles(titles) + "的评审发现与本需求存在业务冲突，请复核后再确认。", nil
	}
	if v.Evidence == nil {
		return "", nil // Legacy assessments remain readable and confirmable.
	}
	var edited, ended []string
	for _, ref := range v.Evidence.References {
		t, err := s.Store.GetTask(c, ref.TaskID)
		if err != nil {
			return "", err
		}
		switch {
		case t.ProjectID != v.ProjectID || t.Revision != ref.Revision:
			edited = append(edited, t.Title)
		case domain.TaskTerminated(t.Status) && !ref.Ended:
			ended = append(ended, t.Title)
		}
	}
	changes := []string{}
	if len(edited) > 0 {
		changes = append(changes, quoteTitles(edited)+"有修改")
	}
	if len(ended) > 0 {
		changes = append(changes, quoteTitles(ended)+"已结束")
	}
	if len(changes) == 0 {
		return "", nil
	}
	return "评审参考的需求" + strings.Join(changes, "、") + "，确认前需要重新核对。", nil
}

func quoteTitles(titles []string) string {
	shown := titles
	if len(shown) > 3 {
		shown = shown[:3]
	}
	out := "「" + strings.Join(shown, "」「") + "」"
	if len(titles) > len(shown) {
		out += "等 " + strconv.Itoa(len(titles)) + " 条"
	}
	return out
}

// Reverse links are derived from current assessments, so superseded findings
// disappear without rewriting another person's requirement or review history.
// Conflicts found after this review last changed are returned separately.
func (s *Service) ReviewIncomingRelations(c context.Context, v domain.RequirementReview) ([]domain.RequirementRelation, []domain.RequirementRelation, error) {
	reviews, err := s.Store.ListRequirementReviews(c)
	if err != nil {
		return nil, nil, err
	}
	items := []domain.RequirementRelation{}
	conflicts := []domain.RequirementRelation{}
	for _, other := range reviews {
		if other.TaskID == v.TaskID || other.ProjectID != v.ProjectID || other.Status == "closed" {
			continue
		}
		for i := len(other.Messages) - 1; i >= 0; i-- {
			m := other.Messages[i]
			if m.Result == nil || m.Generation != other.Generation {
				continue
			}
			for _, relation := range m.Result.Relations {
				if relation.TaskID != v.TaskID {
					continue
				}
				relation.TaskID, relation.Title = other.TaskID, other.Source.Title
				items = append(items, relation)
				if relation.Kind == "conflict" && m.At > v.UpdatedAt {
					conflicts = append(conflicts, relation)
				}
			}
			break
		}
	}
	return items, conflicts, nil
}
