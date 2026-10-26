package emailconnection

import (
	"context"
	"database/sql"
	"path/filepath"
	"time"

	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/secretstore"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

// RetireResult distinguishes a proven SQL commit from an attempted commit
// whose outcome must be settled by Recover before another operation.
type RetireResult struct {
	IntentPublished    bool
	CommitAttempted    bool
	Committed          bool
	OldDeleteAttempted bool
	OldDeleteConfirmed bool
	IntentRemoved      bool
	RecoveryRequired   bool
	Revision           int64
}

type retireHooks struct {
	beforeCommit       func() *Error
	commit             func(*sql.Tx) error
	beforeIntentRemove func() *Error
}

// Disconnect disables an existing enabled local v3 connection and its schedule but
// retains the connection, sender rules, sync cursor and message receipts. It
// is native-only and has no installed application route.
func Disconnect(ctx context.Context, dataRoot string, backend secretstore.Backend, expectedRevision int64) (RetireResult, *Error) {
	return retireWithHooks(ctx, dataRoot, backend, expectedRevision, emailcredentials.Disconnect, retireHooks{})
}

// DisconnectForHome pins a caller's startup Home before publishing a recovery
// intent; the Home value comes from sidecar admission, never the HTTP body.
func DisconnectForHome(ctx context.Context, dataRoot string, backend secretstore.Backend, expectedHome datahome.Info, expectedRevision int64) (RetireResult, *Error) {
	return retireWithExpected(ctx, dataRoot, backend, expectedRevision, emailcredentials.Disconnect, &expectedHome, retireHooks{})
}

// Forget removes the connection and its FK-owned email metadata, but not
// imported job discoveries. A disabled local v3 row may already lack its secret.
func Forget(ctx context.Context, dataRoot string, backend secretstore.Backend, expectedRevision int64) (RetireResult, *Error) {
	return retireWithHooks(ctx, dataRoot, backend, expectedRevision, emailcredentials.Forget, retireHooks{})
}

// ForgetForHome has the same startup identity boundary as DisconnectForHome.
func ForgetForHome(ctx context.Context, dataRoot string, backend secretstore.Backend, expectedHome datahome.Info, expectedRevision int64) (RetireResult, *Error) {
	return retireWithExpected(ctx, dataRoot, backend, expectedRevision, emailcredentials.Forget, &expectedHome, retireHooks{})
}

func retireWithHooks(ctx context.Context, dataRoot string, backend secretstore.Backend, expectedRevision int64, operation string, hooks retireHooks) (result RetireResult, problem *Error) {
	return retireWithExpected(ctx, dataRoot, backend, expectedRevision, operation, nil, hooks)
}

func retireWithExpected(ctx context.Context, dataRoot string, backend secretstore.Backend, expectedRevision int64, operation string, expectedHome *datahome.Info, hooks retireHooks) (result RetireResult, problem *Error) {
	if ctx == nil || dataRoot == "" || secretstore.IsNilBackend(backend) || expectedRevision < 1 || expectedRevision > emailcredentials.MaxRevision ||
		(operation != emailcredentials.Disconnect && operation != emailcredentials.Forget) ||
		(operation == emailcredentials.Disconnect && expectedRevision == emailcredentials.MaxRevision) {
		return result, failure("EMAIL_CONNECTION_INVALID")
	}
	home, before, problem := retireSnapshot(ctx, dataRoot, expectedRevision, operation, expectedHome)
	if problem != nil {
		return result, problem
	}
	after := emailcredentials.RowWitness{}
	if operation == emailcredentials.Disconnect {
		after = before
		after.Enabled = false
		after.Revision++
	}
	scope, problem := scopeAtPath(dataRoot)
	if problem != nil {
		return result, problem
	}
	intent := emailcredentials.ScopedIntent{Version: 2, Home: *home, RootFingerprint: scope.Fingerprint(), Operation: operation, Before: before, After: after}
	session, problem := stageCredentialIntent(ctx, dataRoot, backend, intent)
	if problem != nil {
		if problem.Code == "EMAIL_CREDENTIAL_PUBLICATION_UNCONFIRMED" {
			result.RecoveryRequired = true
		}
		return result, problem
	}
	result.IntentPublished, result.RecoveryRequired = true, true
	defer func() {
		if closeProblem := session.Close(); closeProblem != nil && problem == nil {
			problem = closeProblem
		}
	}()
	if problem = checkBoundary(ctx, session.root, session.pins, session.db, *home, session.intentRaw, before); problem != nil {
		return result, problem
	}
	writeDB, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(dataRoot, filepath.FromSlash(databasePath)))
	if err != nil {
		return result, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	defer func() {
		if err := writeDB.Close(); err != nil && problem == nil {
			problem = failure("EMAIL_CREDENTIAL_OPERATION_UNCONFIRMED")
		}
	}()
	tx, err := writeDB.BeginTx(ctx, nil)
	if err != nil {
		return result, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	defer tx.Rollback() // An uncertain commit is resolved from the durable intent.
	current, witnessProblem := readWitnessFrom(ctx, tx)
	if witnessProblem != nil {
		return result, witnessProblem
	}
	if current != before || session.pins.Check() != nil {
		return result, failure("EMAIL_CONNECTION_REVISION_CONFLICT")
	}
	if problem = updateRetirementRow(ctx, tx, before, operation); problem != nil {
		return result, problem
	}
	if hooks.beforeCommit != nil {
		if problem = hooks.beforeCommit(); problem != nil {
			return result, problem
		}
	}
	if problem = checkBoundary(ctx, session.root, session.pins, session.db, *home, session.intentRaw, before); problem != nil {
		return result, problem
	}
	result.CommitAttempted = true
	if hooks.commit != nil {
		err = hooks.commit(tx)
	} else {
		err = tx.Commit()
	}
	if err != nil {
		return result, failure("EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED")
	}
	result.Committed, result.Revision = true, after.Revision
	if problem = checkBoundary(ctx, session.root, session.pins, session.db, *home, session.intentRaw, after); problem != nil {
		return result, problem
	}
	secrets, err := newBoundSecrets(ctx, session.root, *home, backend, session.intentRaw)
	if err != nil {
		return result, credentialStoreProblem(err)
	}
	found, problem := secretPresent(secrets, before.SecretName)
	if problem != nil {
		return result, problem
	}
	if found {
		if problem = checkBoundary(ctx, session.root, session.pins, session.db, *home, session.intentRaw, after); problem != nil {
			return result, problem
		}
		result.OldDeleteAttempted = true
		if _, err := secrets.Delete(before.SecretName); err != nil {
			return result, credentialStoreProblem(err)
		}
	}
	if problem = checkBoundary(ctx, session.root, session.pins, session.db, *home, session.intentRaw, after); problem != nil {
		return result, problem
	}
	found, problem = secretPresent(secrets, before.SecretName)
	if problem != nil || found {
		return result, failure("EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED")
	}
	result.OldDeleteConfirmed = true
	if hooks.beforeIntentRemove != nil {
		if problem = hooks.beforeIntentRemove(); problem != nil {
			return result, problem
		}
	}
	if problem = checkBoundary(ctx, session.root, session.pins, session.db, *home, session.intentRaw, after); problem != nil {
		return result, problem
	}
	removed, pathProblem := session.root.RemoveFile(datahome.EmailCredentialRecoveryPath, session.intentRaw)
	if pathProblem != nil || !removed {
		return result, failure("EMAIL_CREDENTIAL_INTENT_REMOVE_UNCONFIRMED")
	}
	result.IntentRemoved, result.RecoveryRequired = true, false
	admitted, admission := datahome.Inspect(dataRoot, nil)
	if admission != nil || admitted == nil || *admitted != *home || session.pins.Check() != nil {
		return result, failure("EMAIL_CREDENTIAL_OPERATION_UNCONFIRMED")
	}
	return result, nil
}

func retireSnapshot(ctx context.Context, dataRoot string, revision int64, operation string, expectedHome *datahome.Info) (home *datahome.Info, witness emailcredentials.RowWitness, problem *Error) {
	root, pathProblem := runtimepaths.Open(dataRoot)
	if pathProblem != nil || root == nil {
		return nil, witness, failure("EMAIL_CREDENTIAL_PATH_UNAVAILABLE")
	}
	defer func() {
		if err := root.Close(); err != nil && problem == nil {
			problem = failure("EMAIL_CREDENTIAL_OPERATION_UNCONFIRMED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return nil, witness, failure("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	home, admission := datahome.Inspect(dataRoot, nil)
	if admission != nil || home == nil {
		if admission != nil {
			return nil, witness, failure(admission.Code)
		}
		return nil, witness, failure("DATA_HOME_UNAVAILABLE")
	}
	if expectedHome != nil && *home != *expectedHome {
		return nil, witness, failure("DATA_HOME_CHANGED")
	}
	pins, pathProblem := root.GuardSQLiteFiles(databasePath)
	if pathProblem != nil || pins == nil {
		return nil, witness, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	defer func() {
		if err := pins.Close(); err != nil && problem == nil {
			problem = failure("EMAIL_CREDENTIAL_OPERATION_UNCONFIRMED")
		}
	}()
	db, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(dataRoot, filepath.FromSlash(databasePath)))
	if err != nil {
		return nil, witness, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	defer func() {
		if err := db.Close(); err != nil && problem == nil {
			problem = failure("EMAIL_CREDENTIAL_OPERATION_UNCONFIRMED")
		}
	}()
	witness, problem = readWitness(ctx, db)
	if problem != nil {
		return nil, witness, problem
	}
	if !witness.Present {
		return nil, witness, failure("EMAIL_CONNECTION_NOT_CONFIGURED")
	}
	if witness.Revision != revision {
		return nil, witness, failure("EMAIL_CONNECTION_REVISION_CONFLICT")
	}
	if problem := boundTargetProblem(root, *home, witness.SecretName); problem != nil {
		return nil, witness, problem
	}
	if operation == emailcredentials.Disconnect && !witness.Enabled {
		return nil, witness, failure("EMAIL_CONNECTION_ALREADY_DISABLED")
	}
	if pins.Check() != nil {
		return nil, witness, failure("EMAIL_CREDENTIAL_DATABASE_PATH_CHANGED")
	}
	return home, witness, nil
}

func updateRetirementRow(ctx context.Context, tx *sql.Tx, before emailcredentials.RowWitness, operation string) *Error {
	var updated sql.Result
	var err error
	if operation == emailcredentials.Disconnect {
		updated, err = tx.ExecContext(ctx, `UPDATE job_email_connections
			SET enabled=0,schedule_enabled=0,revision=revision+1,updated_at=?
			WHERE connection_id='primary' AND revision=? AND enabled=1 AND secret_name=?`,
			time.Now().UTC().Format("2006-01-02T15:04:05Z"), before.Revision, before.SecretName)
	} else {
		enabled := 0
		if before.Enabled {
			enabled = 1
		}
		updated, err = tx.ExecContext(ctx, `DELETE FROM job_email_connections
			WHERE connection_id='primary' AND revision=? AND enabled=? AND secret_name=?`,
			before.Revision, enabled, before.SecretName)
	}
	if err != nil {
		return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED")
	}
	count, err := updated.RowsAffected()
	if err != nil || count != 1 {
		return failure("EMAIL_CONNECTION_REVISION_CONFLICT")
	}
	return nil
}
