package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (d *DB) GetRequirementIntake(c context.Context, id string) (domain.RequirementIntake, error) {
	var body string
	err := d.db.QueryRowContext(c, "SELECT payload FROM requirement_intakes WHERE id=?", id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	if err != nil {
		return domain.RequirementIntake{}, err
	}
	return decode[domain.RequirementIntake](body)
}

func (d *DB) ListRequirementIntakes(c context.Context, project string) ([]domain.RequirementIntake, error) {
	rows, err := d.db.QueryContext(c, "SELECT payload FROM requirement_intakes WHERE project_id=? ORDER BY rowid", project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.RequirementIntake{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		v, e := decode[domain.RequirementIntake](body)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) CommitRequirementIntake(c context.Context, v domain.RequirementIntake, expected int, task *domain.Task, ev *domain.Event, receipt *domain.Receipt, batches ...domain.MilestoneBatch) error {
	tx, err := d.db.BeginTx(c, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = writeMilestoneBatches(c, tx, batches); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var result sql.Result
	if expected == 0 {
		result, err = tx.ExecContext(c, "INSERT OR IGNORE INTO requirement_intakes(id,project_id,version,payload) VALUES(?,?,?,?)", v.ID, v.ProjectID, v.Version, string(b))
	} else {
		result, err = tx.ExecContext(c, "UPDATE requirement_intakes SET version=?,payload=? WHERE id=? AND version=?", v.Version, string(b), v.ID, expected)
	}
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return domain.ErrConflict
	}
	if task != nil {
		b, err = json.Marshal(task)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(c, "INSERT INTO tasks(id,project_id,version,claim_hash,payload) VALUES(?,?,?,?,?)", task.ID, task.ProjectID, task.Version, "", string(b)); err != nil {
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
