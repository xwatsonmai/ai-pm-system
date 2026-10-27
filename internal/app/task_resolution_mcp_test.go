package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMCPTaskResolutionRequiresExplicitUserAuthorization(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	task := create(t, s, p.ID)
	a := domain.Actor{ID: "local-owner", UserID: "local-owner", Name: "负责人 · MCP", Kind: "developer", ProjectID: p.ID}
	in := app.MCPTaskResolutionInput{Action: "close", RequestID: "close", Version: task.Version, Reason: "本期不再开发", UserConfirmed: true, UserInstruction: "请关闭这个需求，本期不做了"}
	for _, change := range []struct {
		name string
		edit func(*app.MCPTaskResolutionInput)
	}{
		{"not confirmed", func(v *app.MCPTaskResolutionInput) { v.UserConfirmed = false }},
		{"no instruction", func(v *app.MCPTaskResolutionInput) { v.UserInstruction = " \n" }},
		{"no reason", func(v *app.MCPTaskResolutionInput) { v.Reason = " \n" }},
		{"no version", func(v *app.MCPTaskResolutionInput) { v.Version = 0 }},
		{"negative version", func(v *app.MCPTaskResolutionInput) { v.Version = -1 }},
		{"no request id", func(v *app.MCPTaskResolutionInput) { v.RequestID = " " }},
		{"unrelated human operation", func(v *app.MCPTaskResolutionInput) { v.Action = "accept" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			bad := in
			change.edit(&bad)
			if _, e := s.ResolveTaskFromMCP(c, a, task.ID, bad); e == nil {
				t.Fatal("operation without authorization accepted")
			}
			current, _ := s.Store.GetTask(c, task.ID)
			if current.Version != task.Version || current.Status != task.Status {
				t.Fatal("failed validation changed task")
			}
		})
	}
	for _, bad := range []domain.Actor{app.Human(), developer(p.ID, "legacy-token"), {ID: "platform", Kind: "platform", ProjectID: p.ID}, {ID: "client", UserID: a.UserID, Kind: "developer", ProjectID: p.ID}} {
		if _, e := s.ResolveTaskFromMCP(c, bad, task.ID, in); !errors.Is(e, domain.ErrForbidden) {
			t.Fatal("unbound identity accepted", bad, e)
		}
	}
	// A personal OAuth actor still cannot use ordinary developer actions to
	// bypass the dedicated confirmation gate or acquire other human privileges.
	for _, operation := range []string{"close", "decline", "reopen", "accept", "ready"} {
		if _, e := s.Act(c, a, task.ID, app.Action{Action: operation, RequestID: "direct-" + operation, Body: "自行决定"}); !errors.Is(e, domain.ErrForbidden) {
			t.Fatal("generic action bypassed authorization", operation, e)
		}
	}
	ended, e := s.ResolveTaskFromMCP(c, a, task.ID, in)
	if e != nil || ended.Task.Status != "closed" {
		t.Fatal(ended, e)
	}
	for _, edit := range []func(*app.MCPTaskResolutionInput){
		func(v *app.MCPTaskResolutionInput) { v.Reason = "不同原因" },
		func(v *app.MCPTaskResolutionInput) { v.UserInstruction = "不同的用户原话" },
		func(v *app.MCPTaskResolutionInput) { v.Action = "reopen"; v.Version = ended.Task.Version },
	} {
		changed := in
		edit(&changed)
		if _, e = s.ResolveTaskFromMCP(c, a, task.ID, changed); !errors.Is(e, domain.ErrConflict) {
			t.Fatal("request id reused with different authorization", e)
		}
	}
	if _, e = s.ResolveTaskFromMCP(c, a, task.ID, app.MCPTaskResolutionInput{Action: "reopen", RequestID: "automatic-reopen", Version: ended.Task.Version, Reason: "继续讨论"}); !errors.Is(e, domain.ErrForbidden) {
		t.Fatal("reopen did not require fresh explicit authorization", e)
	}
}

