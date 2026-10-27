package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (d *DB) GetPlatformSettings(c context.Context) (domain.PlatformSettings, error) {
	return get[domain.PlatformSettings](c, d, "platform", "settings")
}
func (d *DB) PutPlatformSettings(c context.Context, p domain.PlatformSettings) error {
	return d.put(c, "platform", "settings", "", p)
}
func (d *DB) GetDirectory(c context.Context) (domain.DirectorySnapshot, error) {
	return get[domain.DirectorySnapshot](c, d, "directory", "oa")
}
func (d *DB) PutDirectory(c context.Context, p domain.DirectorySnapshot) error {
	tx, e := d.db.BeginTx(c, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	b, e := json.Marshal(p)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(c, `INSERT INTO documents(kind,id,project_id,payload) VALUES('directory','oa','',?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload`, string(b)); e != nil {
		return e
	}
	for _, u := range p.Members {
		b, e := json.Marshal(u.User)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(c, `INSERT INTO documents(kind,id,project_id,payload) VALUES('user',?,'',?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload`, u.ID, string(b)); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (d *DB) ListPlatformAdmins(c context.Context) ([]string, error) {
	rows, e := d.db.QueryContext(c, "SELECT user_id FROM platform_admins ORDER BY user_id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (d *DB) SetPlatformAdmin(c context.Context, id string, enabled bool) error {
	if enabled {
		_, e := d.db.ExecContext(c, "INSERT OR IGNORE INTO platform_admins(user_id) VALUES(?)", id)
		return e
	}
	// The last persisted administrator cannot be removed concurrently.
	tx, e := d.db.BeginTx(c, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var n int
	if e = tx.QueryRowContext(c, "SELECT COUNT(*) FROM platform_admins").Scan(&n); e != nil {
		return e
	}
	if n <= 1 {
		return errors.New("至少保留一位平台管理员")
	}
	if _, e = tx.ExecContext(c, "DELETE FROM platform_admins WHERE user_id=?", id); e != nil {
		return e
	}
	return tx.Commit()
}
func (d *DB) GetMCPAccount(c context.Context, id string) (domain.MCPAccount, error) {
	p, e := get[domain.MCPAccount](c, d, "mcp_account", id)
	if errors.Is(e, domain.ErrNotFound) {
		return domain.MCPAccount{UserID: id}, nil
	}
	return p, e
}
func (d *DB) PutMCPAccount(c context.Context, p domain.MCPAccount) error {
	tx, e := d.db.BeginTx(c, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	b, e := json.Marshal(p)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(c, `INSERT INTO documents(kind,id,project_id,payload) VALUES('mcp_account',?,'',json_set(?,'$.generation',1)) ON CONFLICT(kind,id) DO UPDATE SET payload=json_set(excluded.payload,'$.generation',COALESCE(json_extract(documents.payload,'$.generation'),0)+CASE WHEN json_extract(documents.payload,'$.enabled')!=json_extract(excluded.payload,'$.enabled') THEN 1 ELSE 0 END)`, p.UserID, string(b)); e != nil {
		return e
	}
	if !p.Enabled {
		if _, e = tx.ExecContext(c, "DELETE FROM oauth_grants WHERE user_id=?", p.UserID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(c, "DELETE FROM oauth_codes WHERE json_extract(payload,'$.UserID')=?", p.UserID); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (d *DB) GetOAuthClient(c context.Context, id string) (domain.OAuthClient, error) {
	return get[domain.OAuthClient](c, d, "oauth_client", id)
}
func (d *DB) PutOAuthClient(c context.Context, p domain.OAuthClient) error {
	return d.put(c, "oauth_client", p.ID, "", p)
}
func (d *DB) PutOAuthCode(c context.Context, p domain.OAuthCode) error {
	b, e := json.Marshal(p)
	if e != nil {
		return e
	}
	if _, e = d.db.ExecContext(c, "DELETE FROM oauth_codes WHERE expires_at<=?", domain.Now()); e != nil {
		return e
	}
	result, e := d.db.ExecContext(c, `INSERT INTO oauth_codes(hash,client_id,redirect_uri,challenge,resource,payload,expires_at) SELECT ?,?,?,?,?,?,? FROM documents WHERE kind='mcp_account' AND id=? AND json_extract(payload,'$.enabled')=1 AND json_extract(payload,'$.generation')=?`, p.Hash, p.ClientID, p.RedirectURI, p.Challenge, p.Resource, string(b), p.ExpiresAt, p.UserID, p.AccountGeneration)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return domain.ErrForbidden
	}
	return nil
}
func (d *DB) ConsumeOAuthCode(c context.Context, p domain.OAuthCode) (domain.OAuthCode, error) {
	var b string
	e := d.db.QueryRowContext(c, "DELETE FROM oauth_codes WHERE hash=? AND client_id=? AND redirect_uri=? AND challenge=? AND resource=? AND expires_at>? RETURNING payload", p.Hash, p.ClientID, p.RedirectURI, p.Challenge, p.Resource, domain.Now()).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		e = domain.ErrForbidden
	}
	if e != nil {
		return domain.OAuthCode{}, e
	}
	return decode[domain.OAuthCode](b)
}

type storedOAuthGrant struct {
	domain.OAuthGrant
	AccessHash, RefreshHash, Resource, UserName string
	RefreshExpiresAt, AccountGeneration         int64
	PreviousRefreshHash                         string
	PreviousRefreshUntil                        int64
}

func (d *DB) PutOAuthGrant(c context.Context, p domain.OAuthGrant, previousRefresh string) error {
	b, e := json.Marshal(storedOAuthGrant{OAuthGrant: p, AccessHash: p.AccessHash, RefreshHash: p.RefreshHash, Resource: p.Resource, UserName: p.UserName, RefreshExpiresAt: p.RefreshExpiresAt, AccountGeneration: p.AccountGeneration, PreviousRefreshHash: p.PreviousRefreshHash, PreviousRefreshUntil: p.PreviousRefreshUntil})
	if e != nil {
		return e
	}
	var result sql.Result
	if previousRefresh == "" {
		result, e = d.db.ExecContext(c, `INSERT INTO oauth_grants(id,user_id,access_hash,refresh_hash,payload) SELECT ?,?,?,?,? FROM documents WHERE kind='mcp_account' AND id=? AND json_extract(payload,'$.enabled')=1 AND json_extract(payload,'$.generation')=?`, p.ID, p.UserID, p.AccessHash, p.RefreshHash, string(b), p.UserID, p.AccountGeneration)
	} else {
		result, e = d.db.ExecContext(c, `UPDATE oauth_grants SET access_hash=?,refresh_hash=?,payload=? WHERE id=? AND user_id=? AND refresh_hash=? AND EXISTS (SELECT 1 FROM documents WHERE kind='mcp_account' AND id=? AND json_extract(payload,'$.enabled')=1 AND json_extract(payload,'$.generation')=?)`, p.AccessHash, p.RefreshHash, string(b), p.ID, p.UserID, previousRefresh, p.UserID, p.AccountGeneration)
	}
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return domain.ErrForbidden
	}
	return nil
}
func decodeGrant(b string) (domain.OAuthGrant, error) {
	var v storedOAuthGrant
	e := json.Unmarshal([]byte(b), &v)
	p := v.OAuthGrant
	p.AccessHash = v.AccessHash
	p.RefreshHash = v.RefreshHash
	p.Resource = v.Resource
	p.UserName = v.UserName
	p.RefreshExpiresAt = v.RefreshExpiresAt
	p.AccountGeneration = v.AccountGeneration
	p.PreviousRefreshHash = v.PreviousRefreshHash
	p.PreviousRefreshUntil = v.PreviousRefreshUntil
	return p, e
}

// FindOAuthGrant with refresh finds the grant a refresh token can redeem now:
// its current one, or the one it just rotated away from inside the grace window.
func (d *DB) FindOAuthGrant(c context.Context, hash string, refresh bool) (domain.OAuthGrant, error) {
	var b string
	var e error
	if refresh {
		e = d.db.QueryRowContext(c, `SELECT payload FROM oauth_grants WHERE refresh_hash=? OR (json_extract(payload,'$.PreviousRefreshHash')=? AND json_extract(payload,'$.PreviousRefreshUntil')>?) ORDER BY refresh_hash=? DESC LIMIT 1`, hash, hash, domain.Now(), hash).Scan(&b)
	} else {
		e = d.db.QueryRowContext(c, "SELECT payload FROM oauth_grants WHERE access_hash=?", hash).Scan(&b)
	}
	if errors.Is(e, sql.ErrNoRows) {
		e = domain.ErrNotFound
	}
	if e != nil {
		return domain.OAuthGrant{}, e
	}
	return decodeGrant(b)
}
func (d *DB) ListOAuthGrants(c context.Context, id string) ([]domain.OAuthGrant, error) {
	rows, e := d.db.QueryContext(c, "SELECT payload FROM oauth_grants WHERE user_id=?", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.OAuthGrant{}
	for rows.Next() {
		var b string
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		p, e := decodeGrant(b)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (d *DB) DeleteOAuthGrant(c context.Context, user, id string) error {
	_, e := d.db.ExecContext(c, "DELETE FROM oauth_grants WHERE user_id=? AND id=?", user, id)
	return e
}
