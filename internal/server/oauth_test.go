package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage/sqlite"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

func TestPersonalOAuthDiscoveryPKCERefreshAndLiveMembership(t *testing.T) {
	ctx := context.Background()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	owner := domain.Actor{ID: "alice", Name: "Alice", Kind: "human"}
	bob := domain.Actor{ID: "bob", Name: "Bob", Kind: "human"}
	for _, a := range []domain.Actor{owner, bob} {
		if e = db.PutUser(ctx, domain.User{ID: a.ID, Name: a.Name}); e != nil {
			t.Fatal(e)
		}
	}
	p, e := svc.SaveProjectAs(ctx, owner, domain.Project{Name: "Assigned project"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = svc.SetProjectMember(ctx, owner, p.ID, bob.ID, "developer", false); e != nil {
		t.Fatal(e)
	}
	other, e := svc.SaveProjectAs(ctx, owner, domain.Project{Name: "Unassigned project"})
	if e != nil {
		t.Fatal(e)
	}
	api := &Server{App: svc}
	handler := api.Handler()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := bob
		if r.Header.Get("X-Test-Actor") == "alice" {
			a = owner
		}
		handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, a)))
	}))
	defer site.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, path, body, content, actor string, status int) (http.Header, string) {
		t.Helper()
		req, _ := http.NewRequest(method, site.URL+path, strings.NewReader(body))
		if content != "" {
			req.Header.Set("Content-Type", content)
		}
		if actor != "" {
			req.Header.Set("X-Test-Actor", actor)
		}
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, resp.StatusCode, status, b)
		}
		return resp.Header, string(b)
	}
	h, _ := request("POST", "/mcp", "{}", "application/json", "", 401)
	if !strings.Contains(h.Get("WWW-Authenticate"), site.URL+"/.well-known/oauth-protected-resource/mcp") {
		t.Fatal("missing resource discovery")
	}
	_, meta := request("GET", "/.well-known/oauth-authorization-server", "", "", "", 200)
	if !strings.Contains(meta, `"client_id_metadata_document_supported":false`) {
		t.Fatal(meta)
	}
	request("PUT", "/api/mcp", `{"enabled":true}`, "application/json", "", 200)
	_, reg := request("POST", "/oauth/register", `{"client_name":"SDK","redirect_uris":["http://127.0.0.1:32100/callback"],"jwks_uri":"https://unreachable.invalid/keys","software_statement":"ignored"}`, "application/json", "", 201)
	var registered domain.OAuthClient
	if e = json.Unmarshal([]byte(reg), &registered); e != nil || registered.ID == "" {
		t.Fatal(reg, e)
	}
	verifier := strings.Repeat("v", 43)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	query := url.Values{"client_id": {"https://unreachable.invalid/client.json"}, "redirect_uri": {"http://127.0.0.1:32100/callback"}, "response_type": {"code"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "scope": {"mcp"}, "state": {"sdk-state"}, "resource": {site.URL + "/mcp"}}
	_, html := request("GET", "/oauth/authorize?"+query.Encode(), "", "", "", 200)
	if !strings.Contains(html, p.Name) || strings.Contains(html, other.Name) {
		t.Fatal("consent project isolation")
	}
	query.Set("project_id", other.ID)
	query.Set("decision", "allow")
	request("POST", "/oauth/authorize", query.Encode(), "application/x-www-form-urlencoded", "", 403)
	query.Set("project_id", p.ID)
	h, _ = request("POST", "/oauth/authorize", query.Encode(), "application/x-www-form-urlencoded", "", 303)
	callback, _ := url.Parse(h.Get("Location"))
	if callback.Query().Get("state") != "sdk-state" {
		t.Fatal("state lost")
	}
	code := callback.Query().Get("code")
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {query.Get("client_id")}, "code": {code}, "redirect_uri": {query.Get("redirect_uri")}, "resource": {site.URL + "/mcp"}, "code_verifier": {strings.Repeat("x", 43)}}
	request("POST", "/oauth/token", form.Encode(), "application/x-www-form-urlencoded", "", 400)
	form.Set("code_verifier", verifier)
	_, body := request("POST", "/oauth/token", form.Encode(), "application/x-www-form-urlencoded", "", 200)
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	if e = json.Unmarshal([]byte(body), &token); e != nil || token.Access == "" || token.Refresh == "" {
		t.Fatal(body, e)
	}
	request("POST", "/oauth/token", form.Encode(), "application/x-www-form-urlencoded", "", 400)
	_, body = request("GET", "/api/mcp", "", "", "", 200)
	if strings.Contains(body, token.Access) || strings.Contains(body, token.Refresh) || strings.Contains(body, "AccessHash") {
		t.Fatal("token material leaked")
	}
	connect := func(raw string) *mcp.ClientSession {
		t.Helper()
		c := mcp.NewClient(&mcp.Implementation{Name: "oauth-test", Version: "1"}, nil)
		session, e := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: site.URL + "/mcp", HTTPClient: &http.Client{Transport: oauthBearerTransport{raw}}, DisableStandaloneSSE: true}, nil)
		if e != nil {
			t.Fatal(e)
		}
		return session
	}
	session := connect(token.Access)
	tools, e := session.ListTools(ctx, nil)
	if e != nil || len(tools.Tools) != 41 {
		t.Fatal(e)
	}
	result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "task_list", Arguments: map[string]string{}})
	if e != nil || result.IsError {
		t.Fatal(e, result)
	}
	session.Close()
	// Refresh rotation binds both client and resource. Redeeming the rotated
	// token again inside the grace window replaces the pair it was exchanged
	// for, as if that response had been lost; once rotated twice it is spent.
	refresh := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token.Refresh}, "client_id": {query.Get("client_id")}, "resource": {site.URL + "/mcp"}}
	refresh.Set("client_id", "wrong")
	request("POST", "/oauth/token", refresh.Encode(), "application/x-www-form-urlencoded", "", 400)
	refresh.Set("client_id", query.Get("client_id"))
	type pair struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	var lost, rotated pair
	_, body = request("POST", "/oauth/token", refresh.Encode(), "application/x-www-form-urlencoded", "", 200)
	json.Unmarshal([]byte(body), &lost)
	_, body = request("POST", "/oauth/token", refresh.Encode(), "application/x-www-form-urlencoded", "", 200)
	json.Unmarshal([]byte(body), &rotated)
	if rotated.Refresh == lost.Refresh || rotated.Access == lost.Access {
		t.Fatal("grace redemption reused the lost pair")
	}
	if _, e = db.FindOAuthGrant(ctx, app.Hash(lost.Access), false); e == nil {
		t.Fatal("lost access token stayed valid")
	}
	refresh.Set("refresh_token", lost.Refresh)
	request("POST", "/oauth/token", refresh.Encode(), "application/x-www-form-urlencoded", "", 400)
	if _, e = svc.SetProjectMember(ctx, owner, p.ID, bob.ID, "", true); e != nil {
		t.Fatal(e)
	}
	request("GET", "/api/projects/"+p.ID+"/context", "", "", "", 403)
	grant, e := db.FindOAuthGrant(ctx, app.Hash(rotated.Access), false)
	if e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("POST", site.URL+"/mcp", nil)
	if _, e = api.grantActor(req, grant); e == nil {
		t.Fatal("revoked membership remained usable")
	}
	refresh.Set("refresh_token", rotated.Refresh)
	request("POST", "/oauth/token", refresh.Encode(), "application/x-www-form-urlencoded", "", 400)
	// Closing the account atomically removes all outstanding grant and code handles.
	request("PUT", "/api/mcp", `{"enabled":false}`, "application/json", "", 200)
	if _, e = db.FindOAuthGrant(ctx, app.Hash(rotated.Access), false); e == nil {
		t.Fatal("account closure retained grant")
	}
	if _, e = db.FindOAuthGrant(ctx, app.Hash(rotated.Refresh), true); e == nil {
		t.Fatal("account closure retained refresh")
	}
	request("GET", "/api/platform/settings", "", "", "", 403)
}

