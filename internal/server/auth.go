package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/auth"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

type actorKey struct{}

func (s *Server) actor(r *http.Request) domain.Actor {
	if a, ok := r.Context().Value(actorKey{}).(domain.Actor); ok {
		return a
	}
	if s.Auth == nil {
		return app.Human()
	}
	return domain.Actor{} // Never grant local-owner privileges in authenticated mode.
}

// publicAuthPaths answer without a usable session: signing in, and the steps a
// session waiting for a password change may still take (it checks its own).
var publicAuthPaths = []string{"/api/health", "/api/auth/session", "/api/auth/login", "/api/auth/setup", "/api/auth/password", "/api/auth/logout"}

func loginRequired(w http.ResponseWriter) {
	respondStatus(w, http.StatusUnauthorized, map[string]string{"error": "请先登录", "code": "login_required"})
}

func (s *Server) authRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/session", func(w http.ResponseWriter, r *http.Request) {
		if s.Auth == nil {
			respond(w, map[string]any{"enabled": false, "authenticated": true, "platform_admin": true, "user": domain.User{ID: "local-owner", Name: "项目负责人"}})
			return
		}
		out := map[string]any{"enabled": true, "authenticated": false}
		if s.Auth.Redirect != nil {
			out["login"], out["login_label"] = "redirect", s.Auth.Redirect.AccountLabel()
		} else {
			out["login"], out["setup_required"] = "password", s.Auth.Accounts.SetupToken() != ""
		}
		session, err := s.Auth.Session(r)
		if err != nil {
			respond(w, out)
			return
		}
		out["authenticated"], out["user"] = true, session.User
		if session.MustChangePassword {
			out["must_change_password"] = true
			respond(w, out)
			return
		}
		// External providers refresh the user record from the session; built-in
		// accounts own it, and a stale session must not undo a rename.
		if s.Auth.Accounts == nil {
			if err := s.App.Store.PutUser(r.Context(), session.User); err != nil {
				fail(w, err)
				return
			}
		}
		a, e := s.userActor(r.Context(), session.User)
		if e != nil {
			fail(w, e)
			return
		}
		out["platform_admin"] = a.PlatformAdmin
		if d := s.Auth.Directory; d != nil {
			out["directory"] = map[string]any{"label": d.DirectoryLabel(), "syncable": d.Syncable()}
		}
		respond(w, out)
	})
	mux.HandleFunc("GET /auth/login", func(w http.ResponseWriter, r *http.Request) {
		if s.Auth == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		// Always establish the browser binding on the configured public host.
		if r.Host != s.Auth.PublicHost() {
			http.Redirect(w, r, s.Auth.PublicURL()+r.URL.RequestURI(), http.StatusSeeOther)
			return
		}
		returnTo := auth.ReturnTo(r.URL.Query().Get("return_to"))
		if s.Auth.Redirect == nil {
			to := "/"
			if returnTo != "/" {
				to += "?" + url.Values{"return_to": {returnTo}}.Encode()
			}
			http.Redirect(w, r, s.appPath(to), http.StatusSeeOther)
			return
		}
		if err := s.Auth.Redirect.Begin(w, r, returnTo); err != nil {
			s.authFailure(w, r, err)
		}
	})
	mux.HandleFunc("GET /auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if s.Auth == nil || s.Auth.Redirect == nil {
			http.NotFound(w, r)
			return
		}
		id, err := s.Auth.Redirect.Complete(w, r)
		if err == nil {
			err = s.Auth.Issue(r.Context(), w, r, id)
		}
		if err != nil {
			s.authFailure(w, r, err)
			return
		}
		http.Redirect(w, r, s.appPath(auth.ReturnTo(id.ReturnTo)), http.StatusSeeOther)
	})
	mux.HandleFunc("POST /api/auth/login", s.withAccounts(func(w http.ResponseWriter, r *http.Request, accounts *auth.Accounts) {
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
			ReturnTo string `json:"return_to"`
		}
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		s.signIn(w, r, auth.ReturnTo(in.ReturnTo))(accounts.Authenticate(r.Context(), in.Username, in.Password))
	}))
	mux.HandleFunc("POST /api/auth/setup", s.withAccounts(func(w http.ResponseWriter, r *http.Request, accounts *auth.Accounts) {
		var in struct {
			Token    string `json:"token"`
			Username string `json:"username"`
			Name     string `json:"name"`
			Password string `json:"password"`
		}
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		s.signIn(w, r, "/")(accounts.Setup(r.Context(), in.Token, in.Username, in.Name, in.Password))
	}))
	mux.HandleFunc("POST /api/auth/password", s.withAccounts(func(w http.ResponseWriter, r *http.Request, accounts *auth.Accounts) {
		var in struct {
			Current  string `json:"current_password"`
			Password string `json:"new_password"`
			ReturnTo string `json:"return_to"`
		}
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		session, err := s.Auth.Session(r)
		if err != nil {
			loginRequired(w)
			return
		}
		s.signIn(w, r, auth.ReturnTo(in.ReturnTo))(accounts.ChangePassword(r.Context(), session.User.ID, in.Current, in.Password))
	}))
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if s.Auth != nil {
			if err := s.Auth.Logout(w, r); err != nil {
				fail(w, err)
				return
			}
		}
		respond(w, map[string]bool{"ok": true})
	})
}

