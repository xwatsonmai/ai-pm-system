package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

func oauthSecret() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func (s *Server) origin(r *http.Request) string {
	if s.Auth != nil {
		return s.Auth.PublicURL()
	}
	return "http://" + r.Host
}
func (s *Server) oauthError(w http.ResponseWriter, status int, code, description string) {
	respondStatus(w, status, map[string]string{"error": code, "error_description": description})
}
func validRedirect(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))
}
func validPKCE(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	return true
}

func (s *Server) oauthRoutes(mux *http.ServeMux) {
	resource := func(w http.ResponseWriter, r *http.Request) {
		origin := s.origin(r)
		respond(w, map[string]any{"resource": origin + "/mcp", "authorization_servers": []string{origin}, "scopes_supported": []string{"mcp"}, "bearer_methods_supported": []string{"header"}})
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", resource)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", resource)
	metadata := func(w http.ResponseWriter, r *http.Request) {
		origin := s.origin(r)
		respond(w, map[string]any{"issuer": origin, "authorization_endpoint": origin + "/oauth/authorize", "token_endpoint": origin + "/oauth/token", "registration_endpoint": origin + "/oauth/register", "revocation_endpoint": origin + "/oauth/revoke", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": []string{"mcp"}, "client_id_metadata_document_supported": false})
	}
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", metadata)
	// An issuer with a path is discovered under that path, the only part a
	// proxy forwards here (RFC 8414 path insertion lands on the proxy's root).
	if s.appPath("") != "" {
		mux.HandleFunc("GET /.well-known/openid-configuration", metadata)
	}
	mux.HandleFunc("POST /oauth/register", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name            string   `json:"client_name"`
			RedirectURIs    []string `json:"redirect_uris"`
			GrantTypes      []string `json:"grant_types"`
			ResponseTypes   []string `json:"response_types"`
			AuthMethod      string   `json:"token_endpoint_auth_method"`
			ClientURI       string   `json:"client_uri"`
			LogoURI         string   `json:"logo_uri"`
			Scope           string   `json:"scope"`
			Contacts        []string `json:"contacts"`
			SoftwareID      string   `json:"software_id"`
			SoftwareVersion string   `json:"software_version"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		decoder := json.NewDecoder(r.Body)
		decodeErr := decoder.Decode(&in)
		if decodeErr == nil {
			if e := decoder.Decode(new(any)); e != io.EOF {
				decodeErr = errors.New("invalid JSON")
			}
		}
		if decodeErr != nil {
			s.oauthError(w, 400, "invalid_client_metadata", "注册参数无效")
			return
		}
		if len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > 20 {
			s.oauthError(w, 400, "invalid_redirect_uri", "请提供客户端回调地址")
			return
		}
		for _, u := range in.RedirectURIs {
			if !validRedirect(u) {
				s.oauthError(w, 400, "invalid_redirect_uri", "回调必须为 HTTPS 或本机 HTTP 地址")
				return
			}
		}
		// Public clients do not need a secret, software statement, JWKS or network fetch.
		p := domain.OAuthClient{ID: domain.ID("mcp_client_"), Name: in.Name, RedirectURIs: in.RedirectURIs}
		if e := s.App.Store.PutOAuthClient(r.Context(), p); e != nil {
			fail(w, e)
			return
		}
		respondStatus(w, http.StatusCreated, map[string]any{"client_id": p.ID, "client_name": p.Name, "redirect_uris": p.RedirectURIs, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none"})
	})
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/token", s.oauthToken)
	mux.HandleFunc("POST /oauth/revoke", s.oauthRevoke)
	mux.HandleFunc("GET /api/mcp", func(w http.ResponseWriter, r *http.Request) {
		a := s.actor(r)
		account, e := s.App.Store.GetMCPAccount(r.Context(), a.ID)
		if e != nil {
			fail(w, e)
			return
		}
		grants, e := s.App.Store.ListOAuthGrants(r.Context(), a.ID)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, map[string]any{"enabled": account.Enabled, "url": s.origin(r) + "/mcp", "grants": grants})
	})
	mux.HandleFunc("PUT /api/mcp", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		p := domain.MCPAccount{UserID: s.actor(r).ID, Enabled: in.Enabled}
		if e := s.App.Store.PutMCPAccount(r.Context(), p); e != nil {
			fail(w, e)
			return
		}
		respond(w, map[string]bool{"enabled": p.Enabled})
	})
	mux.HandleFunc("DELETE /api/mcp/grants/{id}", func(w http.ResponseWriter, r *http.Request) {
		if e := s.App.Store.DeleteOAuthGrant(r.Context(), s.actor(r).ID, r.PathValue("id")); e != nil {
			fail(w, e)
			return
		}
		respond(w, map[string]bool{"ok": true})
	})
}

var consentPage = template.Must(template.New("consent").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>MCP 授权 · 智驱</title><link rel="stylesheet" href="{{.Base}}/style.css"></head><body><main class="oauth-page"><div class="brand-mark">H</div><h1>授权开发 MCP</h1><p>{{.Name}}，客户端将以你的身份访问所选项目。</p><p class="hint">客户端：{{.Client}}<br>回调：{{.Redirect}}</p>{{if .Enabled}}<form method="post" action="{{.Base}}/oauth/authorize">{{range $key,$value:=.Fields}}<input type="hidden" name="{{$key}}" value="{{$value}}">{{end}}<div class="field"><label for="oauth-project">授权项目</label><select id="oauth-project" name="project_id" required>{{range .Projects}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></div><p class="hint">可查询、认领及提交事项。移除项目成员或撤销授权后停止访问。</p><div class="row"><button type="submit" name="decision" value="allow" class="primary">确认授权</button><button type="submit" name="decision" value="deny">取消</button></div></form>{{else}}{{if .HasProjects}}<p>请先在工作台的「我的 MCP」中开通个人 MCP，再回到客户端重新授权。</p>{{else}}<p>你尚未加入项目，请联系项目或平台管理员分配项目后重新授权。</p>{{end}}<a href="{{.Base}}/">打开工作台</a>{{end}}</main></body></html>`))

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if e := r.ParseForm(); e != nil {
		s.oauthError(w, 400, "invalid_request", "授权参数无效")
		return
	}
	q := r.Form
	client, redirect, challenge := q.Get("client_id"), q.Get("redirect_uri"), q.Get("code_challenge")
	if client == "" || len(client) > 2048 || !validRedirect(redirect) || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || len(challenge) != 43 || !validPKCE(challenge) {
		s.oauthError(w, 400, "invalid_request", "授权需要 client_id、有效回调和 S256 PKCE")
		return
	}
	registered, e := s.App.Store.GetOAuthClient(r.Context(), client)
	if e == nil {
		if !slices.Contains(registered.RedirectURIs, redirect) {
			s.oauthError(w, 400, "invalid_redirect_uri", "回调不在客户端登记列表中")
			return
		}
	} else if !errors.Is(e, domain.ErrNotFound) {
		fail(w, e)
		return
	}
	// Accept URL-shaped and other public client IDs without fetching their metadata.
	resource := q.Get("resource")
	if resource == "" {
		resource = s.origin(r) + "/mcp"
	}
	if resource != s.origin(r)+"/mcp" {
		s.oauthError(w, 400, "invalid_target", "资源地址必须为本站 MCP")
		return
	}
	if scope := q.Get("scope"); scope != "" && scope != "mcp" {
		s.oauthError(w, 400, "invalid_scope", "仅支持 mcp 范围")
		return
	}
	a := s.actor(r)
	if s.Auth != nil {
		u, e := s.Auth.User(r)
		if e != nil {
			if r.Method == "POST" {
				s.oauthError(w, 401, "login_required", "请重新发起授权并登录")
				return
			}
			http.Redirect(w, r, s.appPath("/auth/login?"+url.Values{"return_to": {r.URL.RequestURI()}}.Encode()), 303)
			return
		}
		a, e = s.userActor(r.Context(), u)
		if e != nil {
			fail(w, e)
			return
		}
	}
	account, e := s.App.Store.GetMCPAccount(r.Context(), a.ID)
	if e != nil {
		fail(w, e)
		return
	}
	callback := func(values url.Values) {
		u, _ := url.Parse(redirect)
		v := u.Query()
		for key, items := range values {
			v[key] = items
		}
		if state := q.Get("state"); state != "" {
			v.Set("state", state)
		}
		u.RawQuery = v.Encode()
		http.Redirect(w, r, u.String(), 303)
	}
	if r.Method == "POST" {
		if q.Get("decision") != "allow" {
			callback(url.Values{"error": {"access_denied"}})
			return
		}
		if !account.Enabled {
			s.oauthError(w, 403, "access_denied", "个人 MCP 尚未开通")
			return
		}
		project := q.Get("project_id")
		if e = s.App.CheckProject(r.Context(), a, project, false); e != nil {
			fail(w, e)
			return
		}
		raw := oauthSecret()
		code := domain.OAuthCode{Hash: app.Hash(raw), ClientID: client, RedirectURI: redirect, Challenge: challenge, Resource: resource, UserID: a.ID, UserName: a.Name, ProjectID: project, ExpiresAt: time.Now().Add(5 * time.Minute).UnixMilli(), AccountGeneration: account.Generation}
		if e = s.App.Store.PutOAuthCode(r.Context(), code); e != nil {
			fail(w, e)
			return
		}
		callback(url.Values{"code": {raw}})
		return
	}
	all, e := s.App.Store.ListProjects(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	projects := []domain.Project{}
	for _, p := range all {
		if app.CanRead(a, p) {
			projects = append(projects, p)
		}
	}
	fields := map[string]string{}
	for _, key := range []string{"client_id", "redirect_uri", "response_type", "code_challenge", "code_challenge_method", "state", "scope"} {
		fields[key] = q.Get(key)
	}
	fields["resource"] = resource
	// no-referrer makes form POSTs send Origin: null. Plain HTTP browsers also
	// omit Sec-Fetch-Site, so retain the origin for our same-origin consent form.
	// The POST response keeps no-referrer for the external OAuth callback.
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = consentPage.Execute(w, map[string]any{"Base": s.appPath(""), "Name": a.Name, "Client": client, "Redirect": redirect, "Enabled": account.Enabled && len(projects) > 0, "Fields": fields, "Projects": projects, "HasProjects": len(projects) > 0})
}

func (s *Server) grantActor(r *http.Request, g domain.OAuthGrant) (domain.Actor, error) {
	account, e := s.App.Store.GetMCPAccount(r.Context(), g.UserID)
	if e != nil {
		return domain.Actor{}, e
	}
	if !account.Enabled || account.Generation != g.AccountGeneration {
		return domain.Actor{}, domain.ErrForbidden
	}
	ids, e := s.App.Store.ListPlatformAdmins(r.Context())
	if e != nil {
		return domain.Actor{}, e
	}
	a := domain.Actor{ID: g.UserID, ClientID: g.ClientID, Name: g.UserName + " · MCP", Kind: "developer", UserID: g.UserID, ProjectID: g.ProjectID, PlatformAdmin: slices.Contains(ids, g.UserID)}
	if e = s.App.CheckProject(r.Context(), a, g.ProjectID, false); e != nil {
		return a, e
	}
	return a, nil
}

// refreshGrace is how long a rotated refresh token can still be redeemed.
var refreshGrace = time.Minute

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if e := r.ParseForm(); e != nil {
		s.oauthError(w, 400, "invalid_request", "请求无效")
		return
	}
	f := r.PostForm
	client := f.Get("client_id")
	if client == "" {
		if id, _, ok := r.BasicAuth(); ok {
			client = id
		}
	}
	resource := f.Get("resource")
	if resource == "" {
		resource = s.origin(r) + "/mcp"
	}
	var grant domain.OAuthGrant
	var previous string
	switch f.Get("grant_type") {
	case "authorization_code":
		verifier := f.Get("code_verifier")
		if !validPKCE(verifier) {
			s.oauthError(w, 400, "invalid_grant", "PKCE verifier 无效")
			return
		}
		hash := sha256.Sum256([]byte(verifier))
		code, e := s.App.Store.ConsumeOAuthCode(r.Context(), domain.OAuthCode{Hash: app.Hash(f.Get("code")), ClientID: client, RedirectURI: f.Get("redirect_uri"), Resource: resource, Challenge: base64.RawURLEncoding.EncodeToString(hash[:])})
		if e != nil {
			s.oauthError(w, 400, "invalid_grant", "授权码已失效或绑定信息不匹配")
			return
		}
		grant = domain.OAuthGrant{ID: domain.ID("grant_"), UserID: code.UserID, UserName: code.UserName, AccountGeneration: code.AccountGeneration, ClientID: client, ProjectID: code.ProjectID, Resource: resource, CreatedAt: domain.Now(), RefreshExpiresAt: time.Now().Add(30 * 24 * time.Hour).UnixMilli()}
	case "refresh_token":
		presented := app.Hash(f.Get("refresh_token"))
		var e error
		grant, e = s.App.Store.FindOAuthGrant(r.Context(), presented, true)
		if e != nil || grant.ClientID != client || grant.Resource != resource || grant.RefreshExpiresAt <= domain.Now() {
			s.oauthError(w, 400, "invalid_grant", "刷新令牌已失效或绑定信息不匹配")
			return
		}
		// Rotation stays conditional on the refresh hash just read, so two
		// redemptions of one token cannot both succeed.
		previous = grant.RefreshHash
		if presented == grant.RefreshHash {
			grant.PreviousRefreshHash, grant.PreviousRefreshUntil = presented, time.Now().Add(refreshGrace).UnixMilli()
		}
		// Otherwise the client is retrying a refresh whose response it lost: the
		// pair it never received is replaced and the original deadline is kept.
	default:
		s.oauthError(w, 400, "unsupported_grant_type", "仅支持 authorization_code 和 refresh_token")
		return
	}
	if _, e := s.grantActor(r, grant); e != nil {
		s.oauthError(w, 400, "invalid_grant", "MCP 已关闭或项目访问权限已撤销")
		return
	}
	access, refresh := oauthSecret(), oauthSecret()
	grant.AccessHash = app.Hash(access)
	grant.RefreshHash = app.Hash(refresh)
	grant.AccessExpiresAt = time.Now().Add(time.Hour).UnixMilli()
	if e := s.App.Store.PutOAuthGrant(r.Context(), grant, previous); e != nil {
		s.oauthError(w, 400, "invalid_grant", "授权已被撤销或刷新令牌已使用")
		return
	}
	respond(w, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600, "refresh_token": refresh, "scope": "mcp"})
}

func (s *Server) oauthRevoke(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if e := r.ParseForm(); e != nil {
		s.oauthError(w, 400, "invalid_request", "请求无效")
		return
	}
	hash := app.Hash(r.PostForm.Get("token"))
	g, e := s.App.Store.FindOAuthGrant(r.Context(), hash, false)
	if e != nil {
		g, e = s.App.Store.FindOAuthGrant(r.Context(), hash, true)
	}
	if e == nil && g.ClientID == r.PostForm.Get("client_id") {
		if e = s.App.Store.DeleteOAuthGrant(r.Context(), g.UserID, g.ID); e != nil {
			fail(w, e)
			return
		}
	}
	respond(w, map[string]bool{"ok": true})
}
