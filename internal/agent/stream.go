package agent

import (
	"ai-pm-system/internal/domain"
	"context"
	"slices"
)

// WatchRun coalesces notifications; a slow browser never blocks model output.
// Subscribe before reading the snapshot so an update cannot fall into a gap.
func (m *Manager) WatchRun(id string) (<-chan struct{}, func()) {
	m.streamMu.Lock()
	defer m.streamMu.Unlock()
	if m.runWatchers == nil {
		m.runWatchers = make(map[string]map[chan struct{}]struct{})
	}
	if m.runWatchers[id] == nil {
		m.runWatchers[id] = make(map[chan struct{}]struct{})
	}
	ch := make(chan struct{}, 1)
	m.runWatchers[id][ch] = struct{}{}
	return ch, func() {
		m.streamMu.Lock()
		defer m.streamMu.Unlock()
		delete(m.runWatchers[id], ch)
		if len(m.runWatchers[id]) == 0 {
			delete(m.runWatchers, id)
		}
	}
}

func (m *Manager) publishRun(r domain.Run) {
	// The worker continues mutating its slices after this call.
	r.Steps = slices.Clone(r.Steps)
	r.Usage = slices.Clone(r.Usage)
	if r.CodeSnapshot != nil {
		copy := *r.CodeSnapshot
		copy.Submodules = slices.Clone(copy.Submodules)
		copy.Warnings = slices.Clone(copy.Warnings)
		r.CodeSnapshot = &copy
	}
	m.streamMu.Lock()
	defer m.streamMu.Unlock()
	if m.liveRuns == nil {
		m.liveRuns = make(map[string]domain.Run)
	}
	m.liveRuns[r.ID] = r
	for ch := range m.runWatchers[r.ID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (m *Manager) clearLiveRun(id string) {
	m.streamMu.Lock()
	defer m.streamMu.Unlock()
	delete(m.liveRuns, id)
}

// ReadLiveRun retains persisted deletion/manual-title rules while delivering
// text without waiting for the next SQLite checkpoint. Callers authorize it.
func (m *Manager) ReadLiveRun(ctx context.Context, id string) (domain.Run, error) {
	stored, err := m.App.Store.GetRun(ctx, id)
	if err != nil {
		return domain.Run{}, err
	}
	m.streamMu.Lock()
	live, ok := m.liveRuns[id]
	m.streamMu.Unlock()
	if ok && (stored.Status == "queued" || stored.Status == "running") {
		if stored.TitleStatus == "manual" {
			live.ConversationTitle, live.TitleStatus = stored.ConversationTitle, stored.TitleStatus
		}
		stored = live
	}
	if stored.TriggerType == "collaboration_result" {
		if _, err := m.replySource(ctx, stored.CollaborationID); err != nil {
			return domain.Run{}, err
		}
	}
	return stored, nil
}
