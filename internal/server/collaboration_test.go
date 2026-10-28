package server_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/server"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPWorkSubscriptionAndCollaborationRoundTrip(t *testing.T) {
	for _, personal := range []bool{false, true} {
		name := "legacy"
		if personal {
			name = "personal-oauth"
		}
		t.Run(name, func(t *testing.T) { testMCPWorkSubscription(t, personal) })
	}
}

func testMCPWorkSubscription(t *testing.T, personal bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	p, err := svc.SaveProject(ctx, domain.Project{Name: "subscription project"})
	if err != nil {
		t.Fatal(err)
	}
	token, raw, err := svc.CreateToken(ctx, p.ID, "worker")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer((&server.Server{App: svc}).Handler())
	defer srv.Close()
	var grant domain.OAuthGrant
	if personal {
		p.Memberships = []domain.ProjectMember{{UserID: app.Human().ID, Roles: []string{"admin"}}, {UserID: "employee", Roles: []string{"developer"}}}
		if err = db.PutProject(ctx, p); err != nil {
			t.Fatal(err)
		}
		if err = db.PutMCPAccount(ctx, domain.MCPAccount{UserID: "employee", Enabled: true}); err != nil {
			t.Fatal(err)
		}
		raw = "oauth-fixture-secret"
		grant = domain.OAuthGrant{ID: "grant", UserID: "employee", UserName: "开发者", ProjectID: p.ID, Resource: srv.URL + "/mcp", AccessHash: app.Hash(raw), RefreshHash: app.Hash("refresh-fixture"), AccessExpiresAt: domain.Now() + 60000, RefreshExpiresAt: domain.Now() + 120000, AccountGeneration: 1}
		if err = db.PutOAuthGrant(ctx, grant, ""); err != nil {
			t.Fatal(err)
		}
	}
	changes := make(chan string, 10)
	client := mcp.NewClient(&mcp.Implementation{Name: "collaboration-check", Version: "1"}, &mcp.ClientOptions{ResourceUpdatedHandler: func(_ context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
		select {
		case changes <- req.Params.URI:
		default:
		}
	}})
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{raw}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	resources, err := session.ListResources(ctx, nil)
	if err != nil || len(resources.Resources) != 1 {
		t.Fatal(resources, err)
	}
	uri := resources.Resources[0].URI
	if err = session.Subscribe(ctx, &mcp.SubscribeParams{URI: uri}); err != nil {
		t.Fatal(err)
	}
	initial, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil || !strings.Contains(initial.Contents[0].Text, `"collaborations":[]`) {
		t.Fatal(initial, err)
	}
	r := domain.Run{ID: "origin", ProjectID: p.ID, Kind: "question", ConversationID: "secret-conversation", Prompt: "private prompt", OwnerID: app.Human().ID, OwnerKind: "human", Status: "succeeded"}
	if err = db.PutRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	created, err := svc.CreateCollaboration(ctx, app.Human(), app.CollaborationInput{RequestID: "create", OriginRunID: r.ID, Title: "日志核查", Brief: "只分享 trace-42", Environment: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-changes:
		if got != uri {
			t.Fatal(got)
		}
	case <-ctx.Done():
		t.Fatal("no standard resource notification")
	}
	call := func(name string, args map[string]string) string {
		t.Helper()
		result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil {
			t.Fatal(e)
		}
		if result.IsError {
			t.Fatalf("%s: %#v", name, result.Content)
		}
		return result.Content[0].(*mcp.TextContent).Text
	}
	work := call("work_list", nil)
	if !strings.Contains(work, created.Collaboration.ID) || strings.Contains(work, "secret-conversation") || strings.Contains(work, "private prompt") {
		t.Fatal(work)
	}
	var claimed domain.CollaborationMutation
	if err = json.Unmarshal([]byte(call("collaboration_claim", map[string]string{"collaboration_id": created.Collaboration.ID, "request_id": "claim"})), &claimed); err != nil {
		t.Fatal(err)
	}
	call("collaboration_report", map[string]string{"collaboration_id": created.Collaboration.ID, "request_id": "report", "claim_id": claimed.ClaimID, "body": "已读取日志", "report_kind": "progress"})
	var current domain.CollaborationView
	if err = json.Unmarshal([]byte(call("collaboration_get", map[string]string{"collaboration_id": created.Collaboration.ID})), &current); err != nil {
		t.Fatal(err)
	}
	version, _ := json.Marshal(current.Version)
	args := map[string]string{"collaboration_id": created.Collaboration.ID, "request_id": "result", "claim_id": claimed.ClaimID, "version": string(version), "outcome": "inconclusive", "summary": "日志没有覆盖该时间范围", "environment": "prod", "observed_at": "2026-10-14T10:00:00+08:00", "deployment_version": "未知", "limitations": "需要业务方补充发生时间"}
	first := call("collaboration_submit_result", args)
	if repeat := call("collaboration_submit_result", args); repeat != first {
		t.Fatal("idempotent response changed")
	}
	jobs, err := db.ListReplyJobs(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	// Disconnect/reconnect must recover from the durable work list without a
	// notification replay dependency.
	if err = session.Unsubscribe(ctx, &mcp.UnsubscribeParams{URI: uri}); err != nil {
		t.Fatal(err)
	}
	if err = session.Subscribe(ctx, &mcp.SubscribeParams{URI: uri}); err != nil {
		t.Fatal(err)
	}
	if body := call("work_list", nil); strings.Contains(body, created.Collaboration.ID) {
		t.Fatal("completed item remains claimable", body)
	}
	if personal {
		err = db.DeleteOAuthGrant(ctx, grant.UserID, grant.ID)
	} else {
		err = db.DeleteToken(ctx, p.ID, token.ID)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "work_list"}); err == nil {
		t.Fatal("revoked token accepted")
	}
}
