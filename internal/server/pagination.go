package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

func pageQuery(r *http.Request) (domain.PageQuery, bool, error) {
	values := r.URL.Query()
	explicit := values.Has("page") || values.Has("page_size")
	q := domain.PageQuery{Query: values.Get("q")}
	var e error
	if values.Has("page") {
		q.Page, e = strconv.Atoi(values.Get("page"))
		if e != nil || q.Page < 1 {
			return q, explicit, errors.New("无效的 page")
		}
	}
	if values.Has("page_size") {
		q.PageSize, e = strconv.Atoi(values.Get("page_size"))
		if e != nil || q.PageSize < 1 {
			return q, explicit, errors.New("无效的 page_size")
		}
	}
	q, e = q.Normalized()
	return q, explicit, e
}
func (s *Server) queryStore() (storage.PagedQueries, error) {
	store := s.App.Store
	for {
		wrapper, ok := store.(interface{ UnderlyingStore() storage.Store })
		if !ok {
			break
		}
		store = wrapper.UnderlyingStore()
	}
	v, ok := store.(storage.PagedQueries)
	if !ok {
		return nil, errors.New("存储适配器未实现分页查询")
	}
	return v, nil
}

// pagedList preserves old response shapes unless pagination was requested.
func (s *Server) pagedList(w http.ResponseWriter, r *http.Request, resource string) bool {
	q, explicit, e := pageQuery(r)
	if !explicit {
		return false
	}
	if e != nil {
		fail(w, e)
		return true
	}
	store, e := s.queryStore()
	if e != nil {
		fail(w, e)
		return true
	}
	project := r.PathValue("id")
	a := s.actor(r)
	if resource == "tasks" {
		tasks, e := store.QueryTasks(r.Context(), project, domain.TaskListQuery{PageQuery: q, Status: r.URL.Query().Get("status"), Kind: r.URL.Query().Get("kind"), ProjectVersionID: r.URL.Query().Get("project_version_id"), ParentRequirementID: r.URL.Query().Get("parent_requirement_id")})
		if e != nil {
			fail(w, e)
			return true
		}
		ids := []string{}
		options := []app.VersionChoice{}
		seenVersions := map[string]bool{}
		for _, t := range tasks.Items {
			ids = append(ids, t.ID)
			if t.ProjectVersionID == "" || seenVersions[t.ProjectVersionID] {
				continue
			}
			seenVersions[t.ProjectVersionID] = true
			choice, err := s.App.ProjectVersionChoice(r.Context(), a, project, t.ProjectVersionID)
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			if err != nil {
				fail(w, err)
				return true
			}
			options = append(options, choice)
		}
		reviews, e := store.QueryReviewSummaries(r.Context(), ids)
		if e != nil {
			fail(w, e)
			return true
		}
		respond(w, map[string]any{"items": tasks.Items, "total": tasks.Total, "page": tasks.Page, "page_size": tasks.PageSize, "requirement_reviews": reviews, "version_options": options})
		return true
	}
	if resource == "members" {
		items, e := store.QueryMembers(r.Context(), project, q)
		if e != nil {
			fail(w, e)
			return true
		}
		p, e := s.App.Store.GetProject(r.Context(), project)
		if e != nil {
			fail(w, e)
			return true
		}
		respond(w, map[string]any{"items": items.Items, "total": items.Total, "page": items.Page, "page_size": items.PageSize, "can_manage": app.CanManage(a, p), "version": p.AccessVersion})
		return true
	}
	if resource == "artifacts" && r.URL.Query().Get("delivery") == "true" {
		resource = "delivery-evidence"
	}
	query := domain.ListDocumentQuery{PageQuery: q, Resource: resource, ProjectID: project, UserID: a.ID, ActorKind: a.Kind, TaskID: r.URL.Query().Get("task_id"), ConversationID: r.URL.Query().Get("conversation_id"), Summary: r.URL.Query().Get("summary") == "true", Active: r.URL.Query().Get("active") == "true"}
	if resource == "artifacts" || resource == "delivery-evidence" || resource == "events" {
		p, e := s.App.Store.GetProject(r.Context(), project)
		if e != nil {
			fail(w, e)
			return true
		}
		mode, git := app.ArtifactAccess(a, p)
		query.ArtifactMode = mode
		query.ExcludeGit = !git
		query.ExcludeGitEvents = !git
		if resource == "delivery-evidence" && !app.CanAccessPage(a, p, "reviews") {
			fail(w, domain.ErrForbidden)
			return true
		}
	}
	if resource == "runs" || resource == "conversations" {
		p, e := s.App.Store.GetProject(r.Context(), project)
		if e != nil {
			fail(w, e)
			return true
		}
		query.LegacyOwner = a.Kind == "human" && (p.OwnerID == a.ID || (p.OwnerID == "" && a.ID == "local-owner"))
		query.ExcludeQuestions = !app.CanAccessPage(a, p, "chat")
		query.OnlyQuestions = !app.CanAccessAnyPage(a, p, "tasks", "reviews", "versions")
	}
	out, e := store.QueryDocuments(r.Context(), query)
	if e != nil {
		fail(w, e)
		return true
	}
	if resource == "projects" || resource == "platform-projects" {
		for i, v := range out.Items {
			var p domain.Project
			if e = json.Unmarshal(v, &p); e != nil {
				fail(w, e)
				return true
			}
			out.Items[i], e = json.Marshal(s.repositoryProjectFor(r, p))
			if e != nil {
				fail(w, e)
				return true
			}
		}
	}
	respond(w, out)
	return true
}
func (s *Server) paginationRoutes(handle func(string, http.HandlerFunc)) {
	for _, resource := range []string{"conversations", "artifacts", "events"} {
		handle("GET /api/projects/{id}/"+resource, func(w http.ResponseWriter, r *http.Request) {
			if !r.URL.Query().Has("page") && !r.URL.Query().Has("page_size") {
				values := r.URL.Query()
				values.Set("page", "1")
				r.URL.RawQuery = values.Encode()
			}
			s.pagedList(w, r, resource)
		})
	}
	handle("GET /api/projects/{id}/summary", s.projectSummary)
}
func (s *Server) projectSummary(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("id")
	if e := s.App.CheckProject(r.Context(), s.actor(r), project, false); e != nil {
		fail(w, e)
		return
	}
	p, e := s.App.Store.GetProject(r.Context(), project)
	if e != nil {
		fail(w, e)
		return
	}
	store, e := s.queryStore()
	if e != nil {
		fail(w, e)
		return
	}
	a := s.actor(r)
	stats := domain.TaskListStats{ByStatus: map[string]int{}}
	if app.CanAccessAnyPage(a, p, "overview", "tasks", "reviews", "versions") {
		stats, e = store.TaskListStats(r.Context(), project)
		if e != nil {
			fail(w, e)
			return
		}
	}
	choices := []app.VersionChoice{}
	var milestone any
	if app.CanAccessAnyPage(a, p, "overview", "tasks", "reviews", "versions") {
		options, err := s.App.ProjectVersionChoices(r.Context(), a, project, domain.VersionListQuery{PageQuery: domain.PageQuery{Page: 1, PageSize: 20}, Status: "active"})
		if err != nil {
			fail(w, err)
			return
		}
		choices = options.Items
		current, err := s.App.Store.GetCurrentMilestone(r.Context(), project)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			fail(w, err)
			return
		}
		if current.VersionID != "" {
			choice, err := s.App.ProjectVersionChoice(r.Context(), a, project, current.VersionID)
			if err != nil {
				fail(w, err)
				return
			}
			milestone = choice
		}
	}
	respond(w, map[string]any{"project": s.repositoryProjectFor(r, p), "stats": stats, "tasks": []domain.Task{}, "artifacts": []domain.Artifact{}, "events": []domain.Event{}, "requirement_reviews": []domain.RequirementReviewSummary{}, "project_versions": choices, "current_milestone": milestone})
}

