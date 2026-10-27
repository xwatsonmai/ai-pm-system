package agent

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func replyFixture(t *testing.T) (*Manager, *sqlite.DB, domain.Collaboration, domain.ReplyJob) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := &app.Service{Store: db}
	m := New(ctx, s, nil, "missing", t.TempDir())
	t.Cleanup(func() { m.Close(); db.Close() })
	p := domain.Project{ID: "p", Name: "P", OwnerID: "alice", AccessMembers: []string{"bob"}}
	if err = db.PutProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	r := domain.Run{ID: "origin", ProjectID: p.ID, ConversationID: "conv", Kind: "question", OwnerID: "alice", OwnerKind: "human", Status: "succeeded"}
	if err = db.PutRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	a := domain.Actor{ID: "alice", Kind: "human"}
	v, err := s.CreateCollaboration(ctx, a, app.CollaborationInput{OriginRunID: r.ID, RequestID: "create", Title: "investigate", Brief: "brief", Environment: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	dev := domain.Actor{ID: "dev", Kind: "developer", ProjectID: p.ID}
	v, err = s.ActCollaboration(ctx, dev, v.Collaboration.ID, app.CollaborationAction{Action: "claim", RequestID: "claim"})
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.ActCollaboration(ctx, dev, v.Collaboration.ID, app.CollaborationAction{Action: "submit", RequestID: "result", ClaimID: v.ClaimID, Version: v.Collaboration.Version, Result: &domain.InvestigationResult{Outcome: "inconclusive", Summary: "need logs", Environment: "prod", ObservedAt: "2026-10-14", DeploymentVersion: "unknown", Limitations: "not enough data"}})
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := db.GetCollaboration(ctx, v.Collaboration.ID)
	job, err := db.GetReplyJob(ctx, stored.Result.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m, db, stored, job
}

func TestReplyRecoveryDoesNotDuplicateSavedAnswer(t *testing.T) {
	m, db, v, job := replyFixture(t)
	ctx := context.Background()
	r := domain.Run{ID: job.RunID, ProjectID: v.ProjectID, ConversationID: v.ConversationID, OwnerID: v.OwnerID, OwnerKind: v.OwnerKind, Kind: "question", TriggerType: "collaboration_result", CollaborationID: v.ID, SourceEventID: v.Result.ID, Status: "succeeded", Output: "saved answer"}
	job.Status = "running"
	job.Version++
	job.Attempts = 1
	if err := db.CommitReplyJob(ctx, job, 1, &r); err != nil {
		t.Fatal(err)
	}
	if err := m.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.dispatchReplies(); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetReplyJob(ctx, job.ID)
	if got.Status != "delivered" || got.Attempts != 1 {
		t.Fatal(got)
	}
	runs, _ := db.ListRuns(ctx, v.ProjectID)
	if len(runs) != 2 {
		t.Fatal("duplicated answer", runs)
	}
	if err := m.dispatchReplies(); err != nil {
		t.Fatal(err)
	}
}

func TestReplyDeletionCancellationRevocationAndBusyConversation(t *testing.T) {
	for _, scenario := range []string{"deleted", "canceled", "revoked", "busy", "missing-model"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, v, job := replyFixture(t)
			ctx := context.Background()
			switch scenario {
			case "deleted":
				if _, err := m.EditConversation(ctx, domain.Actor{ID: "alice", Kind: "human"}, v.ProjectID, v.ConversationID, "", true); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				if _, err := m.CancelCollaboration(ctx, domain.Actor{ID: "alice", Kind: "human"}, v.ID, "cancel"); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				p, _ := db.GetProject(ctx, v.ProjectID)
				p.OwnerID = "bob"
				p.AccessMembers = nil
				if err := db.PutProject(ctx, p); err != nil {
					t.Fatal(err)
				}
			case "busy":
				if err := db.PutRun(ctx, domain.Run{ID: "followup", ProjectID: v.ProjectID, ConversationID: v.ConversationID, Kind: "question", OwnerID: v.OwnerID, OwnerKind: v.OwnerKind, Status: "running"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.dispatchReplies(); err != nil {
				t.Fatal(err)
			}
			got, _ := db.GetReplyJob(ctx, job.ID)
			want := "skipped"
			if scenario == "busy" {
				want = "pending"
			}
			if scenario == "missing-model" {
				want = "failed"
			}
			if got.Status != want {
				t.Fatal(got)
			}
			if _, err := db.GetRun(ctx, job.RunID); err == nil {
				t.Fatal("unexpected answer")
			}
			if scenario == "missing-model" {
				if _, err := m.RetryCollaboration(ctx, domain.Actor{ID: "bob", Kind: "human"}, v.ID); err == nil {
					t.Fatal("another user retried")
				}
				j, err := m.RetryCollaboration(ctx, domain.Actor{ID: "alice", Kind: "human"}, v.ID)
				if err != nil || j.Status != "pending" {
					t.Fatal(j, err)
				}
			}
		})
	}
}

func TestCancelRunningReplyDiscardsLateOutput(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required")
	}
	m, db, v, job := replyFixture(t)
	ctx := context.Background()
	worker := filepath.Join(t.TempDir(), "worker.mjs")
	js := `import readline from 'node:readline';
const rl=readline.createInterface({input:process.stdin});
rl.on('line',line=>{const m=JSON.parse(line);if(m.type==='start'){process.stdout.write(JSON.stringify({type:'delta',text:'partial investigation'})+'\n');}if(m.type==='stop'){process.stdout.write(JSON.stringify({type:'done',text:'late result must be discarded'})+'\n');process.exit(0);}});`
	if err := os.WriteFile(worker, []byte(js), 0600); err != nil {
		t.Fatal(err)
	}
	m.Worker = worker
	if err := db.PutProfile(ctx, domain.Profile{ID: "profile", BaseURL: "http://unused", Model: "fixture"}); err != nil {
		t.Fatal(err)
	}
	p, _ := db.GetProject(ctx, v.ProjectID)
	p.ProfileID = "profile"
	if err := db.PutPlatformSettings(ctx, domain.PlatformSettings{ProfileID: "profile"}); err != nil {
		t.Fatal(err)
	}
	if err := db.PutProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := m.dispatchReplies(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, _ := db.GetRun(ctx, job.RunID)
		if r.Status == "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	origin, err := db.GetRun(ctx, v.OriginRunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collaborationTool(t, m, origin, "cancel_collaboration", v.ID); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		j, _ := db.GetReplyJob(ctx, job.ID)
		if j.Status == "skipped" {
			r, _ := db.GetRun(ctx, job.RunID)
			if r.Status != "stopped" || r.Output != "" || r.ProcessOutput != "" {
				t.Fatal("late output published", r)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("canceled reply did not stop")
}
