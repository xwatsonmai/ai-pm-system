package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"sort"
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

// A catalog change means that potentially conflicting scope must be checked
// again. Development progress alone must not invalidate a product review.
func (s *Service) ReviewCatalogFingerprint(c context.Context, project, exclude string) (string, error) {
	tasks, err := s.Store.ListTasks(c, project)
	if err != nil {
		return "", err
	}
	type scope struct {
		ID                 string
		Revision           int
		Title, Description string
		Acceptance         []string
		Ended              bool
	}
	items := []scope{}
	for _, t := range tasks {
		if t.ID != exclude {
			items = append(items, scope{t.ID, t.Revision, t.Title, t.Description, t.Acceptance, domain.TaskTerminated(t.Status)})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	b, _ := json.Marshal(items)
	return Hash(string(b)), nil
}

func (s *Service) ReviewRecheckReason(c context.Context, v domain.RequirementReview) (string, error) {
	_, conflict, err := s.ReviewIncomingRelations(c, v)
	if err != nil {
		return "", err
	}
	if conflict {
		return "其他需求的后续评审发现了与本需求的业务冲突，请复核后再确认。", nil
	}
	if v.Evidence == nil {
		return "", nil // Legacy assessments remain readable and confirmable.
	}
	fp, err := s.ReviewCatalogFingerprint(c, v.ProjectID, v.TaskID)
	if err != nil {
		return "", err
	}
	if fp != v.Evidence.CatalogFingerprint {
		return "项目中有需求新增、范围修改或结束，请重新核查需求之间的交叉与冲突。", nil
	}
	for _, ref := range v.Evidence.References {
		t, err := s.Store.GetTask(c, ref.TaskID)
		if err != nil {
			return "", err
		}
		if t.ProjectID != v.ProjectID || t.Revision != ref.Revision {
			return "评审参考的需求已修改，请重新评审。", nil
		}
		r, err := s.Store.GetRequirementReview(c, ref.TaskID)
		if err != nil && err != domain.ErrNotFound {
			return "", err
		}
		if r.Version != ref.ReviewVersion {
			return "评审参考的其他需求已有新的评审或用户补充，请重新核对。", nil
		}
	}
	return "", nil
}

// Reverse links are derived from current assessments, so superseded findings
// disappear without rewriting another person's requirement or review history.
func (s *Service) ReviewIncomingRelations(c context.Context, v domain.RequirementReview) ([]domain.RequirementRelation, bool, error) {
	reviews, err := s.Store.ListRequirementReviews(c)
	if err != nil {
		return nil, false, err
	}
	items := []domain.RequirementRelation{}
	conflict := false
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
				conflict = conflict || (relation.Kind == "conflict" && m.At > v.UpdatedAt)
			}
			break
		}
	}
	return items, conflict, nil
}
