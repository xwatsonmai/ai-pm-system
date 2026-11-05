package agent

import (
	"ai-pm-system/internal/domain"
	"context"
	"strings"
	"time"
)

// A question's platform worker borrows the persisted owner's current page
// capabilities. The model cannot turn a chat grant into access to other pages.
func (m *Manager) questionReadActor(c context.Context, r domain.Run) (domain.Actor, error) {
	id, kind := r.OwnerID, r.OwnerKind
	if id == "" {
		p, e := m.App.Store.GetProject(c, r.ProjectID)
		if e != nil {
			return domain.Actor{}, e
		}
		id = p.OwnerID
		if id == "" {
			id = "local-owner"
		}
		kind = "human"
	}
	return m.App.CollaborationOwner(c, id, kind, r.ProjectID)
}

func userOwnedRun(r domain.Run) bool { return r.Kind == "question" || r.OwnerID != "" }

func (m *Manager) runReadActor(c context.Context, r domain.Run) (domain.Actor, error) {
	if r.Kind == "question" {
		return m.questionReadActor(c, r)
	}
	a, e := m.App.ProjectOwnerActor(c, r.OwnerID, r.OwnerKind, r.ProjectID)
	if e != nil {
		return a, e
	}
	return a, m.App.RequirePages(c, a, r.ProjectID, "tasks")
}

func (m *Manager) requireRunAction(c context.Context, a domain.Actor, r domain.Run) error {
	action := domain.ActionRunAnalysis
	if r.Kind == "question" {
		action = domain.ActionAskAgent
	}
	return m.App.RequireAction(c, a, r.ProjectID, action)
}

func (m *Manager) authorizeRunTool(c context.Context, a domain.Actor, r domain.Run, name string) error {
	var pages []string
	switch {
	case name == "requirement_pool" || name == "requirement_detail" || name == "requirement_iterations":
		pages = []string{"tasks", "reviews", "versions"}
	case strings.HasPrefix(name, "project_version_") || name == "project_milestone_get":
		pages = []string{"versions"}
	case name == "project_timeline":
		pages = []string{"timeline"}
	case strings.HasPrefix(name, "repository_") || name == "read" || name == "grep" || name == "find" || name == "ls":
		pages = []string{"code"}
	case name == "save_analysis":
		pages = []string{"overview", "chat"}
	case name == "create_bug":
		return m.App.RequireAction(c, a, r.ProjectID, domain.ActionCreateBug)
	case name == "create_collaboration":
		return m.App.RequireAction(c, a, r.ProjectID, domain.ActionCreateCollaboration)
	}
	if len(pages) == 0 {
		return nil
	}
	return m.App.RequirePages(c, a, r.ProjectID, pages...)
}

// Native sandbox tools execute in the worker. Stop that worker promptly when
// its owner loses chat/source access; ordinary RPC tools recheck on every call.
func (m *Manager) guardRunPermissions(c context.Context, cancel context.CancelFunc, r domain.Run, source bool) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.Done():
			return
		case <-ticker.C:
			a, e := m.runReadActor(c, r)
			if e == nil {
				e = m.requireRunAction(c, a, r)
			}
			if e != nil {
				cancel()
				return
			}
			if source {
				if e = m.App.RequirePages(c, a, r.ProjectID, "code"); e != nil {
					cancel()
					return
				}
			}
		}
	}
}
