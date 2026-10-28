package server

import (
	"ai-pm-system/internal/agent"
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/attachment"
	"ai-pm-system/internal/auth"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/gitrepo"
	"ai-pm-system/internal/web"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Server struct {
	App   *app.Service
	Git   *gitrepo.Repositories
	Agent *agent.Manager
	Auth  *auth.Manager
}

func number(v string) int { n, _ := strconv.Atoi(v); return n }
func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
func respondStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, e error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(e, domain.ErrNotFound):
		status = 404
	case errors.Is(e, domain.ErrForbidden):
		status = 403
	case errors.Is(e, domain.ErrConflict):
		status = 409
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": e.Error()})
}
func read(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return errors.New("请求必须为单个 JSON 对象")
	}
	return nil
}
func (s *Server) Handler() http.Handler {
	if s.Git != nil {
		s.App.ValidateTestingCode = s.Git.ValidateTestingCode
	}
	mux := http.NewServeMux()
	handle := func(pattern string, handler http.HandlerFunc) { mux.Handle(pattern, s.projectAccess(handler)) }
	s.authRoutes(mux)
	s.accountRoutes(mux)
	s.platformRoutes(mux)
	s.oauthRoutes(mux)
	s.repositoryRoutes(mux)
	s.collaborationRoutes(mux)
	s.projectVersionRoutes(handle)
	handle("POST /api/projects/{id}/attachments", s.uploadAttachment)
	handle("GET /api/projects/{id}/attachments/{attachment}", s.downloadAttachment)
	handle("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		mode := "local"
		if s.Auth != nil {
			mode = "authenticated"
		}
		respond(w, map[string]any{"status": "ok", "version": "0.1.0", "database": "sqlite", "runtime": "Pi SDK 1.1.0", "mode": mode})
	})
	handle("GET /api/projects", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.visibleProjects(r)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("POST /api/projects", s.saveProject)
	handle("PUT /api/projects/{id}", s.saveProject)
	handle("GET /api/projects/{id}/context", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.App.Context(r.Context(), s.actor(r), r.PathValue("id"), "")
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("POST /api/projects/{id}/sync", func(w http.ResponseWriter, r *http.Request) {
		if e := s.Git.Sync(r.Context(), r.PathValue("id")); e != nil {
			fail(w, e)
			return
		}
		p, e := s.App.Store.GetProject(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, p)
	})
	handle("GET /api/projects/{id}/code", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		v, e := s.Git.Read(r.Context(), r.PathValue("id"), gitrepo.Query{Action: q.Get("action"), Ref: q.Get("ref"), Path: q.Get("path"), Query: q.Get("query"), Base: q.Get("base")})
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("GET /api/projects/{id}/tasks", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.App.Store.ListTasks(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("POST /api/projects/{id}/tasks", func(w http.ResponseWriter, r *http.Request) {
		var in app.TaskInput
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		v, e := s.App.CreateTask(r.Context(), s.actor(r), r.PathValue("id"), in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("GET /api/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		t, e := s.App.Store.GetTask(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		v, e := s.App.Context(r.Context(), s.actor(r), t.ProjectID, t.ID)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("PUT /api/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in app.TaskInput
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		v, e := s.App.EditTask(r.Context(), s.actor(r), r.PathValue("id"), in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("GET /api/tasks/{id}/requirement-review", s.getRequirementReview)
	handle("GET /api/projects/{id}/requirement-intakes", s.listRequirementIntakes)
	handle("POST /api/requirement-intakes/{id}/resolve", s.resolveRequirementIntake)
	handle("POST /api/tasks/{id}/requirement-review", s.saveRequirementReview)
	handle("POST /api/tasks/{id}/actions", func(w http.ResponseWriter, r *http.Request) {
		var in app.Action
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		v, e := s.App.Act(r.Context(), s.actor(r), r.PathValue("id"), in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("POST /api/projects/{id}/artifacts", func(w http.ResponseWriter, r *http.Request) {
		var a domain.Artifact
		if e := read(r, &a); e != nil {
			fail(w, e)
			return
		}
		a.ID = ""
		a.ProjectID = r.PathValue("id")
		v, e := s.App.Artifact(r.Context(), s.actor(r), a)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("GET /api/profiles", s.onlyAdmin(func(w http.ResponseWriter, r *http.Request) {
		v, e := s.App.Store.ListProfiles(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	}))
	handle("POST /api/profiles", s.onlyAdmin(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			domain.Profile
			Key string `json:"key"`
		}
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		v, e := s.App.SaveProfileAs(r.Context(), s.actor(r), in.Profile, in.Key)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	}))
	handle("GET /api/projects/{id}/tokens", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.App.Store.ListTokens(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("POST /api/projects/{id}/tokens", func(w http.ResponseWriter, r *http.Request) {
		if s.Auth != nil {
			w.WriteHeader(http.StatusGone)
			respond(w, map[string]string{"error": "请通过个人 MCP 菜单使用 OAuth 授权"})
			return
		}
		var in struct {
			Name string `json:"name"`
		}
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		t, raw, e := s.App.CreateToken(r.Context(), r.PathValue("id"), in.Name)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, map[string]any{"token": t, "secret": raw})
	})
	handle("DELETE /api/projects/{id}/tokens/{token}", func(w http.ResponseWriter, r *http.Request) {
		if e := s.App.Store.DeleteToken(r.Context(), r.PathValue("id"), r.PathValue("token")); e != nil {
			fail(w, e)
			return
		}
		respond(w, map[string]bool{"ok": true})
	})
	handle("GET /api/projects/{id}/runs", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.App.Store.ListRuns(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		project, e := s.App.Store.GetProject(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		visible := []domain.Run{}
		for _, run := range v {
			if app.CanReadRun(s.actor(r), project, run) {
				visible = append(visible, run)
			}
		}
		respond(w, visible)
	})
	handle("PATCH /api/projects/{id}/conversations/{conversation}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title string `json:"title"`
		}
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		v, e := s.Agent.EditConversation(r.Context(), s.actor(r), r.PathValue("id"), r.PathValue("conversation"), in.Title, false)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("DELETE /api/projects/{id}/conversations/{conversation}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Agent.EditConversation(r.Context(), s.actor(r), r.PathValue("id"), r.PathValue("conversation"), "", true)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("POST /api/projects/{id}/runs", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			TaskID        string   `json:"task_id"`
			Kind          string   `json:"kind"`
			Prompt        string   `json:"prompt"`
			ParentID      string   `json:"parent_id"`
			CodeRef       string   `json:"code_ref"`
			AttachmentIDs []string `json:"attachment_ids"`
		}
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		v, e := s.Agent.StartAsAtRef(r.Context(), s.actor(r), r.PathValue("id"), in.TaskID, in.Kind, in.Prompt, in.ParentID, in.CodeRef, in.AttachmentIDs...)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("GET /api/runs/{id}/events", s.streamRun)
	handle("GET /api/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.App.Store.GetRun(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, v)
	})
	handle("POST /api/runs/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]bool{"stopping": s.Agent.Stop(r.PathValue("id"))})
	})
	handle("/mcp", s.MCP)
	assets, _ := fs.Sub(web.Files, "static")
	fileServer := http.FileServer(http.FS(assets))
	mux.Handle("/", fileServer)
	protection := http.NewCrossOriginProtection()
	for _, path := range []string{"/oauth/register", "/oauth/token", "/oauth/revoke", "/mcp"} {
		protection.AddInsecureBypassPattern(path)
	}
	protected := protection.Handler(s.requireLogin(mux))
	base := s.appPath("")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A proxy forwards the mounted path whole; routes below never see the
		// prefix. Unprefixed paths still serve local and container health checks.
		if base != "" && r.URL.Path == base {
			http.Redirect(w, r, base+"/", http.StatusFound)
			return
		}
		if base != "" && strings.HasPrefix(r.URL.Path, base+"/") {
			r = stripBase(r, base)
		}
		host := r.Host
		if h, _, e := net.SplitHostPort(host); e == nil {
			host = h
		}
		// Deployment tools probe health at the container address, which is not
		// the public host behind a proxy; health reveals nothing else.
		if host != "localhost" && host != "127.0.0.1" && host != "::1" && !(s.Auth != nil && r.Host == s.Auth.PublicHost()) && r.URL.Path != "/api/health" {
			fail(w, domain.ErrForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; connect-src 'self'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			limit := int64(2 << 20)
			if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/attachments") {
				limit = attachment.MaxSize + (1 << 20)
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		if r.URL.Path == "/mcp" || r.URL.Path == "/oauth/register" || r.URL.Path == "/oauth/token" || r.URL.Path == "/oauth/revoke" || strings.HasPrefix(r.URL.Path, "/.well-known/oauth-") || r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version, MCP-Session-Id")
			w.Header().Set("Access-Control-Expose-Headers", "WWW-Authenticate, MCP-Session-Id")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		protected.ServeHTTP(w, r)
	})
}

// appPath turns an app-relative path into one a browser can follow when the
// workspace is mounted under a path.
func (s *Server) appPath(p string) string {
	if s.Auth == nil {
		return p
	}
	return s.Auth.Base() + p
}

func stripBase(r *http.Request, base string) *http.Request {
	out := new(http.Request)
	*out = *r
	out.URL = new(url.URL)
	*out.URL = *r.URL
	out.URL.Path = strings.TrimPrefix(r.URL.Path, base)
	out.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, base)
	return out
}
