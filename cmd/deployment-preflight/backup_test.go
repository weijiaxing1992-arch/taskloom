package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestOnlineBackupIncludesCommittedWALAndIsStandalone(t *testing.T) {
	source, _ := fixture(t)
	db, err := sql.Open("sqlite", backupURI(source, "rw", "journal_mode(WAL)", "wal_autocheckpoint(0)"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`UPDATE requirements SET title='committed WAL requirement'; CREATE TABLE attachment_bytes(id INTEGER PRIMARY KEY, data BLOB); INSERT INTO attachment_bytes VALUES(1,?)`, bytes.Repeat([]byte{0, 4, 8, 255}, 20000)); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(source + "-wal"); err != nil || info.Size() == 0 {
		t.Fatal("test source has no uncheckpointed WAL")
	}
	before, _ := os.ReadFile(source)
	walBefore, _ := os.ReadFile(source + "-wal")
	output := filepath.Join(t.TempDir(), "complete.db")
	r, err := run(context.Background(), options{db: source, backup: output})
	canonicalOutput, _ := filepath.EvalSymlinks(output)
	if err != nil || !r.BackedUp || !r.Ready || r.BackupPath != canonicalOutput || r.Provisioned {
		t.Fatalf("backup failed: %+v %v", r, err)
	}
	after, _ := os.ReadFile(source)
	walAfter, _ := os.ReadFile(source + "-wal")
	if !bytes.Equal(before, after) || !bytes.Equal(walBefore, walAfter) {
		t.Fatal("backup modified the source database or its WAL")
	}
	if info, err := os.Stat(output); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("backup file must be private")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(output + suffix); !os.IsNotExist(err) {
			t.Fatalf("backup is not a self-contained snapshot: %s", suffix)
		}
	}
	// A moved single-file copy must include the committed WAL-only rows and BLOBs.
	snapshotBytes, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(moved, snapshotBytes, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Open("sqlite", backupURI(moved, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var title string
	var blob []byte
	if err := restored.QueryRow(`SELECT title FROM requirements WHERE id=42`).Scan(&title); err != nil || title != "committed WAL requirement" {
		t.Fatal("WAL requirement missing from standalone backup")
	}
	if err := restored.QueryRow(`SELECT data FROM attachment_bytes WHERE id=1`).Scan(&blob); err != nil || !bytes.Equal(blob, bytes.Repeat([]byte{0, 4, 8, 255}, 20000)) {
		t.Fatal("attachment bytes missing from standalone backup")
	}
}

func TestBackupNeverOverwritesSourceLinksOrExistingFiles(t *testing.T) {
	source, _ := fixture(t)
	existing := filepath.Join(t.TempDir(), "existing.db")
	if err := os.WriteFile(existing, []byte("preserve existing backup"), 0600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(t.TempDir(), "link.db")
	if err := os.Symlink(source, symlink); err != nil {
		t.Fatal(err)
	}
	hardlink := filepath.Join(filepath.Dir(source), "hardlink.db")
	if err := os.Link(source, hardlink); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(source)
	for _, output := range []string{source, source + "-wal", source + "-shm", source + "-journal", existing, symlink, hardlink, "relative.db"} {
		if _, err := run(context.Background(), options{db: source, backup: output}); err == nil {
			t.Fatal("unsafe backup target accepted")
		}
	}
	after, _ := os.ReadFile(source)
	if !bytes.Equal(before, after) {
		t.Fatal("source overwritten")
	}
	content, _ := os.ReadFile(existing)
	if string(content) != "preserve existing backup" {
		t.Fatal("existing backup overwritten")
	}
}

func TestBackupValidationFailureAndCancellationRemoveOnlyNewOutput(t *testing.T) {
	for _, kind := range []string{"cancel", "foreign-keys", "provision-mixed", "missing-source"} {
		t.Run(kind, func(t *testing.T) {
			source, _ := fixture(t)
			output := filepath.Join(t.TempDir(), "new.db")
			o := options{db: source, backup: output}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "cancel":
				cancel()
			case "foreign-keys":
				db, err := sql.Open("sqlite", source)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec(`CREATE TABLE orphan(id INTEGER REFERENCES requirements(id)); INSERT INTO orphan VALUES(999999)`)
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "provision-mixed":
				o.provision = true
			case "missing-source":
				o.db = filepath.Join(t.TempDir(), "missing.db")
			}
			if _, err := run(ctx, o); err == nil {
				t.Fatal("unsafe or failed backup reported success")
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("failed backup left a purported snapshot")
			}
		})
	}
}

func TestOnlineBackupWithConcurrentTransactions(t *testing.T) {
	source, _ := fixture(t)
	db, err := sql.Open("sqlite", backupURI(source, "rw", "journal_mode(WAL)", "wal_autocheckpoint(0)"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE consistent_pair(id INTEGER PRIMARY KEY,value INTEGER); INSERT INTO consistent_pair VALUES(1,0),(2,0)`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for n := 1; n <= 40; n++ {
			if _, err := db.Exec(`UPDATE consistent_pair SET value=?`, n); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for n := 0; n < 3; n++ {
		output := filepath.Join(t.TempDir(), fmt.Sprintf("snapshot-%d.db", n))
		if _, err := run(context.Background(), options{db: source, backup: output}); err != nil {
			t.Fatal(err)
		}
		backup, err := sql.Open("sqlite", backupURI(output, "ro"))
		if err != nil {
			t.Fatal(err)
		}
		var distinct int
		err = backup.QueryRow(`SELECT COUNT(DISTINCT value) FROM consistent_pair`).Scan(&distinct)
		backup.Close()
		if err != nil || distinct != 1 {
			t.Fatal("snapshot contains a partial transaction")
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
