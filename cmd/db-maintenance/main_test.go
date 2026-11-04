package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (string, *sql.DB) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "spaces & # query?")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "source database?.db")
	uri, err := databaseURI(path, "rwc")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`
	 PRAGMA journal_mode=WAL;
	 PRAGMA wal_autocheckpoint=0;
	 CREATE TABLE documents(kind TEXT,id TEXT,payload TEXT,PRIMARY KEY(kind,id));
	 CREATE TABLE requirement_reviews(task_id TEXT PRIMARY KEY,payload TEXT);
	 CREATE TABLE reply_jobs(id TEXT PRIMARY KEY,payload TEXT);
	 CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY);
	 INSERT INTO schema_migrations VALUES(14);
	 CREATE INDEX documents_expression ON documents(kind,json_extract(payload,'$.status'));
	 PRAGMA wal_checkpoint(TRUNCATE);`)
	if err != nil {
		t.Fatal(err)
	}
	return path, db
}

func invoke(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := run(context.Background(), args, &out, &stderr)
	return code, out.String(), stderr.String()
}

func digest(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func TestOnlineBackupIncludesCommittedWALAndLeavesWriterUsable(t *testing.T) {
	source, writer := fixture(t)
	const original = `{"status":"succeeded","body":"private original preserved"}`
	if _, err := writer.Exec("INSERT INTO documents VALUES('run','committed',?)", original); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`CREATE TABLE bulk(payload BLOB);
	 WITH RECURSIVE seq(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM seq WHERE x<320)
	 INSERT INTO bulk SELECT zeroblob(4096) FROM seq`); err != nil {
		t.Fatal(err)
	}
	var pages int
	if err := writer.QueryRow("PRAGMA page_count").Scan(&pages); err != nil || pages <= 128 {
		t.Fatal("fixture does not exercise multiple Backup.Step calls", pages, err)
	}
	// This row is still exclusively in WAL; the main file has no document rows.
	uri, _ := databaseURI(source, "ro")
	mainOnly, err := sql.Open("sqlite", uri+"&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	var mainCount int
	err = mainOnly.QueryRow("SELECT count(*) FROM documents").Scan(&mainCount)
	mainOnly.Close()
	if err != nil || mainCount != 0 {
		t.Fatal("fixture is not WAL-only", mainCount, err)
	}
	mainBefore, walBefore := digest(t, source), digest(t, source+"-wal")
	// An in-flight writer must remain uncommitted in the backup and must still
	// be able to commit after the backup releases its read connection.
	tx, err := writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO documents VALUES('run','uncommitted','{\"status\":\"succeeded\"}')"); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(filepath.Dir(source), "snapshot with spaces #?.db")
	if code, out, stderr := invoke(t, "backup", source, destination); code != 0 || out != "ok\n" || stderr != "" {
		t.Fatal("backup failed", code, out, stderr)
	}
	if digest(t, source) != mainBefore || digest(t, source+"-wal") != walBefore {
		t.Fatal("backup modified source main file or committed WAL")
	}
	uri, _ = databaseURI(destination, "ro")
	// Reading immutable mode proves the result needs no WAL/SHM sidecar.
	backupDB, err := sql.Open("sqlite", uri+"&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer backupDB.Close()
	var count, schema, indexes int
	var body, mode string
	if err = backupDB.QueryRow("SELECT count(*) FROM documents").Scan(&count); err != nil || count != 1 {
		t.Fatal("backup omitted WAL or included uncommitted work", count, err)
	}
	var bulkCount, bulkBytes int
	if err = backupDB.QueryRow("SELECT count(*),sum(length(payload)) FROM bulk").Scan(&bulkCount, &bulkBytes); err != nil || bulkCount != 320 || bulkBytes != 320*4096 {
		t.Fatal("multi-step backup lost pages", bulkCount, bulkBytes, err)
	}
	if err = backupDB.QueryRow("SELECT payload FROM documents WHERE id='committed'").Scan(&body); err != nil || body != original {
		t.Fatal("backup changed source text", body, err)
	}
	if err = backupDB.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "delete" {
		t.Fatal("backup is not a standalone DELETE database", mode, err)
	}
	if err = backupDB.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&schema); err != nil || schema != 14 {
		t.Fatal("backup ran application migrations", schema, err)
	}
	if err = backupDB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name='documents_expression'").Scan(&indexes); err != nil || indexes != 1 {
		t.Fatal("expression index lost", indexes, err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err = os.Stat(destination + suffix); !os.IsNotExist(err) {
			t.Fatal("backup requires a sidecar", suffix, err)
		}
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("backup is not private", info, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal("backup prevented source writer from committing", err)
	}
	if _, err = writer.Exec("INSERT INTO reply_jobs VALUES('later','{\"status\":\"succeeded\"}')"); err != nil {
		t.Fatal("source writer unusable after backup", err)
	}
	if err = writer.QueryRow("SELECT count(*) FROM documents").Scan(&count); err != nil || count != 2 {
		t.Fatal("source rows changed unexpectedly", count, err)
	}
	if err = writer.QueryRow("SELECT payload FROM documents WHERE id='committed'").Scan(&body); err != nil || body != original {
		t.Fatal("source original text changed", body, err)
	}
	if err = writer.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatal("backup changed source journal mode", mode, err)
	}
	if code, out, stderr := invoke(t, "check", destination); code != 0 || out != "ok\n" || stderr != "" {
		t.Fatal("backup check failed", code, out, stderr)
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(source), ".db-maintenance-*"))
	if len(files) != 0 {
		t.Fatal("backup retained temporary files", files)
	}
}

