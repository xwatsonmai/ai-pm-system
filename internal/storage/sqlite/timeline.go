package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
)

func (d *DB) ProjectTimelineEvents(c context.Context, p string, from, until int64, offset, limit int) ([]domain.Event, int, error) {
	var total int
	e := d.db.QueryRowContext(c, "SELECT count(*) FROM events WHERE project_id=? AND (task_id<>'' OR kind LIKE 'project_version_%') AND at>=? AND at<?", p, from, until).Scan(&total)
	if e != nil {
		return nil, 0, e
	}
	rows, e := d.db.QueryContext(c, "SELECT seq,project_id,task_id,kind,actor,body,data,at FROM events WHERE project_id=? AND (task_id<>'' OR kind LIKE 'project_version_%') AND at>=? AND at<? ORDER BY at DESC,seq DESC LIMIT ? OFFSET ?", p, from, until, limit, offset)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []domain.Event{}
	for rows.Next() {
		var ev domain.Event
		var data string
		if e = rows.Scan(&ev.Seq, &ev.ProjectID, &ev.TaskID, &ev.Kind, &ev.Actor, &ev.Body, &data, &ev.At); e != nil {
			return nil, 0, e
		}
		ev.Data = []byte(data)
		out = append(out, ev)
	}
	return out, total, rows.Err()
}
