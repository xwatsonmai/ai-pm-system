package sqlite

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/notify"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func (d *DB) GetChannelSettings(c context.Context, channel string) (notify.Settings, error) {
	return get[notify.Settings](c, d, "channel", channel)
}
func (d *DB) PutChannelSettings(c context.Context, s notify.Settings) error {
	return d.put(c, "channel", s.Channel, "", s)
}

func (d *DB) GetTodo(c context.Context, id string) (notify.Todo, error) {
	var s string
	err := d.db.QueryRowContext(c, "SELECT payload FROM notify_todos WHERE id=?", id).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return notify.Todo{}, domain.ErrNotFound
	}
	if err != nil {
		return notify.Todo{}, err
	}
	return decode[notify.Todo](s)
}

func (d *DB) todos(c context.Context, q string, args ...any) ([]notify.Todo, error) {
	rows, err := d.db.QueryContext(c, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []notify.Todo{}
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			return nil, err
		}
		v, e := decode[notify.Todo](s)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) OpenTodos(c context.Context, task string) ([]notify.Todo, error) {
	if task == "" {
		return d.todos(c, "SELECT payload FROM notify_todos WHERE open=1 ORDER BY created_at")
	}
	return d.todos(c, "SELECT payload FROM notify_todos WHERE task_id=? AND open=1 ORDER BY created_at", task)
}

func (d *DB) UserTodos(c context.Context, user string, open bool, limit int) ([]notify.Todo, error) {
	return d.todos(c, "SELECT t.payload FROM notify_recipients r JOIN notify_todos t ON t.id=r.todo_id WHERE r.user_id=? AND t.open=? ORDER BY t.created_at DESC LIMIT ?", user, open, limit)
}

func (d *DB) SaveTodo(c context.Context, t notify.Todo, deliveries []notify.Delivery) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	tx, err := d.db.BeginTx(c, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A closed to-do keeps its final state; the same work waiting again gets a new ID.
	var open bool
	if err = tx.QueryRowContext(c, "SELECT open FROM notify_todos WHERE id=?", t.ID).Scan(&open); err == nil && !open {
		return nil
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(c, "INSERT INTO notify_todos(id,task_id,open,created_at,payload) VALUES(?,?,1,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", t.ID, t.TaskID, t.CreatedAt, string(b)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(c, "DELETE FROM notify_recipients WHERE todo_id=?", t.ID); err != nil {
		return err
	}
	for _, u := range t.Recipients {
		if _, err = tx.ExecContext(c, "INSERT OR IGNORE INTO notify_recipients(todo_id,user_id) VALUES(?,?)", t.ID, u); err != nil {
			return err
		}
	}
	for _, v := range deliveries {
		p, e := json.Marshal(v)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(c, "INSERT OR IGNORE INTO notify_deliveries(id,todo_id,state,next_at,payload) VALUES(?,?,?,?,?)", v.ID, v.TodoID, v.State, v.NextAt, string(p)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) CloseTodo(c context.Context, id, done string, at int64) error {
	tx, err := d.db.BeginTx(c, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var s string
	if err = tx.QueryRowContext(c, "SELECT payload FROM notify_todos WHERE id=? AND open=1", id).Scan(&s); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	t, err := decode[notify.Todo](s)
	if err != nil {
		return err
	}
	t.Open, t.Done, t.ClosedAt = false, done, at
	b, _ := json.Marshal(t)
	if _, err = tx.ExecContext(c, "UPDATE notify_todos SET open=0,payload=? WHERE id=?", string(b), id); err != nil {
		return err
	}
	rows, err := tx.QueryContext(c, "SELECT payload FROM notify_deliveries WHERE todo_id=? AND state IN ('pending','sent')", id)
	if err != nil {
		return err
	}
	pending := []notify.Delivery{}
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		v, e := decode[notify.Delivery](p)
		if e != nil {
			rows.Close()
			return e
		}
		pending = append(pending, v)
	}
	rows.Close()
	for _, v := range pending {
		v.State, v.NextAt, v.UpdatedAt = map[string]string{"pending": "dropped", "sent": "closing"}[v.State], at, at
		if err = putDelivery(c, tx, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func putDelivery(c context.Context, x executor, v notify.Delivery) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = x.ExecContext(c, "UPDATE notify_deliveries SET state=?,next_at=?,payload=? WHERE id=?", v.State, v.NextAt, string(b), v.ID)
	return err
}

func (d *DB) deliveries(c context.Context, q string, args ...any) ([]notify.Delivery, error) {
	rows, err := d.db.QueryContext(c, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []notify.Delivery{}
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			return nil, err
		}
		v, e := decode[notify.Delivery](s)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) TodoDeliveries(c context.Context, todo string) ([]notify.Delivery, error) {
	return d.deliveries(c, "SELECT payload FROM notify_deliveries WHERE todo_id=? ORDER BY rowid", todo)
}
func (d *DB) DueDeliveries(c context.Context, now int64, limit int) ([]notify.Delivery, error) {
	return d.deliveries(c, "SELECT payload FROM notify_deliveries WHERE state IN ('pending','closing') AND next_at<=? ORDER BY next_at LIMIT ?", now, limit)
}
func (d *DB) GetDelivery(c context.Context, id string) (notify.Delivery, error) {
	out, err := d.deliveries(c, "SELECT payload FROM notify_deliveries WHERE id=?", id)
	if err == nil && len(out) == 0 {
		err = domain.ErrNotFound
	}
	if err != nil {
		return notify.Delivery{}, err
	}
	return out[0], nil
}
func (d *DB) PutDelivery(c context.Context, v notify.Delivery) error { return putDelivery(c, d.db, v) }

func (d *DB) binding(c context.Context, q string, args ...any) (notify.Binding, error) {
	var b notify.Binding
	err := d.db.QueryRowContext(c, q, args...).Scan(&b.Channel, &b.UserID, &b.ExternalID, &b.At)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	return b, err
}
func (d *DB) GetBinding(c context.Context, channel, user string) (notify.Binding, error) {
	return d.binding(c, "SELECT channel,user_id,external_id,at FROM channel_bindings WHERE channel=? AND user_id=?", channel, user)
}
func (d *DB) FindBinding(c context.Context, channel, external string) (notify.Binding, error) {
	return d.binding(c, "SELECT channel,user_id,external_id,at FROM channel_bindings WHERE channel=? AND external_id=?", channel, external)
}
func (d *DB) PutBinding(c context.Context, b notify.Binding) error {
	_, err := d.db.ExecContext(c, "INSERT INTO channel_bindings(channel,user_id,external_id,at) VALUES(?,?,?,?) ON CONFLICT(channel,user_id) DO UPDATE SET external_id=excluded.external_id,at=excluded.at", b.Channel, b.UserID, b.ExternalID, b.At)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return domain.ErrConflict
	}
	return err
}
func (d *DB) DeleteBinding(c context.Context, channel, user string) error {
	_, err := d.db.ExecContext(c, "DELETE FROM channel_bindings WHERE channel=? AND user_id=?", channel, user)
	return err
}
