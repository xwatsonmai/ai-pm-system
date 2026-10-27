package agent

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func collaborationToolsFixture(t *testing.T) (*Manager, *sqlite.DB, domain.Run, domain.Collaboration) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(ctx, &app.Service{Store: db}, nil, "missing", t.TempDir())
	t.Cleanup(func() { m.Close(); db.Close() })
	if err = db.PutProject(ctx, domain.Project{ID: "p", OwnerID: "alice", AccessMembers: []string{"bob"}}); err != nil {
		t.Fatal(err)
	}
	r := domain.Run{ID: "origin", ProjectID: "p", ConversationID: "conv", Kind: "question", OwnerID: "alice", OwnerKind: "human", Status: "succeeded"}
	if err = db.PutRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	v, err := m.App.CreateCollaboration(ctx, domain.Actor{ID: "alice", Kind: "human"}, app.CollaborationInput{OriginRunID: r.ID, RequestID: "create", Title: "回调调查", Brief: "trace-42", Environment: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	return m, db, r, v.Collaboration
}

func collaborationTool(t *testing.T, m *Manager, r domain.Run, name, id string) (any, error) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"collaboration_id": id})
	return m.callTool(context.Background(), domain.Actor{ID: "platform", Kind: "platform", ProjectID: r.ProjectID}, r, "call", name, b)
}

