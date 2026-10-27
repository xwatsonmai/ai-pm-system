package sqlite

import (
	"ai-pm-system/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

func (d *DB) GetCurrentMilestone(c context.Context, p string) (domain.CurrentMilestone, error) {
	state := domain.CurrentMilestone{ProjectID: p}
	e := d.db.QueryRowContext(c, "SELECT version_id,version FROM current_milestones WHERE project_id=?", p).Scan(&state.VersionID, &state.Version)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	}
	return state, e
}
func writeMilestoneBatches(c context.Context, tx *sql.Tx, batches []domain.MilestoneBatch) error {
	for _, b := range batches {
		if b.State != nil {
			s := b.State
			if s.Version != b.Expected+1 {
				return domain.ErrConflict
			}
			var r sql.Result
			var e error
			if b.Expected == 0 {
				r, e = tx.ExecContext(c, "INSERT OR IGNORE INTO current_milestones(project_id,version_id,version) VALUES(?,?,?)", s.ProjectID, s.VersionID, s.Version)
			} else {
				r, e = tx.ExecContext(c, "UPDATE current_milestones SET version_id=?,version=? WHERE project_id=? AND version=?", s.VersionID, s.Version, s.ProjectID, b.Expected)
			}
			if e != nil {
				return e
			}
			if n, _ := r.RowsAffected(); n != 1 {
				return domain.ErrConflict
			}
		}
		for _, v := range b.Versions {
			if e := writeProjectVersion(c, tx, v.Value, v.Expected); e != nil {
				return e
			}
		}
		for _, ev := range b.Events {
			if e := insertEvent(c, tx, ev); e != nil {
				return e
			}
		}
	}
	return nil
}

// Upgrade preserves old plans as queued plans, selects at most one as current,
// and backfills missing associations without changing requirements or claims.
func (d *DB) migrateMilestones() error {
	c := context.Background()
	tx, e := d.db.BeginTx(c, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var exists int
	e = tx.QueryRow("SELECT 1 FROM schema_migrations WHERE version=11").Scan(&exists)
	if e == nil {
		return nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	rows, e := tx.Query("SELECT payload FROM project_versions ORDER BY rowid DESC")
	if e != nil {
		return e
	}
	versions := map[string][]domain.ProjectVersion{}
	for rows.Next() {
		var b string
		if e = rows.Scan(&b); e != nil {
			rows.Close()
			return e
		}
		v, x := decode[domain.ProjectVersion](b)
		if x != nil {
			rows.Close()
			return x
		}
		versions[v.ProjectID] = append(versions[v.ProjectID], v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	rows, e = tx.Query("SELECT payload,claim_hash FROM tasks ORDER BY rowid")
	if e != nil {
		return e
	}
	tasks := map[string][]domain.Task{}
	for rows.Next() {
		var b, claim string
		if e = rows.Scan(&b, &claim); e != nil {
			rows.Close()
			return e
		}
		t, x := decode[domain.Task](b)
		if x != nil {
			rows.Close()
			return x
		}
		t.ClaimHash = claim
		tasks[t.ProjectID] = append(tasks[t.ProjectID], t)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	projects := map[string]bool{}
	for p := range versions {
		projects[p] = true
	}
	for p := range tasks {
		projects[p] = true
	}
	ids := []string{}
	for p := range projects {
		ids = append(ids, p)
	}
	sort.Strings(ids)
	now := domain.Now()
	for _, p := range ids {
		vs := versions[p]
		current := -1
		for i := range vs {
			if vs[i].Status != "closed" && vs[i].Status != "completed" {
				current = i
				break
			}
		}
		missing := false
		for _, t := range tasks[p] {
			if t.ProjectVersionID == "" {
				missing = true
			}
		}
		if current < 0 && missing {
			vs = append(vs, newMigratedVersion(p, vs, "默认版本", now, "active"))
			current = len(vs) - 1
		}
		state := domain.CurrentMilestone{ProjectID: p, Version: 1}
		if current >= 0 {
			state.VersionID = vs[current].ID
		}
		for i := range vs {
			v := &vs[i]
			old := v.Version
			changed := old == 0
			if i == current {
				v.Status = "active"
				if v.StartedAt == 0 {
					v.StartedAt = v.CreatedAt
					if v.StartedAt == 0 {
						v.StartedAt = now
					}
				}
				v.ScopeFrozen = false
				changed = true
			} else if v.Status != "closed" && v.Status != "completed" {
				v.Status = "planned"
				v.ScopeFrozen = false
				changed = true
			}
			if (v.Status == "closed" || v.Status == "completed") && v.EndedAt == 0 {
				v.EndedAt = v.UpdatedAt
				if v.EndedAt == 0 {
					v.EndedAt = now
				}
				changed = true
			}
			if (v.Status == "closed" || v.Status == "completed") && v.StartedAt == 0 {
				v.StartedAt = v.CreatedAt
				if v.StartedAt == 0 {
					v.StartedAt = v.EndedAt
				}
				changed = true
			}
			for _, original := range tasks[p] {
				if original.ProjectVersionID != "" {
					continue
				}
				if current != i {
					continue
				}
				t := original
				t.ProjectVersionID = v.ID
				t.Version++
				t.UpdatedAt = now
				b, _ := json.Marshal(t)
				if _, e = tx.Exec("UPDATE tasks SET version=?,payload=? WHERE id=?", t.Version, string(b), t.ID); e != nil {
					return e
				}
				v.Scope = append(v.Scope, domain.VersionScopeItem{Task: t, Published: t.Deployment != nil && t.Deployment.Revision == t.Revision})
				changed = true
				data, _ := json.Marshal(map[string]string{"project_version_id": v.ID})
				if e = insertEvent(c, tx, domain.Event{ProjectID: p, TaskID: t.ID, Kind: "milestone_assigned", Actor: "system", Body: "升级为里程碑管理，保留原文和状态", Data: data, At: now}); e != nil {
					return e
				}
			}
			if changed {
				v.Version = old + 1
				v.UpdatedAt = now
				v.History = append(v.History, domain.VersionHistoryEntry{Action: "milestone_upgrade", Body: "切换为当前里程碑管理，原计划与发布历史保留", Actor: "system", At: now})
				if e = writeProjectVersion(c, tx, *v, old); e != nil {
					return e
				}
			}
		}
		if e = writeMilestoneBatches(c, tx, []domain.MilestoneBatch{{State: &state}}); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("CREATE INDEX IF NOT EXISTS events_timeline ON events(project_id,at,seq)"); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO schema_migrations(version) VALUES(11)"); e != nil {
		return e
	}
	return tx.Commit()
}
func newMigratedVersion(p string, vs []domain.ProjectVersion, label string, now int64, status string) domain.ProjectVersion {
	name := label
	for n := 1; ; n++ {
		used := false
		for _, v := range vs {
			if v.Name == name {
				used = true
			}
		}
		if !used {
			break
		}
		name = fmt.Sprintf("%s %d", label, n)
	}
	return domain.ProjectVersion{ID: domain.ID("ver_"), ProjectID: p, Name: name, Status: status, CreatedAt: now, UpdatedAt: now, Scope: []domain.VersionScopeItem{}, History: []domain.VersionHistoryEntry{}, Releases: []domain.VersionRelease{}}
}
