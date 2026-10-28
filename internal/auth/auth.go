// Package auth signs people in to the web workspace. auth.json picks one
// provider: the built-in accounts here, or an external one registered by the
// binary, such as a company OAuth service.
package auth

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"time"
)

// Settings are the auth.json fields every provider shares.
type Settings struct {
	Provider              string   `json:"provider"`
	PublicURL             string   `json:"public_url"`
	PlatformAdminLoginIDs []string `json:"platform_admin_login_ids"`
}

func (s *Settings) Validate() error {
	s.PublicURL = strings.TrimRight(strings.TrimSpace(s.PublicURL), "/")
	u, err := url.Parse(s.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("登录配置的 public_url 必须为有效的 HTTP(S) 地址")
	}
	// A path mounts the workspace under that prefix, behind a reverse proxy.
	if u.Path != "" && (u.RawPath != "" || path.Clean(u.Path) != u.Path) {
		return errors.New("public_url 的路径须为规范的绝对路径，例如 https://example.com/workspace")
	}
	return nil
}

// File is a loaded auth.json. Providers decode their own fields from it.
type File struct {
	Settings
	raw []byte
}

// Decode strictly decodes the whole file into a provider's configuration,
// which should embed Settings.
func (f *File) Decode(out any) error { return decode(f.raw, out, true) }

func decode(raw []byte, out any, strict bool) error {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	if strict {
		d.DisallowUnknownFields()
	}
	if err := d.Decode(out); err != nil {
		return errors.New("登录配置文件不是有效配置")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("登录配置文件只能包含一个 JSON 对象")
	}
	return nil
}

// Provider builds a sign-in method from auth.json.
type Provider func(context.Context, *File, storage.Store) (Login, error)

var providers = map[string]Provider{"local": openAccounts}

// Register makes a provider selectable by name in auth.json. Call it from init.
func Register(name string, p Provider) { providers[name] = p }

// Load reads auth.json. A missing default file keeps the loopback-only local
// mode; a present but invalid file is an error and never disables sign-in.
func Load(path string, required bool) (*File, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !required {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("无法读取登录配置文件")
	}
	f := &File{raw: b}
	if err = decode(b, &f.Settings, false); err != nil {
		return nil, err
	}
	if _, ok := providers[f.Provider]; !ok {
		names := []string{}
		for name := range providers {
			if name != "" {
				names = append(names, name)
			}
		}
		slices.Sort(names)
		if f.Provider == "" {
			return nil, fmt.Errorf("登录配置需要填写 provider，可选：%s", strings.Join(names, "、"))
		}
		return nil, fmt.Errorf("登录配置的 provider %q 不可用，可选：%s", f.Provider, strings.Join(names, "、"))
	}
	if err = f.Settings.Validate(); err != nil {
		return nil, err
	}
	return f, nil
}

// Login is what a provider contributes. Exactly one of Redirect and Accounts is set.
type Login struct {
	Redirect Redirect
	Accounts *Accounts
	// Directory lists people who can be added to projects; nil means any known user.
	Directory Directory
	// Fingerprint identifies the identity source, such as issuer and client ID.
	// Changing it invalidates sessions issued under the previous one.
	Fingerprint string
}

// Redirect signs people in through an external identity service.
type Redirect interface {
	// AccountLabel names the account people sign in with, such as "统一账号".
	AccountLabel() string
	// Begin sends the browser to the identity service; returnTo is already validated.
	Begin(w http.ResponseWriter, r *http.Request, returnTo string) error
	// Complete validates the identity service's callback.
	Complete(w http.ResponseWriter, r *http.Request) (Identity, error)
}

// Directory lists the people who can be added to projects.
type Directory interface {
	// DirectoryLabel names the directory in the UI, such as "平台账号".
	DirectoryLabel() string
	// Syncable reports whether administrators can refresh it from upstream.
	Syncable() bool
	SyncDirectory(ctx context.Context, force bool) (domain.DirectorySnapshot, error)
}

// Identity is a signed-in person and how long their session may last.
type Identity struct {
	User               domain.User
	ExpiresAt          time.Time
	ReturnTo           string
	MustChangePassword bool
	credential         string // password hash a built-in account was verified with
}

// Failure is a sign-in error code the web page explains (auth_error).
type Failure string

func (f Failure) Error() string { return string(f) }

// ReturnTo keeps only the local pages a sign-in may continue to.
func ReturnTo(raw string) string {
	if strings.HasPrefix(raw, "/oauth/authorize?") {
		return raw
	}
	return "/"
}

