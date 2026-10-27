package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type runStreamUpdate struct {
	Run           domain.Run `json:"run"`
	Append        bool       `json:"append"`
	ProcessAppend bool       `json:"process_append"`
}

func (s *Server) streamRun(w http.ResponseWriter, r *http.Request) {
	if s.Agent == nil {
		http.Error(w, "运行推送暂不可用", http.StatusServiceUnavailable)
		return
	}
	updates, unwatch := s.Agent.WatchRun(r.PathValue("id"))
	defer unwatch()
	read := func() (domain.Run, error) {
		actor := s.actor(r)
		if s.Auth != nil {
			user, err := s.Auth.User(r)
			if err != nil {
				return domain.Run{}, domain.ErrForbidden
			}
			actor, err = s.userActor(r.Context(), user)
			if err != nil {
				return domain.Run{}, err
			}
		}
		run, err := s.Agent.ReadLiveRun(r.Context(), r.PathValue("id"))
		if err != nil {
			return run, err
		}
		project, err := s.App.Store.GetProject(r.Context(), run.ProjectID)
		if err == nil && !app.CanReadRun(actor, project, run) {
			err = domain.ErrForbidden
		}
		if err == nil {
			err = s.App.CheckProject(r.Context(), actor, run.ProjectID, false)
		}
		return run, err
	}
	current, err := read()
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	write := func(event string, payload []byte) error {
		// Bound only the write, not the idle interval between events. Leaving a
		// deadline armed can reset an idle HTTP/2 stream before its heartbeat.
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		defer controller.SetWriteDeadline(time.Time{})
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
			return err
		}
		return controller.Flush()
	}
	var previous, previousProcess, previousJSON string
	sent := false
	send := func(run domain.Run) bool {
		full, _ := json.Marshal(run)
		if string(full) != previousJSON {
			update := runStreamUpdate{Run: run}
			if sent && strings.HasPrefix(run.Output, previous) {
				update.Append = true
				update.Run.Output = strings.TrimPrefix(run.Output, previous)
			}
			if sent && strings.HasPrefix(run.ProcessOutput, previousProcess) {
				update.ProcessAppend = true
				update.Run.ProcessOutput = strings.TrimPrefix(run.ProcessOutput, previousProcess)
			}
			payload, _ := json.Marshal(update)
			if write("run", payload) != nil {
				return false
			}
			previous, previousJSON, sent = run.Output, string(full), true
			previousProcess = run.ProcessOutput
		}
		return run.Status == "running" || run.Status == "queued"
	}
	if !send(current) {
		return
	}
	// Coalesce token bursts into at most 25 updates/sec without delaying the
	// first snapshot. Recheck persisted metadata/access even during tool silence.
	frame := time.NewTicker(40 * time.Millisecond)
	defer frame.Stop()
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	dirty := false
	for {
		select {
		case <-r.Context().Done():
			return
		case <-updates:
			dirty = true
		case <-frame.C:
			if !dirty {
				continue
			}
			dirty = false
			current, err = read()
			if err != nil || !send(current) {
				return
			}
		case <-heartbeat.C:
			current, err = read()
			if err != nil || !send(current) || write("ping", []byte("{}")) != nil {
				return
			}
		}
	}
}
