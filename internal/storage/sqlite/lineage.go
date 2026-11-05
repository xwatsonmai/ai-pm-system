package sqlite

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"context"
	"database/sql"
	"errors"
	"strings"
)

var _ storage.LineageQueries = (*DB)(nil)

// Counts use the entire non-removed version scope, including preserved historical
// revisions. They are independent of graph paging, search and expansion state.
const lineageVersionColumns = `v.id,COALESCE(json_extract(v.payload,'$.name'),''),COALESCE(json_extract(v.payload,'$.status'),''),
 (SELECT COUNT(*) FROM json_each(v.payload,'$.scope') s WHERE COALESCE(json_extract(s.value,'$.removed'),0)=0 AND COALESCE(json_extract(s.value,'$.task.kind'),'requirement')<>'bug'),
 (SELECT COUNT(*) FROM json_each(v.payload,'$.scope') s WHERE COALESCE(json_extract(s.value,'$.removed'),0)=0 AND json_extract(s.value,'$.task.kind')='bug'),
 (SELECT COUNT(*) FROM json_each(v.payload,'$.scope') s WHERE COALESCE(json_extract(s.value,'$.removed'),0)=0 AND json_extract(s.value,'$.task.status') IN ('done','resolved'))`

func scanLineageVersion(row versionScanner) (domain.LineageVersion, error) {
	var v domain.LineageVersion
	e := row.Scan(&v.ID, &v.Name, &v.Status, &v.Requirements, &v.Bugs, &v.Completed)
	if errors.Is(e, sql.ErrNoRows) {
		e = domain.ErrNotFound
	}
	return v, e
}

