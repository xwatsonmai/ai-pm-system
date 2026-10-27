package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (d *DB) PutLoginAttempt(ctx context.Context, a domain.LoginAttempt) error {
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	if _, err = d.db.ExecContext(ctx, "DELETE FROM login_attempts WHERE expires_at<=?", domain.Now()); err != nil {
		return err
	}
	_, err = d.db.ExecContext(ctx, "INSERT INTO login_attempts(hash,browser_hash,payload,expires_at) VALUES(?,?,?,?)", a.Hash, a.BrowserHash, string(b), a.ExpiresAt)
	return err
}
func (d *DB) ConsumeLoginAttempt(ctx context.Context, hash, browser string, now int64) (domain.LoginAttempt, error) {
	var b string
	err := d.db.QueryRowContext(ctx, "DELETE FROM login_attempts WHERE hash=? AND browser_hash=? AND expires_at>? RETURNING payload", hash, browser, now).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	if err != nil {
		return domain.LoginAttempt{}, err
	}
	return decode[domain.LoginAttempt](b)
}
func (d *DB) PutWebSession(ctx context.Context, s domain.WebSession) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if _, err = d.db.ExecContext(ctx, "DELETE FROM web_sessions WHERE expires_at<=?", domain.Now()); err != nil {
		return err
	}
	_, err = d.db.ExecContext(ctx, "INSERT INTO web_sessions(hash,payload,expires_at) VALUES(?,?,?)", s.Hash, string(b), s.ExpiresAt)
	return err
}
func (d *DB) GetWebSession(ctx context.Context, hash string) (domain.WebSession, error) {
	var b string
	err := d.db.QueryRowContext(ctx, "SELECT payload FROM web_sessions WHERE hash=?", hash).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	if err != nil {
		return domain.WebSession{}, err
	}
	return decode[domain.WebSession](b)
}
func (d *DB) DeleteWebSession(ctx context.Context, hash string) error {
	_, err := d.db.ExecContext(ctx, "DELETE FROM web_sessions WHERE hash=?", hash)
	return err
}
