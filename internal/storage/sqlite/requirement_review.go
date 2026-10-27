package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (d *DB) GetRequirementReview(c context.Context, id string) (domain.RequirementReview, error) {
	var body string
	err := d.db.QueryRowContext(c, "SELECT payload FROM requirement_reviews WHERE task_id=?", id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	if err != nil {
		return domain.RequirementReview{}, err
	}
	return decode[domain.RequirementReview](body)
}
func (d *DB) ListRequirementReviewSummaries(c context.Context, projectID string) ([]domain.RequirementReviewSummary, error) {
	rows, err := d.db.QueryContext(c, `SELECT r.task_id, json_extract(r.payload,'$.task_revision'), json_extract(r.payload,'$.status')
		FROM tasks t JOIN requirement_reviews r ON r.task_id=t.id WHERE t.project_id=?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.RequirementReviewSummary{}
	for rows.Next() {
		var v domain.RequirementReviewSummary
		if err = rows.Scan(&v.TaskID, &v.TaskRevision, &v.Status); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) ListRequirementReviews(c context.Context) ([]domain.RequirementReview, error) {
	rows, err := d.db.QueryContext(c, "SELECT payload FROM requirement_reviews")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.RequirementReview{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		v, e := decode[domain.RequirementReview](body)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (d *DB) CommitRequirementReview(c context.Context, v domain.RequirementReview, expected, taskRevision int, task *domain.Task, ev *domain.Event, receipt *domain.Receipt, batches ...domain.MilestoneBatch) error {
	tx, err := d.db.BeginTx(c, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = writeMilestoneBatches(c, tx, batches); err != nil {
		return err
	}
	var taskBody string
	if err = tx.QueryRowContext(c, "SELECT payload FROM tasks WHERE id=?", v.TaskID).Scan(&taskBody); err != nil {
		return err
	}
	current, err := decode[domain.Task](taskBody)
	if err != nil {
		return err
	}
	if current.Revision != taskRevision {
		return domain.ErrConflict
	}
	if domain.TaskTerminated(current.Status) && v.Status != "closed" {
		return domain.ErrConflict
	}
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if expected == 0 {
		result, e := tx.ExecContext(c, "INSERT OR IGNORE INTO requirement_reviews(task_id,version,payload) VALUES(?,?,?)", v.TaskID, v.Version, string(body))
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return domain.ErrConflict
		}
	} else {
		result, e := tx.ExecContext(c, "UPDATE requirement_reviews SET version=?,payload=? WHERE task_id=? AND version=?", v.Version, string(body), v.TaskID, expected)
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return domain.ErrConflict
		}
	}
	if task != nil {
		if err = validateTestingWrite(c, tx, *task, current.Version); err != nil {
			return err
		}
		if task.Version != current.Version+1 {
			return domain.ErrConflict
		}
		b, e := json.Marshal(task)
		if e != nil {
			return e
		}
		result, e := tx.ExecContext(c, "UPDATE tasks SET version=?,claim_hash=?,payload=? WHERE id=? AND version=?", task.Version, task.ClaimHash, string(b), task.ID, current.Version)
		if e != nil {
			return e
		}
		if n, e := result.RowsAffected(); e != nil || n != 1 {
			if e != nil {
				return e
			}
			return domain.ErrConflict
		}
		if err = syncBugParent(c, tx, *task); err != nil {
			return err
		}
		if err = syncTaskProjectVersion(c, tx, *task); err != nil {
			return err
		}
	}
	if ev != nil {
		if err = insertEvent(c, tx, *ev); err != nil {
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

// A task write updates its existing review in the same transaction: a terminal
// task makes the review read-only, and a lifecycle note leaves a trace in it.
func syncTaskReview(c context.Context, tx *sql.Tx, task domain.Task, note *domain.RequirementReviewNote) error {
	terminal := domain.TaskTerminated(task.Status)
	if !terminal && note == nil {
		return nil
	}
	var payload string
	err := tx.QueryRowContext(c, "SELECT payload FROM requirement_reviews WHERE task_id=?", task.ID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	v, err := decode[domain.RequirementReview](payload)
	if err != nil {
		return err
	}
	if note != nil {
		if note.ExpireForms != "" {
			v.ExpireForms(nil, note.ExpireForms, task.UpdatedAt)
		}
		m := note.Message
		if m.Source != nil {
			source := task
			m.Source = &source
		}
		m.Generation = v.Generation
		v.Messages = append(v.Messages, m)
	}
	if terminal {
		v.Status = "closed"
		v.Generation++
		v.Source = task
		v.TaskRevision = task.Revision
	}
	v.Version++
	v.UpdatedAt = task.UpdatedAt
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(c, "UPDATE requirement_reviews SET version=?,payload=? WHERE task_id=?", v.Version, string(b), task.ID)
	return err
}
