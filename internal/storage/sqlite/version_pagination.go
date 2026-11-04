package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Listing queries never load requirement documents or release snapshots. Counts
// are computed over the full scope before paging the version rows.
const versionListingColumns = `json_remove(v.payload,'$.scope','$.releases','$.history'),
 COALESCE(json_array_length(v.payload,'$.scope'),0),
 (SELECT COUNT(*) FROM json_each(v.payload,'$.scope') WHERE COALESCE(json_extract(value,'$.removed'),0)=1),
 (SELECT COALESCE(json_group_object(status,n),'{}') FROM
  (SELECT json_extract(value,'$.task.status') status,COUNT(*) n FROM json_each(v.payload,'$.scope') WHERE COALESCE(json_extract(value,'$.removed'),0)=0 GROUP BY status)),
 (SELECT COUNT(*) FROM json_each(v.payload,'$.scope') WHERE COALESCE(json_extract(value,'$.removed'),0)=0 AND COALESCE(json_extract(value,'$.task.blocker'),'')<>'' AND json_extract(value,'$.task.status') NOT IN ('closed','rejected','canceled','resolved')),
 (SELECT COUNT(*) FROM json_each(v.payload,'$.scope') WHERE COALESCE(json_extract(value,'$.removed'),0)=0 AND json_extract(value,'$.task.status') IN ('review','done') AND json_extract(value,'$.task.deployment.revision')=json_extract(value,'$.task.revision'))`

type versionScanner interface{ Scan(...any) error }