// Compact detail is opt-in for the web UI. The legacy context endpoint remains
// available to existing desktop and MCP clients.
func (s *Server) compactTaskContext(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Query().Get("compact") != "true" {
		return false
	}
	t, e := s.App.Store.GetTask(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, e)
		return true
	}
	a := s.actor(r)
	if e = s.App.CheckProject(r.Context(), a, t.ProjectID, false); e != nil {
		fail(w, e)
		return true
	}
	store, e := s.queryStore()
	if e != nil {
		fail(w, e)
		return true
	}
	related, e := store.QueryTasks(r.Context(), t.ProjectID, domain.TaskListQuery{ParentRequirementID: t.ID})
	if e != nil {
		fail(w, e)
		return true
	}
	opened, e := store.QueryTasks(r.Context(), t.ProjectID, domain.TaskListQuery{ParentRequirementID: t.ID, Status: "active", PageQuery: domain.PageQuery{Page: 1, PageSize: 1}})
	if e != nil {
		fail(w, e)
		return true
	}
	project, e := s.App.Store.GetProject(r.Context(), t.ProjectID)
	if e != nil {
		fail(w, e)
		return true
	}
	mode, git := app.ArtifactAccess(a, project)
	artifacts, e := store.QueryDocuments(r.Context(), domain.ListDocumentQuery{Resource: "artifacts", ProjectID: t.ProjectID, TaskID: t.ID, ArtifactMode: mode, ExcludeGit: !git})
	if e != nil {
		fail(w, e)
		return true
	}
	events, e := store.QueryDocuments(r.Context(), domain.ListDocumentQuery{Resource: "events", ProjectID: t.ProjectID, TaskID: t.ID, ExcludeGitEvents: !git})
	if e != nil {
		fail(w, e)
		return true
	}
	history, e := s.App.RequirementIterations(r.Context(), a, t.ID, 0, 10)
	if e != nil {
		fail(w, e)
		return true
	}
	tasks := append([]domain.Task{t}, related.Items...)
	if t.ParentRequirementID != "" {
		if parent, e := s.App.Store.GetTask(r.Context(), t.ParentRequirementID); e == nil && parent.ProjectID == t.ProjectID {
			tasks = append(tasks, domain.Task{ID: parent.ID, Title: parent.Title})
		}
	}
	ids := []string{}
	for _, v := range tasks {
		ids = append(ids, v.ID)
	}
	reviews, e := store.QueryReviewSummaries(r.Context(), ids)
	if e != nil {
		fail(w, e)
		return true
	}
	p, e := s.App.Store.GetProject(r.Context(), t.ProjectID)
	if e != nil {
		fail(w, e)
		return true
	}
	if !app.CanAccessPage(a, p, "reviews") {
		artifacts = domain.Page[json.RawMessage]{Items: []json.RawMessage{}, Page: 1, PageSize: 20}
	}
	respond(w, map[string]any{"task": t, "tasks": tasks, "artifacts": artifacts.Items, "events": events.Items, "requirement_reviews": reviews, "iteration_history": history, "related_bugs": map[string]any{"items": related.Items, "total": related.Total, "page": related.Page, "page_size": related.PageSize, "open_total": opened.Total}, "artifact_page": artifacts, "event_page": events})
	return true
}
