package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (d *DB) GetCollaboration(ctx context.Context, id string) (domain.Collaboration, error) {
	var body, hash string
	err := d.db.QueryRowContext(ctx, "SELECT payload,claim_hash FROM collaborations WHERE id=?", id).Scan(&body, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	if err != nil {
		return domain.Collaboration{}, err
	}
	v, err := decode[domain.Collaboration](body)
	v.ClaimHash = hash
	return v, err
}

func (d *DB) ListCollaborations(ctx context.Context, project string) ([]domain.Collaboration, error) {
	query, args := "SELECT payload,claim_hash FROM collaborations", []any{}
	if project != "" {
		query += " WHERE project_id=?"
		args = append(args, project)
	}
	rows, err := d.db.QueryContext(ctx, query+" ORDER BY rowid DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all := []domain.Collaboration{}
	for rows.Next() {
		var body, hash string
		if err = rows.Scan(&body, &hash); err != nil {
			return nil, err
		}
		v, e := decode[domain.Collaboration](body)
		if e != nil {
			return nil, e
		}
		v.ClaimHash = hash
		all = append(all, v)
	}
	return all, rows.Err()
}

func changedOne(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return domain.ErrConflict
	}
	return err
}

func (d *DB) CommitCollaboration(ctx context.Context, v domain.Collaboration, expected int, receipt *domain.Receipt, job *domain.ReplyJob) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if receipt != nil {
		var one int
		err = tx.QueryRowContext(ctx, "SELECT 1 FROM receipts WHERE key=?", receipt.Key).Scan(&one)
		if err == nil {
			return domain.ErrConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if expected == 0 {
		err = changedOne(tx.ExecContext(ctx, "INSERT INTO collaborations(id,project_id,version,claim_hash,payload) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING", v.ID, v.ProjectID, v.Version, v.ClaimHash, string(body)))
	} else {
		err = changedOne(tx.ExecContext(ctx, "UPDATE collaborations SET version=?,claim_hash=?,payload=? WHERE id=? AND version=?", v.Version, v.ClaimHash, string(body), v.ID, expected))
	}
	if err != nil {
		return err
	}
	if receipt != nil {
		if _, err = tx.ExecContext(ctx, "INSERT INTO receipts(key,fingerprint,response) VALUES(?,?,?)", receipt.Key, receipt.Fingerprint, receipt.Response); err != nil {
			return err
		}
	}
	if job != nil {
		b, e := json.Marshal(job)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO reply_jobs(id,version,payload) VALUES(?,?,?)", job.ID, job.Version, string(b)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) GetReplyJob(ctx context.Context, id string) (domain.ReplyJob, error) {
	var body string
	err := d.db.QueryRowContext(ctx, "SELECT payload FROM reply_jobs WHERE id=?", id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	if err != nil {
		return domain.ReplyJob{}, err
	}
	return decode[domain.ReplyJob](body)
}

func (d *DB) ListReplyJobs(ctx context.Context) ([]domain.ReplyJob, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT payload FROM reply_jobs ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all := []domain.ReplyJob{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		v, e := decode[domain.ReplyJob](body)
		if e != nil {
			return nil, e
		}
		all = append(all, v)
	}
	return all, rows.Err()
}

func (d *DB) CommitReplyJob(ctx context.Context, job domain.ReplyJob, expected int, run *domain.Run) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	b, err := json.Marshal(job)
	if err != nil {
		return err
	}
	if err = changedOne(tx.ExecContext(ctx, "UPDATE reply_jobs SET version=?,payload=? WHERE id=? AND version=?", job.Version, string(b), job.ID, expected)); err != nil {
		return err
	}
	if run != nil {
		b, err = json.Marshal(run)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO documents(kind,id,project_id,payload) VALUES('run',?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload`, run.ID, run.ProjectID, string(b)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