func (s *Server) authFailure(w http.ResponseWriter, r *http.Request, err error) {
	code := "session_unavailable"
	if f := auth.Failure(""); errors.As(err, &f) {
		code = string(f)
	}
	http.Redirect(w, r, s.appPath("/?auth_error="+code), http.StatusSeeOther)
}

// withAccounts serves endpoints that exist only for built-in accounts.
func (s *Server) withAccounts(next func(http.ResponseWriter, *http.Request, *auth.Accounts)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Auth == nil || s.Auth.Accounts == nil {
			respondStatus(w, http.StatusNotFound, map[string]string{"error": "当前登录方式不使用平台账号"})
			return
		}
		next(w, r, s.Auth.Accounts)
	}
}

// signIn issues a session for a verified account and tells the page where to go.
func (s *Server) signIn(w http.ResponseWriter, r *http.Request, returnTo string) func(auth.Identity, error) {
	return func(id auth.Identity, err error) {
		if err == nil {
			err = s.Auth.Issue(r.Context(), w, r, id)
		}
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, map[string]any{"redirect": s.appPath(returnTo), "must_change_password": id.MustChangePassword})
	}
}

func (s *Server) requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Auth == nil || !strings.HasPrefix(r.URL.Path, "/api/") || slices.Contains(publicAuthPaths, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		user, err := s.Auth.User(r)
		if err != nil {
			loginRequired(w)
			return
		}
		actor, e := s.userActor(r.Context(), user)
		if e != nil {
			fail(w, e)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, actor)))
	})
}

func (s *Server) userActor(c context.Context, u domain.User) (domain.Actor, error) {
	a := domain.Actor{ID: u.ID, Name: u.Name, Kind: "human"}
	ids, e := s.App.Store.ListPlatformAdmins(c)
	if e != nil {
		return a, e
	}
	if s.Auth != nil && s.Auth.BootstrapAdmin(u.LoginID) && !slices.Contains(ids, u.ID) {
		if e = s.App.Store.SetPlatformAdmin(c, u.ID, true); e != nil {
			return a, e
		}
		ids = append(ids, u.ID)
	}
	a.PlatformAdmin = slices.Contains(ids, u.ID)
	return a, nil
}
func platformAdmin(a domain.Actor) bool {
	return a.Kind == "human" && (a.PlatformAdmin || a.ID == "local-owner")
}
func (s *Server) onlyAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !platformAdmin(s.actor(r)) {
			fail(w, domain.ErrForbidden)
			return
		}
		next(w, r)
	}
}

// listed rejects people the configured directory no longer lists, so an old
// user record cannot be granted new access.
func (s *Server) listed(c context.Context, userID string) error {
	if s.Auth == nil || s.Auth.Directory == nil {
		return nil
	}
	snapshot, e := s.Auth.Directory.SyncDirectory(c, false)
	if e != nil {
		return e
	}
	if !slices.ContainsFunc(snapshot.Members, func(u domain.DirectoryMember) bool { return u.ID == userID }) {
		return errors.New(s.Auth.Directory.DirectoryLabel() + "中没有该成员，或其已停用")
	}
	return nil
}

func (s *Server) accountRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/platform/accounts", s.onlyAdmin(s.withAccounts(func(w http.ResponseWriter, r *http.Request, accounts *auth.Accounts) {
		list, e := accounts.List(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, list)
	})))
	mux.HandleFunc("POST /api/platform/accounts", s.onlyAdmin(s.withAccounts(func(w http.ResponseWriter, r *http.Request, accounts *auth.Accounts) {
		var in struct {
			Username string `json:"username"`
			Name     string `json:"name"`
			Password string `json:"password"`
		}
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		account, e := accounts.Create(r.Context(), in.Username, in.Name, in.Password)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, account)
	})))
	mux.HandleFunc("PATCH /api/platform/accounts/{user}", s.onlyAdmin(s.withAccounts(func(w http.ResponseWriter, r *http.Request, accounts *auth.Accounts) {
		var in auth.AccountChange
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		account, e := accounts.Update(r.Context(), s.actor(r).ID, r.PathValue("user"), in)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, account)
	})))
}
