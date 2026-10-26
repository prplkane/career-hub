package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"os"

	"modernc.org/sqlite"
)

const MaxBackupBytes int64 = 512 << 20

type BackupError struct {
	Stage string
	cause error
}

func (e *BackupError) Error() string { return "SQLite recovery copy failed at " + e.Stage }
func (e *BackupError) Unwrap() error { return e.cause }

// BackupIntoEmpty creates a SQLite-consistent copy in a caller-reserved empty
// ordinary file. It does not create directories or overwrite a nonempty file.
// The caller must retain native guards for both paths and serialize writers
// through a DIFFERENT connection. SQLite refuses backup from a source connection
// that itself owns a write transaction. Cancellation is checked between bounded
// page batches, never by abandoning a goroutine with live native resources.
// Failure can leave an incomplete destination; do not publish recovery metadata.
func BackupIntoEmpty(ctx context.Context, source *sql.Conn, destination string) error {
	if ctx == nil || source == nil {
		return &BackupError{Stage: "precondition"}
	}
	if err := ctx.Err(); err != nil {
		return &BackupError{Stage: "canceled", cause: err}
	}
	dsn, err := readWriteURI(destination, true)
	if err != nil {
		return &BackupError{Stage: "destination", cause: err}
	}
	// This check is not a containment or race-free VFS guarantee; the caller's
	// owned empty-file reservation and retained guard are required separately.
	info, err := os.Lstat(destination)
	if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
		return &BackupError{Stage: "destination"}
	}
	var pages, pageSize int64
	if err := source.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return &BackupError{Stage: "size", cause: err}
	}
	if err := source.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
		return &BackupError{Stage: "size", cause: err}
	}
	if pageSize < 512 || pageSize > 65536 || pages < 0 || pages > MaxBackupBytes/pageSize {
		return &BackupError{Stage: "limit"}
	}
	err = source.Raw(func(raw any) (result error) {
		backuper, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return &BackupError{Stage: "driver"}
		}
		if err := ctx.Err(); err != nil {
			return &BackupError{Stage: "canceled", cause: err}
		}
		copy, err := backuper.NewBackup(dsn)
		if err != nil {
			return &BackupError{Stage: "open", cause: err}
		}
		defer func() {
			// Commit finishes the backup handle even after failure/incomplete
			// stepping (SQLite rolls back an incomplete copy). Unlike Finish,
			// the successful path returns the destination for checked Close.
			destination, finishErr := copy.Commit()
			if finishErr != nil {
				result = &BackupError{Stage: "finish", cause: errors.Join(result, finishErr)}
			}
			if destination != nil {
				if closeErr := destination.Close(); closeErr != nil {
					result = &BackupError{Stage: "close", cause: errors.Join(result, closeErr)}
				}
			}
		}()
		for {
			if err := ctx.Err(); err != nil {
				return &BackupError{Stage: "canceled", cause: err}
			}
			more, err := copy.Step(128)
			if err != nil {
				return &BackupError{Stage: "step", cause: err}
			}
			if int64(copy.PageCount()) > MaxBackupBytes/pageSize {
				return &BackupError{Stage: "limit"}
			}
			if !more {
				if err := ctx.Err(); err != nil {
					return &BackupError{Stage: "canceled", cause: err}
				}
				return nil
			}
		}
	})
	if err != nil {
		return err
	}
	// The backup copies page1, including WAL-format header bits. Setting the
	// destination's mode only before copying is insufficient. Reopen the copied
	// image, explicitly convert it to DELETE, then close every handle so a ready
	// recovery image is standalone and does not depend on separate WAL/SHM files.
	return makeStandalone(ctx, destination)
}

func makeStandalone(ctx context.Context, path string) (result error) {
	db, err := OpenReadWriteExisting(ctx, path)
	if err != nil {
		return &BackupError{Stage: "standalone-open", cause: err}
	}
	defer func() {
		if err := db.Close(); err != nil {
			result = &BackupError{Stage: "close", cause: errors.Join(result, err)}
		}
	}()
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&mode); err != nil || mode != "delete" {
		return &BackupError{Stage: "standalone-mode", cause: err}
	}
	return nil
}

// VerifyBackup checks the persisted copy after all backup-native handles close.
// Foreign-key validity is deliberately not required: a recovery image must also
// preserve a legacy database whose dangling references make upgrading fail.
func VerifyBackup(ctx context.Context, source *sql.Conn, expectedVersion int) error {
	if ctx == nil || source == nil || expectedVersion < 0 {
		return &BackupError{Stage: "precondition"}
	}
	var version int
	if err := source.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return &BackupError{Stage: "version", cause: err}
	}
	if version != expectedVersion {
		return &BackupError{Stage: "version"}
	}
	rows, err := source.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return &BackupError{Stage: "integrity", cause: err}
	}
	var result string
	valid := rows.Next() && rows.Scan(&result) == nil && result == "ok"
	if valid && rows.Next() {
		valid = false
	}
	err = errors.Join(rows.Err(), rows.Close())
	if !valid || err != nil {
		return &BackupError{Stage: "integrity", cause: err}
	}
	return nil
}
