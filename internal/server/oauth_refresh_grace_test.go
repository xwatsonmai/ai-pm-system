package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLostRefreshResponseCanBeRetriedOnlyInsideTheGraceWindow(t *testing.T) {
	ctx := context.Background()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	p, e := svc.SaveProject(ctx, domain.Project{Name: "宽限期"})
	if e != nil {
		t.Fatal(e)
	}
	p.Memberships = []domain.ProjectMember{{UserID: "employee", Roles: []string{"developer"}}}
	if e = db.PutProject(ctx, p); e != nil {
		t.Fatal(e)
	}
	if e = db.PutMCPAccount(ctx, domain.MCPAccount{UserID: "employee", Enabled: true}); e != nil {
		t.Fatal(e)
	}
	site := httptest.NewServer((&Server{App: svc}).Handler())
	defer site.Close()
	if e = db.PutOAuthGrant(ctx, domain.OAuthGrant{ID: "grant", ClientID: "desktop", UserID: "employee", UserName: "开发者", ProjectID: p.ID, Resource: site.URL + "/mcp", AccessHash: app.Hash("a0"), RefreshHash: app.Hash("r0"), AccessExpiresAt: domain.Now() + 60000, RefreshExpiresAt: domain.Now() + 600000, AccountGeneration: 1}, ""); e != nil {
		t.Fatal(e)
	}
	redeem := func(refresh string, status int) string {
		t.Helper()
		form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {"desktop"}, "resource": {site.URL + "/mcp"}}
		res, e := http.Post(site.URL+"/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var body struct {
			Refresh string `json:"refresh_token"`
		}
		json.NewDecoder(res.Body).Decode(&body)
		if res.StatusCode != status {
			t.Fatalf("redeeming %s: status %d, want %d", refresh, res.StatusCode, status)
		}
		return body.Refresh
	}

	r1 := redeem("r0", 200)
	grant, _ := db.FindOAuthGrant(ctx, app.Hash(r1), true)
	deadline := grant.PreviousRefreshUntil
	if deadline <= domain.Now() || deadline > domain.Now()+refreshGrace.Milliseconds() {
		t.Fatalf("grace deadline %d", deadline)
	}
	// The response carrying r1 is lost twice; each retry with r0 replaces the
	// pair before it and never moves the deadline.
	r2 := redeem("r0", 200)
	redeem(r1, 400)
	r3 := redeem("r0", 200)
	redeem(r2, 400)
	if grant, _ = db.FindOAuthGrant(ctx, app.Hash(r3), true); grant.PreviousRefreshUntil != deadline {
		t.Fatalf("retry moved the deadline: %d -> %d", deadline, grant.PreviousRefreshUntil)
	}
	// A normal rotation starts a new window for r3 and retires r0 for good.
	r4 := redeem(r3, 200)
	redeem("r0", 400)
	// After the window only the current token works.
	grant, _ = db.FindOAuthGrant(ctx, app.Hash(r4), true)
	grant.PreviousRefreshUntil = time.Now().Add(-time.Second).UnixMilli()
	if e = db.PutOAuthGrant(ctx, grant, grant.RefreshHash); e != nil {
		t.Fatal(e)
	}
	redeem(r3, 400)
	redeem(r4, 200)
}
