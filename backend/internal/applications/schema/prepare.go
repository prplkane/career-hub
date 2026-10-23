package schema

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

const databaseRelative = "applications/career_hub.sqlite3"
const recoveryDirectory = "backups/sqlite-schema-recovery"
const PreparationTimeout = 60 * time.Second

// Preparation preserves commit/publication facts even if later cleanup fails.
// Never interpret an error following CommitAttempted as proof of rollback.
type Preparation struct {
	PreviousVersion int       `json:"previous_version"`
	DatabaseCreated bool      `json:"database_created"`
	CommitAttempted bool      `json:"commit_attempted"`
	Committed       bool      `json:"committed"`
	Recovery        *Recovery `json:"recovery,omitempty"`
}

type Recovery struct {
	Kind                  string `json:"kind"`
	Status                string `json:"status"`
	PreviousSchemaVersion int    `json:"previous_schema_version"`
	TargetSchemaVersion   int    `json:"target_schema_version"`
	DatabaseRelativePath  string `json:"database_relative_path"`
	BackupFile            string `json:"backup_file"`
	SizeBytes             int64  `json:"size_bytes"`
	SHA256                string `json:"sha256"`
}

// PreparationError is a service boundary: no paths, SQL text, private values or
// native causes can escape via formatting or errors.Unwrap.
type PreparationError struct{ Code string }

func (e *PreparationError) Error() string              { return "Application database preparation failed." }
func (e *PreparationError) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func prepareFailure(code string) error                 { return &PreparationError{Code: code} }

// PrepareOwned is explicit, never called by a read. Its caller admits the exact
// initialized Data Home/channel and prevents legacy writers before entry. This
// function additionally requires this process's native claim, retained path
// guards, an IMMEDIATE SQLite writer transaction and a separate backup reader.
// Only maintainer bootstrap selects it; the installed app and read-only mode do
// not. Do not abandon it on timeout: native cleanup is synchronous; process
// ownership lasts until process exit.
func PrepareOwned(ctx context.Context, dataRoot string) (*Preparation, error) {
	return prepareOwned(ctx, dataRoot, nil)
}

