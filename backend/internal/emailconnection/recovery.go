// Package emailconnection owns native Gmail credential recovery. Callers must
// supply an explicit, already owned Data Home and a credential backend.
package emailconnection

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"time"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emailcredentials"
	"careerhub.local/backend/internal/emailgate"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/secretstore"
	"careerhub.local/backend/internal/sqlitestore"
)

const databasePath = "applications/career_hub.sqlite3"
const gateWait = 20 * time.Second

// Error exposes only fixed categories, never paths, account details, target
// names, credentials or backend exception text.
type Error struct{ Code string }

func (e *Error) Error() string   { return e.Code }
func failure(code string) *Error { return &Error{Code: code} }

type Result struct {
	DatabaseSide     emailcredentials.DatabaseSide
	Cleanup          emailcredentials.CleanupAction
	CleanupAttempted bool
	CleanupConfirmed bool
	IntentRemoved    bool
}

// Recover settles one fixed nonsecret intent without replaying its SQL write.
// It admits only current-root version-2 journals. Adoption/rebinding observes
// only the new local v3 target and leaves unbound/foreign credentials untouched.
// No intent is ever published or credential created by this operation.
func Recover(ctx context.Context, dataRoot string, backend secretstore.Backend) (result Result, problem *Error) {
	return recoverOwned(ctx, dataRoot, backend, nil)
}

// RecoverPinned additionally binds recovery to the Home admitted by a process
// boundary. Check it before reading the intent or touching the credential store.
func RecoverPinned(ctx context.Context, dataRoot string, backend secretstore.Backend, expected datahome.Info) (Result, *Error) {
	return recoverOwned(ctx, dataRoot, backend, &expected)
}

