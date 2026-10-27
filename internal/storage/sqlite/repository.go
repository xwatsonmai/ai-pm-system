package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"errors"
)

func (d *DB) PutRepositoryCredential(c context.Context, v domain.RepositoryCredential) error {
	_, err := d.db.ExecContext(c, "INSERT INTO repository_credentials(id,owner_id,ciphertext) VALUES(?,?,?)", v.ID, v.OwnerID, v.Ciphertext)
	return err
}
func (d *DB) GetRepositoryCredential(c context.Context, id string) (domain.RepositoryCredential, error) {
	var v domain.RepositoryCredential
	err := d.db.QueryRowContext(c, "SELECT id,owner_id,ciphertext FROM repository_credentials WHERE id=?", id).Scan(&v.ID, &v.OwnerID, &v.Ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	return v, err
}
func (d *DB) RepositoryCredentialCount(c context.Context) (int, error) {
	var n int
	err := d.db.QueryRowContext(c, "SELECT COUNT(*) FROM repository_credentials").Scan(&n)
	return n, err
}
func (d *DB) PutUser(c context.Context, u domain.User) error { return d.put(c, "user", u.ID, "", u) }
func (d *DB) ListUsers(c context.Context) ([]domain.User, error) {
	return list[domain.User](c, d, "user", "")
}
