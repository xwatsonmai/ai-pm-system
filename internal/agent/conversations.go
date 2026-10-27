package agent

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

// EditConversation shares the admission lock with StartAs so a deletion cannot
// race a new turn into an otherwise deleted Pi session. Metadata is independent
// of runs, so late model/title updates cannot undo a rename or deletion.
func (m *Manager) EditConversation(c context.Context, a domain.Actor, projectID, id, title string, remove bool) (domain.Conversation, error) {
	title = strings.TrimSpace(title)
	if !remove && (title == "" || utf8.RuneCountInString(title) > 80) {
		return domain.Conversation{}, errors.New("对话名称需要 1 到 80 个字")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	project, err := m.App.Store.GetProject(c, projectID)
	if err != nil {
		return domain.Conversation{}, err
	}
	if !app.CanRead(a, project) {
		return domain.Conversation{}, domain.ErrForbidden
	}
	runs, err := m.App.Store.ListRuns(c, projectID)
	if err != nil {
		return domain.Conversation{}, err
	}
	var turns []domain.Run
	for _, r := range runs {
		conversationID := r.ConversationID
		if conversationID == "" {
			conversationID = r.ID
		}
		if r.Kind != "question" || conversationID != id {
			continue
		}
		if !app.CanReadRun(a, project, r) {
			return domain.Conversation{}, domain.ErrForbidden
		}
		turns = append(turns, r)
	}
	if len(turns) == 0 {
		return domain.Conversation{}, domain.ErrNotFound
	}
	v, err := m.App.Store.GetConversation(c, id)
	if errors.Is(err, domain.ErrNotFound) {
		v = domain.Conversation{ID: id, ProjectID: projectID}
	} else if err != nil {
		return v, err
	}
	if remove {
		v.DeletedAt = domain.Now()
	} else {
		v.Title = title
	}
	if err = m.App.Store.PutConversation(c, v); err != nil {
		return v, err
	}
	if remove {
		m.App.NotifyWork(projectID)
		for _, r := range turns {
			if active, ok := m.active[r.ID]; ok {
				active.stop()
			}
		}
	}
	return v, nil
}
