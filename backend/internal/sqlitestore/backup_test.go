package sqlitestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExistingWriteConnectionNeverCreatesMissingDatabase(t *testing.T) {
	path := ownedPath(t, "missing.sqlite")
	if db, err := OpenReadWriteExisting(context.Background(), path); err == nil {
		_ = db.Close()
		t.Fatal("write-existing created missing database")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed open changed filesystem")
	}
}

func TestBackupWithReservedWriterPreservesWALAndBlocksOtherWriters(t *testing.T) {
	for _, mode := range []string{"DELETE", "WAL"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			path := ownedPath(t, "source.sqlite")
			createFixture(t, path)
			writer, err := OpenReadWriteExisting(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			for pragma, want := range map[string]int{"foreign_keys": 1, "synchronous": 2, "temp_store": 2, "busy_timeout": 5000} {
				var got int
				if err := writer.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil || got != want {
					t.Fatalf("write connection %s=%d, want %d: %v", pragma, got, want, err)
				}
			}
			if _, err := writer.ExecContext(ctx, "PRAGMA journal_mode="+mode+"; PRAGMA wal_autocheckpoint=0; PRAGMA user_version=17; INSERT INTO sample VALUES (2,'only in live WAL',NULL,NULL)"); err != nil {
				t.Fatal(err)
			}
			tx, err := writer.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			reader, err := OpenReadOnly(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			readConn, err := reader.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer readConn.Close()
			destination := filepath.Join(filepath.Dir(path), "recovery.sqlite")
			if err := os.WriteFile(destination, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := BackupIntoEmpty(ctx, readConn, destination); err != nil {
				t.Fatalf("reserved writer + separate backup: %v; cause %v", err, errors.Unwrap(err))
			}
			competing, err := OpenReadWriteExisting(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := competing.ExecContext(ctx, "PRAGMA busy_timeout=1; INSERT INTO sample VALUES (3,'must not appear',NULL,NULL)"); err == nil {
				_ = competing.Close()
				t.Fatal("BEGIN IMMEDIATE did not reserve the writer")
			}
			if err := competing.Close(); err != nil {
				t.Fatal(err)
			}
			backup, err := OpenReadOnly(ctx, destination)
			if err != nil {
				t.Fatal(err)
			}
			defer backup.Close()
			backupConn, err := backup.Conn(ctx)
			if err != nil {
				_ = backup.Close()
				t.Fatal(err)
			}
			defer backupConn.Close()
			if err := VerifyBackup(ctx, backupConn, 17); err != nil {
				t.Fatal(err)
			}
			var count int
			var journal string
			if err := backupConn.QueryRowContext(ctx, "SELECT count(*) FROM sample").Scan(&count); err != nil || count != 2 {
				t.Fatalf("backup dropped committed WAL row: %v, count %d", err, count)
			}
			if err := backupConn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil || journal != "delete" {
				t.Fatalf("recovery image depends on WAL: %q, %v", journal, err)
			}
			if err := errors.Join(backupConn.Close(), backup.Close()); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(destination, destination+".closed"); err != nil {
				t.Fatal("backup connection leaked a native handle", err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBackupRefusesNonemptyDestinationAndCanceledWork(t *testing.T) {
	path := ownedPath(t, "source.sqlite")
	createFixture(t, path)
	db, err := OpenReadOnly(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := BackupIntoEmpty(context.Background(), conn, path); err == nil {
		t.Fatal("nonempty source overwritten as destination")
	}
	missing := filepath.Join(filepath.Dir(path), "missing.sqlite")
	if err := BackupIntoEmpty(context.Background(), conn, missing); err == nil {
		t.Fatal("backup created an unreserved destination")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := BackupIntoEmpty(ctx, conn, missing); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled work continued: %v", err)
	}
	if err := VerifyBackup(context.Background(), conn, 99); err == nil {
		t.Fatal("wrong recovery version accepted")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled backup created file")
	}
}

func TestBackupWriteTransactionOnSourceFailsWithoutHanging(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	path := ownedPath(t, "source.sqlite")
	createFixture(t, path)
	db, err := OpenReadWriteExisting(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	destination := filepath.Join(filepath.Dir(path), "recovery.sqlite")
	if err := os.WriteFile(destination, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := BackupIntoEmpty(ctx, conn, destination); err == nil {
		t.Fatal("backup from a write transaction unexpectedly succeeded")
	}
	if ctx.Err() != nil {
		t.Fatal("same-connection misuse hung instead of failing")
	}
}