func TestOfficialSDKAutomaticallyDiscoversAndAuthorizesMCP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, e := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	p, e := svc.SaveProjectAs(ctx, app.Human(), domain.Project{Name: "SDK project"})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.PutMCPAccount(ctx, domain.MCPAccount{UserID: "local-owner", Enabled: true}); e != nil {
		t.Fatal(e)
	}
	site := httptest.NewServer((&Server{App: svc}).Handler())
	defer site.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	fetcher := func(ctx context.Context, args *sdkauth.AuthorizationArgs) (*sdkauth.AuthorizationResult, error) {
		u, e := url.Parse(args.URL)
		if e != nil {
			return nil, e
		}
		q := u.Query()
		q.Set("project_id", p.ID)
		q.Set("decision", "allow")
		req, _ := http.NewRequestWithContext(ctx, "POST", site.URL+"/oauth/authorize", strings.NewReader(q.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, e := client.Do(req)
		if e != nil {
			return nil, e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 303 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("SDK consent: %d %s", resp.StatusCode, b)
		}
		callback, _ := url.Parse(resp.Header.Get("Location"))
		return &sdkauth.AuthorizationResult{Code: callback.Query().Get("code"), State: callback.Query().Get("state")}, nil
	}
	oauth, e := sdkauth.NewAuthorizationCodeHandler(&sdkauth.AuthorizationCodeHandlerConfig{ClientIDMetadataDocumentConfig: &sdkauth.ClientIDMetadataDocumentConfig{URL: "https://unreachable.invalid/client.json"}, DynamicClientRegistrationConfig: &sdkauth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{ClientName: "official SDK", RedirectURIs: []string{"http://127.0.0.1:32100/callback"}}}, AuthorizationCodeFetcher: fetcher, Client: client})
	if e != nil {
		t.Fatal(e)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "SDK-oauth", Version: "1"}, nil)
	session, e := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: site.URL + "/mcp", OAuthHandler: oauth, DisableStandaloneSSE: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	result, e := session.ListTools(ctx, nil)
	if e != nil || len(result.Tools) != 41 {
		t.Fatal(e)
	}
}

