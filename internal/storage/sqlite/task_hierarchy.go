package sqlite

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var _ storage.TaskTreeQueries = (*DB)(nil)

// UNION bounds ancestor traversal even for malformed cyclic/imported data.
// Cycles use their smallest member as a root; only that root's parent edge is
// cut. Missing and foreign-project parents never add another project's data.
const taskTreeCTE = `WITH RECURSIVE
nodes AS (
 SELECT t.id,COALESCE(parent.id,'') parent_id,
 COALESCE(json_extract(t.payload,'$.updated_at'),0) updated_at,
 CASE WHEN parent.id IS NULL THEN '' WHEN COALESCE(json_extract(t.payload,'$.parent_requirement_id'),'')<>'' THEN 'bug' ELSE 'iteration' END relation
 FROM tasks t LEFT JOIN tasks parent ON parent.project_id=t.project_id AND parent.id=
 COALESCE(NULLIF(json_extract(t.payload,'$.parent_requirement_id'),''),json_extract(t.payload,'$.iteration.parent_task_id'))
 WHERE t.project_id=?
), ancestors(child_id,ancestor_id) AS (
 SELECT id,id FROM nodes UNION
 SELECT a.child_id,n.parent_id FROM ancestors a JOIN nodes n ON n.id=a.ancestor_id WHERE n.parent_id<>''
), cyclic AS (
 SELECT n.id FROM nodes n WHERE n.parent_id<>'' AND EXISTS(SELECT 1 FROM ancestors a WHERE a.child_id=n.parent_id AND a.ancestor_id=n.id)
), families AS (
 SELECT a.child_id id,COALESCE(MAX(CASE WHEN n.parent_id='' THEN n.id END),MIN(c.id)) root_id
 FROM ancestors a JOIN nodes n ON n.id=a.ancestor_id LEFT JOIN cyclic c ON c.id=n.id GROUP BY a.child_id
), tree AS (
 SELECT n.id,CASE WHEN f.root_id=n.id THEN '' ELSE n.parent_id END parent_id,n.relation
 FROM nodes n JOIN families f ON f.id=n.id
), matches AS (
 SELECT t.id,COALESCE(json_extract(t.payload,'$.updated_at'),0) updated_at,CASE WHEN ` + pendingReview + ` THEN 1 ELSE 0 END pending
 ` + taskJoin + ` WHERE %s
), branches AS (
 SELECT tree.id,tree.parent_id,tree.relation,MAX(m.updated_at) sort_updated,COUNT(*) matching_count,SUM(m.pending) pending_count
 FROM tree JOIN ancestors a ON a.ancestor_id=tree.id JOIN matches m ON m.id=a.child_id GROUP BY tree.id,tree.parent_id,tree.relation
) `

func (d *DB) QueryTaskTree(c context.Context, project string, q domain.TaskTreeQuery) (domain.TaskTreePage, error) {
	out := domain.TaskTreePage{Page: domain.Page[domain.TaskTreeNode]{Items: []domain.TaskTreeNode{}}}
	page, err := q.PageQuery.Normalized()
	if err != nil {
		return out, err
	}
	if len(q.ParentID) > 256 {
		return out, errors.New("无效的层级父节点")
	}
	q.PageQuery = page
	where, args, err := taskListWhere(project, q.TaskListQuery)
	if err != nil {
		return out, err
	}
	// The SQL template is static; filter values remain bound parameters.
	cte := fmt.Sprintf(taskTreeCTE, where)
	base := append([]any{project}, args...)
	tx, err := d.db.BeginTx(c, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if q.ParentID != "" {
		var found int
		if err = tx.QueryRowContext(c, "SELECT 1 FROM tasks WHERE project_id=? AND id=?", project, q.ParentID).Scan(&found); errors.Is(err, sql.ErrNoRows) {
			return out, domain.ErrNotFound
		} else if err != nil {
			return out, err
		}
	}
	if err = tx.QueryRowContext(c, cte+"SELECT (SELECT COUNT(*) FROM branches WHERE parent_id=?),(SELECT COUNT(*) FROM matches)", append(append([]any{}, base...), q.ParentID)...).Scan(&out.Total, &out.MatchingTotal); err != nil {
		return out, err
	}
	if out.Total == 0 {
		page.Page = 1
	} else if page.Offset() >= out.Total {
		page.Page = (out.Total-1)/page.PageSize + 1
	}
	out.Page.Page, out.Page.PageSize = page.Page, page.PageSize
	query := cte + `SELECT ` + taskListProjection + `,b.parent_id,CASE WHEN b.parent_id='' THEN '' ELSE b.relation END,
 EXISTS(SELECT 1 FROM matches m WHERE m.id=b.id),(SELECT COUNT(*) FROM branches children WHERE children.parent_id=b.id),b.matching_count,b.pending_count
 FROM branches b JOIN tasks t ON t.id=b.id WHERE b.parent_id=? ORDER BY b.sort_updated DESC,b.id DESC LIMIT ? OFFSET ?`
	values := append(append([]any{}, base...), q.ParentID, page.PageSize, page.Offset())
	rows, err := tx.QueryContext(c, query, values...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var raw string
		var node domain.TaskTreeNode
		if err = rows.Scan(&raw, &node.TreeParentID, &node.Relation, &node.MatchesFilter, &node.ChildrenCount, &node.MatchingCount, &node.PendingCount); err != nil {
			rows.Close()
			return out, err
		}
		if err = json.Unmarshal([]byte(raw), &node.Task); err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, node)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
