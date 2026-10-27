package agent

import (
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// RunTimeouts is configured before starting the manager's workers. Model timers
// apply to each request; tool execution and SDK preparation only consume Total.
type RunTimeouts struct {
	FirstResponse time.Duration
	StreamIdle    time.Duration
	Total         time.Duration
}

func DefaultRunTimeouts() RunTimeouts {
	return RunTimeouts{FirstResponse: 3 * time.Minute, StreamIdle: 3 * time.Minute, Total: 30 * time.Minute}
}

func (t RunTimeouts) Validate() error {
	if t.FirstResponse <= 0 || t.StreamIdle <= 0 || t.Total <= 0 {
		return errors.New("Agent 首次响应、流式无响应和总时限必须大于 0")
	}
	return nil
}

type runTimeoutError struct {
	phase string
	limit time.Duration
}

func (e *runTimeoutError) Error() string {
	limit := e.limit.String()
	if e.limit%time.Minute == 0 {
		limit = fmt.Sprintf("%d 分钟", e.limit/time.Minute)
	}
	switch e.phase {
	case "first_response":
		return "模型首次响应超时（" + limit + "内未收到内容），已有内容已保存"
	case "stream_idle":
		return "模型流式响应超时（连续 " + limit + "无新增内容），已有内容已保存"
	default:
		return "分析达到总时限（" + limit + "），已有内容已保存"
	}
}

func (m *Manager) runContext() (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(m.ctx, m.Timeouts.Total, &runTimeoutError{phase: "total", limit: m.Timeouts.Total})
}

func applyRunCancellation(ctx context.Context, r *domain.Run) {
	r.Status = "stopped"
	var timeout *runTimeoutError
	if errors.As(context.Cause(ctx), &timeout) {
		r.Status, r.Error = "failed", timeout.Error()
	}
}

type modelDeadline struct {
	timer      *time.Timer
	generation uint64
}

// The provider emits request boundaries and meaningful content activity, never
// synthetic heartbeats. A generation check prevents an expired timer racing with
// new content from canceling a refreshed request.
type modelWatchdog struct {
	ctx      context.Context
	cancel   context.CancelCauseFunc
	limits   RunTimeouts
	mu       sync.Mutex
	requests map[string]*modelDeadline
}

func newModelWatchdog(parent context.Context, limits RunTimeouts) *modelWatchdog {
	ctx, cancel := context.WithCancelCause(parent)
	return &modelWatchdog{ctx: ctx, cancel: cancel, limits: limits, requests: map[string]*modelDeadline{}}
}

func (w *modelWatchdog) arm(id, phase string, limit time.Duration) {
	r := w.requests[id]
	if r.timer != nil {
		r.timer.Stop()
	}
	r.generation++
	generation := r.generation
	r.timer = time.AfterFunc(limit, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.ctx.Err() == nil && w.requests[id] == r && r.generation == generation {
			w.cancel(&runTimeoutError{phase: phase, limit: limit})
		}
	})
}

func (w *modelWatchdog) observe(kind, id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ctx.Err() != nil || id == "" {
		return
	}
	switch kind {
	case "model_start":
		if w.requests[id] == nil {
			w.requests[id] = &modelDeadline{}
			w.arm(id, "first_response", w.limits.FirstResponse)
		}
	case "model_activity":
		if w.requests[id] != nil {
			w.arm(id, "stream_idle", w.limits.StreamIdle)
		}
	case "model_end":
		if r := w.requests[id]; r != nil {
			r.timer.Stop()
			delete(w.requests, id)
		}
	}
}

func (w *modelWatchdog) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cancel(nil)
	for id, r := range w.requests {
		r.timer.Stop()
		delete(w.requests, id)
	}
}