func TestMCPTaskResolutionAcrossStagesRetainsHistory(t *testing.T) {
	for _, stage := range []string{"clarify", "ready", "doing", "pending_release", "review"} {
		for _, operation := range []string{"close", "decline"} {
			t.Run(stage+"/"+operation, func(t *testing.T) {
				s, p := setup(t)
				c := context.Background()
				task, claim, review := taskAtStage(t, s, p.ID, stage)
				a := domain.Actor{ID: "local-owner", UserID: "local-owner", Name: "负责人 · MCP", Kind: "developer", ProjectID: p.ID}
				in := app.MCPTaskResolutionInput{Action: operation, RequestID: "resolve", Version: task.Version, Reason: "业务范围调整", UserConfirmed: true, UserInstruction: "请结束这个需求，原因是业务范围调整"}
				ended, e := s.ResolveTaskFromMCP(c, a, task.ID, in)
				if e != nil {
					t.Fatal(e)
				}
				if ended.Task.Status != map[string]string{"close": "closed", "decline": "rejected"}[operation] || ended.Task.Resolution.Reason != in.Reason || ended.Task.Resolution.FromStatus != stage || ended.Task.Resolution.Actor != a.Name {
					t.Fatal("wrong resolution", ended.Task)
				}
				if ended.Task.Description != task.Description || ended.Task.Submission != nil && ended.Task.Submission.SHA != "v1" || ended.Task.Deployment != nil && ended.Task.Deployment.SHA != "v1" {
					t.Fatal("original content or delivery evidence lost")
				}
				retry, e := s.ResolveTaskFromMCP(c, a, task.ID, in)
				if e != nil || retry.Task.Version != ended.Task.Version {
					t.Fatal("lost-response retry duplicated the decision", e)
				}
				rr, e := s.RequirementReview(c, app.Human(), task.ID)
				if e != nil || rr.Status != "closed" || rr.Generation <= review.Generation {
					t.Fatal("review not closed atomically", e)
				}
				if _, e = s.Act(c, developer(p.ID, "dev"), task.ID, app.Action{Action: "report", RequestID: "old-claim", ClaimID: claim.ClaimID, Body: "旧进度"}); !errors.Is(e, domain.ErrConflict) {
					t.Fatal("developer continued ended task", e)
				}
				events, _ := s.Store.Events(c, p.ID, task.ID, 0)
				last := events[len(events)-1]
				var data struct {
					Authorization domain.TaskActionAuthorization `json:"authorization"`
				}
				if e = json.Unmarshal(last.Data, &data); e != nil || last.Actor != a.Name || last.Kind != operation || data.Authorization.Source != "mcp" || data.Authorization.UserID != a.UserID || data.Authorization.ActorID != a.ID || !data.Authorization.UserConfirmed || data.Authorization.UserInstruction != in.UserInstruction || !strings.Contains(last.Body, in.UserInstruction) {
					t.Fatal("authorization not retained with real MCP identity", last, e)
				}
				beforeEvents := len(events)
				reopen := app.MCPTaskResolutionInput{Action: "reopen", RequestID: "reopen", Version: ended.Task.Version, Reason: "范围已重新确定", UserConfirmed: true, UserInstruction: "重新打开这个需求，范围已重新确定"}
				opened, e := s.ResolveTaskFromMCP(c, a, task.ID, reopen)
				if e != nil || opened.Task.Status != "clarify" || opened.Task.Revision != task.Revision+1 || opened.Task.Resolution != nil || opened.Task.Claimant != "" {
					t.Fatal("reopen did not enter new review", opened, e)
				}
				if _, e = s.ResolveTaskFromMCP(c, a, task.ID, reopen); e != nil {
					t.Fatal(e)
				}
				events, _ = s.Store.Events(c, p.ID, task.ID, 0)
				last = events[len(events)-1]
				if len(events) != beforeEvents+1 || last.Kind != "reopen" || !strings.Contains(string(last.Data), reopen.UserInstruction) {
					t.Fatal("reopen history missing or duplicated", last)
				}
			})
		}
	}
}

func TestHumanResolutionRetainsLegacyReceiptFingerprint(t *testing.T) {
	s, p := setup(t)
	c := context.Background()
	task := create(t, s, p.ID)
	in := app.Action{Action: "close", RequestID: "web-close", Version: task.Version, Body: "网页关闭"}
	if _, e := s.Act(c, app.Human(), task.ID, in); e != nil {
		t.Fatal(e)
	}
	legacy, _ := json.Marshal(struct {
		ID     string
		Action app.Action
	}{task.ID, in})
	digest := sha256.Sum256(legacy)
	receipt, e := s.Store.GetReceipt(c, app.Human().ID+":"+p.ID+":"+in.RequestID)
	if e != nil || receipt.Fingerprint != hex.EncodeToString(digest[:]) {
		t.Fatal("existing web receipts can no longer be retried", e)
	}
}
