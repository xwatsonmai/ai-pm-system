package sqlite

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

var _ storage.PagedQueries = (*DB)(nil)

// The projection is intentionally assembled in SQL. Large descriptions,
// attachment metadata and historical evidence never leave SQLite for a list.
const taskListProjection = `json_remove(t.payload,'$.description','$.acceptance','$.attachments','$.bug_context','$.requirement_change','$.submission.summary','$.submission.tests','$.submission.limitations','$.submission.references','$.deployment.body','$.review.note','$.review.checks','$.alignment.messages','$.test_verification.note','$.iteration.source','$.iteration.baseline','$.resolution.reason')`
const taskStatus = `json_extract(t.payload,'$.status')`
const reviewCurrent = `(COALESCE(json_extract(t.payload,'$.change_request_id'),'')='' AND json_extract(r.payload,'$.task_revision')=json_extract(t.payload,'$.revision'))`
const pendingReview = `(COALESCE(json_extract(t.payload,'$.change_request_id'),'')='' AND (` + taskStatus + `='aligning' OR (` + taskStatus + `='clarify' AND ` + reviewCurrent + ` AND json_extract(r.payload,'$.status') IN ('ready_to_confirm','needs_input','rejection_pending'))))`
const attentionTask = `(` + taskStatus + ` NOT IN ('done','resolved','closed','rejected','canceled') AND (COALESCE(json_extract(t.payload,'$.blocker'),'')<>'' OR ` + taskStatus + ` IN ('testing','retest','verifying','pending_release','review') OR (` + taskStatus + `='aligning' AND COALESCE(json_extract(t.payload,'$.change_request_id'),'')='') OR (` + taskStatus + `='clarify' AND ` + reviewCurrent + ` AND json_extract(r.payload,'$.status') IN ('ready_to_confirm','needs_input','rejection_pending','failed'))))`
const taskJoin = ` FROM tasks t LEFT JOIN requirement_reviews r ON r.task_id=t.id `

