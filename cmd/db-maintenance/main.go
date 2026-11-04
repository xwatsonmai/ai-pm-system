// db-maintenance uses the bundled SQLite engine without application migrations,
// background jobs, or notification workers. Host SQLite versions need not parse
// the application's expression indexes.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"modernc.org/sqlite"
)

func main() { os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || (args[0] != "idle" && args[0] != "backup" && args[0] != "check") ||
		(args[0] == "backup" && len(args) != 3) || (args[0] != "backup" && len(args) != 2) {
		fmt.Fprintln(stderr, "usage: db-maintenance idle SOURCE_DB | backup SOURCE_DB DEST_DB | check SOURCE_DB")
		return 2
	}
	var err error
	switch args[0] {
	case "backup":
		err = backup(ctx, args[1], args[2])
	default:
		var db *sql.DB
		db, err = openReadOnly(ctx, args[1])
		if err == nil {
			if args[0] == "idle" {
				err = idle(ctx, db)
			} else {
				err = quickCheck(ctx, db)
			}
			err = errors.Join(err, db.Close())
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "db-maintenance:", err)
		return 1
	}
	fmt.Fprintln(stdout, "ok")
	return 0
}

func databaseURI(path, mode string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	q := url.Values{"mode": {mode}}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func openReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("source database must be a regular file")
	}
	uri, err := databaseURI(path, "ro")
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, "PRAGMA query_only=ON; PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func idle(ctx context.Context, db *sql.DB) error {
	// One statement observes all work queues at the same SQLite snapshot and
	// returns only counts; task bodies and model prompts never leave the DB.
	var runs, reviews, replies int
	err := db.QueryRowContext(ctx, `SELECT
	 (SELECT count(*) FROM documents WHERE kind='run' AND json_extract(payload,'$.status') IN ('queued','running','pending')),
	 (SELECT count(*) FROM requirement_reviews WHERE json_extract(payload,'$.status') IN ('queued','running','pending')),
	 (SELECT count(*) FROM reply_jobs WHERE json_extract(payload,'$.status') IN ('queued','running','pending'))`).Scan(&runs, &reviews, &replies)
	if err != nil {
		return fmt.Errorf("cannot check active work: %w", err)
	}
	if runs+reviews+replies != 0 {
		return fmt.Errorf("active Agent work; retry after it finishes (runs=%d reviews=%d reply_jobs=%d)", runs, reviews, replies)
	}
	return nil
}

func quickCheck(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return fmt.Errorf("database quick_check failed: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var result string
		if err = rows.Scan(&result); err != nil {
			return err
		}
		if result != "ok" {
			return errors.New("database quick_check reported corruption")
		}
		count++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if count != 1 {
		return errors.New("database quick_check returned an unexpected result")
	}
	return nil
}

func backup(ctx context.Context, source, destination string) error {
	db, err := openReadOnly(ctx, source)
	if err != nil {
		return err
	}
	defer db.Close()
	owner, err := os.Stat(source)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(destination); err == nil {
		return errors.New("backup destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// A private file in the destination directory permits an atomic no-replace
	// publication after the complete snapshot has passed integrity checks.
	file, err := os.CreateTemp(filepath.Dir(destination), ".db-maintenance-*.db")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() {
		for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
			os.Remove(temporary + suffix)
		}
	}()
	if err = file.Close(); err != nil {
		return err
	}
	uri, err := databaseURI(temporary, "rw")
	if err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		backuper, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("bundled SQLite engine does not support online backup")
		}
		copy, err := backuper.NewBackup(uri)
		if err != nil {
			return err
		}
		for {
			if err = ctx.Err(); err != nil {
				return errors.Join(err, copy.Finish())
			}
			more, stepErr := copy.Step(128)
			if stepErr != nil {
				return errors.Join(stepErr, copy.Finish())
			}
			if !more {
				return copy.Finish()
			}
		}
	})
	if err != nil {
		return fmt.Errorf("online backup failed: %w", err)
	}
	if err = finishBackup(ctx, temporary, uri); err != nil {
		return err
	}
	if err = preserveOwner(temporary, owner); err != nil {
		return err
	}
	// Link refuses an existing destination, including one created while the
	// snapshot was running. The temporary file is removed by the deferred cleanup.
	if err = os.Link(temporary, destination); err != nil {
		return fmt.Errorf("publish backup without replacement: %w", err)
	}
	return nil
}

func finishBackup(ctx context.Context, path, uri string) error {
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	var mode string
	err = db.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&mode)
	if err == nil && !strings.EqualFold(mode, "delete") {
		err = errors.New("backup did not switch to standalone DELETE journal mode")
	}
	if err == nil {
		err = quickCheck(ctx, db)
	}
	if err = errors.Join(err, db.Close()); err != nil {
		return err
	}
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
