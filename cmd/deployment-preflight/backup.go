package main

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
)

// backupDatabase is deliberately independent of account readiness: recovery
// copies must still be possible when an account or production env needs repair.
// It never migrates, provisions or writes to the source database.
func backupDatabase(parent context.Context, o options) (result report, finalErr error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	source, _, err := existingDatabase(o.db)
	if err != nil {
		return result, err
	}
	if !filepath.IsAbs(o.backup) {
		return result, errors.New("the backup output must be a new absolute private path")
	}
	directory, err := filepath.EvalSymlinks(filepath.Dir(o.backup))
	if err != nil {
		return result, errors.New("the backup directory must already exist")
	}
	target := filepath.Join(directory, filepath.Base(o.backup))
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if target == source+suffix {
			return result, errors.New("the backup must not replace a source database or its sidecars")
		}
		if _, err := os.Lstat(target + suffix); err == nil || !os.IsNotExist(err) {
			return result, errors.New("the backup path or its sidecars already exist")
		}
	}
	if o.web != "" {
		web, err := filepath.EvalSymlinks(o.web)
		if err != nil {
			return result, errors.New("unable to verify the backup is outside the web directory")
		}
		if rel, err := filepath.Rel(web, target); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return result, errors.New("backups must not be written inside the web directory")
		}
	}
	// O_EXCL also rejects existing symlinks, hardlinks and a source-path target.
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return result, errors.New("unable to create a new backup; existing files are never overwritten")
	}
	outputInfo, err := output.Stat()
	if err != nil {
		output.Close()
		return result, errors.New("unable to verify the new backup file")
	}
	defer func() {
		output.Close()
		if finalErr != nil {
			// Only remove the exact empty/partial output created by this call.
			// Never touch a replaced file, another snapshot or the source WAL.
			if current, err := os.Lstat(target); err == nil && os.SameFile(outputInfo, current) {
				_ = os.Remove(target)
			}
		}
	}()
	srcDB, err := sql.Open("sqlite", backupURI(source, "ro", "query_only(ON)"))
	if err != nil {
		return result, errors.New("unable to open the backup source read-only")
	}
	defer srcDB.Close()
	srcDB.SetMaxOpenConns(1)
	if err := srcDB.QueryRowContext(ctx, `SELECT sqlite_version()`).Scan(&result.SQLiteVersion); err != nil || !walFixVersion(result.SQLiteVersion) {
		return result, errors.New("backup requires the SQLite WAL-reset fix")
	}
	conn, err := srcDB.Conn(ctx)
	if err != nil {
		return result, errors.New("unable to read the backup source")
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) (copyErr error) {
		backuper, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("embedded online backup is unavailable")
		}
		backup, err := backuper.NewBackup(backupURI(target, "rw", "journal_mode(DELETE)", "synchronous(FULL)"))
		if err != nil {
			return errors.New("unable to initialize the new backup")
		}
		defer func() {
			if err := backup.Finish(); err != nil && copyErr == nil {
				copyErr = errors.New("unable to finalize the new backup")
			}
		}()
		for {
			if err := ctx.Err(); err != nil {
				return errors.New("backup was cancelled or exceeded its two-minute time limit")
			}
			more, err := backup.Step(256)
			if err != nil {
				var sqliteErr *sqlite.Error
				if !errors.As(err, &sqliteErr) || (sqliteErr.Code()&255 != 5 && sqliteErr.Code()&255 != 6) {
					return errors.New("unable to copy the database snapshot")
				}
				select {
				case <-ctx.Done():
					return errors.New("backup was cancelled or exceeded its two-minute time limit")
				case <-time.After(25 * time.Millisecond):
				}
				continue
			}
			if !more {
				return nil
			}
		}
	})
	if err != nil {
		return result, err
	}
	if err := verifyBackup(ctx, target); err != nil {
		return result, err
	}
	if err := output.Sync(); err != nil {
		return result, errors.New("unable to sync the verified backup to disk")
	}
	dir, err := os.Open(directory)
	if err != nil {
		return result, errors.New("unable to sync the backup directory")
	}
	err = dir.Sync()
	dir.Close()
	if err != nil {
		return result, errors.New("unable to sync the backup directory")
	}
	result.BackedUp, result.Ready, result.BackupPath = true, true, target
	return result, nil
}

func backupURI(path, mode string, pragmas ...string) string {
	values := url.Values{"mode": []string{mode}, "_pragma": append([]string{"busy_timeout(5000)"}, pragmas...)}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: values.Encode()}).String()
}

func verifyBackup(ctx context.Context, path string) error {
	// The online backup API can copy the source WAL journal-mode header. Change
	// ONLY the newly created destination to DELETE so the verified result is a
	// standalone file, with any destination WAL checkpointed before returning.
	db, err := sql.Open("sqlite", backupURI(path, "rw", "journal_mode(DELETE)", "synchronous(FULL)"))
	if err != nil {
		return errors.New("unable to reopen the new backup")
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return errors.New("new backup integrity_check failed")
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return errors.New("unable to verify new backup foreign keys")
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		return errors.New("new backup foreign_key_check failed")
	}
	return nil
}