// A private per-call fault seam runs only at fixed milestones; no global or
// public mutation hooks are exposed to the product.
func prepareOwned(ctx context.Context, dataRoot string, checkpoint func(string) error) (report *Preparation, problem error) {
	report = &Preparation{}
	if ctx == nil || ctx.Err() != nil {
		return report, prepareFailure("APPLICATION_PREPARATION_CANCELED")
	}
	ctx, cancel := context.WithTimeout(ctx, PreparationTimeout)
	defer cancel()
	defer func() {
		if ctx.Err() != nil && problem == nil {
			problem = prepareFailure("APPLICATION_PREPARATION_CANCELED")
		}
	}()
	root, pathErr := runtimepaths.Open(dataRoot)
	if pathErr != nil {
		return report, prepareFailure("APPLICATION_PATH_REJECTED")
	}
	defer func() {
		if root.Close() != nil {
			problem = prepareFailure("APPLICATION_CLEANUP_FAILED")
		}
	}()
	if e := root.RequireProcessOwnership(); e != nil {
		return report, prepareFailure("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	// Refuse unsafe existing main/companion paths before any creation or writes.
	files, pathErr := root.GuardSQLiteFiles(databaseRelative)
	if pathErr != nil {
		return report, prepareFailure("APPLICATION_PATH_REJECTED")
	}
	if files == nil {
		for _, suffix := range []string{"-journal", "-wal", "-shm"} {
			if present, e := root.Exists(databaseRelative + suffix); e != nil || present {
				return report, prepareFailure("APPLICATION_ORPHANED_SQLITE_FILES")
			}
		}
		if _, e := root.EnsureDirectory("applications"); e != nil {
			return report, prepareFailure("APPLICATION_PATH_REJECTED")
		}
		created, e := root.CreateFile(databaseRelative, nil)
		report.DatabaseCreated = created
		if e != nil || !created {
			return report, prepareFailure("APPLICATION_DATABASE_CREATE_FAILED")
		}
		files, pathErr = root.GuardSQLiteFiles(databaseRelative)
		if pathErr != nil || files == nil {
			return report, prepareFailure("APPLICATION_PATH_REJECTED")
		}
	}
	defer func() {
		if files.Close() != nil {
			problem = prepareFailure("APPLICATION_PATH_REJECTED")
		}
	}()
	path := filepath.Join(dataRoot, filepath.FromSlash(databaseRelative))
	db, err := sqlitestore.OpenReadWriteExisting(ctx, path)
	if err != nil {
		return report, prepareFailure("APPLICATION_DATABASE_UNAVAILABLE")
	}
	defer func() {
		if db.Close() != nil {
			problem = prepareFailure("APPLICATION_CLEANUP_FAILED")
		}
	}()
	writeConn, err := db.Conn(ctx)
	if err != nil {
		return report, prepareFailure("APPLICATION_DATABASE_UNAVAILABLE")
	}
	defer func() {
		if err := writeConn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			problem = prepareFailure("APPLICATION_CLEANUP_FAILED")
		}
	}()
	tx, err := beginPreparationTransaction(ctx, writeConn)
	if err != nil {
		return report, prepareFailure("APPLICATION_UPGRADE_LOCK_FAILED")
	}
	defer func() {
		if !report.Committed {
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				problem = prepareFailure("APPLICATION_ROLLBACK_FAILED")
			}
		}
	}()
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&report.PreviousVersion); err != nil {
		return report, prepareFailure("APPLICATION_SCHEMA_READ_FAILED")
	}
	if report.PreviousVersion > Version || report.PreviousVersion < 0 {
		return report, prepareFailure("APPLICATION_SCHEMA_TOO_NEW")
	}
	if report.PreviousVersion == Version {
		// No migration/backup/commit on an already-current database.
		return report, nil
	}
	mark := func(stage string) error {
		if ctx.Err() != nil {
			return prepareFailure("APPLICATION_PREPARATION_CANCELED")
		}
		if checkpoint != nil && checkpoint(stage) != nil {
			return prepareFailure("APPLICATION_PREPARATION_CHECKPOINT_FAILED")
		}
		if ctx.Err() != nil {
			return prepareFailure("APPLICATION_PREPARATION_CANCELED")
		}
		return nil
	}
	if err := mark("reserved"); err != nil {
		return report, err
	}
	if report.PreviousVersion > 0 {
		report.Recovery, err = createRecovery(ctx, root, dataRoot, path, report.PreviousVersion)
		if err != nil {
			return report, err
		}
	}
	if err := mark("backed-up"); err != nil {
		return report, err
	}
	if err := applySteps(ctx, tx, report.PreviousVersion); err != nil {
		return report, prepareFailure("APPLICATION_SCHEMA_UPGRADE_FAILED")
	}
	if err := mark("upgraded"); err != nil {
		return report, err
	}
	if e := files.Check(); e != nil {
		return report, prepareFailure("APPLICATION_PATH_REJECTED")
	}
	report.CommitAttempted = true
	if err := tx.Commit(); err != nil {
		return report, prepareFailure("APPLICATION_COMMIT_UNCONFIRMED")
	}
	report.Committed = true
	if err := mark("committed"); err != nil {
		return report, err
	}
	return report, nil
}

func beginPreparationTransaction(ctx context.Context, connection *sql.Conn) (*sql.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Own rollback synchronously. A BeginTx cancellation context otherwise
	// starts database/sql's asynchronous rollback: ErrTxDone alone then does
	// not prove the native rollback/connection close finished. Every statement
	// and backup still uses the caller deadline; begin-lock waiting is bounded
	// by this connection's busy_timeout. Always end the transaction ourselves
	// before closing its lease, DB, native guard and Root, in that order.
	return connection.BeginTx(context.WithoutCancel(ctx), nil)
}