func scanVersionListing(row versionScanner) (domain.ProjectVersion, error) {
	var payload, statuses string
	counts := domain.ProjectVersionCounts{ByStatus: map[string]int{}}
	if e := row.Scan(&payload, &counts.Total, &counts.Removed, &statuses, &counts.Blocked, &counts.Released); e != nil {
		return domain.ProjectVersion{}, e
	}
	v, e := decode[domain.ProjectVersion](payload)
	if e != nil {
		return v, e
	}
	if e = json.Unmarshal([]byte(statuses), &counts.ByStatus); e != nil {
		return v, e
	}
	for status, n := range counts.ByStatus {
		if domain.TaskTerminated(status) || status == "resolved" {
			counts.Ended += n
			continue
		}
		counts.Included += n
		switch status {
		case "testing", "fixing", "retest", "verifying", "pending_release", "review", "done":
			counts.DevelopmentDone += n
		}
	}
	counts.Accepted = counts.ByStatus["done"]
	v.ListingCounts = &counts
	return v, nil
}
func (d *DB) ListProjectVersionsPage(c context.Context, p string, q domain.VersionListQuery) (domain.Page[domain.ProjectVersion], error) {
	var e error
	q.PageQuery, e = q.PageQuery.Normalized()
	if e != nil {
		return domain.Page[domain.ProjectVersion]{}, e
	}
	out := domain.Page[domain.ProjectVersion]{Items: []domain.ProjectVersion{}, Page: q.Page, PageSize: q.PageSize}
	where := "v.project_id=?"
	args := []any{p}
	if q.Status != "" {
		where += " AND json_extract(v.payload,'$.status')=?"
		args = append(args, q.Status)
	}
	if q.Query != "" {
		expression := "json_extract(v.payload,'$.name')"
		if !q.NamesOnly {
			expression += " || ' ' || COALESCE(json_extract(v.payload,'$.goal'),'')"
		}
		where += " AND instr(lower(" + expression + "),lower(?))>0"
		args = append(args, strings.TrimSpace(q.Query))
	}
	if e = d.db.QueryRowContext(c, "SELECT COUNT(*) FROM project_versions v WHERE "+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	args = append(args, q.PageSize, q.Offset())
	rows, e := d.db.QueryContext(c, "SELECT "+versionListingColumns+" FROM project_versions v WHERE "+where+" ORDER BY v.rowid DESC LIMIT ? OFFSET ?", args...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		v, e := scanVersionListing(rows)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	return out, rows.Err()
}
func (d *DB) GetProjectVersionListing(c context.Context, id string) (domain.ProjectVersion, error) {
	v, e := scanVersionListing(d.db.QueryRowContext(c, "SELECT "+versionListingColumns+" FROM project_versions v WHERE v.id=?", id))
	if errors.Is(e, sql.ErrNoRows) {
		e = domain.ErrNotFound
	}
	return v, e
}
func (d *DB) ProjectVersionScopePage(c context.Context, id string, q domain.VersionScopeQuery) (domain.Page[domain.VersionScopeItem], error) {
	var e error
	q.PageQuery, e = q.PageQuery.Normalized()
	if e != nil {
		return domain.Page[domain.VersionScopeItem]{}, e
	}
	out := domain.Page[domain.VersionScopeItem]{Items: []domain.VersionScopeItem{}, Page: q.Page, PageSize: q.PageSize}
	where := "v.id=?"
	args := []any{id}
	if !q.IncludeRemoved {
		where += " AND COALESCE(json_extract(j.value,'$.removed'),0)=0"
	}
	if q.Status != "" {
		where += " AND json_extract(j.value,'$.task.status')=?"
		args = append(args, q.Status)
	}
	if q.Query != "" {
		where += " AND instr(lower(json_extract(j.value,'$.task.title')),lower(?))>0"
		args = append(args, q.Query)
	}
	source := " FROM project_versions v,json_each(v.payload,'$.scope') j WHERE " + where
	if e = d.db.QueryRowContext(c, "SELECT COUNT(*)"+source, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	args = append(args, q.PageSize, q.Offset())
	rows, e := d.db.QueryContext(c, "SELECT j.value"+source+" ORDER BY CAST(j.key AS INTEGER) LIMIT ? OFFSET ?", args...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		if e = rows.Scan(&payload); e != nil {
			return out, e
		}
		v, e := decode[domain.VersionScopeItem](payload)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	return out, rows.Err()
}
func (d *DB) ProjectVersionReleasesPage(c context.Context, id string, q domain.PageQuery) (domain.Page[domain.VersionRelease], error) {
	var e error
	q, e = q.Normalized()
	if e != nil {
		return domain.Page[domain.VersionRelease]{}, e
	}
	out := domain.Page[domain.VersionRelease]{Items: []domain.VersionRelease{}, Page: q.Page, PageSize: q.PageSize}
	source := " FROM project_versions v,json_each(v.payload,'$.releases') j WHERE v.id=?"
	if e = d.db.QueryRowContext(c, "SELECT COUNT(*)"+source, id).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := d.db.QueryContext(c, "SELECT json_remove(j.value,'$.tasks'),COALESCE(json_array_length(j.value,'$.tasks'),0)"+source+" ORDER BY CAST(j.key AS INTEGER) DESC LIMIT ? OFFSET ?", id, q.PageSize, q.Offset())
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		var count int
		if e = rows.Scan(&payload, &count); e != nil {
			return out, e
		}
		v, e := decode[domain.VersionRelease](payload)
		if e != nil {
			return out, e
		}
		v.TaskCount = count
		out.Items = append(out.Items, v)
	}
	return out, rows.Err()
}
func (d *DB) ProjectVersionReleaseTasksPage(c context.Context, id, release string, q domain.PageQuery) (domain.Page[domain.Task], error) {
	var e error
	q, e = q.Normalized()
	if e != nil {
		return domain.Page[domain.Task]{}, e
	}
	out := domain.Page[domain.Task]{Items: []domain.Task{}, Page: q.Page, PageSize: q.PageSize}
	source := " FROM project_versions v,json_each(v.payload,'$.releases') r,json_each(r.value,'$.tasks') j WHERE v.id=? AND json_extract(r.value,'$.id')=?"
	if e = d.db.QueryRowContext(c, "SELECT COUNT(*)"+source, id, release).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := d.db.QueryContext(c, "SELECT j.value"+source+" ORDER BY CAST(j.key AS INTEGER) LIMIT ? OFFSET ?", id, release, q.PageSize, q.Offset())
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		if e = rows.Scan(&payload); e != nil {
			return out, e
		}
		v, e := decode[domain.Task](payload)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	return out, rows.Err()
}

func (d *DB) ProjectVersionHistoryPage(c context.Context, id string, q domain.PageQuery) (domain.Page[domain.VersionHistoryEntry], error) {
	var e error
	q, e = q.Normalized()
	if e != nil {
		return domain.Page[domain.VersionHistoryEntry]{}, e
	}
	out := domain.Page[domain.VersionHistoryEntry]{Items: []domain.VersionHistoryEntry{}, Page: q.Page, PageSize: q.PageSize}
	source := " FROM project_versions v,json_each(v.payload,'$.history') j WHERE v.id=?"
	if e = d.db.QueryRowContext(c, "SELECT COUNT(*)"+source, id).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := d.db.QueryContext(c, "SELECT j.value"+source+" ORDER BY CAST(j.key AS INTEGER) DESC LIMIT ? OFFSET ?", id, q.PageSize, q.Offset())
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		if e = rows.Scan(&payload); e != nil {
			return out, e
		}
		v, e := decode[domain.VersionHistoryEntry](payload)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	return out, rows.Err()
}
