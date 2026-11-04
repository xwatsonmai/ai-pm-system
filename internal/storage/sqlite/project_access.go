package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"encoding/json"
)

func (d *DB) migrateProjectAccess() error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var done int
	if err = tx.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=15").Scan(&done); err != nil {
		return err
	}
	if done != 0 {
		return nil
	}
	rows, err := tx.Query("SELECT payload FROM documents WHERE kind='project'")
	if err != nil {
		return err
	}
	projects := []domain.Project{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		p, e := decode[domain.Project](raw)
		if e != nil {
			rows.Close()
			return e
		}
		projects = append(projects, domain.NormalizeProjectAccess(p))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range projects {
		raw, e := json.Marshal(p)
		if e != nil {
			return e
		}
		if _, err = tx.Exec("UPDATE documents SET payload=? WHERE kind='project' AND id=?", string(raw), p.ID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO schema_migrations(version) VALUES(15)"); err != nil {
		return err
	}
	return tx.Commit()
}

// CommitProjectAccess compares a project-level access revision in SQLite, so
// concurrent role/member edits cannot remove the last manager or lose grants.
func (d *DB) CommitProjectAccess(c context.Context, p domain.Project, expected int) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	result, err := d.db.ExecContext(c, "UPDATE documents SET payload=? WHERE kind='project' AND id=? AND COALESCE(json_extract(payload,'$.access_version'),0)=?", string(raw), p.ID, expected)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return domain.ErrConflict
	}
	return err
}

func (d *DB) ListProjectRolesPage(c context.Context, project string, page, size int, query string) ([]domain.ProjectRole, int, error) {
	if page < 1 || size < 1 || size > 100 {
		return nil, 0, domain.ErrConflict
	}
	where := ` FROM documents d,json_each(d.payload,'$.project_roles') r WHERE d.kind='project' AND d.id=? AND (?='' OR instr(lower(json_extract(r.value,'$.name')||char(10)||json_extract(r.value,'$.description')),lower(?))>0)`
	var total int
	if e := d.db.QueryRowContext(c, "SELECT COUNT(*)"+where, project, query, query).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := d.db.QueryContext(c, "SELECT r.value"+where+" ORDER BY CAST(r.key AS INTEGER) LIMIT ? OFFSET ?", project, query, query, size, (page-1)*size)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	items := []domain.ProjectRole{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, 0, e
		}
		r, e := decode[domain.ProjectRole](raw)
		if e != nil {
			return nil, 0, e
		}
		items = append(items, r)
	}
	return items, total, rows.Err()
}