func recoverOwned(ctx context.Context, dataRoot string, backend secretstore.Backend, expected *datahome.Info) (result Result, problem *Error) {
	if ctx == nil || dataRoot == "" || secretstore.IsNilBackend(backend) {
		return result, failure("EMAIL_CREDENTIAL_RECOVERY_INVALID")
	}
	gateCtx, cancelGateWait := context.WithTimeout(ctx, gateWait)
	defer cancelGateWait()
	releaseEmail, err := emailgate.Acquire(gateCtx)
	if err != nil {
		return result, failure("EMAIL_CREDENTIAL_RECOVERY_CANCELED")
	}
	defer releaseEmail()
	releaseGeneral, err := operationgate.Acquire(gateCtx)
	if err != nil {
		return result, failure("EMAIL_CREDENTIAL_RECOVERY_CANCELED")
	}
	defer releaseGeneral()
	root, pathError := runtimepaths.Open(dataRoot)
	if pathError != nil {
		return result, failure("EMAIL_CREDENTIAL_PATH_UNAVAILABLE")
	}
	var pins *runtimepaths.SQLiteFiles
	var db *sql.DB
	defer func() {
		if db != nil && db.Close() != nil {
			problem = failure("EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED")
		}
		if pins != nil && pins.Close() != nil {
			problem = failure("EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED")
		}
		if root.Close() != nil {
			problem = failure("EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return result, failure("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	home, admission := datahome.InspectOwnedForEmailCredentialRecovery(root, nil)
	if admission != nil {
		return result, failure(admission.Code)
	}
	if home == nil || expected != nil && *home != *expected {
		return result, failure("EMAIL_CREDENTIAL_HOME_CHANGED")
	}
	raw, present, pathError := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	if pathError != nil {
		return result, failure("EMAIL_CREDENTIAL_INTENT_UNAVAILABLE")
	}
	if !present {
		return result, nil
	}
	scope, scopeError := root.CredentialScope()
	if scopeError != nil {
		return result, failure("EMAIL_CREDENTIAL_SCOPE_UNAVAILABLE")
	}
	intent, err := emailcredentials.DecodeScopedIntent(raw, *home, scope)
	if err != nil {
		return result, failure(err.Error())
	}
	pins, pathError = root.GuardSQLiteFiles(databasePath)
	if pathError != nil || pins == nil {
		return result, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	db, err = sqlitestore.OpenReadOnly(ctx, filepath.Join(dataRoot, filepath.FromSlash(databasePath)))
	if err != nil {
		return result, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	current, problem := readWitness(ctx, db)
	if problem != nil {
		return result, problem
	}
	if current != intent.Before && current != intent.After {
		return result, failure("EMAIL_CREDENTIAL_DATABASE_STATE_CHANGED")
	}
	if problem = checkBoundary(ctx, root, pins, db, *home, raw, current); problem != nil {
		return result, problem
	}
	secrets, err := newBoundSecrets(ctx, root, *home, backend, raw)
	if err != nil {
		return result, credentialStoreProblem(err)
	}
	presence, problem := observeSecrets(secrets, intent)
	if problem != nil {
		return result, problem
	}
	plan, err := emailcredentials.PlanScopedRecovery(intent, scope, current, presence)
	if err != nil {
		return result, failure(err.Error())
	}
	result.DatabaseSide, result.Cleanup = plan.DatabaseSide, plan.Cleanup
	if problem = checkBoundary(ctx, root, pins, db, *home, raw, current); problem != nil {
		return result, problem
	}
	checked, problem := observeSecrets(secrets, intent)
	if problem != nil {
		return result, problem
	}
	if next, e := emailcredentials.PlanScopedRecovery(intent, scope, current, checked); e != nil || next != plan {
		return result, failure("EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED")
	}
	if plan.Cleanup != emailcredentials.CleanupNone {
		if problem = checkBoundary(ctx, root, pins, db, *home, raw, current); problem != nil {
			return result, problem
		}
		name := intent.After.SecretName
		if plan.Cleanup == emailcredentials.CleanupOld {
			name = intent.Before.SecretName
		}
		result.CleanupAttempted = true
		if _, err := secrets.Delete(name); err != nil {
			return result, credentialStoreProblem(err)
		}
	}
	if problem = checkBoundary(ctx, root, pins, db, *home, raw, current); problem != nil {
		return result, problem
	}
	checked, problem = observeSecrets(secrets, intent)
	if problem != nil {
		return result, problem
	}
	settled, err := emailcredentials.PlanScopedRecovery(intent, scope, current, checked)
	if err != nil || settled.DatabaseSide != plan.DatabaseSide || settled.Cleanup != emailcredentials.CleanupNone {
		return result, failure("EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED")
	}
	result.CleanupConfirmed = true
	if problem = checkBoundary(ctx, root, pins, db, *home, raw, current); problem != nil {
		return result, problem
	}
	result.IntentRemoved, pathError = root.RemoveFile(datahome.EmailCredentialRecoveryPath, raw)
	if pathError != nil || !result.IntentRemoved {
		return result, failure("EMAIL_CREDENTIAL_INTENT_REMOVE_UNCONFIRMED")
	}
	admitted, admission := datahome.Inspect(dataRoot, nil)
	if admission != nil || *admitted != *home || pins.Check() != nil {
		return result, failure("EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED")
	}
	return result, nil
}

func readWitness(ctx context.Context, db *sql.DB) (emailcredentials.RowWitness, *Error) {
	return readWitnessFrom(ctx, db)
}

type witnessReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readWitnessFrom(ctx context.Context, db witnessReader) (emailcredentials.RowWitness, *Error) {
	var witness emailcredentials.RowWitness
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != applications.SchemaVersion {
		return witness, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	var enabled int
	err := db.QueryRowContext(ctx, `SELECT revision,enabled,secret_name FROM job_email_connections WHERE connection_id='primary'`).Scan(
		&witness.Revision, &enabled, &witness.SecretName)
	if errors.Is(err, sql.ErrNoRows) {
		return emailcredentials.RowWitness{}, nil
	}
	if err != nil {
		return witness, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	if enabled != 0 && enabled != 1 || witness.Revision < 1 || witness.Revision > emailcredentials.MaxRevision {
		return witness, failure("EMAIL_CREDENTIAL_DATABASE_STATE_CHANGED")
	}
	witness.Present, witness.Enabled = true, enabled == 1
	return witness, nil
}

func observeSecrets(secrets *boundSecrets, intent emailcredentials.ScopedIntent) (emailcredentials.CredentialObservation, *Error) {
	var observed emailcredentials.CredentialObservation
	if intent.Before.Present && !nonDeletingOperation(intent.Operation) {
		found, problem := secretPresent(secrets, intent.Before.SecretName)
		if problem != nil {
			return observed, problem
		}
		observed.OldFound = &found
	}
	if intent.Operation == emailcredentials.InitialConfigure || intent.Operation == emailcredentials.Rotate || nonDeletingOperation(intent.Operation) {
		found, problem := secretPresent(secrets, intent.After.SecretName)
		if problem != nil {
			return observed, problem
		}
		observed.NewFound = &found
	}
	return observed, nil
}

func secretPresent(secrets *boundSecrets, name string) (bool, *Error) {
	value, found, err := secrets.Get(name)
	if err != nil {
		return false, credentialStoreProblem(err)
	}
	if found && value == "" {
		return false, failure("EMAIL_CREDENTIAL_RECOVERY_REQUIRED")
	}
	return found, nil
}

func checkBoundary(ctx context.Context, root *runtimepaths.Root, pins *runtimepaths.SQLiteFiles, db *sql.DB,
	home datahome.Info, intentRaw []byte, expected emailcredentials.RowWitness) *Error {
	if ctx.Err() != nil {
		return failure("EMAIL_CREDENTIAL_RECOVERY_CANCELED")
	}
	admitted, admission := datahome.InspectOwnedForEmailCredentialRecovery(root, nil)
	if admission != nil || *admitted != home {
		return failure("EMAIL_CREDENTIAL_HOME_CHANGED")
	}
	raw, present, pathError := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	if pathError != nil || !present || !bytes.Equal(raw, intentRaw) {
		return failure("EMAIL_CREDENTIAL_INTENT_CHANGED")
	}
	scope, e := root.CredentialScope()
	if e != nil {
		return failure("EMAIL_CREDENTIAL_SCOPE_CHANGED")
	}
	if _, err := emailcredentials.DecodeScopedIntent(raw, home, scope); err != nil {
		return failure(err.Error())
	}
	if pins.Check() != nil {
		return failure("EMAIL_CREDENTIAL_DATABASE_PATH_CHANGED")
	}
	current, problem := readWitness(ctx, db)
	if problem != nil {
		return problem
	}
	if current != expected || pins.Check() != nil {
		return failure("EMAIL_CREDENTIAL_DATABASE_STATE_CHANGED")
	}
	return nil
}
