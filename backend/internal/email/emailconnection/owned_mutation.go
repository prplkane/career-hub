package emailconnection

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"time"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/email/emailgate"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

// ConnectionMutationResult separates a proven commit from an attempted one.
// An uncertain result requires a fresh revision read before any retry.
type ConnectionMutationResult struct {
	CommitAttempted         bool
	Committed               bool
	RevisionRefreshRequired bool
	Revision                int64
}

type ownedMutationHooks struct {
	beforeCommit func() *Error
	commit       func(*sql.Tx) error
	now          func() time.Time
}

// ownedEmailMutation serializes a single source-shaped connection edit while
// preserving the exact process, Home, schema, path and commit boundaries. The
// caller changes only its use-case rows inside the supplied transaction; this
// coordinator owns the connection revision and updated_at CAS.
func ownedEmailMutation(ctx context.Context, dataRoot string, expected datahome.Info, expectedRevision int64, hooks ownedMutationHooks, apply func(context.Context, *sql.Tx, string, string) *Error) (result ConnectionMutationResult, problem *Error) {
	if ctx == nil || dataRoot == "" || !expected.Initialized || expectedRevision < 1 || expectedRevision >= emailcredentials.MaxRevision || apply == nil {
		return result, failure("EMAIL_CONNECTION_INVALID")
	}
	if ctx.Err() != nil {
		return result, failure("EMAIL_CONNECTION_CANCELED")
	}
	gateCtx, cancel := context.WithTimeout(ctx, gateWait)
	defer cancel()
	releaseEmail, err := emailgate.Acquire(gateCtx)
	if err != nil {
		return result, failure("EMAIL_CONNECTION_CANCELED")
	}
	defer releaseEmail()
	releaseGeneral, err := operationgate.Acquire(gateCtx)
	if err != nil {
		return result, failure("EMAIL_CONNECTION_CANCELED")
	}
	defer releaseGeneral()
	root, pathProblem := runtimepaths.Open(dataRoot)
	if pathProblem != nil || root == nil {
		return result, failure("DATA_HOME_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			result.RevisionRefreshRequired = true
			problem = failure("EMAIL_CONNECTION_CLEANUP_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return result, failure("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	if admission := ownedMutationHomeProblem(dataRoot, expected); admission != nil {
		return result, admission
	}
	pins, pathProblem := root.GuardSQLiteFiles(databasePath)
	if pathProblem != nil || pins == nil {
		return result, failure("EMAIL_CONNECTION_DATABASE_UNAVAILABLE")
	}
	defer func() {
		if pins.Close() != nil {
			result.RevisionRefreshRequired = true
			problem = failure("EMAIL_CONNECTION_CLEANUP_FAILED")
		}
	}()
	db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(dataRoot, filepath.FromSlash(databasePath)))
	if err != nil {
		return result, ownedMutationSQLProblem(ctx)
	}
	defer func() {
		if db.Close() != nil {
			result.RevisionRefreshRequired = true
			problem = failure("EMAIL_CONNECTION_CLEANUP_FAILED")
		}
	}()
	tx, err := db.BeginTx(ctx, nil) // sqlitestore uses an IMMEDIATE writer reservation.
	if err != nil {
		return result, ownedMutationSQLProblem(ctx)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) && !result.Committed {
			result.RevisionRefreshRequired = true
			problem = failure("EMAIL_CONNECTION_OPERATION_UNCONFIRMED")
		}
	}()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return result, ownedMutationSQLProblem(ctx)
	}
	if version < applications.SchemaVersion {
		return result, failure("EMAIL_CONNECTION_SCHEMA_PREPARATION_REQUIRED")
	}
	if version > applications.SchemaVersion {
		return result, failure("EMAIL_CONNECTION_SCHEMA_TOO_NEW")
	}
	var revision int64
	var folder string
	err = tx.QueryRowContext(ctx, `SELECT revision, folder FROM job_email_connections WHERE connection_id='primary'`).Scan(&revision, &folder)
	if errors.Is(err, sql.ErrNoRows) {
		return result, failure("EMAIL_CONNECTION_NOT_CONFIGURED")
	}
	if err != nil {
		return result, ownedMutationSQLProblem(ctx)
	}
	if revision != expectedRevision {
		return result, failure("EMAIL_CONNECTION_REVISION_CONFLICT")
	}
	now := time.Now()
	if hooks.now != nil {
		now = hooks.now()
	}
	stamp := now.UTC().Format("2006-01-02T15:04:05Z")
	if problem := apply(ctx, tx, folder, stamp); problem != nil {
		return result, problem
	}
	updated, err := tx.ExecContext(ctx, `UPDATE job_email_connections
		SET revision=revision+1, updated_at=?
		WHERE connection_id='primary' AND revision=?`, stamp, expectedRevision)
	if err != nil {
		return result, ownedMutationSQLProblem(ctx)
	}
	rows, err := updated.RowsAffected()
	if err != nil {
		return result, ownedMutationSQLProblem(ctx)
	}
	if rows != 1 {
		return result, failure("EMAIL_CONNECTION_REVISION_CONFLICT")
	}
	if hooks.beforeCommit != nil {
		if problem = hooks.beforeCommit(); problem != nil {
			return result, problem
		}
	}
	if boundary := ownedMutationBoundaryProblem(ctx, root, pins, dataRoot, expected); boundary != nil {
		return result, boundary
	}
	result.CommitAttempted = true
	if hooks.commit != nil {
		err = hooks.commit(tx)
	} else {
		err = tx.Commit()
	}
	if err != nil {
		result.RevisionRefreshRequired = true
		return result, failure("EMAIL_CONNECTION_COMMIT_UNCONFIRMED")
	}
	result.Committed, result.Revision = true, expectedRevision+1
	if boundary := ownedMutationBoundaryProblem(ctx, root, pins, dataRoot, expected); boundary != nil {
		result.RevisionRefreshRequired = true
		return result, boundary
	}
	return result, nil
}

func ownedMutationBoundaryProblem(ctx context.Context, root *runtimepaths.Root, pins *runtimepaths.SQLiteFiles, dataRoot string, expected datahome.Info) *Error {
	if ctx.Err() != nil {
		return failure("EMAIL_CONNECTION_CANCELED")
	}
	if root.RequireProcessOwnership() != nil {
		return failure("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	if pins.Check() != nil {
		return failure("EMAIL_CONNECTION_PATH_CHANGED")
	}
	return ownedMutationHomeProblem(dataRoot, expected)
}

func ownedMutationHomeProblem(dataRoot string, expected datahome.Info) *Error {
	actual, admission := datahome.Inspect(dataRoot, &expected.Channel)
	if admission != nil {
		return failure(admission.Code)
	}
	if actual == nil || *actual != expected {
		return failure("DATA_HOME_CHANGED")
	}
	return nil
}

func ownedMutationSQLProblem(ctx context.Context) *Error {
	if ctx.Err() != nil {
		return failure("EMAIL_CONNECTION_CANCELED")
	}
	return failure("EMAIL_CONNECTION_DATABASE_UNAVAILABLE")
}