func TestHTTPConsentPreservesOriginAndRejectsCrossOrigin(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := &app.Service{Store: db}
	p, err := svc.SaveProjectAs(ctx, app.Human(), domain.Project{Name: "HTTP consent"})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.PutMCPAccount(ctx, domain.MCPAccount{UserID: "local-owner", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	handler := (&Server{App: svc}).Handler()
	origin := "http://127.0.0.1:8090"
	query := url.Values{"client_id": {"http-consent-test"}, "redirect_uri": {"http://127.0.0.1:32100/callback"}, "response_type": {"code"}, "code_challenge": {strings.Repeat("v", 43)}, "code_challenge_method": {"S256"}, "resource": {origin + "/mcp"}}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest("GET", origin+"/oauth/authorize?"+query.Encode(), nil))
	if page.Code != 200 || page.Header().Get("Referrer-Policy") != "same-origin" || !strings.Contains(page.Body.String(), "确认授权") {
		t.Fatalf("HTTP consent must retain Origin for form submissions: %d %v", page.Code, page.Header())
	}
	query.Set("project_id", p.ID)
	query.Set("decision", "allow")
	for _, tc := range []struct {
		name, origin, fetchSite string
		status                  int
	}{
		{"HTTP same-origin without fetch metadata", origin, "", 303},
		{"opaque origin", "null", "", 403},
		{"foreign origin", "https://untrusted.example", "", 403},
		{"cross-site fetch metadata", origin, "cross-site", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", origin+"/oauth/authorize", strings.NewReader(query.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", tc.origin)
			if tc.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)
			if resp.Code != tc.status {
				t.Fatalf("got %d, want %d: %s", resp.Code, tc.status, resp.Body.String())
			}
			if resp.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("OAuth callback must not disclose consent URL")
			}
			if tc.status == 303 {
				callback, err := url.Parse(resp.Header().Get("Location"))
				if err != nil || callback.Query().Get("code") == "" {
					t.Fatal("same-origin consent did not issue authorization code")
				}
			}
		})
	}
}

type oauthBearerTransport struct{ token string }

func (t oauthBearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(copy)
}
