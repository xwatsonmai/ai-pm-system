package notify

import "context"

// Settings configures one channel. Secret values live encrypted in the
// credential vault under SecretID and are never returned by the API.
type Settings struct {
	Channel string `json:"channel"`
	Enabled bool   `json:"enabled"`
	// SignIn lets people with a bound account open the platform from the
	// channel without signing in again.
	SignIn bool `json:"sign_in"`
	// PublicURL is reached by the channel's servers and people's devices;
	// empty means the platform's own public URL.
	PublicURL string            `json:"public_url"`
	Values    map[string]string `json:"values"`
	SecretID  string            `json:"secret_id,omitempty"`
	UpdatedBy string            `json:"updated_by,omitempty"`
	UpdatedAt int64             `json:"updated_at"`
}

// Delivery is one card for one person on one channel.
type Delivery struct {
	ID         string `json:"id"`
	TodoID     string `json:"todo_id"`
	UserID     string `json:"user_id"`
	Channel    string `json:"channel"`
	ExternalID string `json:"external_id"`
	// State is pending (to send), sent, closing (sent, then the to-do closed
	// and the card awaits its final text), closed, dropped (closed before it
	// was sent) or failed.
	State     string `json:"state"`
	Ref       string `json:"ref,omitempty"`
	Attempts  int    `json:"attempts"`
	NextAt    int64  `json:"next_at"`
	Error     string `json:"error,omitempty"`
	SentAt    int64  `json:"sent_at,omitempty"`
	UpdatedAt int64  `json:"updated_at"`
}

// Binding links a platform user to their account in a channel.
type Binding struct {
	Channel    string `json:"channel"`
	UserID     string `json:"user_id"`
	ExternalID string `json:"external_id"`
	At         int64  `json:"at"`
}

type Store interface {
	GetChannelSettings(ctx context.Context, channel string) (Settings, error)
	PutChannelSettings(ctx context.Context, s Settings) error
	GetTodo(ctx context.Context, id string) (Todo, error)
	// OpenTodos lists a task's open to-dos, or every open to-do for "".
	OpenTodos(ctx context.Context, taskID string) ([]Todo, error)
	// UserTodos lists the newest to-dos a person receives.
	UserTodos(ctx context.Context, userID string, open bool, limit int) ([]Todo, error)
	// SaveTodo stores an open to-do, replacing its recipients and facts, and
	// adds the deliveries that are not queued yet.
	SaveTodo(ctx context.Context, t Todo, deliveries []Delivery) error
	// CloseTodo closes a to-do, drops its unsent deliveries and marks sent
	// ones for their final card update, in one transaction.
	CloseTodo(ctx context.Context, id, done string, at int64) error
	TodoDeliveries(ctx context.Context, todoID string) ([]Delivery, error)
	// DueDeliveries lists pending deliveries due by now and closing ones.
	DueDeliveries(ctx context.Context, now int64, limit int) ([]Delivery, error)
	GetDelivery(ctx context.Context, id string) (Delivery, error)
	PutDelivery(ctx context.Context, d Delivery) error
	GetBinding(ctx context.Context, channel, userID string) (Binding, error)
	FindBinding(ctx context.Context, channel, externalID string) (Binding, error)
	// PutBinding replaces the user's binding; an external ID bound to someone
	// else is domain.ErrConflict.
	PutBinding(ctx context.Context, b Binding) error
	DeleteBinding(ctx context.Context, channel, userID string) error
}
