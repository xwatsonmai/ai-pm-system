package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/notify"
	"ai-pm-system/internal/web"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// IM cookies live only under /integrations/: one ties a channel authorization
// to the browser that started it, the other remembers an unbound channel
// account until its owner signs in.
const (
	imStateCookie   = "ai_pm_im_state"
	imPendingCookie = "ai_pm_im_pending"
	imStateTTL      = 10 * time.Minute
)

var todoPath = regexp.MustCompile(`^/todo(/td_[0-9a-f]+(_[0-9]+)?)?$`)

func randomToken() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) notifyRoutes(mux *http.ServeMux) {
	page := func(w http.ResponseWriter, r *http.Request) {
		b, err := web.Files.ReadFile("static/todo.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page := strings.NewReplacer(`href="/`, `href="`+html.EscapeString(s.appPath("/")), `src="/`, `src="`+html.EscapeString(s.appPath("/"))).Replace(string(b))
		_, _ = w.Write([]byte(page))
	}
	mux.HandleFunc("GET /todo", page)
	mux.HandleFunc("GET /todo/{id}", page)
	if s.Notify == nil {
		return
	}
	mux.HandleFunc("GET /api/todos", s.todos)
	mux.HandleFunc("GET /api/todos/{id}", s.todo)
	mux.HandleFunc("GET /api/platform/channels", s.onlyAdmin(s.channels))
	mux.HandleFunc("PUT /api/platform/channels/{name}", s.onlyAdmin(func(w http.ResponseWriter, r *http.Request) {
		var in notify.Input
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		if in.Enabled && s.Auth == nil {
			fail(w, errors.New("消息推送需要成员登录：请先配置登录方式（auth.json），再启用渠道"))
			return
		}
		if e := s.Notify.Configure(r.Context(), r.PathValue("name"), in, s.actor(r).Name); e != nil {
			fail(w, e)
			return
		}
		s.channels(w, r)
	}))
	mux.HandleFunc("POST /api/platform/channels/{name}/test", s.onlyAdmin(func(w http.ResponseWriter, r *http.Request) {
		if e := s.Notify.Test(r.Context(), r.PathValue("name"), s.actor(r).ID); e != nil {
			fail(w, e)
			return
		}
		respond(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("GET /api/me/channels", func(w http.ResponseWriter, r *http.Request) {
		views, e := s.Notify.Views(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		out := []map[string]any{}
		for _, v := range views {
			if !v.Running {
				continue
			}
			item := map[string]any{"name": v.Name, "label": v.Label, "bound": false, "bind_url": "/integrations/" + v.Name + "/bind"}
			if b, err := s.Notify.Store.GetBinding(r.Context(), v.Name, s.actor(r).ID); err == nil {
				item["bound"], item["bound_at"] = true, b.At
			}
			out = append(out, item)
		}
		respond(w, map[string]any{"items": out})
	})
	mux.HandleFunc("DELETE /api/me/channels/{name}", func(w http.ResponseWriter, r *http.Request) {
		if e := s.Notify.Unbind(r.Context(), r.PathValue("name"), s.actor(r).ID); e != nil {
			fail(w, e)
			return
		}
		respond(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /integrations/{name}/bind", s.channelBind)
	mux.HandleFunc("GET /integrations/{name}/entry", s.channelEntry)
	mux.HandleFunc("GET /integrations/{name}/oauth", s.channelOAuth)
	mux.HandleFunc("GET /integrations/{name}/complete", s.channelComplete)
	mux.HandleFunc("/integrations/{name}/callback", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		ch, _, ok := s.Notify.Channel(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		ch.Callback(func(c context.Context, in notify.Interaction) notify.Card { return s.Notify.Interact(c, name, in) }).ServeHTTP(w, r)
	})
}

func (s *Server) channels(w http.ResponseWriter, r *http.Request) {
	views, e := s.Notify.Views(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, map[string]any{"items": views, "login": s.Auth != nil})
}

// todos lists the viewer's to-dos; without sign-in the local owner sees all.
func (s *Server) todos(w http.ResponseWriter, r *http.Request) {
	store := s.Notify.Store
	a := s.actor(r)
	open := r.URL.Query().Get("status") != "closed"
	var list []notify.Todo
	var e error
	if a.ID == "local-owner" {
		list, e = store.OpenTodos(r.Context(), "")
		if !open {
			list = nil
		}
	} else {
		list, e = store.UserTodos(r.Context(), a.ID, open, 100)
	}
	if e != nil {
		fail(w, e)
		return
	}
	out := []notify.Todo{}
	for _, t := range list {
		if s.App.CheckProject(r.Context(), a, t.ProjectID, false) == nil {
			out = append(out, t)
		}
	}
	respond(w, map[string]any{"items": out})
}

// todo returns a to-do with its task for the to-do page; any project member
// may open it, so a shared link still works.
func (s *Server) todo(w http.ResponseWriter, r *http.Request) {
	t, e := s.Notify.Store.GetTodo(r.Context(), r.PathValue("id"))
	if e != nil {
		fail(w, e)
		return
	}
	a := s.actor(r)
	if e = s.App.CheckProject(r.Context(), a, t.ProjectID, false); e != nil {
		fail(w, e)
		return
	}
	task, e := s.App.Store.GetTask(r.Context(), t.TaskID)
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, map[string]any{"todo": t, "task": task, "recipient": slices.Contains(t.Recipients, a.ID) || a.ID == "local-owner"})
}

// imChannel finds a running channel and moves the browser to its public
// address first, where the channel's authorization returns.
func (s *Server) imChannel(w http.ResponseWriter, r *http.Request) (notify.Channel, notify.Settings, string, bool) {
	ch, settings, ok := s.Notify.Channel(r.PathValue("name"))
	if !ok || s.Auth == nil {
		http.NotFound(w, r)
		return nil, settings, "", false
	}
	base := s.Notify.BaseURL(settings.Channel)
	if u, err := url.Parse(base); err == nil && r.Host != u.Host {
		http.Redirect(w, r, base+r.URL.RequestURI(), http.StatusSeeOther)
		return nil, settings, "", false
	}
	return ch, settings, base, true
}

func imBasePath(base string) string { u, _ := url.Parse(base); return u.Path }

func (s *Server) imCookie(w http.ResponseWriter, base, name, value string, ttl time.Duration) {
	maxAge := int(ttl.Seconds())
	if ttl < 0 {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: imBasePath(base) + "/integrations/", MaxAge: maxAge, HttpOnly: true, Secure: strings.HasPrefix(base, "https://"), SameSite: http.SameSiteLaxMode})
}

// imFailure shows a problem on the to-do page, which works signed in or not.
func (s *Server) imFailure(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, s.appPath("/todo?")+url.Values{"error": {message}}.Encode(), http.StatusSeeOther)
}

func (s *Server) loginURL(returnTo string) string {
	if s.Auth.Redirect != nil {
		return s.appPath("/auth/login?") + url.Values{"return_to": {returnTo}}.Encode()
	}
	return s.appPath("/?") + url.Values{"return_to": {returnTo}}.Encode()
}

// imAuthorize sends the browser to the channel's authorization; flow is
// "bind:<user>" or "entry".
func (s *Server) imAuthorize(w http.ResponseWriter, r *http.Request, ch notify.Channel, base, flow, returnTo string) {
	name := r.PathValue("name")
	state, browser := randomToken(), randomToken()
	err := s.App.Store.PutLoginAttempt(r.Context(), domain.LoginAttempt{Hash: app.Hash(state), BrowserHash: app.Hash(browser), Namespace: "im\x00" + name + "\x00" + flow, ReturnTo: returnTo, ExpiresAt: time.Now().Add(imStateTTL).UnixMilli()})
	if err != nil {
		s.imFailure(w, r, "暂时无法发起授权，请稍后重试")
		return
	}
	s.imCookie(w, base, imStateCookie, browser, imStateTTL)
	http.Redirect(w, r, ch.AuthorizeURL(r, base+"/integrations/"+name+"/oauth", state), http.StatusSeeOther)
}

// channelBind starts binding the signed-in member's channel account.
func (s *Server) channelBind(w http.ResponseWriter, r *http.Request) {
	ch, _, base, ok := s.imChannel(w, r)
	if !ok {
		return
	}
	user, err := s.Auth.User(r)
	if err != nil {
		http.Redirect(w, r, s.loginURL(r.URL.Path), http.StatusSeeOther)
		return
	}
	s.imAuthorize(w, r, ch, base, "bind:"+user.ID, "/todo")
}

// channelEntry is where cards and the channel's app home page link: it
// identifies the person through the channel, then opens the page.
func (s *Server) channelEntry(w http.ResponseWriter, r *http.Request) {
	ch, settings, base, ok := s.imChannel(w, r)
	if !ok {
		return
	}
	to := r.URL.Query().Get("to")
	if !todoPath.MatchString(to) {
		to = "/todo"
	}
	if user, err := s.Auth.User(r); err == nil {
		if _, err = s.Notify.Store.GetBinding(r.Context(), settings.Channel, user.ID); err == nil {
			http.Redirect(w, r, s.appPath(to), http.StatusSeeOther)
			return
		}
	}
	s.imAuthorize(w, r, ch, base, "entry", to)
}

func (s *Server) channelOAuth(w http.ResponseWriter, r *http.Request) {
	ch, settings, base, ok := s.imChannel(w, r)
	if !ok {
		return
	}
	name := settings.Channel
	cookie, err := r.Cookie(imStateCookie)
	if err != nil {
		s.imFailure(w, r, "授权已过期，请重新打开")
		return
	}
	s.imCookie(w, base, imStateCookie, "", -1)
	attempt, err := s.App.Store.ConsumeLoginAttempt(r.Context(), app.Hash(r.URL.Query().Get("state")), app.Hash(cookie.Value), domain.Now())
	if err != nil || !strings.HasPrefix(attempt.Namespace, "im\x00"+name+"\x00") {
		s.imFailure(w, r, "授权已过期，请重新打开")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		s.imFailure(w, r, "没有完成授权")
		return
	}
	external, err := ch.Identify(r.Context(), code)
	if err != nil {
		s.imFailure(w, r, err.Error())
		return
	}
	flow := strings.TrimPrefix(attempt.Namespace, "im\x00"+name+"\x00")
	if userID, binding := strings.CutPrefix(flow, "bind:"); binding {
		user, err := s.Auth.User(r)
		if err != nil || user.ID != userID {
			s.imFailure(w, r, "登录状态已变化，请重新绑定")
			return
		}
		if err = s.Notify.Bind(r.Context(), name, user.ID, external); err != nil {
			s.imFailure(w, r, err.Error())
			return
		}
		http.Redirect(w, r, s.appPath("/todo?")+url.Values{"bound": {name}}.Encode(), http.StatusSeeOther)
		return
	}
	s.enter(w, r, settings, base, external, attempt.ReturnTo)
}

// enter opens a page for someone the channel identified: as the member
// bound to that account, binding it first when they are signed in.
func (s *Server) enter(w http.ResponseWriter, r *http.Request, settings notify.Settings, base, external, to string) {
	name := settings.Channel
	user, signedIn := s.Auth.User(r)
	b, err := s.Notify.Store.FindBinding(r.Context(), name, external)
	switch {
	case err == nil && signedIn == nil && user.ID == b.UserID:
	case err == nil && settings.SignIn:
		if err = s.listed(r.Context(), b.UserID); err == nil {
			err = s.Auth.IssueFor(r.Context(), w, r, b.UserID)
		}
		if err != nil {
			s.imFailure(w, r, "无法用绑定的账号进入："+err.Error())
			return
		}
	case err == nil:
		http.Redirect(w, r, s.loginURL(to), http.StatusSeeOther)
		return
	case errors.Is(err, domain.ErrNotFound) && signedIn == nil:
		if err = s.Notify.Bind(r.Context(), name, user.ID, external); err != nil {
			s.imFailure(w, r, err.Error())
			return
		}
	case errors.Is(err, domain.ErrNotFound):
		// Keep the channel account until its owner signs in, then bind it.
		token := randomToken()
		if err = s.App.Store.PutLoginAttempt(r.Context(), domain.LoginAttempt{Hash: app.Hash("pending:" + token), BrowserHash: app.Hash(token), Namespace: "im\x00" + name + "\x00pending", Verifier: external, ReturnTo: to, ExpiresAt: time.Now().Add(imStateTTL).UnixMilli()}); err != nil {
			s.imFailure(w, r, "暂时无法完成绑定，请稍后重试")
			return
		}
		s.imCookie(w, base, imPendingCookie, token, imStateTTL)
		http.Redirect(w, r, s.loginURL("/integrations/"+name+"/complete"), http.StatusSeeOther)
		return
	default:
		s.imFailure(w, r, "暂时无法确认账号，请稍后重试")
		return
	}
	http.Redirect(w, r, s.appPath(to), http.StatusSeeOther)
}

// channelComplete binds the account remembered by enter once its owner signed in.
func (s *Server) channelComplete(w http.ResponseWriter, r *http.Request) {
	_, settings, base, ok := s.imChannel(w, r)
	if !ok {
		return
	}
	user, err := s.Auth.User(r)
	if err != nil {
		http.Redirect(w, r, s.loginURL(r.URL.Path), http.StatusSeeOther)
		return
	}
	cookie, err := r.Cookie(imPendingCookie)
	if err != nil {
		s.imFailure(w, r, "绑定已过期，请从"+s.channelLabel(settings.Channel)+"重新打开")
		return
	}
	s.imCookie(w, base, imPendingCookie, "", -1)
	attempt, err := s.App.Store.ConsumeLoginAttempt(r.Context(), app.Hash("pending:"+cookie.Value), app.Hash(cookie.Value), domain.Now())
	if err != nil || attempt.Namespace != "im\x00"+settings.Channel+"\x00pending" {
		s.imFailure(w, r, "绑定已过期，请从"+s.channelLabel(settings.Channel)+"重新打开")
		return
	}
	if err = s.Notify.Bind(r.Context(), settings.Channel, user.ID, attempt.Verifier); err != nil {
		s.imFailure(w, r, err.Error())
		return
	}
	to := attempt.ReturnTo
	if !todoPath.MatchString(to) {
		to = "/todo"
	}
	http.Redirect(w, r, s.appPath(to), http.StatusSeeOther)
}

func (s *Server) channelLabel(name string) string {
	if views, err := s.Notify.Views(context.Background()); err == nil {
		for _, v := range views {
			if v.Name == name {
				return v.Label
			}
		}
	}
	return "消息应用"
}

// IMAct performs a card button as the member who clicked, through the same
// service calls and checks as the workspace.
func (s *Server) IMAct(c context.Context, userID string, todo notify.Todo, requestID string) error {
	users, err := s.App.Store.ListUsers(c)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(users, func(u domain.User) bool { return u.ID == userID })
	if i < 0 {
		return domain.ErrForbidden
	}
	if err = s.listed(c, userID); err != nil {
		return err
	}
	if s.Auth != nil && s.Auth.Accounts != nil {
		if account, e := s.App.Store.GetAccount(c, userID); e != nil || account.Disabled {
			return domain.ErrForbidden
		}
	}
	actor, err := s.userActor(c, users[i])
	if err != nil {
		return err
	}
	t, err := s.App.Store.GetTask(c, todo.TaskID)
	if err != nil {
		return err
	}
	p, err := s.App.Store.GetProject(c, t.ProjectID)
	if err != nil {
		return err
	}
	var review *domain.RequirementReview
	if v, e := s.App.Store.GetRequirementReview(c, t.ID); e == nil {
		review = &v
	}
	// The action commits to what the card showed: the same work must still wait.
	if !slices.ContainsFunc(notify.Derive(t, review, p), func(d notify.Todo) bool { return d.Base == todo.Base && d.Action == todo.Action }) {
		return domain.ErrConflict
	}
	note := "通过消息卡片确认"
	switch todo.Action {
	case "confirm", "confirm_rejection":
		_, err = s.App.SaveRequirementReview(c, actor, t.ID, app.RequirementReviewInput{Action: todo.Action, RequestID: requestID, Generation: review.Generation})
	case "test_pass", "verify_bug":
		_, err = s.App.Act(c, actor, t.ID, app.Action{Action: todo.Action, RequestID: requestID, Version: t.Version, Revision: t.Revision, TestVerification: &domain.TestVerification{Revision: t.Revision, SHA: t.Submission.SHA, Environment: t.Submission.Environment, Note: note + "：在 " + t.Submission.Environment + " 验证通过"}})
	case "accept":
		checks := make([]string, len(t.Acceptance))
		for i := range checks {
			checks[i] = "passed"
		}
		_, err = s.App.Act(c, actor, t.ID, app.Action{Action: "accept", RequestID: requestID, Version: t.Version, Revision: t.Revision, Review: &domain.Review{Revision: t.Revision, SHA: t.Submission.SHA, Environment: t.Deployment.Environment, Checks: checks, Note: note + "：全部验收条件通过"}})
	default:
		err = domain.ErrConflict
	}
	return err
}
