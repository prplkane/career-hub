package homebackup

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

const databasePath = "applications/career_hub.sqlite3"

type databaseSnapshot struct {
	RelativePath  string
	SchemaVersion int64
	File          File
}

// withDatabaseSnapshot retains the source's IMMEDIATE writer reservation through
// consume, using a DIFFERENT read connection for SQLite's backup API. The caller
// must run in a dedicated admitted maintenance process, with managed/legacy file
// writers stopped. A SQLite reservation alone is not a multi-file Home snapshot.
// No schema initialization/upgrade, commit, listener or credential call occurs.
func withDatabaseSnapshot(ctx context.Context, dataRoot, snapshot string, consume func(*runtimepaths.Root, *databaseSnapshot) *Error) (problem *Error) {
	return withDatabaseSnapshotFrom(ctx, dataRoot, snapshot, databasePath, consume)
}

// Only the active backup source or a fixed private physical copy inside this
// reserved snapshot may be opened. Restore validation uses the latter so SQLite
// recovery/checkpoint/SHM effects cannot touch the active source or rollback tree.
func withDatabaseSnapshotFrom(ctx context.Context, dataRoot, snapshot, sourceRelative string, consume func(*runtimepaths.Root, *databaseSnapshot) *Error) (problem *Error) {
	if ctx == nil || ctx.Err() != nil {
		return failure("BACKUP_CANCELED")
	}
	if consume == nil || !strings.HasPrefix(snapshot, snapshotPrefix) || !lowerHex(strings.TrimPrefix(snapshot, snapshotPrefix), 32) {
		return failure("BACKUP_SNAPSHOT_INVALID")
	}
	if sourceRelative != databasePath && sourceRelative != snapshot+"/.physical/"+databasePath {
		return failure("BACKUP_DATABASE_PATH_INVALID")
	}
	root, pathProblem := runtimepaths.Open(dataRoot)
	if pathProblem != nil {
		return failure("BACKUP_SOURCE_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			problem = failure("BACKUP_DATABASE_CLEANUP_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	guard, pathProblem := root.GuardSQLiteFiles(sourceRelative)
	if pathProblem != nil {
		return failure("BACKUP_DATABASE_PATH_INVALID")
	}
	if guard == nil {
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if present, p := root.Exists(sourceRelative + suffix); p != nil || present {
				return failure("BACKUP_DATABASE_ORPHANED_FILES")
			}
		}
		problem = consume(root, nil)
		if ctx.Err() != nil {
			return failure("BACKUP_CANCELED")
		}
		return problem
	}
	defer func() {
		if guard.Close() != nil {
			problem = failure("BACKUP_DATABASE_CLEANUP_FAILED")
		}
	}()
	sourcePath := filepath.Join(dataRoot, filepath.FromSlash(sourceRelative))
	db, err := sqlitestore.OpenReadWriteExisting(ctx, sourcePath)
	if err != nil {
		return failure("BACKUP_DATABASE_OPEN_FAILED")
	}
	defer func() {
		if db.Close() != nil {
			problem = failure("BACKUP_DATABASE_CLEANUP_FAILED")
		}
	}()
	writer, err := db.Conn(ctx)
	if err != nil {
		return failure("BACKUP_DATABASE_OPEN_FAILED")
	}
	defer func() {
		if err := writer.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			problem = failure("BACKUP_DATABASE_CLEANUP_FAILED")
		}
	}()
	// Own rollback synchronously, as schema preparation does. Native lock waiting
	// is bounded by the connection's busy_timeout; don't abandon a live goroutine.
	if ctx.Err() != nil {
		return failure("BACKUP_CANCELED")
	}
	tx, err := writer.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return failure("BACKUP_DATABASE_BUSY")
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			problem = failure("BACKUP_DATABASE_ROLLBACK_FAILED")
		}
	}()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return failure("BACKUP_DATABASE_SCHEMA_INVALID")
	}
	if version < 0 || version > applications.SchemaVersion {
		return failure("BACKUP_SCHEMA_UNSUPPORTED")
	}
	if ctx.Err() != nil {
		return failure("BACKUP_CANCELED")
	}
	// All stage writes are below the supplied random snapshot namespace. A failed
	// copy stays unready; no archive is published and no source file is removed.
	if _, p := root.EnsureDirectory(snapshot + "/applications"); p != nil {
		return failure("BACKUP_SNAPSHOT_UNAVAILABLE")
	}
	target := snapshot + "/" + databasePath
	if created, p := root.CreateFile(target, nil); p != nil || !created {
		return failure("BACKUP_DATABASE_STAGE_FAILED")
	}
	destinationGuard, p := root.GuardSQLiteFiles(target)
	if p != nil || destinationGuard == nil {
		return failure("BACKUP_DATABASE_STAGE_FAILED")
	}
	defer func() {
		if destinationGuard.Close() != nil {
			problem = failure("BACKUP_DATABASE_CLEANUP_FAILED")
		}
	}()
	destination := filepath.Join(dataRoot, filepath.FromSlash(target))
	if p := copySnapshotDatabase(ctx, sourcePath, destination, version); p != nil {
		return p
	}
	fingerprint, p := destinationGuard.SyncAndFingerprint(ctx, sqlitestore.MaxBackupBytes)
	if p != nil {
		return failure("BACKUP_DATABASE_VERIFY_FAILED")
	}
	if guard.Check() != nil {
		return failure("BACKUP_DATABASE_PATH_INVALID")
	}
	result := &databaseSnapshot{RelativePath: target, SchemaVersion: int64(version), File: File{RelativePath: databasePath, SizeBytes: fingerprint.SizeBytes, SHA256: fingerprint.SHA256}}
	problem = consume(root, result)
	if guard.Check() != nil {
		return failure("BACKUP_DATABASE_PATH_INVALID")
	}
	if ctx.Err() != nil {
		return failure("BACKUP_CANCELED")
	}
	return problem
}

func copySnapshotDatabase(ctx context.Context, source, destination string, version int) (problem *Error) {
	db, err := sqlitestore.OpenReadOnly(ctx, source)
	if err != nil {
		return failure("BACKUP_DATABASE_OPEN_FAILED")
	}
	defer func() {
		if db.Close() != nil {
			problem = failure("BACKUP_DATABASE_CLEANUP_FAILED")
		}
	}()
	reader, err := db.Conn(ctx)
	if err != nil {
		return failure("BACKUP_DATABASE_OPEN_FAILED")
	}
	defer func() {
		if err := reader.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			problem = failure("BACKUP_DATABASE_CLEANUP_FAILED")
		}
	}()
	if err := sqlitestore.BackupIntoEmpty(ctx, reader, destination); err != nil {
		return failure("BACKUP_DATABASE_COPY_FAILED")
	}
	copy, err := sqlitestore.OpenReadOnly(ctx, destination)
	if err != nil {
		return failure("BACKUP_DATABASE_VERIFY_FAILED")
	}
	defer func() {
		if copy.Close() != nil {
			problem = failure("BACKUP_DATABASE_CLEANUP_FAILED")
		}
	}()
	connection, err := copy.Conn(ctx)
	if err != nil {
		return failure("BACKUP_DATABASE_VERIFY_FAILED")
	}
	defer func() {
		if err := connection.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			problem = failure("BACKUP_DATABASE_CLEANUP_FAILED")
		}
	}()
	if err := sqlitestore.VerifyBackup(ctx, connection, version); err != nil {
		return failure("BACKUP_DATABASE_VERIFY_FAILED")
	}
	return nil
}