func TestConversationCollaborationQueryIsFresh(t *testing.T) {
	m, db, r, v := collaborationToolsFixture(t)
	ctx := context.Background()
	query := func(id string) collaborationQuery {
		t.Helper()
		out, err := collaborationTool(t, m, r, "get_collaborations", id)
		if err != nil {
			t.Fatal(err)
		}
		return out.(collaborationQuery)
	}
	got := query("")
	if len(got.Collaborations) != 1 || got.Collaborations[0].Status != "pending" || got.AsOf < v.CreatedAt {
		t.Fatal(got)
	}
	dev := domain.Actor{ID: "dev", Name: "开发 A", Kind: "developer", ProjectID: "p"}
	claimed, err := m.App.ActCollaboration(ctx, dev, v.ID, app.CollaborationAction{Action: "claim", RequestID: "claim"})
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := m.App.ActCollaboration(ctx, dev, v.ID, app.CollaborationAction{Action: "report", RequestID: "progress", ClaimID: claimed.ClaimID, ReportKind: "blocked", Body: "等待日志权限"})
	if err != nil {
		t.Fatal(err)
	}
	got = query(v.ID)
	fresh := got.Collaborations[0]
	if fresh.Status != "blocked" || fresh.Version != blocked.Collaboration.Version || fresh.ClaimantName != dev.Name || fresh.Updates[len(fresh.Updates)-1].Body != "等待日志权限" || fresh.LeaseUntil <= got.AsOf {
		t.Fatal("query used a stale snapshot", got)
	}
	// Report lease expiry without pretending the local executor is still online.
	stored, _ := db.GetCollaboration(ctx, v.ID)
	stored.LeaseUntil = domain.Now() - 1
	stored.Version++
	if err = db.CommitCollaboration(ctx, stored, stored.Version-1, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got = query(v.ID); got.Collaborations[0].LeaseUntil >= got.AsOf {
		t.Fatal(got)
	}
	second, err := m.App.CreateCollaboration(ctx, domain.Actor{ID: r.OwnerID, Kind: r.OwnerKind}, app.CollaborationInput{OriginRunID: r.ID, RequestID: "second", Title: "另一问题", Brief: "trace-43", Environment: "prod"})
	if err != nil || len(query("").Collaborations) != 2 || len(query(second.Collaboration.ID).Collaborations) != 1 {
		t.Fatal(second, err)
	}
	if _, err = collaborationTool(t, m, r, "cancel_collaboration", ""); err == nil {
		t.Fatal("ambiguous cancellation must require a task ID")
	}
	if got = query(""); got.Collaborations[0].Status == "canceled" || got.Collaborations[1].Status == "canceled" {
		t.Fatal("missing ID canceled tasks", got)
	}
}

func TestConversationCollaborationCancelFencesLateWorkAndIsIdempotent(t *testing.T) {
	for _, status := range []string{"pending", "investigating", "blocked"} {
		t.Run(status, func(t *testing.T) {
			m, db, r, v := collaborationToolsFixture(t)
			ctx := context.Background()
			dev := domain.Actor{ID: "dev", Kind: "developer", ProjectID: "p"}
			claimID := ""
			if status != "pending" {
				claimed, err := m.App.ActCollaboration(ctx, dev, v.ID, app.CollaborationAction{Action: "claim", RequestID: "claim"})
				if err != nil {
					t.Fatal(err)
				}
				claimID = claimed.ClaimID
				if status == "blocked" {
					if _, err = m.App.ActCollaboration(ctx, dev, v.ID, app.CollaborationAction{Action: "report", RequestID: "blocked", ClaimID: claimID, ReportKind: "blocked", Body: "缺少日志"}); err != nil {
						t.Fatal(err)
					}
				}
			}
			changes, unsubscribe := m.App.SubscribeWork("p")
			defer unsubscribe()
			if _, err := collaborationTool(t, m, r, "cancel_collaboration", v.ID); err != nil {
				t.Fatal(err)
			}
			select {
			case <-changes:
			default:
				t.Fatal("cancellation did not notify developer subscribers")
			}
			canceled, _ := db.GetCollaboration(ctx, v.ID)
			if canceled.Status != "canceled" || canceled.ClaimHash != "" || canceled.LeaseUntil != 0 {
				t.Fatal(canceled)
			}
			// Retry in the same turn and a later user turn: no duplicate audit update.
			for _, runID := range []string{r.ID, "later-user-turn"} {
				r.ID = runID
				if err := db.PutRun(ctx, r); err != nil {
					t.Fatal(err)
				}
				if _, err := collaborationTool(t, m, r, "cancel_collaboration", v.ID); err != nil {
					t.Fatal(err)
				}
			}
			after, _ := db.GetCollaboration(ctx, v.ID)
			if after.Version != canceled.Version || len(after.Updates) != len(canceled.Updates) {
				t.Fatal("duplicate cancellation", after)
			}
			for _, action := range []string{"claim", "report", "submit"} {
				_, err := m.App.ActCollaboration(ctx, dev, v.ID, app.CollaborationAction{Action: action, RequestID: "late-" + action, ClaimID: claimID, Version: after.Version, Body: "迟到进展", Result: &domain.InvestigationResult{Outcome: "inconclusive", Summary: "迟到结果", Environment: "prod", ObservedAt: "now", DeploymentVersion: "unknown", Limitations: "unknown"}})
				if !errors.Is(err, domain.ErrConflict) {
					t.Fatal("late work accepted", action, err)
				}
			}
			work, err := m.App.WorkList(ctx, dev)
			jobs, jobErr := db.ListReplyJobs(ctx)
			if err != nil || jobErr != nil || len(work["collaborations"].([]domain.Collaboration)) != 0 || len(jobs) != 0 {
				t.Fatal(work, jobs, err, jobErr)
			}
		})
	}
}

func TestConversationCollaborationCompletedResultAndCancellation(t *testing.T) {
	m, db, v, job := replyFixture(t)
	ctx := context.Background()
	r, _ := db.GetRun(ctx, v.OriginRunID)
	out, err := collaborationTool(t, m, r, "get_collaborations", v.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := out.(collaborationQuery).Collaborations[0]
	if got.Result == nil || got.Result.Summary != v.Result.Summary || got.Reply == nil || got.Reply.Status != "pending" {
		t.Fatal("query lost result or delivery status", got)
	}
	if _, err = collaborationTool(t, m, r, "cancel_collaboration", v.ID); err != nil {
		t.Fatal(err)
	}
	if err = m.dispatchReplies(); err != nil {
		t.Fatal(err)
	}
	stored, _ := db.GetCollaboration(ctx, v.ID)
	reply, _ := db.GetReplyJob(ctx, job.ID)
	if stored.Result == nil || stored.Result.ID != v.Result.ID || reply.Status != "skipped" {
		t.Fatal("cancellation discarded evidence or allowed a reply", stored, reply)
	}
	if _, err = db.GetRun(ctx, job.RunID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("canceled result created a reply", err)
	}
}

func TestConversationCollaborationToolsEnforceScope(t *testing.T) {
	for _, scenario := range []string{"other-conversation", "other-owner", "other-project", "not-question", "automatic-reply", "deleted", "revoked", "forged-run", "missing-owner", "revoked-developer"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, r, v := collaborationToolsFixture(t)
			ctx := context.Background()
			switch scenario {
			case "other-conversation", "other-owner", "other-project":
				v.ID = "foreign"
				if scenario == "other-conversation" {
					v.ConversationID = "another-conversation"
				} else if scenario == "other-owner" {
					v.OwnerID = "bob"
				} else {
					v.ProjectID = "another-project"
				}
				if err := db.CommitCollaboration(ctx, v, 0, nil, nil); err != nil {
					t.Fatal(err)
				}
				out, err := collaborationTool(t, m, r, "get_collaborations", "")
				if err != nil || len(out.(collaborationQuery).Collaborations) != 1 || out.(collaborationQuery).Collaborations[0].ID == v.ID {
					t.Fatal("foreign task leaked in list", out, err)
				}
			case "not-question":
				r.Kind = "progress"
			case "automatic-reply":
				r.TriggerType = "collaboration_result"
			case "deleted":
				if _, err := m.EditConversation(ctx, domain.Actor{ID: r.OwnerID, Kind: r.OwnerKind}, r.ProjectID, r.ConversationID, "", true); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				if err := db.PutProject(ctx, domain.Project{ID: "p", OwnerID: "bob"}); err != nil {
					t.Fatal(err)
				}
			case "forged-run":
				r.OwnerID = "bob"
			case "missing-owner":
				r.OwnerID = ""
			case "revoked-developer":
				r.OwnerID, r.OwnerKind = "dev", "developer"
				if err := db.PutRun(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			for _, tool := range []string{"get_collaborations", "cancel_collaboration"} {
				if _, err := collaborationTool(t, m, r, tool, v.ID); err == nil {
					t.Fatal("unauthorized tool accepted", tool)
				}
			}
			stored, _ := db.GetCollaboration(ctx, v.ID)
			if stored.Status != "pending" || stored.Version != v.Version {
				t.Fatal("unauthorized cancellation changed task", stored)
			}
		})
	}
}

func TestConversationCollaborationToolsOfferedOnlyForUserQuestions(t *testing.T) {
	for _, r := range []domain.Run{{Kind: "question"}, {Kind: "progress"}, {Kind: "question", TriggerType: "collaboration_result"}} {
		found := map[string]bool{}
		for _, tool := range toolsFor(r) {
			found[tool.Name] = true
		}
		want := r.Kind == "question" && r.TriggerType == ""
		if found["get_collaborations"] != want || found["cancel_collaboration"] != want {
			t.Fatal(r, found)
		}
	}
}

func TestPersonalMCPCollaborationOwnerAndRevocation(t *testing.T) {
	for _, revoke := range []string{"membership", "account"} {
		t.Run(revoke, func(t *testing.T) {
			m, db, r, _ := collaborationToolsFixture(t)
			ctx := context.Background()
			r.ID, r.OwnerKind = "personal-mcp-run", "developer"
			if err := db.PutRun(ctx, r); err != nil {
				t.Fatal(err)
			}
			if err := db.PutMCPAccount(ctx, domain.MCPAccount{UserID: r.OwnerID, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			platform := domain.Actor{ID: "platform", Kind: "platform", ProjectID: r.ProjectID}
			created, err := m.App.CreateCollaboration(ctx, platform, app.CollaborationInput{OriginRunID: r.ID, RequestID: "personal", Title: "个人 MCP 发起", Brief: "只读调查", Environment: "prod"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = collaborationTool(t, m, r, "get_collaborations", created.Collaboration.ID); err != nil {
				t.Fatal(err)
			}
			v := created.Collaboration
			v.Version++
			v.Status = "completed"
			v.Result = &domain.InvestigationResult{ID: "result", Summary: "test"}
			if err = db.CommitCollaboration(ctx, v, v.Version-1, nil, nil); err != nil {
				t.Fatal(err)
			}
			if _, err = m.replySource(ctx, v.ID); err != nil {
				t.Fatal("personal MCP reply rejected", err)
			}
			if revoke == "membership" {
				err = db.PutProject(ctx, domain.Project{ID: r.ProjectID, OwnerID: "bob"})
			} else {
				err = db.PutMCPAccount(ctx, domain.MCPAccount{UserID: r.OwnerID, Enabled: false})
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.replySource(ctx, v.ID); !errors.Is(err, domain.ErrForbidden) {
				t.Fatal("revoked MCP reply allowed", err)
			}
			if _, err = collaborationTool(t, m, r, "cancel_collaboration", v.ID); !errors.Is(err, domain.ErrForbidden) {
				t.Fatal("revoked owner allowed", err)
			}
		})
	}
}