// Count and items come from one read transaction, so the pager describes the
// same snapshot even while another connection writes.
func queryJSONPage(c context.Context, d *DB, selectSQL, from, where, order string, args []any, q domain.PageQuery) (domain.Page[json.RawMessage], error) {
	out := domain.Page[json.RawMessage]{Items: []json.RawMessage{}, Page: q.Page, PageSize: q.PageSize}
	tx, e := d.db.BeginTx(c, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = tx.QueryRowContext(c, "SELECT COUNT(*) "+from+" WHERE "+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	if out.Total == 0 {
		out.Page = 1
		q.Page = 1
	}
	if out.Total > 0 && q.Offset() >= out.Total {
		out.Page = (out.Total-1)/q.PageSize + 1
		q.Page = out.Page
	}
	pagedArgs := append(append([]any{}, args...), q.PageSize, q.Offset())
	rows, e := tx.QueryContext(c, "SELECT "+selectSQL+" "+from+" WHERE "+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", pagedArgs...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, json.RawMessage(raw))
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (d *DB) QueryTasks(c context.Context, p string, q domain.TaskListQuery) (domain.Page[domain.Task], error) {
	var out domain.Page[domain.Task]
	normalized, e := q.PageQuery.Normalized()
	if e != nil {
		return out, e
	}
	q.PageQuery = normalized
	where := "t.project_id=?"
	args := []any{p}
	switch q.Status {
	case "":
	case "attention":
		where += " AND " + attentionTask
	case "review_pending":
		where += " AND " + pendingReview
	case "active":
		where += " AND " + taskStatus + " NOT IN ('done','resolved','closed','rejected','canceled')"
	case "testing_queue":
		where += " AND " + taskStatus + " IN ('testing','fixing','retest','verifying')"
	default:
		for _, status := range strings.Split(q.Status, ",") {
			if !strings.Contains(",clarify,ready,doing,aligning,testing,fixing,retest,verifying,resolved,pending_release,review,done,closed,rejected,canceled,", ","+status+",") {
				return out, errors.New("无效的状态筛选")
			}
		}
		marks := []string{}
		for _, v := range strings.Split(q.Status, ",") {
			marks = append(marks, "?")
			args = append(args, v)
		}
		where += " AND " + taskStatus + " IN (" + strings.Join(marks, ",") + ")"
	}
	if q.Kind != "" {
		if q.Kind != "bug" && q.Kind != "requirement" {
			return out, errors.New("无效的类型筛选")
		}
		where += " AND json_extract(t.payload,'$.kind')=?"
		args = append(args, q.Kind)
	}
	if q.ProjectVersionID != "" {
		where += " AND COALESCE(json_extract(t.payload,'$.project_version_id'),'')=?"
		v := q.ProjectVersionID
		if v == "unplanned" {
			v = ""
		}
		args = append(args, v)
	}
	if q.ParentRequirementID != "" {
		where += " AND json_extract(t.payload,'$.parent_requirement_id')=?"
		args = append(args, q.ParentRequirementID)
	}
	if q.Query != "" {
		where += " AND instr(lower(COALESCE(json_extract(t.payload,'$.title'),'')||' '||COALESCE(json_extract(t.payload,'$.description'),'')||' '||COALESCE(json_extract(t.payload,'$.owner'),'')||' '||t.id),lower(?))>0"
		args = append(args, q.Query)
	}
	raw, e := queryJSONPage(c, d, taskListProjection, taskJoin, where, "COALESCE(json_extract(t.payload,'$.updated_at'),0) DESC,t.id DESC", args, q.PageQuery)
	if e != nil {
		return out, e
	}
	out = domain.Page[domain.Task]{Items: []domain.Task{}, Total: raw.Total, Page: raw.Page, PageSize: raw.PageSize}
	for _, v := range raw.Items {
		var t domain.Task
		if e = json.Unmarshal(v, &t); e != nil {
			return out, e
		}
		out.Items = append(out.Items, t)
	}
	return out, nil
}
func (d *DB) TaskListStats(c context.Context, p string) (domain.TaskListStats, error) {
	out := domain.TaskListStats{ByStatus: map[string]int{}}
	rows, e := d.db.QueryContext(c, "SELECT "+taskStatus+",COUNT(*),SUM(CASE WHEN "+attentionTask+" THEN 1 ELSE 0 END),SUM(CASE WHEN "+pendingReview+" THEN 1 ELSE 0 END)"+taskJoin+" WHERE t.project_id=? GROUP BY "+taskStatus, p)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n, a, r int
		if e = rows.Scan(&status, &n, &a, &r); e != nil {
			return out, e
		}
		out.ByStatus[status] = n
		out.Total += n
		out.Attention += a
		out.ReviewPending += r
	}
	return out, rows.Err()
}
func (d *DB) QueryReviewSummaries(c context.Context, ids []string) ([]domain.RequirementReviewSummary, error) {
	out := []domain.RequirementReviewSummary{}
	if len(ids) == 0 {
		return out, nil
	}
	marks := []string{}
	args := []any{}
	for _, id := range ids {
		marks = append(marks, "?")
		args = append(args, id)
	}
	rows, e := d.db.QueryContext(c, `SELECT json_object('task_id',task_id,'task_revision',json_extract(payload,'$.task_revision'),'status',json_extract(payload,'$.status'),'generation',json_extract(payload,'$.generation'),'updated_at',json_extract(payload,'$.updated_at'),'error',json_extract(payload,'$.error')) FROM requirement_reviews WHERE task_id IN (`+strings.Join(marks, ",")+`)`, args...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if e = rows.Scan(&s); e != nil {
			return out, e
		}
		v, e := decode[domain.RequirementReviewSummary](s)
		if e != nil {
			return out, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) QueryDocuments(c context.Context, q domain.ListDocumentQuery) (domain.Page[json.RawMessage], error) {
	var empty domain.Page[json.RawMessage]
	page, e := q.PageQuery.Normalized()
	if e != nil {
		return empty, e
	}
	from, where, projection, order := "FROM documents d", "1=1", "d.payload", "d.rowid DESC"
	args := []any{}
	switch q.Resource {
	case "projects", "platform-projects":
		where = "d.kind='project'"
		projection = `json_remove(d.payload,'$.credential_id')`
		if q.Resource == "projects" && q.UserID != "local-owner" {
			where += ` AND (EXISTS(SELECT 1 FROM json_each(d.payload,'$.memberships') m WHERE json_extract(m.value,'$.user_id')=?) OR (COALESCE(json_array_length(d.payload,'$.memberships'),0)=0 AND (json_extract(d.payload,'$.owner_id')=? OR EXISTS(SELECT 1 FROM json_each(d.payload,'$.access_members') a WHERE a.value=?))))`
			args = append(args, q.UserID, q.UserID, q.UserID)
		}
		order = "lower(json_extract(d.payload,'$.name')),d.id"
	case "version-options":
		from = "FROM project_versions d"
		where = "d.project_id=?"
		args = append(args, q.ProjectID)
		projection = `json_remove(d.payload,'$.scope','$.releases')`
		if q.Active {
			where += ` AND json_extract(d.payload,'$.status')='active'`
		}
	case "profiles":
		where = "d.kind='profile'"
	case "artifacts", "delivery-evidence":
		where = "d.kind='artifact' AND d.project_id=?"
		args = append(args, q.ProjectID)
		order = "COALESCE(json_extract(d.payload,'$.at'),0) DESC,d.id DESC"
		if q.Resource == "delivery-evidence" {
			from = `FROM (SELECT id,project_id,payload FROM documents WHERE kind='artifact' AND json_extract(payload,'$.kind') IN ('acceptance','deployment','test') UNION ALL SELECT id,project_id,json_object('id','deployment:'||id,'task_id',id,'project_id',project_id,'kind','deployment','title',json_extract(payload,'$.title')||' · 上线记录 · R'||COALESCE(json_extract(payload,'$.deployment.revision'),0),'body',COALESCE(json_extract(payload,'$.deployment.body'),''),'environment',json_extract(payload,'$.deployment.environment'),'sha',json_extract(payload,'$.deployment.sha'),'source',COALESCE(json_extract(payload,'$.deployment.actor'),'开发汇报'),'at',json_extract(payload,'$.deployment.at')) FROM tasks WHERE json_extract(payload,'$.deployment') IS NOT NULL) d`
			where = "d.project_id=?"
		}
		switch q.ArtifactMode {
		case "none":
			where += " AND 0"
		case "overview":
			where += ` AND json_extract(d.payload,'$.kind') IN ('analysis','knowledge','question') AND COALESCE(json_extract(d.payload,'$.task_id'),'')=''`
		case "", "all":
		default:
			return empty, errors.New("无效的证据访问范围")
		}
		if q.ExcludeGit {
			where += ` AND COALESCE(json_extract(d.payload,'$.kind'),'')<>'git'`
		}
	case "tokens":
		from = "FROM tokens d"
		where = "d.project_id=?"
		args = append(args, q.ProjectID)
		projection = `json_object('id',d.id,'project_id',d.project_id,'name',d.name,'at',d.at)`
		order = "d.at DESC,d.id DESC"
	case "intakes":
		from = "FROM requirement_intakes d JOIN documents origin ON origin.kind='run' AND origin.id=json_extract(d.payload,'$.origin_run_id') LEFT JOIN documents meta ON meta.kind='conversation' AND meta.project_id=d.project_id AND meta.id=COALESCE(NULLIF(json_extract(origin.payload,'$.conversation_id'),''),origin.id)"
		where = "d.project_id=? AND json_extract(d.payload,'$.owner_id')=? AND COALESCE(json_extract(meta.payload,'$.deleted_at'),0)=0"
		args = append(args, q.ProjectID, q.UserID)
		if q.ConversationID != "" {
			where += " AND json_extract(d.payload,'$.conversation_id')=?"
			args = append(args, q.ConversationID)
		}
	case "collaborations":
		from = `FROM collaborations d JOIN documents origin ON origin.kind='run' AND origin.id=json_extract(d.payload,'$.origin_run_id') AND origin.project_id=d.project_id LEFT JOIN documents meta ON meta.kind='conversation' AND meta.project_id=d.project_id AND meta.id=COALESCE(NULLIF(json_extract(origin.payload,'$.conversation_id'),''),origin.id)`
		where = "d.project_id=? AND json_extract(d.payload,'$.owner_id')=? AND json_extract(d.payload,'$.owner_kind')=? AND COALESCE(json_extract(meta.payload,'$.deleted_at'),0)=0"
		args = append(args, q.ProjectID, q.UserID, q.ActorKind)
		if q.ConversationID != "" {
			where += " AND json_extract(d.payload,'$.conversation_id')=?"
			args = append(args, q.ConversationID)
		}
	case "accounts":
		from = "FROM accounts d"
		projection = `json_object('user_id',user_id,'username',username,'name',name,'disabled',json(CASE disabled WHEN 1 THEN 'true' ELSE 'false' END),'must_change_password',json(CASE must_change_password WHEN 1 THEN 'true' ELSE 'false' END),'version',version,'created_at',created_at,'updated_at',updated_at)`
		order = "created_at,user_id"
	case "admins":
		from = `FROM platform_admins a LEFT JOIN documents d ON d.kind='user' AND d.id=a.user_id`
		projection = `COALESCE(d.payload,json_object('id',a.user_id,'name',a.user_id))`
		order = "a.user_id"
	case "grants":
		from = "FROM oauth_grants d"
		where = "d.user_id=?"
		args = append(args, q.UserID)
		projection = `json_object('id',json_extract(d.payload,'$.id'),'user_id',json_extract(d.payload,'$.user_id'),'client_id',json_extract(d.payload,'$.client_id'),'project_id',json_extract(d.payload,'$.project_id'),'expires_at',json_extract(d.payload,'$.expires_at'),'created_at',json_extract(d.payload,'$.created_at'))`
		order = "d.rowid DESC"
	case "events":
		from = "FROM events d"
		where = "d.project_id=?"
		args = append(args, q.ProjectID)
		if q.ExcludeGitEvents {
			where += " AND d.kind <> 'git_sync'"
		}
		projection = `json_object('seq',d.seq,'project_id',d.project_id,'task_id',d.task_id,'kind',d.kind,'actor',d.actor,'body',d.body,'at',d.at)`
		order = "d.seq DESC"
	case "runs", "conversations":
		cid := `COALESCE(NULLIF(json_extract(d.payload,'$.conversation_id'),''),d.id)`
		from = `FROM documents d LEFT JOIN documents meta ON meta.kind='conversation' AND meta.project_id=d.project_id AND meta.id=` + cid
		where = `d.kind='run' AND d.project_id=? AND COALESCE(json_extract(meta.payload,'$.deleted_at'),0)=0 AND (json_extract(d.payload,'$.kind')<>'question' OR (COALESCE(json_extract(d.payload,'$.owner_id'),'')<>'' AND json_extract(d.payload,'$.owner_id')=? AND json_extract(d.payload,'$.owner_kind')=?) OR (COALESCE(json_extract(d.payload,'$.owner_id'),'')='' AND ?))`
		args = append(args, q.ProjectID, q.UserID, q.ActorKind, q.LegacyOwner)
		if q.ExcludeQuestions {
			where += ` AND json_extract(d.payload,'$.kind')<>'question'`
		}
		if q.OnlyQuestions {
			where += ` AND json_extract(d.payload,'$.kind')='question'`
		}
		order = "COALESCE(json_extract(d.payload,'$.started_at'),0) DESC,d.id DESC"
		projection = `CASE WHEN COALESCE(json_extract(meta.payload,'$.title'),'')<>'' THEN json_set(d.payload,'$.conversation_title',json_extract(meta.payload,'$.title'),'$.title_status','manual') ELSE d.payload END`
		if q.Summary && q.Resource == "runs" {
			projection = "json_remove(" + projection + ",'$.output','$.process_output','$.steps','$.prompt','$.attachments','$.code_snapshot','$.review_result','$.review_evidence','$.usage')"
		}
		if q.ConversationID != "" {
			where += " AND " + cid + "=?"
			args = append(args, q.ConversationID)
		}
		if q.Active {
			where += ` AND json_extract(d.payload,'$.status') IN ('running','queued')`
		}
		if q.Resource == "conversations" {
			where += ` AND json_extract(d.payload,'$.kind')='question'`
			// Window functions select one representative per conversation without
			// decoding any historical outputs. Ordering is independent of rowid.
			base := `SELECT ` + cid + ` cid,d.payload,meta.payload metadata,ROW_NUMBER() OVER(PARTITION BY ` + cid + ` ORDER BY COALESCE(json_extract(d.payload,'$.started_at'),0),d.id) first_row,MAX(COALESCE(json_extract(d.payload,'$.started_at'),0)) OVER(PARTITION BY ` + cid + `) updated_at,MAX(CASE WHEN json_extract(d.payload,'$.status') IN ('running','queued') THEN 1 ELSE 0 END) OVER(PARTITION BY ` + cid + `) active,json_group_array(json_extract(d.payload,'$.source_event_id')) FILTER(WHERE json_extract(d.payload,'$.trigger_type')='collaboration_result' AND json_extract(d.payload,'$.status')='succeeded') OVER(PARTITION BY ` + cid + `) reply_event_ids ` + from + ` WHERE ` + where
			from = "FROM (" + base + ") d"
			where = "d.first_row=1"
			projection = `json_object('id',d.cid,'title',COALESCE(NULLIF(json_extract(d.metadata,'$.title'),''),NULLIF(json_extract(d.payload,'$.conversation_title'),''),NULLIF(substr(trim(json_extract(d.payload,'$.prompt')),1,28),''),json_extract(d.payload,'$.attachments[0].name'),'附件讨论'),'updatedAt',d.updated_at,'active',json(CASE d.active WHEN 1 THEN 'true' ELSE 'false' END),'naming',json(CASE WHEN json_extract(d.payload,'$.title_status')='pending' AND d.active=1 THEN 'true' ELSE 'false' END),'legacy',json(CASE WHEN COALESCE(json_extract(d.payload,'$.owner_id'),'')='' THEN 'true' ELSE 'false' END),'reply_event_ids',json(d.reply_event_ids),'turns',json('[]'))`
			order = "d.updated_at DESC,d.cid DESC"
		}
	default:
		return empty, errors.New("不支持的分页资源")
	}
	if q.TaskID != "" {
		switch q.Resource {
		case "runs", "artifacts", "delivery-evidence", "collaborations":
			where += " AND json_extract(d.payload,'$.task_id')=?"
			args = append(args, q.TaskID)
		case "events":
			where += " AND d.task_id=?"
			args = append(args, q.TaskID)
		}
	}
	if q.Query != "" {
		var search string
		switch q.Resource {
		case "accounts":
			search = "d.username||' '||d.name"
		case "admins":
			search = "COALESCE(d.payload,'')"
		case "conversations":
			search = "json_extract(d.payload,'$.prompt')||' '||COALESCE(json_extract(d.metadata,'$.title'),'')"
		case "events":
			search = "d.body"
		case "tokens":
			search = "d.name"
		default:
			search = "COALESCE(json_extract(d.payload,'$.name'),'')||' '||COALESCE(json_extract(d.payload,'$.title'),'')"
		}
		where += " AND instr(lower(" + search + "),lower(?))>0"
		args = append(args, q.Query)
	}
	return queryJSONPage(c, d, projection, from, where, order, args, page)
}
func (d *DB) QueryMembers(c context.Context, p string, q domain.PageQuery) (domain.Page[json.RawMessage], error) {
	page, e := q.Normalized()
	if e != nil {
		return domain.Page[json.RawMessage]{}, e
	}
	from := `FROM documents p,json_each(p.payload,'$.memberships') m LEFT JOIN documents u ON u.kind='user' AND u.id=json_extract(m.value,'$.user_id')`
	where := `p.kind='project' AND p.id=?`
	args := []any{p}
	if q.Query != "" {
		where += ` AND instr(lower(COALESCE(json_extract(u.payload,'$.name'),'')||' '||COALESCE(json_extract(u.payload,'$.login_id'),'')||' '||json_extract(m.value,'$.user_id')),lower(?))>0`
		args = append(args, q.Query)
	}
	projection := `json_object('user',json(COALESCE(u.payload,json_object('id',json_extract(m.value,'$.user_id'),'name',CASE json_extract(m.value,'$.user_id') WHEN 'local-owner' THEN '本地管理员' ELSE json_extract(m.value,'$.user_id') END))),'roles',json(COALESCE(json_extract(m.value,'$.roles'),json_array(json_extract(m.value,'$.role')))),'responsibilities',json(COALESCE(json_extract(m.value,'$.responsibilities'),'[]')),'department_name','')`
	return queryJSONPage(c, d, projection, from, where, "CAST(m.key AS INTEGER)", args, page)
}
func (d *DB) QueryTodos(c context.Context, user string, open bool, q domain.PageQuery, projects []string) (domain.Page[json.RawMessage], error) {
	page, e := q.Normalized()
	if e != nil {
		return domain.Page[json.RawMessage]{}, e
	}
	where := "t.open=?"
	args := []any{open}
	if user != "local-owner" {
		where += " AND EXISTS(SELECT 1 FROM notify_recipients r WHERE r.todo_id=t.id AND r.user_id=?)"
		args = append(args, user)
	}
	if len(projects) == 0 {
		where += " AND 0"
	} else {
		marks := []string{}
		for _, p := range projects {
			marks = append(marks, "?")
			args = append(args, p)
		}
		where += " AND json_extract(t.payload,'$.project_id') IN (" + strings.Join(marks, ",") + ")"
	}
	// Apply page grants before both COUNT and LIMIT. Historical recipients can
	// remain after a role changes, so recipient membership alone is insufficient.
	if user != "local-owner" {
		where += ` AND EXISTS(SELECT 1 FROM documents p,json_each(p.payload,'$.memberships') m
		 WHERE p.kind='project' AND p.id=json_extract(t.payload,'$.project_id') AND json_extract(m.value,'$.user_id')=?
		 AND (COALESCE(json_extract(p.payload,'$.access_version'),0)=0 OR EXISTS(
		 SELECT 1 FROM json_each(p.payload,'$.project_roles') role,json_each(role.value,'$.pages') grant_page
		 WHERE EXISTS(SELECT 1 FROM json_each(m.value,'$.roles') member_role WHERE member_role.value=json_extract(role.value,'$.id'))
		 AND CASE WHEN json_extract(t.payload,'$.kind') IN ('test','retest','verify','acceptance') THEN grant_page.value='reviews'
		 WHEN json_extract(t.payload,'$.kind')='release' THEN grant_page.value IN ('reviews','versions')
		 WHEN json_extract(t.payload,'$.kind')='bug_rejection' THEN (grant_page.value='tasks' OR (grant_page.value='reviews' AND EXISTS(SELECT 1 FROM tasks source_task WHERE source_task.id=t.task_id AND source_task.project_id=p.id AND COALESCE(json_extract(source_task.payload,'$.parent_requirement_id'),'')<>'')))
		 ELSE grant_page.value='tasks' END)))`
		args = append(args, user)
	}
	return queryJSONPage(c, d, "t.payload", "FROM notify_todos t", where, "t.created_at DESC,t.id DESC", args, page)
}
