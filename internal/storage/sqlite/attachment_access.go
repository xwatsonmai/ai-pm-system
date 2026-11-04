package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
)

// Query only persisted attachment metadata, including frozen task snapshots and
// deleted runs. A deleted binding must not make a private file a draft again.
func (d *DB) AttachmentReferences(c context.Context, project, id string) ([]domain.AttachmentReference, error) {
	rows, e := d.db.QueryContext(c, `WITH owners(kind,id,payload) AS (
	 SELECT 'task',id,payload FROM tasks WHERE project_id=?
	 UNION ALL SELECT 'task',r.task_id,r.payload FROM requirement_reviews r JOIN tasks t ON t.id=r.task_id WHERE t.project_id=?
	 UNION ALL SELECT 'run',id,payload FROM documents WHERE kind='run' AND project_id=?
	 UNION ALL SELECT 'intake',id,payload FROM requirement_intakes WHERE project_id=?
	 UNION ALL SELECT 'version',id,payload FROM project_versions WHERE project_id=?
	) SELECT DISTINCT o.kind,o.id FROM owners o,json_tree(o.payload) j
	 WHERE j.key='id' AND j.atom=? AND j.path LIKE '%.attachments[%]'`, project, project, project, project, project, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.AttachmentReference{}
	for rows.Next() {
		var ref domain.AttachmentReference
		if e = rows.Scan(&ref.Kind, &ref.ID); e != nil {
			return nil, e
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}