func digest(raw string) string { v := sha256.Sum256([]byte(raw)); return hex.EncodeToString(v[:]) }
func random() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Manager is the configured sign-in method and the workspace sessions it issues.
type Manager struct {
	Login
	settings      Settings
	store         storage.Store
	namespace     string
	SessionCookie string
}

func Open(ctx context.Context, f *File, store storage.Store) (*Manager, error) {
	login, err := providers[f.Provider](ctx, f, store)
	if err != nil {
		return nil, err
	}
	if (login.Redirect == nil) == (login.Accounts == nil) {
		return nil, errors.New("登录 provider 必须提供且只提供一种登录方式")
	}
	// The namespace does not depend on the provider name, so naming the provider
	// in an older file keeps its sessions.
	namespace := digest(login.Fingerprint + "\x00" + f.PublicURL)
	return &Manager{Login: login, settings: f.Settings, store: store, namespace: namespace, SessionCookie: "ai_pm_session_" + namespace[:10]}, nil
}

func (m *Manager) PublicURL() string  { return m.settings.PublicURL }
func (m *Manager) PublicHost() string { u, _ := url.Parse(m.settings.PublicURL); return u.Host }

// Base is the path the workspace is mounted under, empty at a site root.
func (m *Manager) Base() string { u, _ := url.Parse(m.settings.PublicURL); return u.Path }
func (m *Manager) BootstrapAdmin(loginID string) bool {
	return loginID != "" && slices.Contains(m.settings.PlatformAdminLoginIDs, loginID)
}

func (m *Manager) cookie(w http.ResponseWriter, value string, ttl time.Duration) {
	maxAge := int(ttl.Seconds())
	if ttl < 0 {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{Name: m.SessionCookie, Value: value, Path: m.Base() + "/", MaxAge: maxAge, Expires: time.Now().Add(ttl), HttpOnly: true, Secure: strings.HasPrefix(m.settings.PublicURL, "https://"), SameSite: http.SameSiteLaxMode})
}

// Issue starts a workspace session for a signed-in person and replaces the
// browser's previous one. Only a digest of the session handle is stored.
func (m *Manager) Issue(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity) error {
	if time.Until(id.ExpiresAt) < time.Second {
		return Failure("session_unavailable")
	}
	raw := random()
	session := domain.WebSession{Hash: digest(raw), Namespace: m.namespace, User: id.User, ExpiresAt: id.ExpiresAt.UnixMilli(), MustChangePassword: id.MustChangePassword}
	if err := m.store.PutUser(ctx, id.User); err != nil {
		return err
	}
	if err := m.store.PutWebSession(ctx, session); err != nil {
		return err
	}
	// Re-checking after the write closes the race with an administrator who
	// disables the account or resets its password meanwhile: that change
	// either deletes this session or is seen here.
	if id.credential != "" && m.Accounts != nil {
		if err := m.Accounts.stillValid(ctx, id); err != nil {
			_ = m.store.DeleteWebSession(ctx, session.Hash)
			return err
		}
	}
	if old, e := r.Cookie(m.SessionCookie); e == nil {
		_ = m.store.DeleteWebSession(ctx, digest(old.Value))
	}
	m.cookie(w, raw, time.Until(id.ExpiresAt))
	return nil
}

// Session returns the browser's current session, including one that may only
// be used to change its password.
func (m *Manager) Session(r *http.Request) (domain.WebSession, error) {
	cookie, err := r.Cookie(m.SessionCookie)
	if err != nil || len(cookie.Value) != 43 {
		return domain.WebSession{}, domain.ErrForbidden
	}
	session, err := m.store.GetWebSession(r.Context(), digest(cookie.Value))
	if err != nil {
		return domain.WebSession{}, err
	}
	if session.Namespace != m.namespace || session.ExpiresAt <= domain.Now() {
		return domain.WebSession{}, domain.ErrForbidden
	}
	return session, nil
}

// User returns the signed-in person for a fully usable session.
func (m *Manager) User(r *http.Request) (domain.User, error) {
	session, err := m.Session(r)
	if err != nil {
		return domain.User{}, err
	}
	if session.MustChangePassword {
		return domain.User{}, domain.ErrForbidden
	}
	return session.User, nil
}

func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) error {
	if cookie, err := r.Cookie(m.SessionCookie); err == nil {
		if err = m.store.DeleteWebSession(r.Context(), digest(cookie.Value)); err != nil {
			return errors.New("退出失败，请稍后重试")
		}
	}
	m.cookie(w, "", -time.Hour)
	return nil
}
