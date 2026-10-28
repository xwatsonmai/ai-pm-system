// Package notify turns work that waits on someone into IM messages and lets
// people act on it from the IM. Channels such as WeCom are plugins registered
// at startup; the core never depends on a particular IM.
package notify

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
)

// Card is one to-do as a channel shows it to one person.
type Card struct {
	// ID is unique per delivery. Channels use it as their message or task ID
	// and report it back with button clicks.
	ID      string
	Title   string // what is waiting, such as 「需求评审等你回复」
	Subject string // the requirement or bug title
	Project string
	Fields  []Field // key facts such as version and environment, at most 4
	// URL opens the to-do page through the channel's sign-in entry.
	URL string
	// Buttons holds at most 3 buttons, callback buttons first.
	Buttons []Button
	// Done replaces the buttons with a final line such as 「已由 陈晨 确认」.
	Done string
}

type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type Button struct {
	Key     string // callback key reported in Interaction; empty for a link
	Text    string
	URL     string // target of a link button
	Primary bool
}

// Interaction is a button click a channel received.
type Interaction struct {
	CardID     string
	Key        string
	ExternalID string // the person's ID in the channel
}

// ErrNotUpdatable reports that a delivered card can no longer be changed.
var ErrNotUpdatable = errors.New("消息已无法更新")

type Channel interface {
	// Send delivers a card and returns an opaque reference for Update.
	Send(ctx context.Context, externalID string, c Card) (ref string, err error)
	// Update rewrites a delivered card, typically after it was handled.
	Update(ctx context.Context, ref string, c Card) error
	// Callback serves the channel's event pushes. on handles a button click
	// and returns the card the person who clicked should now see.
	Callback(on func(context.Context, Interaction) Card) http.Handler
	// AuthorizeURL starts the channel's web authorization; it may choose a
	// silent flow inside the channel's own browser from the request.
	AuthorizeURL(r *http.Request, redirectURI, state string) string
	// Identify exchanges the authorization code for the person's ID.
	Identify(ctx context.Context, code string) (externalID string, err error)
}

// Spec describes a channel plugin: its settings and how to start it.
type Spec struct {
	Label  string // such as 「企业微信」
	Fields []FieldSpec
	// Setup lists what to enter in the channel's own console for a public URL.
	Setup func(publicURL string) []Field
	Open  func(Config) (Channel, error)
}

// FieldSpec is one setting the platform settings page renders.
type FieldSpec struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Hint        string `json:"hint,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Secret      bool   `json:"secret,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type Config struct {
	Values map[string]string // settings by FieldSpec.Key, secrets decrypted
	// PublicURL is the base URL that the channel's servers and people's
	// devices reach, without a trailing slash.
	PublicURL string
}

var (
	specsMu sync.RWMutex
	specs   = map[string]Spec{}
)

// Register makes a channel available under name, such as "wecom".
func Register(name string, s Spec) {
	specsMu.Lock()
	defer specsMu.Unlock()
	specs[name] = s
}

func lookup(name string) (Spec, bool) {
	specsMu.RLock()
	defer specsMu.RUnlock()
	s, ok := specs[name]
	return s, ok
}

// Names lists the registered channels.
func Names() []string {
	specsMu.RLock()
	defer specsMu.RUnlock()
	out := make([]string, 0, len(specs))
	for k := range specs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
