package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"errors"
)

// Old worker artifacts used analysis_<exact run ID>_<12 hex call hash>.
// Match that complete shape rather than treating a broad ID prefix as a grant.
const generatedAnalysisID = `(d.id GLOB 'analysis_*_*' AND length(d.id)>22 AND substr(d.id,-12) NOT GLOB '*[^0-9a-f]*')`
const artifactSourceJoin = ` LEFT JOIN documents source_run ON source_run.kind='run' AND source_run.project_id=d.project_id AND source_run.id=COALESCE(NULLIF(json_extract(d.payload,'$.origin_run_id'),''),CASE WHEN ` + generatedAnalysisID + ` THEN substr(d.id,10,length(d.id)-22) END)
 LEFT JOIN documents source_project ON source_project.kind='project' AND source_project.id=d.project_id
 LEFT JOIN documents source_conversation ON source_conversation.kind='conversation' AND source_conversation.project_id=d.project_id AND source_conversation.id=COALESCE(NULLIF(json_extract(source_run.payload,'$.conversation_id'),''),source_run.id)`
const artifactOwner = `CASE WHEN json_extract(source_run.payload,'$.kind')='question' AND COALESCE(json_extract(source_run.payload,'$.owner_id'),'')='' THEN COALESCE(NULLIF(json_extract(source_project.payload,'$.owner_id'),''),'local-owner') ELSE COALESCE(json_extract(source_run.payload,'$.owner_id'),'') END`
const artifactOwnerKind = `CASE WHEN json_extract(source_run.payload,'$.kind')='question' AND COALESCE(json_extract(source_run.payload,'$.owner_id'),'')='' THEN 'human' ELSE COALESCE(json_extract(source_run.payload,'$.owner_kind'),'') END`
const artifactOriginUnavailable = `((source_run.id IS NULL AND (COALESCE(json_extract(d.payload,'$.origin_run_id'),'')<>'' OR ` + generatedAnalysisID + `)) OR (json_extract(source_run.payload,'$.kind')='question' AND COALESCE(json_extract(source_conversation.payload,'$.deleted_at'),0)<>0))`
const artifactProjection = `json_set(d.payload,'$.origin_run_id',COALESCE(source_run.id,json_extract(d.payload,'$.origin_run_id'),''),'$.origin_owner_id',` + artifactOwner + `,'$.origin_owner_kind',` + artifactOwnerKind + `,'$.origin_run_kind',COALESCE(json_extract(source_run.payload,'$.kind'),''),'$.origin_unavailable',json(CASE WHEN ` + artifactOriginUnavailable + ` THEN 'true' ELSE 'false' END))`

func (d *DB) ListArtifacts(c context.Context, project string) ([]domain.Artifact, error) {
	where, args := "d.kind='artifact'", []any{}
	if project != "" {
		where += " AND d.project_id=?"
		args = append(args, project)
	}
	rows, e := d.db.QueryContext(c, "SELECT "+artifactProjection+" FROM documents d"+artifactSourceJoin+" WHERE "+where+" ORDER BY d.rowid", args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Artifact{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		v, err := decode[domain.Artifact](raw)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) GetArtifact(c context.Context, id string) (domain.Artifact, error) {
	var raw string
	e := d.db.QueryRowContext(c, "SELECT "+artifactProjection+" FROM documents d"+artifactSourceJoin+" WHERE d.kind='artifact' AND d.id=?", id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return domain.Artifact{}, domain.ErrNotFound
	}
	if e != nil {
		return domain.Artifact{}, e
	}
	return decode[domain.Artifact](raw)
}