func createRecovery(ctx context.Context, root *runtimepaths.Root, dataRoot, sourcePath string, version int) (record *Recovery, problem error) {
	if _, e := root.EnsureDirectory(recoveryDirectory); e != nil {
		return nil, prepareFailure("APPLICATION_BACKUP_PATH_FAILED")
	}
	var nonce [6]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, prepareFailure("APPLICATION_BACKUP_NAME_FAILED")
	}
	name := fmt.Sprintf("career_hub_v%d_to_v%d_%s_%s.sqlite3", version, Version, time.Now().UTC().Format("20060102T150405Z"), hex.EncodeToString(nonce[:]))
	relative := recoveryDirectory + "/" + name
	if created, e := root.CreateFile(relative, nil); e != nil || !created {
		return nil, prepareFailure("APPLICATION_BACKUP_CREATE_FAILED")
	}
	guard, e := root.GuardSQLiteFiles(relative)
	if e != nil || guard == nil {
		return nil, prepareFailure("APPLICATION_BACKUP_PATH_FAILED")
	}
	defer func() {
		if guard.Close() != nil {
			record, problem = nil, prepareFailure("APPLICATION_BACKUP_CLEANUP_FAILED")
		}
	}()
	destination := filepath.Join(dataRoot, filepath.FromSlash(relative))
	if err := copyRecovery(ctx, sourcePath, destination, version); err != nil {
		return nil, err
	}
	fingerprint, e := guard.SyncAndFingerprint(ctx, sqlitestore.MaxBackupBytes)
	if e != nil {
		return nil, prepareFailure("APPLICATION_BACKUP_VERIFY_FAILED")
	}
	record = &Recovery{Kind: "sqlite_schema_recovery", Status: "pre_migration_backup", PreviousSchemaVersion: version,
		TargetSchemaVersion: Version, DatabaseRelativePath: databaseRelative, BackupFile: name,
		SizeBytes: fingerprint.SizeBytes, SHA256: fingerprint.SHA256}
	// Go's map encoder sorts keys, preserving the source's compact sorted JSON
	// convention while adding explicit size/digest verification fields.
	metadata, err := json.Marshal(map[string]any{
		"kind": record.Kind, "status": record.Status,
		"previous_schema_version": record.PreviousSchemaVersion, "target_schema_version": record.TargetSchemaVersion,
		"database_relative_path": record.DatabaseRelativePath, "backup_file": record.BackupFile,
		"size_bytes": record.SizeBytes, "sha256": record.SHA256,
	})
	if err != nil {
		return nil, prepareFailure("APPLICATION_BACKUP_METADATA_FAILED")
	}
	// Metadata is the completion record. Failed/interrupted copies without a
	// matching verified record must not be offered for automatic recovery.
	if created, e := root.CreateFile(strings.TrimSuffix(relative, ".sqlite3")+".json", append(metadata, '\n')); e != nil || !created {
		return nil, prepareFailure("APPLICATION_BACKUP_METADATA_FAILED")
	}
	return record, nil
}

func copyRecovery(ctx context.Context, sourcePath, destination string, version int) (problem error) {
	reader, err := sqlitestore.OpenReadOnly(ctx, sourcePath)
	if err != nil {
		return prepareFailure("APPLICATION_BACKUP_SOURCE_FAILED")
	}
	defer func() {
		if reader.Close() != nil {
			problem = prepareFailure("APPLICATION_BACKUP_CLEANUP_FAILED")
		}
	}()
	conn, err := reader.Conn(ctx)
	if err != nil {
		return prepareFailure("APPLICATION_BACKUP_SOURCE_FAILED")
	}
	defer func() {
		if conn.Close() != nil {
			problem = prepareFailure("APPLICATION_BACKUP_CLEANUP_FAILED")
		}
	}()
	if err := sqlitestore.BackupIntoEmpty(ctx, conn, destination); err != nil {
		return prepareFailure("APPLICATION_BACKUP_COPY_FAILED")
	}
	backup, err := sqlitestore.OpenReadOnly(ctx, destination)
	if err != nil {
		return prepareFailure("APPLICATION_BACKUP_VERIFY_FAILED")
	}
	defer func() {
		if backup.Close() != nil {
			problem = prepareFailure("APPLICATION_BACKUP_CLEANUP_FAILED")
		}
	}()
	backupConn, err := backup.Conn(ctx)
	if err != nil {
		return prepareFailure("APPLICATION_BACKUP_VERIFY_FAILED")
	}
	defer func() {
		if backupConn.Close() != nil {
			problem = prepareFailure("APPLICATION_BACKUP_CLEANUP_FAILED")
		}
	}()
	if err := sqlitestore.VerifyBackup(ctx, backupConn, version); err != nil {
		return prepareFailure("APPLICATION_BACKUP_VERIFY_FAILED")
	}
	return nil
}
