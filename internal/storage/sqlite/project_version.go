package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (d *DB) ListProjectVersions(c context.Context, project string) ([]domain.ProjectVersion, error) {
	rows, err := d.db.QueryContext(c, "SELECT payload FROM project_versions WHERE project_id=? ORDER BY rowid DESC", project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ProjectVersion{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		v, e := decode[domain.ProjectVersion](body)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) GetProjectVersion(c context.Context, id string) (domain.ProjectVersion, error) {
	var body string
	err := d.db.QueryRowContext(c, "SELECT payload FROM project_versions WHERE id=?", id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	if err != nil {
		return domain.ProjectVersion{}, err
	}
	return decode[domain.ProjectVersion](body)
}

func writeProjectVersion(c context.Context, tx *sql.Tx, v domain.ProjectVersion, expected int) error {
	if v.Version != expected+1 {
		return domain.ErrConflict
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var result sql.Result
	if expected == 0 {
		result, err = tx.ExecContext(c, "INSERT OR IGNORE INTO project_versions(id,project_id,name_key,version,payload) VALUES(?,?,?,?,?)", v.ID, v.ProjectID, strings.ToLower(strings.TrimSpace(v.Name)), v.Version, string(b))
	} else {
		result, err = tx.ExecContext(c, "UPDATE project_versions SET name_key=?,version=?,payload=? WHERE id=? AND project_id=? AND version=?", strings.ToLower(strings.TrimSpace(v.Name)), v.Version, string(b), v.ID, v.ProjectID, expected)
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("%w: 同一项目版本名称不能重复", domain.ErrConflict)
		}
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: 版本名称已存在或内容已变化，请刷新", domain.ErrConflict)
	}
	return nil
}

func (d *DB) CommitVersionPlan(c context.Context, versions []domain.VersionWrite, tasks []domain.VersionTaskWrite, events []domain.Event, receipt *domain.Receipt, batches ...domain.MilestoneBatch) error {
	tx, err := d.db.BeginTx(c, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if receipt != nil {
		var found int
		err = tx.QueryRowContext(c, "SELECT 1 FROM receipts WHERE key=?", receipt.Key).Scan(&found)
		if err == nil {
			return domain.ErrConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if err = writeMilestoneBatches(c, tx, batches); err != nil {
		return err
	}
	for _, v := range versions {
		if err = writeProjectVersion(c, tx, v.Value, v.Expected); err != nil {
			return err
		}
	}
	for _, write := range tasks {
		t := write.Value
		if err = validateTestingWrite(c, tx, t, write.Expected); err != nil {
			return err
		}
		if t.Version != write.Expected+1 {
			return domain.ErrConflict
		}
		b, e := json.Marshal(t)
		if e != nil {
			return e
		}
		var result sql.Result
		if write.Expected == 0 {
			result, e = tx.ExecContext(c, "INSERT OR IGNORE INTO tasks(id,project_id,version,claim_hash,payload) VALUES(?,?,?,?,?)", t.ID, t.ProjectID, t.Version, t.ClaimHash, string(b))
		} else {
			result, e = tx.ExecContext(c, "UPDATE tasks SET version=?,claim_hash=?,payload=? WHERE id=? AND project_id=? AND version=?", t.Version, t.ClaimHash, string(b), t.ID, t.ProjectID, write.Expected)
		}
		if e != nil {
			return e
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return domain.ErrConflict
		}
		if write.Expected == 0 && t.Iteration != nil {
			if e = insertRequirementIteration(c, tx, t); e != nil {
				return e
			}
		}
		var note *domain.RequirementReviewNote
		for _, ev := range events {
			if ev.TaskID == t.ID && ev.ReviewNote != nil {
				note = ev.ReviewNote
			}
		}
		if e = syncTaskReview(c, tx, t, note); e != nil {
			return e
		}
	}
	for _, write := range tasks {
		if err = syncBugParent(c, tx, write.Value); err != nil {
			return err
		}
	}
	for _, ev := range events {
		if err = insertEvent(c, tx, ev); err != nil {
			return err
		}
	}
	if receipt != nil {
		if _, err = tx.ExecContext(c, "INSERT INTO receipts(key,fingerprint,response) VALUES(?,?,?)", receipt.Key, receipt.Fingerprint, receipt.Response); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Ordinary task actions and review confirmation refresh version progress in the
// same transaction. Published snapshots are never rewritten by a new revision.
func syncTaskProjectVersion(c context.Context, tx *sql.Tx, task domain.Task) error {
	rows, err := tx.QueryContext(c, "SELECT payload FROM project_versions WHERE project_id=?", task.ProjectID)
	if err != nil {
		return err
	}
	versions := []domain.ProjectVersion{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return err
		}
		v, e := decode[domain.ProjectVersion](body)
		if e != nil {
			rows.Close()
			return e
		}
		versions = append(versions, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	foundTarget := task.ProjectVersionID == ""
	for _, v := range versions {
		changed := false
		for i := range v.Scope {
			item := &v.Scope[i]
			if item.Task.ID != task.ID || item.Removed {
				continue
			}
			if task.ProjectVersionID == v.ID {
				foundTarget = true
				if v.Status == "completed" || v.Status == "closed" {
					if domain.TaskActive(task.Status) && task.Status != "review" {
						return fmt.Errorf("%w: 里程碑已结束，后续工作需转入当前里程碑", domain.ErrConflict)
					}
				}
				if item.Task.Revision != task.Revision {
					item.Published = false
				}
				if item.Task.Version == task.Version {
					continue
				}
				if task.Deployment != nil && task.Deployment.Revision == task.Revision && task.Status == "review" && (item.Task.Deployment == nil || item.Task.Deployment.At != task.Deployment.At) {
					if v.Status == "closed" || v.Status == "completed" {
						return fmt.Errorf("%w: 版本已关闭，不能继续登记上线", domain.ErrConflict)
					}
					v.ScopeFrozen = false
					releaseID := task.Deployment.ReleaseID
					if releaseID == "" {
						releaseID = domain.ID("pub_")
					}
					v.Releases = append(v.Releases, domain.VersionRelease{ID: releaseID, Environment: task.Deployment.Environment, Body: task.Deployment.Body, Actor: task.Deployment.Actor, Source: task.Deployment.Source, Tasks: []domain.Task{task}, At: task.Deployment.At})
					item.Published = true
				}
				item.Task = task
				changed = true
			} else if task.Revision > item.Task.Revision && task.Revision > item.DetachedRevision {
				item.DetachedRevision = task.Revision
				v.History = append(v.History, domain.VersionHistoryEntry{Action: "new_revision", Body: "后续修订转入当前里程碑；此处保留原修订与交付记录", Actor: "system", TaskIDs: []string{task.ID}, At: task.UpdatedAt})
				changed = true
			} else if item.Published && v.Status == "closed" && task.Revision == item.Task.Revision && task.Status == "doing" && task.Review != nil && !task.Review.Accepted && (item.Task.Review == nil || item.Task.Review.At != task.Review.At) {
				item.Task.Review, item.Task.Status, item.Task.ProjectVersionID = task.Review, "doing", ""
				item.Reason = "验收退回；版本已关闭，后续开发转入当前里程碑：" + task.Review.Note
				v.History = append(v.History, domain.VersionHistoryEntry{Action: "review_returned", Body: item.Reason, Actor: task.Review.Actor, TaskIDs: []string{task.ID}, At: task.UpdatedAt})
				changed = true
			}
		}
		if changed {
			expected := v.Version
			v.Version++
			v.UpdatedAt = task.UpdatedAt
			if err = writeProjectVersion(c, tx, v, expected); err != nil {
				return err
			}
		}
	}
	if !foundTarget {
		return fmt.Errorf("%w: 需求不在目标版本范围内", domain.ErrConflict)
	}
	return nil
}
