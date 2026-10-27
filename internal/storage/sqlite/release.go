package sqlite

import "ai-pm-system/internal/domain"

// Older submissions entered review without a release confirmation. Preserve the
// submission and history, and require a release record before accepting them.
func (d *DB) migrateReleaseStage() error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := domain.Now()
	_, err = tx.Exec(`INSERT INTO events(project_id,task_id,kind,actor,body,data,at)
 SELECT project_id,id,'release_confirmation_required','system','流程升级：开发结果已保留，请确认实际上线后进入待验收','{}',?
 FROM tasks WHERE json_extract(payload,'$.status')='review' AND json_extract(payload,'$.deployment') IS NULL
 AND NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version=9)`, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE tasks SET version=version+1,payload=json_set(payload,'$.status','pending_release','$.version',version+1,'$.updated_at',?)
 WHERE json_extract(payload,'$.status')='review' AND json_extract(payload,'$.deployment') IS NULL
 AND NOT EXISTS (SELECT 1 FROM schema_migrations WHERE version=9)`, now)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO schema_migrations(version) VALUES(9)`); err != nil {
		return err
	}
	return tx.Commit()
}
