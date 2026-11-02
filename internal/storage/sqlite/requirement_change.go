package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Transfer/freeze the selected feedback in the SAME transaction as its parent
// change. Other Bugs remain open and keep their independent repair histories.
func syncChangeFeedback(c context.Context, tx *sql.Tx, before, after domain.Task) error {
	change := after.RequirementChange
	if change == nil || change.SourceBugID == "" {
		return nil
	}
	starting := after.ReviewingChange() && !before.ReviewingChange()
	finishing := before.ReviewingChange() && before.RequirementChange.ID == change.ID && !after.ReviewingChange()
	if !starting && !finishing {
		return nil
	}
	bug, err := txTask(c, tx, change.SourceBugID)
	if err != nil {
		return err
	}
	if bug.ProjectID != after.ProjectID || bug.ParentRequirementID != after.ID || bug.Kind != "bug" {
		return domain.ErrForbidden
	}
	kind, body, status := "change_feedback_reserved", "本反馈转入原需求变更评审，报告和讨论保留；产品确认新需求稿前不关闭。", "change_pending"
	actor := change.Actor
	if starting {
		if bug.Version != change.SourceBugVersion || bug.ChangeRequestID != "" || (bug.Status != "clarify" && bug.Status != "ready") || bug.ClaimHash != "" {
			return domain.ErrConflict
		}
		bug.ChangeRequestID = change.ID
	} else {
		if bug.ChangeRequestID != change.ID || bug.Version != change.SourceBugVersion+1 {
			return domain.ErrConflict
		}
		switch change.Status {
		case "applied":
			actor = change.ConfirmedBy
			kind, body, status = "change_feedback_transferred", "产品确认新需求稿，本反馈已转为原需求的本轮变更；不表示原实现通过测试或已发布。", "closed"
			bug.Resolution = &domain.TaskResolution{FromStatus: bug.Status, Reason: body, Actor: actor, At: domain.Now()}
			bug.Status, bug.ClaimHash = "closed", ""
		case "canceled":
			kind, body, status = "change_feedback_restored", "原需求变更已取消，反馈保留并恢复原流程；可重新评审。", "failed"
			bug.ChangeRequestID = ""
			if bug.Status == "ready" {
				status = "confirmed"
			}
		default:
			return domain.ErrConflict
		}
	}
	bug.Version++
	bug.UpdatedAt = domain.Now()
	b, err := json.Marshal(bug)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(c, "UPDATE tasks SET version=?,payload=?,claim_hash=? WHERE id=?", bug.Version, string(b), bug.ClaimHash, bug.ID); err != nil {
		return err
	}
	var payload string
	err = tx.QueryRowContext(c, "SELECT payload FROM requirement_reviews WHERE task_id=?", bug.ID).Scan(&payload)
	if err == nil {
		v, e := decode[domain.RequirementReview](payload)
		if e != nil {
			return e
		}
		if starting && v.Version != change.SourceReviewVersion {
			return domain.ErrConflict
		}
		v.Version++
		v.Generation++
		v.Status, v.Source, v.TaskRevision, v.UpdatedAt, v.RunID = status, bug, bug.Revision, bug.UpdatedAt, ""
		v.Error = ""
		v.ExpireForms(nil, body, bug.UpdatedAt)
		v.Messages = append(v.Messages, domain.RequirementReviewMessage{ID: domain.ID("rm_"), Role: "system", Action: kind, Actor: actor, ActorKind: "human", Body: body, Source: &bug, Generation: v.Generation, At: bug.UpdatedAt})
		b, err = json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(c, "UPDATE requirement_reviews SET version=?,payload=? WHERE task_id=?", v.Version, string(b), bug.ID); err != nil {
			return err
		}
	} else if errors.Is(err, sql.ErrNoRows) && starting && change.SourceReviewVersion != 0 {
		return domain.ErrConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = syncTaskProjectVersion(c, tx, bug); err != nil {
		return err
	}
	return insertEvent(c, tx, domain.Event{ProjectID: bug.ProjectID, TaskID: bug.ID, Kind: kind, Actor: actor, Body: body, At: bug.UpdatedAt})
}
