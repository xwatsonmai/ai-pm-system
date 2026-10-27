package agent

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
)

type collaborationQuery struct {
	Collaborations []domain.CollaborationView `json:"collaborations"`
	AsOf           int64                      `json:"as_of"`
}

// Use the persisted user turn as authority, never the platform actor or model
// arguments. Recheck deletion and project membership on every tool call.
func (m *Manager) collaborationScope(ctx context.Context, r domain.Run) (domain.Actor, string, error) {
	if r.Kind != "question" || r.TriggerType == "collaboration_result" || r.OwnerID == "" || (r.OwnerKind != "human" && r.OwnerKind != "developer") {
		return domain.Actor{}, "", domain.ErrForbidden
	}
	current, err := m.App.Store.GetRun(ctx, r.ID)
	if err != nil {
		return domain.Actor{}, "", err
	}
	if current.Kind != r.Kind || current.TriggerType != r.TriggerType || current.ProjectID != r.ProjectID || current.ConversationID != r.ConversationID || current.OwnerID != r.OwnerID || current.OwnerKind != r.OwnerKind {
		return domain.Actor{}, "", domain.ErrForbidden
	}
	p, err := m.App.Store.GetProject(ctx, r.ProjectID)
	if err != nil {
		return domain.Actor{}, "", err
	}
	a, err := m.App.CollaborationOwner(ctx, r.OwnerID, r.OwnerKind, r.ProjectID)
	if err != nil {
		return a, "", err
	}
	if !app.CanReadRun(a, p, current) {
		return a, "", domain.ErrForbidden
	}
	conversation := r.ConversationID
	if conversation == "" {
		conversation = r.ID
	}
	return a, conversation, nil
}

func inCollaborationScope(v domain.Collaboration, a domain.Actor, conversation string) bool {
	return v.ProjectID == a.ProjectID && v.OwnerID == a.ID && v.OwnerKind == a.Kind && v.ConversationID == conversation
}

func (m *Manager) getConversationCollaborations(ctx context.Context, r domain.Run, id string) (collaborationQuery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := collaborationQuery{Collaborations: []domain.CollaborationView{}}
	a, conversation, err := m.collaborationScope(ctx, r)
	if err != nil {
		return out, err
	}
	var items []domain.Collaboration
	if id != "" {
		v, err := m.App.Store.GetCollaboration(ctx, id)
		if err != nil {
			return out, err
		}
		if !inCollaborationScope(v, a, conversation) {
			return out, domain.ErrForbidden
		}
		items = []domain.Collaboration{v}
	} else {
		items, err = m.App.Store.ListCollaborations(ctx, a.ProjectID)
		if err != nil {
			return out, err
		}
	}
	for _, v := range items {
		if !inCollaborationScope(v, a, conversation) {
			continue
		}
		view, err := m.App.GetCollaboration(ctx, a, v.ID)
		if err != nil {
			return out, err
		}
		out.Collaborations = append(out.Collaborations, view)
	}
	out.AsOf = domain.Now()
	return out, nil
}

func (m *Manager) cancelConversationCollaboration(ctx context.Context, r domain.Run, id string) (domain.CollaborationMutation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, conversation, err := m.collaborationScope(ctx, r)
	if err != nil {
		return domain.CollaborationMutation{}, err
	}
	if id == "" {
		return domain.CollaborationMutation{}, errors.New("请先查询并明确要取消的协同 ID")
	}
	v, err := m.App.Store.GetCollaboration(ctx, id)
	if err != nil {
		return domain.CollaborationMutation{}, err
	}
	if !inCollaborationScope(v, a, conversation) {
		return domain.CollaborationMutation{}, domain.ErrForbidden
	}
	if v.Status == "canceled" {
		return domain.CollaborationMutation{Collaboration: v}, nil
	}
	return m.cancelCollaboration(ctx, a, id, "cancel_collaboration:"+r.ID+":"+id)
}
