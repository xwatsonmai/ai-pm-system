package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"fmt"
)

// Eligibility is checked in the same transaction as the new requirement,
// milestone scope, history and receipt. The completed source is never rewritten.
func insertRequirementIteration(c context.Context, tx *sql.Tx, t domain.Task) error {
	i := t.Iteration
	var payload string
	if err := tx.QueryRowContext(c, "SELECT payload FROM tasks WHERE id=? AND project_id=?", i.ParentTaskID, t.ProjectID).Scan(&payload); err != nil {
		return err
	}
	parent, err := decode[domain.Task](payload)
	if err != nil {
		return err
	}
	root := parent.ID
	if parent.Iteration != nil {
		root = parent.Iteration.RootTaskID
	}
	if parent.Status != "done" || parent.Version != i.Source.Version || parent.Revision != i.Source.Revision || i.Source.ID != parent.ID || i.Source.ProjectID != t.ProjectID || i.Source.Status != "done" || i.Source.Iteration != nil || i.RootTaskID != root || t.ID == parent.ID {
		return fmt.Errorf("%w: 只能基于最新的已验收需求追加迭代，请重新核对原需求", domain.ErrConflict)
	}
	if root != parent.ID && (i.Baseline == nil || i.Baseline.ID != root || i.Baseline.ProjectID != t.ProjectID || i.Baseline.Iteration != nil || i.Baseline.Status != "done") {
		return fmt.Errorf("%w: 迭代缺少最初需求的已验收基线", domain.ErrConflict)
	}
	if _, err = tx.ExecContext(c, "INSERT INTO requirement_iterations(task_id,project_id,root_task_id,parent_task_id,at) VALUES(?,?,?,?,?)", t.ID, t.ProjectID, root, parent.ID, t.CreatedAt); err != nil {
		return err
	}
	return nil
}

func (d *DB) ListRequirementIterations(c context.Context, project, root string, offset, limit int) ([]domain.Task, int, error) {
	var total int
	err := d.db.QueryRowContext(c, "SELECT count(*) FROM requirement_iterations WHERE project_id=? AND root_task_id=?", project, root).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	rows, err := d.db.QueryContext(c, "SELECT t.payload FROM requirement_iterations i JOIN tasks t ON t.id=i.task_id AND t.project_id=i.project_id WHERE i.project_id=? AND i.root_task_id=? ORDER BY i.at DESC,i.task_id DESC LIMIT ? OFFSET ?", project, root, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []domain.Task{}
	for rows.Next() {
		var payload string
		if err = rows.Scan(&payload); err != nil {
			return nil, 0, err
		}
		t, e := decode[domain.Task](payload)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}
