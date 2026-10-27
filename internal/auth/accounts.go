package auth

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// accountSession is how long a built-in account stays signed in.
const accountSession = 12 * time.Hour

// After maxFailures consecutive wrong passwords an account waits out lockout.
const (
	maxFailures = 5
	lockout     = 15 * time.Minute
)

var (
	usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,31}$`)
	errBadLogin     = errors.New("用户名或密码错误")
)

// Accounts are built-in sign-in accounts that platform administrators manage.
// They are also the member directory for projects.
type Accounts struct {
	store    storage.Store
	mu       sync.Mutex
	setup    string // one-time token, printed at startup while no account exists
	failures map[string]failures
}

type failures struct {
	count int
	until time.Time
}

func openAccounts(ctx context.Context, f *File, store storage.Store) (Login, error) {
	var c struct{ Settings }
	if err := f.Decode(&c); err != nil {
		return Login{}, err
	}
	a := &Accounts{store: store, failures: map[string]failures{}}
	list, err := store.ListAccounts(ctx)
	if err != nil {
		return Login{}, err
	}
	if len(list) == 0 {
		a.setup = random()
	}
	return Login{Accounts: a, Directory: a, Fingerprint: "local"}, nil
}

// SetupToken is the token for creating the first administrator, or empty once
// any account exists.
func (a *Accounts) SetupToken() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.setup
}

func normalizeUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func accountName(name, fallback string) (string, error) {
	if name = strings.TrimSpace(name); name == "" {
		name = fallback
	}
	if utf8.RuneCountInString(name) > 40 {
		return "", errors.New("姓名不能超过 40 个字")
	}
	return name, nil
}

func validPassword(p string) error {
	if n := utf8.RuneCountInString(p); n < 8 || n > 128 || strings.TrimSpace(p) == "" {
		return errors.New("密码需为 8-128 位")
	}
	return nil
}

func newAccount(username, name, password string) (domain.Account, error) {
	username = normalizeUsername(username)
	if !usernamePattern.MatchString(username) {
		return domain.Account{}, errors.New("用户名为 2-32 位小写字母、数字或 . _ -，以字母或数字开头")
	}
	name, err := accountName(name, username)
	if err != nil {
		return domain.Account{}, err
	}
	if err = validPassword(password); err != nil {
		return domain.Account{}, err
	}
	now := domain.Now()
	return domain.Account{UserID: domain.ID("usr_"), Username: username, Name: name, PasswordHash: hashPassword(password), Version: 1, CreatedAt: now, UpdatedAt: now}, nil
}

func identity(a domain.Account) Identity {
	return Identity{User: domain.User{ID: a.UserID, Name: a.Name, LoginID: a.Username}, ExpiresAt: time.Now().Add(accountSession), MustChangePassword: a.MustChangePassword, credential: a.PasswordHash}
}

// stillValid reports whether the account an identity was verified against is
// unchanged: an administrator may disable it or reset its password while the
// password is being hashed.
func (a *Accounts) stillValid(ctx context.Context, id Identity) error {
	account, err := a.store.GetAccount(ctx, id.User.ID)
	if err != nil {
		return err
	}
	if account.Disabled || account.PasswordHash != id.credential {
		return errors.New("账号状态已变化，请重新登录")
	}
	return nil
}

// Setup creates the first administrator with the token printed at startup.
func (a *Accounts) Setup(ctx context.Context, token, username, name, password string) (Identity, error) {
	want := a.SetupToken()
	if want == "" {
		return Identity{}, errors.New("平台已完成初始化，请直接登录")
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(want)) != 1 {
		return Identity{}, errors.New("初始化口令不正确，请查看服务启动日志")
	}
	account, err := newAccount(username, name, password)
	if err != nil {
		return Identity{}, err
	}
	err = a.store.CreateAccount(ctx, account, true)
	if err == nil || errors.Is(err, domain.ErrConflict) {
		a.mu.Lock()
		a.setup = ""
		a.mu.Unlock()
	}
	if errors.Is(err, domain.ErrConflict) {
		return Identity{}, errors.New("平台已完成初始化，请直接登录")
	}
	if err != nil {
		return Identity{}, err
	}
	return identity(account), nil
}

func (a *Accounts) Authenticate(ctx context.Context, username, password string) (Identity, error) {
	account, err := a.store.FindAccount(ctx, normalizeUsername(username))
	if errors.Is(err, domain.ErrNotFound) {
		checkPassword(dummyHash(), password)
		return Identity{}, errBadLogin
	}
	if err != nil {
		return Identity{}, err
	}
	if err = a.verify(account, password); err != nil {
		return Identity{}, err
	}
	if account.Disabled {
		return Identity{}, errors.New("账号已停用，请联系平台管理员")
	}
	return identity(account), nil
}

// verify checks a password and pauses the account after repeated failures.
// Each attempt is counted before hashing, so parallel guesses cannot all slip
// past the limit while earlier ones are still being checked.
func (a *Accounts) verify(account domain.Account, password string) error {
	a.mu.Lock()
	f := a.failures[account.UserID]
	if !f.until.IsZero() && !time.Now().Before(f.until) {
		f = failures{}
	}
	if f.count >= maxFailures {
		a.failures[account.UserID] = f
		a.mu.Unlock()
		return fmt.Errorf("密码错误次数过多，请 %d 分钟后再试", int(lockout.Minutes()))
	}
	f.count++
	a.failures[account.UserID] = f
	a.mu.Unlock()
	ok := checkPassword(account.PasswordHash, password)
	a.mu.Lock()
	defer a.mu.Unlock()
	if ok {
		delete(a.failures, account.UserID)
		return nil
	}
	if f = a.failures[account.UserID]; f.count >= maxFailures && f.until.IsZero() {
		f.until = time.Now().Add(lockout)
		a.failures[account.UserID] = f
	}
	return errBadLogin
}

// ChangePassword replaces the signed-in person's password. Their sessions end,
// so the caller issues a fresh one from the returned identity.
func (a *Accounts) ChangePassword(ctx context.Context, userID, current, next string) (Identity, error) {
	account, err := a.store.GetAccount(ctx, userID)
	if err != nil {
		return Identity{}, err
	}
	if account.Disabled {
		return Identity{}, domain.ErrForbidden
	}
	if err = a.verify(account, current); errors.Is(err, errBadLogin) {
		return Identity{}, errors.New("当前密码不正确")
	} else if err != nil {
		return Identity{}, err
	}
	if err = validPassword(next); err != nil {
		return Identity{}, err
	}
	if next == current {
		return Identity{}, errors.New("新密码不能与当前密码相同")
	}
	expected := account.Version
	account.PasswordHash, account.MustChangePassword = hashPassword(next), false
	account.Version, account.UpdatedAt = expected+1, domain.Now()
	if err = a.store.UpdateAccount(ctx, account, expected); err != nil {
		return Identity{}, err
	}
	return identity(account), nil
}

func (a *Accounts) List(ctx context.Context) ([]domain.Account, error) {
	return a.store.ListAccounts(ctx)
}

// Create adds an account whose holder chooses a new password at first sign-in.
func (a *Accounts) Create(ctx context.Context, username, name, password string) (domain.Account, error) {
	account, err := newAccount(username, name, password)
	if err != nil {
		return domain.Account{}, err
	}
	account.MustChangePassword = true
	if err = a.store.CreateAccount(ctx, account, false); errors.Is(err, domain.ErrConflict) {
		return domain.Account{}, errors.New("用户名已被使用")
	} else if err != nil {
		return domain.Account{}, err
	}
	return account, nil
}

// AccountChange is an administrator's edit; nil fields stay unchanged.
type AccountChange struct {
	Name     *string `json:"name"`
	Password *string `json:"password"`
	Disabled *bool   `json:"disabled"`
}

// Update applies an administrator's edit. A reset password must be replaced at
// the next sign-in, and nobody can disable their own account.
func (a *Accounts) Update(ctx context.Context, actorID, userID string, c AccountChange) (domain.Account, error) {
	account, err := a.store.GetAccount(ctx, userID)
	if err != nil {
		return domain.Account{}, err
	}
	expected := account.Version
	if c.Name != nil {
		if account.Name, err = accountName(*c.Name, account.Username); err != nil {
			return domain.Account{}, err
		}
	}
	if c.Password != nil {
		if err = validPassword(*c.Password); err != nil {
			return domain.Account{}, err
		}
		account.PasswordHash, account.MustChangePassword = hashPassword(*c.Password), true
	}
	if c.Disabled != nil {
		if *c.Disabled && userID == actorID {
			return domain.Account{}, errors.New("不能停用自己的账号")
		}
		account.Disabled = *c.Disabled
	}
	account.Version, account.UpdatedAt = expected+1, domain.Now()
	if err = a.store.UpdateAccount(ctx, account, expected); err != nil {
		return domain.Account{}, err
	}
	if c.Password != nil || c.Disabled != nil {
		a.mu.Lock()
		delete(a.failures, userID)
		a.mu.Unlock()
	}
	return account, nil
}

func (a *Accounts) DirectoryLabel() string { return "平台账号" }
func (a *Accounts) Syncable() bool         { return false }

// SyncDirectory lists active accounts; there is nothing upstream to refresh.
func (a *Accounts) SyncDirectory(ctx context.Context, _ bool) (domain.DirectorySnapshot, error) {
	list, err := a.store.ListAccounts(ctx)
	if err != nil {
		return domain.DirectorySnapshot{}, err
	}
	out := domain.DirectorySnapshot{Members: []domain.DirectoryMember{}, Roots: []domain.OrganizationUnit{}}
	for _, v := range list {
		if !v.Disabled {
			out.Members = append(out.Members, domain.DirectoryMember{User: domain.User{ID: v.UserID, Name: v.Name, LoginID: v.Username}})
		}
	}
	return out, nil
}
