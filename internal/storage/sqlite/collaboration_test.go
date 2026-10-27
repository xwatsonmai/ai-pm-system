package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestCollaborationOutboxRollbackAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	v := domain.Collaboration{ID: "c", ProjectID: "p", Version: 1, Status: "investigating"}
	job := domain.ReplyJob{ID: "result-c", CollaborationID: "c", ProjectID: "p", RunID: "run-result-c", Version: 1, Status: "pending"}
	if err = db.CommitCollaboration(ctx, v, 0, nil, &job); err != nil {
		t.Fatal(err)
	}
	v.Version++
	v.Status = "completed"
	v.Result = &domain.InvestigationResult{ID: job.ID, Summary: "result"}
	// Duplicate outbox insertion must roll back the result and receipt together.
	if err = db.CommitCollaboration(ctx, v, 1, &domain.Receipt{Key: "receipt", Fingerprint: "fp", Response: []byte("{}")}, &job); err == nil {
		t.Fatal("duplicate outbox accepted")
	}
	actual, _ := db.GetCollaboration(ctx, v.ID)
	if actual.Version != 1 || actual.Result != nil {
		t.Fatal("partial result commit", actual)
	}
	if _, err = db.GetReceipt(ctx, "receipt"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("receipt survived rollback", err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	jobs, err := db.ListReplyJobs(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].RunID != job.RunID {
		t.Fatal("outbox lost after reopen", jobs, err)
	}
	r := domain.Run{ID: job.RunID, ProjectID: "p", Kind: "question", Status: "queued"}
	job.Version++
	job.Status = "running"
	if err = db.CommitReplyJob(ctx, job, 1, &r); err != nil {
		t.Fatal(err)
	}
	r.Output = "stale overwrite"
	if err = db.CommitReplyJob(ctx, job, 1, &r); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	saved, _ := db.GetRun(ctx, r.ID)
	if saved.Output != "" {
		t.Fatal("stale worker overwrote run")
	}
}
