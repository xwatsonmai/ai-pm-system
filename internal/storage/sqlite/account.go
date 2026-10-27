package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const accountColumns = "user_id,username,name,password_hash,disabled,must_change_password,version,created_at,updated_at"

func scanAccount(row interface{ Scan(...any) error }) (domain.Account, error) {
	var a domain.Account
	err := row.Scan(&a.UserID, &a.Username, &a.Name, &a.PasswordHash, &a.Disabled, &a.MustChangePassword, &a.Version, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	return a, err
}

func putUserTx(c context.Context, tx *sql.Tx, u domain.User) error {
	b, e := json.Marshal(u)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(c, `INSERT INTO documents(kind,id,project_id,payload) VALUES('user',?,'',?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload`, u.ID, string(b))
	return e
}

func accountUser(a domain.Account) domain.User {
	return domain.User{ID: a.UserID, Name: a.Name, LoginID: a.Username}
}

func (d *DB) CreateAccount(c context.Context, a domain.Account, firstAdmin bool) error {
	tx, e := d.db.BeginTx(c, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if firstAdmin {
		var n int
		if e = tx.QueryRowContext(c, "SELECT COUNT(*) FROM accounts").Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return domain.ErrConflict
		}
		if _, e = tx.ExecContext(c, "INSERT OR IGNORE INTO platform_admins(user_id) VALUES(?)", a.UserID); e != nil {
			return e
		}
	}
	result, e := tx.ExecContext(c, "INSERT INTO accounts("+accountColumns+") VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING", a.UserID, a.Username, a.Name, a.PasswordHash, a.Disabled, a.MustChangePassword, a.Version, a.CreatedAt, a.UpdatedAt)
	if e != nil {
		return e
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return domain.ErrConflict
	}
	if e = putUserTx(c, tx, accountUser(a)); e != nil {
		return e
	}
	return tx.Commit()
}
func (d *DB) GetAccount(c context.Context, userID string) (domain.Account, error) {
	return scanAccount(d.db.QueryRowContext(c, "SELECT "+accountColumns+" FROM accounts WHERE user_id=?", userID))
}
func (d *DB) FindAccount(c context.Context, username string) (domain.Account, error) {
	return scanAccount(d.db.QueryRowContext(c, "SELECT "+accountColumns+" FROM accounts WHERE username=?", username))
}
func (d *DB) ListAccounts(c context.Context) ([]domain.Account, error) {
	rows, e := d.db.QueryContext(c, "SELECT "+accountColumns+" FROM accounts ORDER BY created_at,username")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Account{}
	for rows.Next() {
		a, e := scanAccount(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (d *DB) UpdateAccount(c context.Context, a domain.Account, expected int) error {
	tx, e := d.db.BeginTx(c, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var oldHash string
	if e = tx.QueryRowContext(c, "SELECT password_hash FROM accounts WHERE user_id=? AND version=?", a.UserID, expected).Scan(&oldHash); errors.Is(e, sql.ErrNoRows) {
		return domain.ErrConflict
	} else if e != nil {
		return e
	}
	if _, e = tx.ExecContext(c, "UPDATE accounts SET name=?,password_hash=?,disabled=?,must_change_password=?,version=?,updated_at=? WHERE user_id=?", a.Name, a.PasswordHash, a.Disabled, a.MustChangePassword, a.Version, a.UpdatedAt, a.UserID); e != nil {
		return e
	}
	if e = putUserTx(c, tx, accountUser(a)); e != nil {
		return e
	}
	if a.Disabled || a.PasswordHash != oldHash {
		if _, e = tx.ExecContext(c, "DELETE FROM web_sessions WHERE json_extract(payload,'$.User.id')=?", a.UserID); e != nil {
			return e
		}
	}
	// An administrator's reset (a new password that must be changed) treats the
	// old one as compromised, so MCP clients authorized with it lose access too.
	if a.Disabled || (a.MustChangePassword && a.PasswordHash != oldHash) {
		if _, e = tx.ExecContext(c, "DELETE FROM oauth_grants WHERE user_id=?", a.UserID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(c, "DELETE FROM oauth_codes WHERE json_extract(payload,'$.UserID')=?", a.UserID); e != nil {
			return e
		}
	}
	return tx.Commit()
}
