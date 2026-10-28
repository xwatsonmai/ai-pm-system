// Package sqlite is the SQLite adapter. A MySQL adapter can implement storage.Store
// independently; SQL, migrations, connection setup and driver errors stay here.
package sqlite

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
)

type DB struct{ db *sql.DB }

var _ storage.Store = (*DB)(nil)

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	d := &DB{db: db}
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;`); err != nil {
		db.Close()
		return nil, err
	}
	if err = d.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}
func (d *DB) Close() error { return d.db.Close() }
func (d *DB) migrate() error {
	_, err := d.db.Exec(`
 CREATE TABLE IF NOT EXISTS repository_credentials (id TEXT PRIMARY KEY,owner_id TEXT NOT NULL,ciphertext BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY);
 CREATE TABLE IF NOT EXISTS documents (kind TEXT NOT NULL,id TEXT NOT NULL,project_id TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(kind,id));
 CREATE INDEX IF NOT EXISTS documents_project ON documents(kind,project_id);
 CREATE TABLE IF NOT EXISTS tasks (id TEXT PRIMARY KEY,project_id TEXT NOT NULL,version INTEGER NOT NULL,claim_hash TEXT NOT NULL,payload TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS current_milestones (project_id TEXT PRIMARY KEY,version_id TEXT NOT NULL,version INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS project_versions (id TEXT PRIMARY KEY,project_id TEXT NOT NULL,name_key TEXT NOT NULL,version INTEGER NOT NULL,payload TEXT NOT NULL,UNIQUE(project_id,name_key));
 CREATE INDEX IF NOT EXISTS project_versions_project ON project_versions(project_id);
 CREATE INDEX IF NOT EXISTS tasks_project ON tasks(project_id);
 CREATE TABLE IF NOT EXISTS requirement_iterations (task_id TEXT PRIMARY KEY,project_id TEXT NOT NULL,root_task_id TEXT NOT NULL,parent_task_id TEXT NOT NULL,at INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS requirement_iterations_root ON requirement_iterations(project_id,root_task_id,at,task_id);
 CREATE TABLE IF NOT EXISTS requirement_reviews (task_id TEXT PRIMARY KEY,version INTEGER NOT NULL,payload TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS requirement_intakes (id TEXT PRIMARY KEY,project_id TEXT NOT NULL,version INTEGER NOT NULL,payload TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS requirement_intakes_project ON requirement_intakes(project_id);
 CREATE TABLE IF NOT EXISTS collaborations (id TEXT PRIMARY KEY,project_id TEXT NOT NULL,version INTEGER NOT NULL,claim_hash TEXT NOT NULL,payload TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS collaborations_project ON collaborations(project_id);
 CREATE TABLE IF NOT EXISTS reply_jobs (id TEXT PRIMARY KEY,version INTEGER NOT NULL,payload TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS events (seq INTEGER PRIMARY KEY AUTOINCREMENT,project_id TEXT NOT NULL,task_id TEXT NOT NULL,kind TEXT NOT NULL,actor TEXT NOT NULL,body TEXT NOT NULL,data TEXT NOT NULL,at INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS events_project ON events(project_id,seq);
 CREATE TABLE IF NOT EXISTS receipts (key TEXT PRIMARY KEY,fingerprint TEXT NOT NULL,response BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS secrets (id TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS tokens (id TEXT PRIMARY KEY,project_id TEXT NOT NULL,name TEXT NOT NULL,hash TEXT UNIQUE NOT NULL,at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS attachments (id TEXT PRIMARY KEY,project_id TEXT NOT NULL,payload TEXT NOT NULL,content BLOB NOT NULL,extracted_text TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS attachments_project ON attachments(project_id);
 CREATE TABLE IF NOT EXISTS login_attempts (hash TEXT PRIMARY KEY,browser_hash TEXT NOT NULL,payload TEXT NOT NULL,expires_at INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS login_attempts_expiry ON login_attempts(expires_at);
 CREATE TABLE IF NOT EXISTS web_sessions (hash TEXT PRIMARY KEY,payload TEXT NOT NULL,expires_at INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS web_sessions_expiry ON web_sessions(expires_at);
 CREATE TABLE IF NOT EXISTS platform_admins (user_id TEXT PRIMARY KEY);
 CREATE TABLE IF NOT EXISTS oauth_codes (hash TEXT PRIMARY KEY,client_id TEXT NOT NULL,redirect_uri TEXT NOT NULL,challenge TEXT NOT NULL,resource TEXT NOT NULL,payload TEXT NOT NULL,expires_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS oauth_grants (id TEXT PRIMARY KEY,user_id TEXT NOT NULL,access_hash TEXT UNIQUE NOT NULL,refresh_hash TEXT UNIQUE NOT NULL,payload TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS oauth_grants_user ON oauth_grants(user_id);
 INSERT OR IGNORE INTO schema_migrations(version) VALUES(1);
 INSERT OR IGNORE INTO schema_migrations(version) VALUES(2);
 INSERT OR IGNORE INTO schema_migrations(version) VALUES(3);
 INSERT OR IGNORE INTO schema_migrations(version) VALUES(4);
 INSERT OR IGNORE INTO documents(kind,id,project_id,payload)
 SELECT 'platform','settings','',json_object('profile_id',COALESCE((SELECT json_extract(payload,'$.profile_id') FROM documents WHERE kind='project' AND COALESCE(json_extract(payload,'$.profile_id'),'')<>'' ORDER BY rowid DESC LIMIT 1),''))
 WHERE NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version=5);
 INSERT OR IGNORE INTO schema_migrations(version) VALUES(5);
 INSERT OR IGNORE INTO schema_migrations(version) VALUES(6);
 INSERT OR IGNORE INTO schema_migrations(version) VALUES(7);
 INSERT OR IGNORE INTO schema_migrations(version) VALUES(8);
 `)
	if err != nil {
		return err
	}
	if err = d.migrateReleaseStage(); err != nil {
		return err
	}
	if _, err = d.db.Exec("INSERT OR IGNORE INTO schema_migrations(version) VALUES(10)"); err != nil {
		return err
	}
	if err = d.migrateMilestones(); err != nil {
		return err
	}
	if _, err = d.db.Exec("INSERT OR IGNORE INTO schema_migrations(version) VALUES(12)"); err != nil {
		return err
	}
	// Version 13 adds built-in sign-in accounts.
	_, err = d.db.Exec(`
 CREATE TABLE IF NOT EXISTS accounts (user_id TEXT PRIMARY KEY,username TEXT UNIQUE NOT NULL,name TEXT NOT NULL,password_hash TEXT NOT NULL,disabled INTEGER NOT NULL,must_change_password INTEGER NOT NULL,version INTEGER NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL);
 INSERT OR IGNORE INTO schema_migrations(version) VALUES(13);
 `)
	if err != nil {
		return err
	}
	// Version 14 adds to-dos, their IM deliveries and account bindings.
	_, err = d.db.Exec(`
 CREATE TABLE IF NOT EXISTS notify_todos (id TEXT PRIMARY KEY,task_id TEXT NOT NULL,open INTEGER NOT NULL,created_at INTEGER NOT NULL,payload TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS notify_todos_task ON notify_todos(task_id,open);
 CREATE TABLE IF NOT EXISTS notify_recipients (todo_id TEXT NOT NULL,user_id TEXT NOT NULL,PRIMARY KEY(todo_id,user_id));
 CREATE INDEX IF NOT EXISTS notify_recipients_user ON notify_recipients(user_id);
 CREATE TABLE IF NOT EXISTS notify_deliveries (id TEXT PRIMARY KEY,todo_id TEXT NOT NULL,state TEXT NOT NULL,next_at INTEGER NOT NULL,payload TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS notify_deliveries_due ON notify_deliveries(state,next_at);
 CREATE INDEX IF NOT EXISTS notify_deliveries_todo ON notify_deliveries(todo_id);
 CREATE TABLE IF NOT EXISTS channel_bindings (channel TEXT NOT NULL,user_id TEXT NOT NULL,external_id TEXT NOT NULL,at INTEGER NOT NULL,PRIMARY KEY(channel,user_id),UNIQUE(channel,external_id));
 INSERT OR IGNORE INTO schema_migrations(version) VALUES(14);
 `)
	return err
}
func decode[T any](s string) (T, error) { var v T; err := json.Unmarshal([]byte(s), &v); return v, err }
func get[T any](ctx context.Context, d *DB, kind, id string) (T, error) {
	var s string
	err := d.db.QueryRowContext(ctx, "SELECT payload FROM documents WHERE kind=? AND id=?", kind, id).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	if err != nil {
		var v T
		return v, err
	}
	return decode[T](s)
}
func list[T any](ctx context.Context, d *DB, kind, project string) ([]T, error) {
	q := "SELECT payload FROM documents WHERE kind=?"
	args := []any{kind}
	if project != "" {
		q += " AND project_id=?"
		args = append(args, project)
	}
	q += " ORDER BY rowid DESC"
	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			return nil, err
		}
		v, e := decode[T](s)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (d *DB) put(ctx context.Context, kind, id, project string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = d.db.ExecContext(ctx, `INSERT INTO documents(kind,id,project_id,payload) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET project_id=excluded.project_id,payload=excluded.payload`, kind, id, project, string(b))
	return err
}
func (d *DB) ListProjects(c context.Context) ([]domain.Project, error) {
	return list[domain.Project](c, d, "project", "")
}
func (d *DB) GetProject(c context.Context, id string) (domain.Project, error) {
	return get[domain.Project](c, d, "project", id)
}
func (d *DB) PutProject(c context.Context, p domain.Project) error {
	return d.put(c, "project", p.ID, p.ID, p)
}
func (d *DB) ListArtifacts(c context.Context, p string) ([]domain.Artifact, error) {
	return list[domain.Artifact](c, d, "artifact", p)
}
func (d *DB) GetArtifact(c context.Context, id string) (domain.Artifact, error) {
	return get[domain.Artifact](c, d, "artifact", id)
}
func (d *DB) PutArtifact(c context.Context, a domain.Artifact) error {
	if a.Kind == "git" && a.Source == "git" {
		return d.put(c, "artifact", a.ID, a.ProjectID, a)
	}
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = d.db.ExecContext(c, `INSERT INTO documents(kind,id,project_id,payload) VALUES('artifact',?,?,?) ON CONFLICT(kind,id) DO NOTHING`, a.ID, a.ProjectID, string(b))
	if err != nil {
		return err
	}
	old, err := d.GetArtifact(c, a.ID)
	if err != nil {
		return err
	}
	old.At, a.At = 0, 0
	b, _ = json.Marshal(a)
	previous, _ := json.Marshal(old)
	if !bytes.Equal(previous, b) {
		return domain.ErrConflict
	}
	return nil
}
func (d *DB) ListRuns(c context.Context, p string) ([]domain.Run, error) {
	runs, err := list[domain.Run](c, d, "run", p)
	if err != nil {
		return nil, err
	}
	metadata, err := list[domain.Conversation](c, d, "conversation", p)
	if err != nil {
		return nil, err
	}
	byID := map[string]domain.Conversation{}
	for _, v := range metadata {
		byID[v.ID] = v
	}
	visible := []domain.Run{}
	for _, r := range runs {
		if applyConversation(&r, byID[conversationID(r)]) {
			visible = append(visible, r)
		}
	}
	return visible, nil
}
func (d *DB) GetRun(c context.Context, id string) (domain.Run, error) {
	r, err := get[domain.Run](c, d, "run", id)
	if err != nil || r.Kind != "question" {
		return r, err
	}
	v, err := d.GetConversation(c, conversationID(r))
	if errors.Is(err, domain.ErrNotFound) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if !applyConversation(&r, v) {
		return domain.Run{}, domain.ErrNotFound
	}
	return r, nil
}
func (d *DB) PutRun(c context.Context, r domain.Run) error {
	return d.put(c, "run", r.ID, r.ProjectID, r)
}
func conversationID(r domain.Run) string {
	if r.ConversationID != "" {
		return r.ConversationID
	}
	return r.ID
}
func applyConversation(r *domain.Run, v domain.Conversation) bool {
	if r.Kind != "question" || v.ProjectID != r.ProjectID {
		return true
	}
	if v.DeletedAt != 0 {
		return false
	}
	if v.Title != "" {
		r.ConversationTitle, r.TitleStatus = v.Title, "manual"
	}
	return true
}
func (d *DB) GetConversation(c context.Context, id string) (domain.Conversation, error) {
	return get[domain.Conversation](c, d, "conversation", id)
}
func (d *DB) PutConversation(c context.Context, v domain.Conversation) error {
	return d.put(c, "conversation", v.ID, v.ProjectID, v)
}
func (d *DB) PutAttachment(c context.Context, a domain.Attachment) error {
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = d.db.ExecContext(c, "INSERT INTO attachments(id,project_id,payload,content,extracted_text) VALUES(?,?,?,?,?)", a.ID, a.ProjectID, string(b), a.Content, a.Text)
	return err
}
func (d *DB) GetAttachment(c context.Context, id string) (domain.Attachment, error) {
	var payload, text string
	var content []byte
	err := d.db.QueryRowContext(c, "SELECT payload,content,extracted_text FROM attachments WHERE id=?", id).Scan(&payload, &content, &text)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	if err != nil {
		return domain.Attachment{}, err
	}
	a, err := decode[domain.Attachment](payload)
	a.Content, a.Text = content, text
	return a, err
}
func (d *DB) ListProfiles(c context.Context) ([]domain.Profile, error) {
	return list[domain.Profile](c, d, "profile", "")
}
func (d *DB) GetProfile(c context.Context, id string) (domain.Profile, error) {
	p, e := get[domain.Profile](c, d, "profile", id)
	if e != nil {
		return p, e
	}
	e = d.db.QueryRowContext(c, "SELECT value FROM secrets WHERE id=?", id).Scan(&p.APIKey)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	}
	return p, e
}
func (d *DB) PutProfile(c context.Context, p domain.Profile) error {
	tx, e := d.db.BeginTx(c, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	b, e := json.Marshal(p)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(c, `INSERT INTO documents(kind,id,project_id,payload) VALUES('profile',?,'',?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload`, p.ID, string(b))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(c, `INSERT INTO secrets(id,value) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value`, p.ID, p.APIKey)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (d *DB) GetTask(c context.Context, id string) (domain.Task, error) {
	var body, hash string
	err := d.db.QueryRowContext(c, "SELECT payload,claim_hash FROM tasks WHERE id=?", id).Scan(&body, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Task{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Task{}, err
	}
	t, err := decode[domain.Task](body)
	t.ClaimHash = hash
	return t, err
}
func (d *DB) ListTasks(c context.Context, p string) ([]domain.Task, error) {
	rows, err := d.db.QueryContext(c, "SELECT payload,claim_hash FROM tasks WHERE project_id=? ORDER BY rowid DESC", p)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Task{}
	for rows.Next() {
		var body, hash string
		if err = rows.Scan(&body, &hash); err != nil {
			return nil, err
		}
		t, e := decode[domain.Task](body)
		if e != nil {
			return nil, e
		}
		t.ClaimHash = hash
		out = append(out, t)
	}
	return out, rows.Err()
}
func (d *DB) CommitTask(c context.Context, t domain.Task, expected int, ev domain.Event, r *domain.Receipt) error {
	tx, err := d.db.BeginTx(c, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if r != nil {
		var one int
		e := tx.QueryRowContext(c, "SELECT 1 FROM receipts WHERE key=?", r.Key).Scan(&one)
		if e == nil {
			return domain.ErrConflict
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
	}
	if err = validateTestingWrite(c, tx, t, expected); err != nil {
		return err
	}
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if expected == 0 {
		_, err = tx.ExecContext(c, "INSERT INTO tasks(id,project_id,version,claim_hash,payload) VALUES(?,?,?,?,?)", t.ID, t.ProjectID, t.Version, t.ClaimHash, string(b))
	} else {
		var result sql.Result
		result, err = tx.ExecContext(c, "UPDATE tasks SET version=?,claim_hash=?,payload=? WHERE id=? AND version=?", t.Version, t.ClaimHash, string(b), t.ID, expected)
		if err == nil {
			n, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return domain.ErrConflict
			}
		}
	}
	if err != nil {
		return err
	}
	if err = syncTaskReview(c, tx, t, ev.ReviewNote); err != nil {
		return err
	}
	if err = syncBugParent(c, tx, t); err != nil {
		return err
	}
	if err = syncTaskProjectVersion(c, tx, t); err != nil {
		return err
	}
	if err = insertEvent(c, tx, ev); err != nil {
		return err
	}
	if r != nil {
		if _, err = tx.ExecContext(c, "INSERT INTO receipts(key,fingerprint,response) VALUES(?,?,?)", r.Key, r.Fingerprint, r.Response); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertEvent(c context.Context, x executor, e domain.Event) error {
	data := string(e.Data)
	if data == "" {
		data = "null"
	}
	_, err := x.ExecContext(c, "INSERT INTO events(project_id,task_id,kind,actor,body,data,at) VALUES(?,?,?,?,?,?,?)", e.ProjectID, e.TaskID, e.Kind, e.Actor, e.Body, data, e.At)
	return err
}
func (d *DB) AppendEvent(c context.Context, e domain.Event) error { return insertEvent(c, d.db, e) }
func (d *DB) Events(c context.Context, p, t string, after int64) ([]domain.Event, error) {
	q := "SELECT seq,project_id,task_id,kind,actor,body,data,at FROM events WHERE project_id=? AND seq>?"
	args := []any{p, after}
	if t != "" {
		q += " AND task_id=?"
		args = append(args, t)
	}
	if after < 0 {
		q += " ORDER BY seq DESC LIMIT 500"
	} else {
		q += " ORDER BY seq LIMIT 500"
	}
	rows, e := d.db.QueryContext(c, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Event{}
	for rows.Next() {
		var v domain.Event
		var data string
		if e = rows.Scan(&v.Seq, &v.ProjectID, &v.TaskID, &v.Kind, &v.Actor, &v.Body, &data, &v.At); e != nil {
			return nil, e
		}
		v.Data = json.RawMessage(data)
		out = append(out, v)
	}
	if after < 0 {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, rows.Err()
}
func (d *DB) GetReceipt(c context.Context, key string) (domain.Receipt, error) {
	r := domain.Receipt{Key: key}
	e := d.db.QueryRowContext(c, "SELECT fingerprint,response FROM receipts WHERE key=?", key).Scan(&r.Fingerprint, &r.Response)
	if errors.Is(e, sql.ErrNoRows) {
		e = domain.ErrNotFound
	}
	return r, e
}
func (d *DB) ListTokens(c context.Context, p string) ([]domain.Token, error) {
	rows, e := d.db.QueryContext(c, "SELECT id,project_id,name,at FROM tokens WHERE project_id=?", p)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Token{}
	for rows.Next() {
		var t domain.Token
		if e = rows.Scan(&t.ID, &t.ProjectID, &t.Name, &t.At); e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (d *DB) PutToken(c context.Context, t domain.Token) error {
	_, e := d.db.ExecContext(c, "INSERT INTO tokens(id,project_id,name,hash,at) VALUES(?,?,?,?,?)", t.ID, t.ProjectID, t.Name, t.Hash, t.At)
	return e
}
func (d *DB) FindToken(c context.Context, h string) (domain.Token, error) {
	var t domain.Token
	e := d.db.QueryRowContext(c, "SELECT id,project_id,name,at FROM tokens WHERE hash=?", h).Scan(&t.ID, &t.ProjectID, &t.Name, &t.At)
	if errors.Is(e, sql.ErrNoRows) {
		e = domain.ErrForbidden
	}
	return t, e
}
func (d *DB) DeleteToken(c context.Context, p, id string) error {
	r, e := d.db.ExecContext(c, "DELETE FROM tokens WHERE id=? AND project_id=?", id, p)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e == nil && n != 1 {
		return fmt.Errorf("%w: token", domain.ErrNotFound)
	}
	return e
}
