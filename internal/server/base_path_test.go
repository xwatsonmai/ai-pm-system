package server_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/auth"
	"ai-pm-system/internal/server"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// A proxy can mount the workspace under a path; pages, sessions, redirects and
// MCP OAuth discovery must all stay inside it.
func TestWorkspaceMountedUnderPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	site := httptest.NewUnstartedServer(nil)
	root := "http://" + site.Listener.Addr().String()
	public := root + "/zhiqu"
	config := filepath.Join(t.TempDir(), "auth.json")
	if e = os.WriteFile(config, []byte(`{"provider":"local","public_url":"`+public+`/"}`), 0600); e != nil {
		t.Fatal(e)
	}
	file, e := auth.Load(config, true)
	if e != nil {
		t.Fatal(e)
	}
	login, e := auth.Open(ctx, file, db)
	if e != nil {
		t.Fatal(e)
	}
	if login.PublicURL() != public || login.Base() != "/zhiqu" {
		t.Fatal(login.PublicURL(), login.Base())
	}
	site.Config.Handler = (&server.Server{App: &app.Service{Store: db}, Auth: login}).Handler()
	site.Start()
	defer site.Close()
	plain := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path, host string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", root+path, nil)
		if host != "" {
			req.Host = host
		}
		resp, e := plain.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	if resp, _ := get("/zhiqu", ""); resp.StatusCode != 302 || resp.Header.Get("Location") != "/zhiqu/" {
		t.Fatal(resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, body := get("/zhiqu/", ""); resp.StatusCode != 200 || !strings.Contains(body, `src="app.js"`) {
		t.Fatal(resp.StatusCode, body)
	}
	if resp, _ := get("/zhiqu/base.mjs", ""); resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	// Deployment tools reach the container by its own address, not the public host.
	if resp, _ := get("/api/health", "10.0.0.9:8090"); resp.StatusCode != 200 {
		t.Fatal("health", resp.StatusCode)
	}
	if resp, _ := get("/zhiqu/api/projects", "10.0.0.9:8090"); resp.StatusCode != 403 {
		t.Fatal("foreign host", resp.StatusCode)
	}

	admin := newBrowser(t, public)
	if r := admin.do("POST", "/api/auth/setup", map[string]string{"token": login.Accounts.SetupToken(), "username": "admin", "password": "admin-pass-1"}, 200); r["redirect"] != "/zhiqu/" {
		t.Fatal(r)
	}
	scoped := false
	for _, c := range admin.client.Jar.Cookies(must(url.Parse(public + "/api/"))) {
		scoped = scoped || c.Name == login.SessionCookie
	}
	for _, c := range admin.client.Jar.Cookies(must(url.Parse(root + "/other/"))) {
		if c.Name == login.SessionCookie {
			t.Fatal("session cookie leaks outside the mount path")
		}
	}
	if !scoped || admin.do("GET", "/api/auth/session", nil, 200)["authenticated"] != true {
		t.Fatal("session not scoped to the mount path")
	}
	project := admin.do("POST", "/api/projects", map[string]string{"name": "子路径项目"}, 200)
	admin.do("PUT", "/api/mcp", map[string]bool{"enabled": true}, 200)
	if u := admin.do("GET", "/api/mcp", nil, 200)["url"]; u != public+"/mcp" {
		t.Fatal(u)
	}

	for _, path := range []string{"/zhiqu/.well-known/oauth-authorization-server", "/zhiqu/.well-known/openid-configuration"} {
		if resp, body := get(path, ""); resp.StatusCode != 200 || !strings.Contains(body, `"issuer":"`+public+`"`) {
			t.Fatal(path, resp.StatusCode, body)
		}
	}
	authorize := "/zhiqu/oauth/authorize?" + url.Values{"client_id": {"public-client"}, "redirect_uri": {"http://127.0.0.1:32100/callback"}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {strings.Repeat("a", 43)}, "state": {"s"}}.Encode()
	if resp, _ := get(authorize, ""); resp.StatusCode != 303 || !strings.HasPrefix(resp.Header.Get("Location"), "/zhiqu/auth/login?return_to=%2Foauth%2Fauthorize%3F") {
		t.Fatal(resp.StatusCode, resp.Header.Get("Location"))
	}
	req, _ := http.NewRequest("GET", root+authorize, nil)
	page, e := admin.client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(body), `href="/zhiqu/style.css"`) || !strings.Contains(string(body), `action="/zhiqu/oauth/authorize"`) {
		t.Fatal(string(body))
	}

	// The official SDK finds the authorization server through the mount path:
	// path insertion at the site root is not forwarded by a proxy.
	fetcher := func(ctx context.Context, args *sdkauth.AuthorizationArgs) (*sdkauth.AuthorizationResult, error) {
		u, e := url.Parse(args.URL)
		if e != nil {
			return nil, e
		}
		if u.Path != "/zhiqu/oauth/authorize" {
			t.Fatalf("authorization endpoint %s", u)
		}
		q := u.Query()
		q.Set("project_id", project["id"].(string))
		q.Set("decision", "allow")
		req, _ := http.NewRequestWithContext(ctx, "POST", root+u.Path, strings.NewReader(q.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, e := admin.client.Do(req)
		if e != nil {
			return nil, e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 303 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("consent: %d %s", resp.StatusCode, b)
		}
		callback, _ := url.Parse(resp.Header.Get("Location"))
		return &sdkauth.AuthorizationResult{Code: callback.Query().Get("code"), State: callback.Query().Get("state")}, nil
	}
	oauth, e := sdkauth.NewAuthorizationCodeHandler(&sdkauth.AuthorizationCodeHandlerConfig{ClientIDMetadataDocumentConfig: &sdkauth.ClientIDMetadataDocumentConfig{URL: "https://unreachable.invalid/client.json"}, DynamicClientRegistrationConfig: &sdkauth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{ClientName: "official SDK", RedirectURIs: []string{"http://127.0.0.1:32100/callback"}}}, AuthorizationCodeFetcher: fetcher, Client: plain})
	if e != nil {
		t.Fatal(e)
	}
	session, e := mcp.NewClient(&mcp.Implementation{Name: "SDK-oauth", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: public + "/mcp", OAuthHandler: oauth, DisableStandaloneSSE: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	if result, e := session.ListTools(ctx, nil); e != nil || len(result.Tools) == 0 {
		t.Fatal(e)
	}
}

func TestPublicURLPathMustBeClean(t *testing.T) {
	for _, raw := range []string{"http://h/a/../b", "http://h/a//b", "http://h/a%2Fb"} {
		config := filepath.Join(t.TempDir(), "auth.json")
		if e := os.WriteFile(config, []byte(`{"provider":"local","public_url":"`+raw+`"}`), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := auth.Load(config, true); e == nil {
			t.Fatal("accepted", raw)
		}
	}
}
