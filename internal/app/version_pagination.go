package app

import (
	"ai-pm-system/internal/domain"
	"context"
	"errors"
)

func (s *Service) ProjectVersionsPage(c context.Context, a domain.Actor, p string, q domain.VersionListQuery) (domain.Page[ProjectVersionSummary], error) {
	out := domain.Page[ProjectVersionSummary]{Items: []ProjectVersionSummary{}}
	if e := s.CheckProject(c, a, p, false); e != nil {
		return out, e
	}
	if q.Status != "" && q.Status != "active" && q.Status != "planned" && q.Status != "completed" && q.Status != "closed" {
		return out, errors.New("未知版本状态")
	}
	rows, e := s.Store.ListProjectVersionsPage(c, p, q)
	if e != nil {
		return out, e
	}
	state, e := s.Store.GetCurrentMilestone(c, p)
	if e != nil {
		return out, e
	}
	out.Total, out.Page, out.PageSize = rows.Total, rows.Page, rows.PageSize
	for _, v := range rows.Items {
		summary := projectVersionSummary(v)
		summary.IsDefault = v.ID == state.VersionID
		summary.Current = summary.IsDefault
		summary.DefaultVersion = state.Version
		out.Items = append(out.Items, summary)
	}
	return out, nil
}
func (s *Service) ProjectVersionListing(c context.Context, a domain.Actor, id string) (ProjectVersionDetail, error) {
	v, e := s.Store.GetProjectVersionListing(c, id)
	if e != nil {
		return ProjectVersionDetail{}, e
	}
	if e = s.CheckProject(c, a, v.ProjectID, false); e != nil {
		return ProjectVersionDetail{}, e
	}
	state, e := s.Store.GetCurrentMilestone(c, v.ProjectID)
	if e != nil {
		return ProjectVersionDetail{}, e
	}
	summary := projectVersionSummary(v)
	summary.IsDefault = v.ID == state.VersionID
	summary.Current = summary.IsDefault
	summary.DefaultVersion = state.Version
	return ProjectVersionDetail{v, summary}, nil
}
func (s *Service) ProjectVersionScopePage(c context.Context, a domain.Actor, id string, q domain.VersionScopeQuery) (domain.Page[domain.VersionScopeItem], error) {
	if _, e := s.ProjectVersionListing(c, a, id); e != nil {
		return domain.Page[domain.VersionScopeItem]{}, e
	}
	return s.Store.ProjectVersionScopePage(c, id, q)
}
func (s *Service) ProjectVersionReleasesPage(c context.Context, a domain.Actor, id string, q domain.PageQuery) (domain.Page[domain.VersionRelease], error) {
	if _, e := s.ProjectVersionListing(c, a, id); e != nil {
		return domain.Page[domain.VersionRelease]{}, e
	}
	return s.Store.ProjectVersionReleasesPage(c, id, q)
}
func (s *Service) ProjectVersionReleaseTasksPage(c context.Context, a domain.Actor, id, release string, q domain.PageQuery) (domain.Page[domain.Task], error) {
	if _, e := s.ProjectVersionListing(c, a, id); e != nil {
		return domain.Page[domain.Task]{}, e
	}
	return s.Store.ProjectVersionReleaseTasksPage(c, id, release, q)
}

func (s *Service) ProjectVersionHistoryPage(c context.Context, a domain.Actor, id string, q domain.PageQuery) (domain.Page[domain.VersionHistoryEntry], error) {
	if _, e := s.ProjectVersionListing(c, a, id); e != nil {
		return domain.Page[domain.VersionHistoryEntry]{}, e
	}
	return s.Store.ProjectVersionHistoryPage(c, id, q)
}

// VersionChoice is safe for requirement forms without access to the version
// management page: no goals, scope, release snapshots or progress counts.
type VersionChoice struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	Version        int    `json:"version"`
	IsDefault      bool   `json:"is_default"`
	DefaultVersion int    `json:"default_version"`
}

func versionChoice(v ProjectVersionSummary) VersionChoice {
	return VersionChoice{v.ID, v.Name, v.Status, v.Version, v.IsDefault, v.DefaultVersion}
}
func (s *Service) ProjectVersionChoices(c context.Context, a domain.Actor, p string, q domain.VersionListQuery) (domain.Page[VersionChoice], error) {
	rows, e := s.ProjectVersionsPage(c, a, p, q)
	if e != nil {
		return domain.Page[VersionChoice]{}, e
	}
	out := domain.Page[VersionChoice]{Items: []VersionChoice{}, Total: rows.Total, Page: rows.Page, PageSize: rows.PageSize}
	for _, v := range rows.Items {
		out.Items = append(out.Items, versionChoice(v))
	}
	return out, nil
}
func (s *Service) ProjectVersionChoice(c context.Context, a domain.Actor, p, id string) (VersionChoice, error) {
	v, e := s.ProjectVersionListing(c, a, id)
	if e != nil {
		return VersionChoice{}, e
	}
	if v.ProjectVersion.ProjectID != p {
		return VersionChoice{}, domain.ErrForbidden
	}
	return versionChoice(v.Summary), nil
}
