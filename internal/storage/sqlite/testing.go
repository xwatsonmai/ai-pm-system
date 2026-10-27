package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

func txTask(c context.Context, tx *sql.Tx, id string) (domain.Task, error) {
	var body, hash string
	if err := tx.QueryRowContext(c, "SELECT payload,claim_hash FROM tasks WHERE id=?", id).Scan(&body, &hash); err != nil {
		return domain.Task{}, err
	}
	t, err := decode[domain.Task](body)
	t.ClaimHash = hash
	return t, err
}

func openRequirementBugs(c context.Context, tx *sql.Tx, id string) (int, error) {
	var n int
	err := tx.QueryRowContext(c, `SELECT count(*) FROM tasks WHERE json_extract(payload,'$.parent_requirement_id')=? AND json_extract(payload,'$.status') NOT IN ('resolved','closed','rejected','canceled')`, id).Scan(&n)
	return n, err
}

// This gate executes in the same transaction as EVERY task mutation. A concurrent
// new/reopened Bug and a test/release decision cannot both succeed from stale data.
func validateTestingWrite(c context.Context, tx *sql.Tx, t domain.Task, expected int) error {
	var old domain.Task
	if expected > 0 {
		var err error
		old, err = txTask(c, tx, t.ID)
		if err != nil {
			return err
		}
		if old.Kind == "requirement" && t.Kind != "requirement" {
			var n int
			if err := tx.QueryRowContext(c, "SELECT count(*) FROM tasks WHERE json_extract(payload,'$.parent_requirement_id')=?", t.ID).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return errors.New("已有测试 Bug 的需求不能改为 Bug 类型")
			}
		}
		if old.ParentRequirementID != t.ParentRequirementID {
			return errors.New("不能更改 Bug 归属")
		}
		if old.Status == "testing" && t.Status == "doing" {
			var n int
			if err := tx.QueryRowContext(c, "SELECT count(*) FROM tasks WHERE json_extract(payload,'$.parent_requirement_id')=?", t.ID).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return fmt.Errorf("%w: 已有关联测试 Bug，不能撤回提前提测", domain.ErrConflict)
			}
		}
	}
	if t.ParentRequirementID != "" {
		if t.Kind != "bug" || t.BugContext == nil || t.BugContext.TaskID != t.ParentRequirementID {
			return errors.New("关联 Bug 缺少需求上下文")
		}
		parent, err := txTask(c, tx, t.ParentRequirementID)
		if err != nil {
			return err
		}
		if parent.ProjectID != t.ProjectID || parent.Kind != "requirement" {
			return domain.ErrForbidden
		}
		if expected == 0 {
			if parent.Version != t.BugContext.Version || parent.Submission == nil || parent.Revision != t.BugContext.Revision || parent.Submission.SHA != t.BugContext.SHA || parent.Branch != t.BugContext.Branch {
				return domain.ErrConflict
			}
		}
		if !domain.TaskFinished(t.Status) && (expected == 0 || domain.TaskFinished(old.Status)) {
			if parent.Status != "testing" && parent.Status != "fixing" && parent.Status != "retest" {
				return fmt.Errorf("%w: 所属需求已离开测试流程，不能新建或重新打开关联 Bug", domain.ErrConflict)
			}
		}
	}
	if t.Kind == "requirement" && (t.Status == "testing" || t.Status == "retest" || t.Status == "pending_release" || t.Status == "review" || domain.TaskFinished(t.Status) || t.Status == "doing") {
		n, err := openRequirementBugs(c, tx, t.ID)
		if err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: 还有 %d 个关联 Bug 未解决或未关闭", domain.ErrConflict, n)
		}
	}
	return nil
}

// Synchronize the parent and its milestone atomically, preserving claim hashes
// and immutable submission/history evidence. No product testing is auto-approved.
func syncBugParent(c context.Context, tx *sql.Tx, bug domain.Task) error {
	if bug.ParentRequirementID == "" {
		return nil
	}
	parent, err := txTask(c, tx, bug.ParentRequirementID)
	if err != nil {
		return err
	}
	if parent.Status != "testing" && parent.Status != "fixing" && parent.Status != "retest" {
		return nil
	}
	n, err := openRequirementBugs(c, tx, parent.ID)
	if err != nil {
		return err
	}
	next := parent.Status
	if n > 0 {
		next = "fixing"
	} else if parent.Status == "fixing" {
		next = "retest"
	}
	// Always bump the parent on a child mutation, including last-Bug resolution,
	// so a stale form cannot approve a changed collection of Bugs.
	parent.Status = next
	parent.Version++
	parent.UpdatedAt = domain.Now()
	parent.TestVerification = nil
	b, err := json.Marshal(parent)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(c, "UPDATE tasks SET version=?,payload=? WHERE id=?", parent.Version, string(b), parent.ID); err != nil {
		return err
	}
	if err = syncTaskProjectVersion(c, tx, parent); err != nil {
		return err
	}
	return insertEvent(c, tx, domain.Event{ProjectID: parent.ProjectID, TaskID: parent.ID, Kind: "bug_progress", Actor: "system", Body: fmt.Sprintf("关联 Bug %s 状态 %s；剩余 %d 个未解决或未关闭", bug.ID, bug.Status, n), At: parent.UpdatedAt})
}
