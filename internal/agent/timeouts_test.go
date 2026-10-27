package agent

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/sandbox"
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func timeoutPhase(t *testing.T, ctx context.Context, want string) {
	t.Helper()
	synctest.Wait()
	var timeout *runTimeoutError
	if !errors.As(context.Cause(ctx), &timeout) || timeout.phase != want {
		t.Fatalf("wanted %s, got %v", want, context.Cause(ctx))
	}
}

func TestModelTimeouts(t *testing.T) {
	for _, scenario := range []string{"first", "idle", "streaming", "tool-and-next-request", "total", "manual-stop", "completed"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := &Manager{ctx: context.Background(), Timeouts: DefaultRunTimeouts()}
				ctx, cancel := m.runContext()
				defer cancel()
				w := newModelWatchdog(ctx, m.Timeouts)
				defer w.close()
				w.observe("model_start", "one")
				switch scenario {
				case "first":
					time.Sleep(2 * time.Minute)
					// Duplicate starts or unrelated events must not buy more time.
					w.observe("model_start", "one")
					w.observe("model_activity", "unknown")
					time.Sleep(time.Minute)
					timeoutPhase(t, w.ctx, "first_response")
				case "idle":
					time.Sleep(2 * time.Minute)
					w.observe("model_activity", "one")
					time.Sleep(2 * time.Minute)
					w.observe("model_activity", "one")
					time.Sleep(3 * time.Minute)
					timeoutPhase(t, w.ctx, "stream_idle")
				case "streaming", "total":
					// Exercise the production minute-scale limits using virtual time.
					for range 29 {
						time.Sleep(time.Minute)
						w.observe("model_activity", "one")
						if w.ctx.Err() != nil {
							t.Fatal("active output was cut off", context.Cause(w.ctx))
						}
					}
					if scenario == "total" {
						time.Sleep(time.Minute)
						timeoutPhase(t, w.ctx, "total")
					}
				case "tool-and-next-request":
					w.observe("model_activity", "one")
					w.observe("model_end", "one")
					time.Sleep(5 * time.Minute) // tools do not consume model idle time
					if w.ctx.Err() != nil {
						t.Fatal(context.Cause(w.ctx))
					}
					w.observe("model_start", "two")
					time.Sleep(3 * time.Minute)
					timeoutPhase(t, w.ctx, "first_response")
				case "manual-stop":
					cancel()
					time.Sleep(4 * time.Minute)
					r := domain.Run{}
					applyRunCancellation(w.ctx, &r)
					if r.Status != "stopped" || r.Error != "" {
						t.Fatal("manual stop became a timeout", r)
					}
				case "completed":
					w.observe("model_activity", "one")
					w.observe("model_end", "one")
					w.observe("model_activity", "one") // late activity cannot re-arm
					time.Sleep(4 * time.Minute)
					if w.ctx.Err() != nil {
						t.Fatal("finished request timed out", context.Cause(w.ctx))
					}
				}
			})
		})
	}
}

func TestRunTimeoutPolicyAndClassification(t *testing.T) {
	limits := DefaultRunTimeouts()
	if limits.FirstResponse != 3*time.Minute || limits.StreamIdle != 3*time.Minute || limits.Total != 30*time.Minute {
		t.Fatal(limits)
	}
	if sandbox.DefaultTimeout <= limits.Total {
		t.Fatal("sandbox runner can interrupt an otherwise valid model run")
	}
	if err := limits.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (RunTimeouts{}).Validate(); err == nil {
		t.Fatal("zero timeouts accepted")
	}
	for _, phase := range []string{"first_response", "stream_idle", "total"} {
		ctx, cancel := context.WithCancelCause(context.Background())
		timeout := &runTimeoutError{phase: phase, limit: time.Minute}
		cancel(timeout)
		r := domain.Run{Output: "partial", ProcessOutput: "earlier"}
		applyRunCancellation(ctx, &r)
		if r.Status != "failed" || r.Error != timeout.Error() || r.Output != "partial" || r.ProcessOutput != "earlier" {
			t.Fatal("timeout lost content or was classified as manual stop", r)
		}
	}
}