func TestIdleDetectsEachQueueAndActiveStatusWithoutExposingText(t *testing.T) {
	for _, table := range []string{"documents", "requirement_reviews", "reply_jobs"} {
		for _, status := range []string{"queued", "running", "pending"} {
			t.Run(table+"/"+status, func(t *testing.T) {
				source, db := fixture(t)
				payload := fmt.Sprintf(`{"status":%q,"body":"sensitive-business-sentinel"}`, status)
				var err error
				if table == "documents" {
					_, err = db.Exec("INSERT INTO documents VALUES('run','active',?)", payload)
				} else {
					_, err = db.Exec("INSERT INTO "+table+" VALUES('active',?)", payload)
				}
				if err != nil {
					t.Fatal(err)
				}
				before, wal := digest(t, source), digest(t, source+"-wal")
				code, out, stderr := invoke(t, "idle", source)
				if code == 0 || out != "" || !strings.Contains(stderr, "active Agent work") || strings.Contains(stderr, "sentinel") {
					t.Fatal("active queue accepted or text exposed", code, out, stderr)
				}
				if digest(t, source) != before || digest(t, source+"-wal") != wal {
					t.Fatal("idle probe changed the source")
				}
			})
		}
	}
}

func TestIdleAndCheckAreReadOnlyAndDoNotMigrate(t *testing.T) {
	source, writer := fixture(t)
	if _, err := writer.Exec(`INSERT INTO documents VALUES('project','not-work','{"status":"running"}'); INSERT INTO documents VALUES('run','done','{"status":"succeeded"}'); INSERT INTO requirement_reviews VALUES('done','{"status":"confirmed"}'); INSERT INTO reply_jobs VALUES('done','{"status":"succeeded"}')`); err != nil {
		t.Fatal(err)
	}
	before, wal := digest(t, source), digest(t, source+"-wal")
	for _, command := range []string{"idle", "check"} {
		if code, out, stderr := invoke(t, command, source); code != 0 || out != "ok\n" || stderr != "" {
			t.Fatal(command, code, out, stderr)
		}
	}
	reader, err := openReadOnly(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.Exec("INSERT INTO schema_migrations VALUES(15)"); err == nil {
		t.Fatal("maintenance connection permits source writes")
	}
	reader.Close()
	if digest(t, source) != before || digest(t, source+"-wal") != wal {
		t.Fatal("read-only probes changed source bytes")
	}
}

func TestBackupRefusesExistingDestinationAndSymlink(t *testing.T) {
	source, _ := fixture(t)
	for _, link := range []bool{false, true} {
		t.Run(fmt.Sprint(link), func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, "keep.db")
			if err := os.WriteFile(target, []byte("keep this exact existing file"), 0640); err != nil {
				t.Fatal(err)
			}
			destination := target
			if link {
				destination = filepath.Join(directory, "link.db")
				if err := os.Symlink(target, destination); err != nil {
					t.Fatal(err)
				}
			}
			before := digest(t, target)
			if code, out, _ := invoke(t, "backup", source, destination); code == 0 || out != "" {
				t.Fatal("existing destination accepted", code, out)
			}
			if digest(t, target) != before {
				t.Fatal("existing destination overwritten")
			}
		})
	}
	if code, _, _ := invoke(t, "backup", source, source); code == 0 {
		t.Fatal("backup accepts the source as its own destination")
	}
}

func TestFailuresDoNotCreateOrPublishDatabases(t *testing.T) {
	directory := t.TempDir()
	missing := filepath.Join(directory, "missing.db")
	for _, command := range []string{"idle", "check"} {
		if code, out, _ := invoke(t, command, missing); code == 0 || out != "" {
			t.Fatal("missing database accepted", command)
		}
		if _, err := os.Stat(missing); !os.IsNotExist(err) {
			t.Fatal("probe created the source database", err)
		}
	}
	corrupt := filepath.Join(directory, "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("not a SQLite database"), 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(directory, "backup.db")
	if code, out, _ := invoke(t, "backup", corrupt, destination); code == 0 || out != "" {
		t.Fatal("corrupt backup accepted")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("failed backup published a destination", err)
	}
	if code, out, _ := invoke(t, "check", corrupt); code == 0 || out != "" {
		t.Fatal("corrupt database passed check")
	}
	files, _ := filepath.Glob(filepath.Join(directory, ".db-maintenance-*"))
	if len(files) != 0 {
		t.Fatal("failed backup retained temporary files", files)
	}
}