func (d *DB) LineageVersions(c context.Context, project string, q domain.PageQuery) (domain.Page[domain.LineageVersion], error) {
	out := domain.Page[domain.LineageVersion]{Items: []domain.LineageVersion{}}
	q, e := q.Normalized()
	if e != nil {
		return out, e
	}
	tx, e := d.db.BeginTx(c, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	where := `v.project_id=? AND instr(lower(COALESCE(json_extract(v.payload,'$.name'),'')),lower(?))>0`
	args := []any{project, strings.TrimSpace(q.Query)}
	if e = tx.QueryRowContext(c, "SELECT COUNT(*) FROM project_versions v WHERE "+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	if out.Total == 0 {
		q.Page = 1
	} else if q.Offset() >= out.Total {
		q.Page = (out.Total-1)/q.PageSize + 1
	}
	out.Page, out.PageSize = q.Page, q.PageSize
	rows, e := tx.QueryContext(c, "SELECT "+lineageVersionColumns+" FROM project_versions v WHERE "+where+` ORDER BY CASE json_extract(v.payload,'$.status') WHEN 'active' THEN 0 WHEN 'planned' THEN 1 ELSE 2 END,v.rowid DESC LIMIT ? OFFSET ?`, append(args, q.PageSize, q.Offset())...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		v, err := scanLineageVersion(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}

// The selected version's scope takes precedence over current tasks: a task may
// have moved on to another revision/version after a historical release. Foreign
// parents are never joined. UNION bounds cycles; the smallest cycle ID is the
// root whose incoming edge is cut, matching the requirement-pool contract.
const lineageCTE = `WITH RECURSIVE
 scope AS (SELECT json_extract(s.value,'$.task.id') id,json_set(json_extract(s.value,'$.task'),'$.project_version_id',v.id) payload FROM project_versions v,json_each(v.payload,'$.scope') s WHERE v.project_id=? AND v.id=? AND COALESCE(json_extract(s.value,'$.removed'),0)=0 AND json_extract(s.value,'$.task.project_id')=v.project_id),
 data AS (SELECT id,payload,1 in_scope FROM scope UNION ALL SELECT t.id,t.payload,0 FROM tasks t WHERE t.project_id=? AND t.id NOT IN (SELECT id FROM scope)),
 nodes AS (SELECT n.*,COALESCE(p.id,'') parent_id,CASE WHEN p.id IS NULL THEN '' WHEN COALESCE(json_extract(n.payload,'$.parent_requirement_id'),'')<>'' THEN 'bug' WHEN COALESCE(json_extract(n.payload,'$.parent_task_id'),'')<>'' THEN 'subrequirement' ELSE 'iteration' END relation
 FROM data n LEFT JOIN data p ON p.id=COALESCE(NULLIF(json_extract(n.payload,'$.parent_requirement_id'),''),NULLIF(json_extract(n.payload,'$.parent_task_id'),''),json_extract(n.payload,'$.iteration.parent_task_id'))),
 ancestors(child_id,ancestor_id) AS (SELECT id,id FROM nodes UNION SELECT a.child_id,n.parent_id FROM ancestors a JOIN nodes n ON n.id=a.ancestor_id WHERE n.parent_id<>''),
 cyclic AS (SELECT n.id FROM nodes n WHERE n.parent_id<>'' AND EXISTS(SELECT 1 FROM ancestors a WHERE a.child_id=n.parent_id AND a.ancestor_id=n.id)),
 families AS (SELECT a.child_id id,COALESCE(MAX(CASE WHEN n.parent_id='' THEN n.id END),MIN(c.id)) root_id FROM ancestors a JOIN nodes n ON n.id=a.ancestor_id LEFT JOIN cyclic c ON c.id=n.id GROUP BY a.child_id),
 tree AS (SELECT n.*,CASE WHEN f.root_id=n.id THEN '' ELSE n.parent_id END tree_parent FROM nodes n JOIN families f ON f.id=n.id),
 matches AS (SELECT id,COALESCE(json_extract(payload,'$.updated_at'),0) updated FROM tree WHERE in_scope=1 AND instr(lower(id||' '||COALESCE(json_extract(payload,'$.title'),'')||' '||COALESCE(json_extract(payload,'$.owner'),'')),lower(?))>0),
 branches AS (SELECT a.ancestor_id id,MAX(m.updated) updated FROM ancestors a JOIN matches m ON m.id=a.child_id GROUP BY a.ancestor_id),
 visible AS (SELECT t.* FROM tree t WHERE EXISTS(SELECT 1 FROM branches b WHERE b.id=t.id) OR (?='' AND EXISTS(SELECT 1 FROM tree p WHERE p.id=t.tree_parent AND p.in_scope=1))) `

func (d *DB) QueryLineage(c context.Context, project, version string, q domain.LineageQuery) (domain.LineagePage, error) {
	out := domain.LineagePage{Page: domain.Page[domain.LineageNode]{Items: []domain.LineageNode{}}}
	page, e := q.PageQuery.Normalized()
	if e != nil {
		return out, e
	}
	if len(q.ParentID) > 256 {
		return out, errors.New("无效的谱系父节点")
	}
	tx, e := d.db.BeginTx(c, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	out.Version, e = scanLineageVersion(tx.QueryRowContext(c, "SELECT "+lineageVersionColumns+" FROM project_versions v WHERE v.project_id=? AND v.id=?", project, version))
	if e != nil {
		return out, e
	}
	query := strings.TrimSpace(page.Query)
	base := []any{project, version, project, query, query}
	if q.ParentID != "" {
		var allowed bool
		e = tx.QueryRowContext(c, lineageCTE+"SELECT EXISTS(SELECT 1 FROM visible WHERE id=?)", append(append([]any{}, base...), q.ParentID)...).Scan(&allowed)
		if e != nil {
			return out, e
		}
		if !allowed {
			return out, domain.ErrNotFound
		}
	}
	where := `v.tree_parent=?`
	if q.ParentID == "" {
		where += ` AND EXISTS(SELECT 1 FROM branches b WHERE b.id=v.id)`
	} else {
		// Cross-version reference nodes are terminals; open their actual version
		// before browsing further. Ancestor context may follow the matched path.
		where += ` AND EXISTS(SELECT 1 FROM visible p WHERE p.id=? AND (p.in_scope=1 OR EXISTS(SELECT 1 FROM branches b WHERE b.id=p.id)))`
	}
	args := append(append([]any{}, base...), q.ParentID)
	if q.ParentID != "" {
		args = append(args, q.ParentID)
	}
	e = tx.QueryRowContext(c, lineageCTE+"SELECT (SELECT COUNT(*) FROM visible v WHERE "+where+"),(SELECT COUNT(*) FROM matches)", args...).Scan(&out.Total, &out.MatchingTotal)
	if e != nil {
		return out, e
	}
	if out.Total == 0 {
		page.Page = 1
	} else if page.Offset() >= out.Total {
		page.Page = (out.Total-1)/page.PageSize + 1
	}
	out.Page.Page, out.Page.PageSize = page.Page, page.PageSize
	columns := `v.id,COALESCE(json_extract(v.payload,'$.title'),''),COALESCE(json_extract(v.payload,'$.kind'),'requirement'),COALESCE(json_extract(v.payload,'$.status'),''),COALESCE(json_extract(v.payload,'$.owner'),''),COALESCE(json_extract(v.payload,'$.revision'),0),COALESCE(json_extract(v.payload,'$.updated_at'),0),COALESCE(json_extract(v.payload,'$.project_version_id'),''),COALESCE((SELECT json_extract(pv.payload,'$.name') FROM project_versions pv WHERE pv.project_id=? AND pv.id=json_extract(v.payload,'$.project_version_id')),''),v.tree_parent,CASE WHEN v.tree_parent='' THEN '' ELSE v.relation END,v.in_scope,EXISTS(SELECT 1 FROM branches b WHERE b.id=v.id),CASE WHEN v.in_scope=1 OR EXISTS(SELECT 1 FROM branches b WHERE b.id=v.id) THEN (SELECT COUNT(*) FROM visible child WHERE child.tree_parent=v.id) ELSE 0 END,NOT EXISTS(SELECT 1 FROM tasks actual WHERE actual.project_id=? AND actual.id=v.id AND COALESCE(json_extract(actual.payload,'$.revision'),0)=COALESCE(json_extract(v.payload,'$.revision'),0) AND COALESCE(json_extract(actual.payload,'$.project_version_id'),'')=COALESCE(json_extract(v.payload,'$.project_version_id'),'')),EXISTS(SELECT 1 FROM tasks actual WHERE actual.id=v.id AND actual.project_id=json_extract(v.payload,'$.project_id'))`
	// The project used for names occurs after the CTE bindings, before WHERE.
	values := append(append([]any{}, base...), project, project, q.ParentID)
	if q.ParentID != "" {
		values = append(values, q.ParentID)
	}
	values = append(values, page.PageSize, page.Offset())
	rows, e := tx.QueryContext(c, lineageCTE+"SELECT "+columns+" FROM visible v WHERE "+where+` ORDER BY v.id LIMIT ? OFFSET ?`, values...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var n domain.LineageNode
		e = rows.Scan(&n.ID, &n.Title, &n.Kind, &n.Status, &n.Owner, &n.Revision, &n.UpdatedAt, &n.ProjectVersionID, &n.VersionName, &n.ParentID, &n.Relation, &n.InScope, &n.PathContext, &n.ChildrenCount, &n.Historical, &n.CurrentAvailable)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, n)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
