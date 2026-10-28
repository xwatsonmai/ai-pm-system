package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
	"sort"
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

type reviewScope struct {
	ID                 string
	Revision           int
	Title, Description string
	Acceptance         []string
	Ended              bool
}

func (s *Service) reviewScopes(c context.Context, project, exclude string) ([]reviewScope, error) {
	tasks, err := s.Store.ListTasks(c, project)
	if err != nil {
		return nil, err
	}
	items := []reviewScope{}
	for _, t := range tasks {
		if t.ID != exclude {
			items = append(items, reviewScope{t.ID, t.Revision, t.Title, t.Description, t.Acceptance, domain.TaskTerminated(t.Status)})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func reviewCatalog(items []reviewScope) (string, []domain.RequirementCatalogEntry) {
	entries := make([]domain.RequirementCatalogEntry, len(items))
	for i, v := range items {
		b, _ := json.Marshal(v)
		entries[i] = domain.RequirementCatalogEntry{TaskID: v.ID, Scope: Hash(string(b))[:16], Ended: v.Ended}
	}
	b, _ := json.Marshal(items)
	return Hash(string(b)), entries
}

// A catalog change means that potentially conflicting scope must be checked
// again. Development progress alone must not invalidate a product review.
func (s *Service) ReviewCatalog(c context.Context, project, exclude string) (string, []domain.RequirementCatalogEntry, error) {
	items, err := s.reviewScopes(c, project, exclude)
	if err != nil {
		return "", nil, err
	}
	fp, entries := reviewCatalog(items)
	return fp, entries, nil
}

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
	items, err := s.reviewScopes(c, v.ProjectID, v.TaskID)
	if err != nil {
		return "", err
	}
	if fp, entries := reviewCatalog(items); fp != v.Evidence.CatalogFingerprint {
		return catalogChangeReason(v.Evidence.Catalog, items, entries), nil
	}
	for _, ref := range v.Evidence.References {
		t, err := s.Store.GetTask(c, ref.TaskID)
		if err != nil {
			return "", err
		}
		if t.ProjectID != v.ProjectID || t.Revision != ref.Revision {
			return "评审参考的需求「" + t.Title + "」已修改，请重新核对。", nil
		}
		r, err := s.Store.GetRequirementReview(c, ref.TaskID)
		if err != nil && err != domain.ErrNotFound {
			return "", err
		}
		if r.Version != ref.ReviewVersion {
			return "评审参考的需求「" + t.Title + "」有新的评审或用户补充，请重新核对。", nil
		}
	}
	return "", nil
}

// Evidence from before per-requirement digests, or a requirement that left the
// project, can only be reported in general terms.
func catalogChangeReason(before []domain.RequirementCatalogEntry, items []reviewScope, entries []domain.RequirementCatalogEntry) string {
	const general = "项目中有需求新增、修改或结束，请重新核查需求之间的交叉与冲突。"
	if before == nil {
		return general
	}
	seen := map[string]domain.RequirementCatalogEntry{}
	for _, e := range before {
		seen[e.TaskID] = e
	}
	var added, edited, ended, reopened []string
	for i, v := range items {
		old, ok := seen[v.ID]
		switch {
		case !ok:
			added = append(added, v.Title)
		case old.Scope == entries[i].Scope: // unchanged
		case v.Ended && !old.Ended:
			ended = append(ended, v.Title)
		case !v.Ended && old.Ended:
			reopened = append(reopened, v.Title)
		default:
			edited = append(edited, v.Title)
		}
	}
	changes := []string{}
	if len(added) > 0 {
		changes = append(changes, "新增需求"+quoteTitles(added))
	}
	if len(edited) > 0 {
		changes = append(changes, "需求"+quoteTitles(edited)+"有修改")
	}
	if len(ended) > 0 {
		changes = append(changes, "需求"+quoteTitles(ended)+"已结束")
	}
	if len(reopened) > 0 {
		changes = append(changes, "需求"+quoteTitles(reopened)+"已重新打开")
	}
	if len(changes) == 0 {
		return general
	}
	return strings.Join(changes, "；") + "。确认前需要重新核查与本需求的交叉与冲突。"
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
