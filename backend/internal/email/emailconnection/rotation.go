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
	"careerhub.local/backend/internal/textcompat"
)

// RotateInput requires a new password for either an enabled local v3 connection
// or a disabled local v3 connection being re-enabled. Unbound retirement refuses.
type RotateInput struct {
	// Optional already-admitted startup Home; never populated from HTTP JSON.
	ExpectedHome       *datahome.Info
	AccountAddress     string
	Password           string
	JobSenders         []string
	ApplicationSenders []string
	SubjectPolicies    map[string]string
	ScheduleEnabled    bool
	IntervalMinutes    *int
	ExpectedRevision   int64
}

type RotateResult struct {
	IntentPublished    bool
	NewWriteAttempted  bool
	CommitAttempted    bool
	Committed          bool
	OldDeleteAttempted bool
	OldDeleteConfirmed bool
	IntentRemoved      bool
	RecoveryRequired   bool
	Revision           int64
}

type rotationHooks struct {
	generateName       func(datahome.Info) (string, error)
	afterPublication   func() *Error
	afterNewWrite      func() *Error
	beforeCommit       func() *Error
	commit             func(*sql.Tx) error
	beforeIntentRemove func() *Error
}

func normalizeRotation(input RotateInput) (normalizedNewConnection, *Error) {
	if input.ExpectedRevision < 1 || input.ExpectedRevision >= emailcredentials.MaxRevision {
		return normalizedNewConnection{}, failure("EMAIL_CONNECTION_INVALID")
	}
	values, problem := normalizeConnectionFields(ConfigureNewInput{
		AccountAddress: input.AccountAddress, JobSenders: input.JobSenders,
		ApplicationSenders: input.ApplicationSenders, SubjectPolicies: input.SubjectPolicies,
		ScheduleEnabled: input.ScheduleEnabled, IntervalMinutes: input.IntervalMinutes,
	}, false)
	if problem != nil {
		return normalizedNewConnection{}, problem
	}
	values.password, problem = normalizeRequiredPassword(input.Password)
	if problem != nil {
		return normalizedNewConnection{}, problem
	}
	return values, nil
}

// Rotate needs a process-owned Home, injected backend and an existing
// schema-28 local v3 connection. The installed app still uses the Python owner.
func Rotate(ctx context.Context, dataRoot string, backend secretstore.Backend, input RotateInput) (RotateResult, *Error) {
	return rotateWithHooks(ctx, dataRoot, backend, input, rotationHooks{})
}

func rotateWithHooks(ctx context.Context, dataRoot string, backend secretstore.Backend, input RotateInput, hooks rotationHooks) (result RotateResult, problem *Error) {
	return rotateWithMode(ctx, dataRoot, backend, input, hooks, false)
}

// rotateWithMode shares publication, new-target and SQL lifetime while keeping
// legacy promotion distinct from rotation's old-target read and retirement.
func rotateWithMode(ctx context.Context, dataRoot string, backend secretstore.Backend, input RotateInput, hooks rotationHooks, promoteLegacy bool) (result RotateResult, problem *Error) {
	operation := emailcredentials.Rotate
	if promoteLegacy {
		operation = emailcredentials.PromoteLegacy
	}
	return rotateWithOperation(ctx, dataRoot, backend, input, hooks, operation)
}

func rotateWithOperation(ctx context.Context, dataRoot string, backend secretstore.Backend, input RotateInput, hooks rotationHooks, operation string) (result RotateResult, problem *Error) {
	values, problem := normalizeRotation(input)
	if problem != nil {
		return result, problem
	}
	if ctx == nil || dataRoot == "" || secretstore.IsNilBackend(backend) {
		return result, failure("EMAIL_CONNECTION_INVALID")
	}
	if operation != emailcredentials.Rotate && input.ExpectedHome == nil {
		return result, failure("EMAIL_CONNECTION_INVALID")
	}
	home, before, problem := rotationSnapshot(ctx, dataRoot, values.account, input.ExpectedRevision, input.ExpectedHome, operation)
	if problem != nil {
		return result, problem
	}
	scope, problem := scopeAtPath(dataRoot)
	if problem != nil {
		return result, problem
	}
	if operation != emailcredentials.Rotate {
		operation = emailcredentials.RebindCopied
		if _, err := emailcredentials.Classify(*home, before.SecretName); err == nil {
			operation = emailcredentials.AdoptUnbound
		}
	}
	nonDeleting := nonDeletingOperation(operation)
	var session *credentialSession
	var intent emailcredentials.ScopedIntent
	for attempt := 0; attempt < 8; attempt++ {
		generate := func(h datahome.Info) (string, error) { return emailcredentials.NewScopedGenerationName(h, scope) }
		if hooks.generateName != nil {
			generate = hooks.generateName
		}
		name, err := generate(*home)
		if err != nil {
			return result, failure("EMAIL_SECRET_TARGET_GENERATION_FAILED")
		}
		if name == before.SecretName {
			continue
		}
		intent = emailcredentials.ScopedIntent{
			Version: 2, Home: *home, RootFingerprint: scope.Fingerprint(), Operation: operation, Before: before,
			After: emailcredentials.RowWitness{Present: true, Revision: before.Revision + 1, Enabled: true, SecretName: name},
		}
		session, problem = stageCredentialIntent(ctx, dataRoot, backend, intent)
		if problem == nil {
			break
		}
		if problem.Code != "EMAIL_CREDENTIAL_TARGET_COLLISION" {
			if problem.Code == "EMAIL_CREDENTIAL_PUBLICATION_UNCONFIRMED" {
				result.RecoveryRequired = true
			}
			return result, problem
		}
	}
	if session == nil {
		return result, failure("EMAIL_CREDENTIAL_TARGET_COLLISION")
	}
	result.IntentPublished, result.RecoveryRequired = true, true
	defer func() {
		if closeProblem := session.Close(); closeProblem != nil && problem == nil {
			problem = closeProblem
		}
	}()
	if hooks.afterPublication != nil {
		if problem = hooks.afterPublication(); problem != nil {
			return result, problem
		}
	}
	secrets, err := newBoundSecrets(ctx, session.root, *home, backend, session.intentRaw)
	if err != nil {
		return result, credentialStoreProblem(err)
	}
	if problem = checkBoundary(ctx, session.root, session.pins, session.db, *home, session.intentRaw, before); problem != nil {
		return result, problem
	}
	account, err := readConnectionAccount(ctx, session.db)
	if err != nil {
		return result, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	if textcompat.Fold(account) != values.account {
		return result, failure("EMAIL_CONNECTION_ACCOUNT_IMMUTABLE")
	}
	result.NewWriteAttempted = true
	if err := secrets.Set(intent.After.SecretName, values.password); err != nil {
		return result, credentialStoreProblem(err)
	}
	if hooks.afterNewWrite != nil {
		if problem = hooks.afterNewWrite(); problem != nil {
			return result, problem
		}
	}
	if problem = verifyNewSecret(secrets, intent.After.SecretName, values.password); problem != nil {
		return result, problem
	}
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
	defer tx.Rollback() // The durable intent handles any uncertain commit side.
	current, witnessProblem := readWitnessFrom(ctx, tx)
	if witnessProblem != nil {
		return result, witnessProblem
	}
	if current != before || session.pins.Check() != nil {
		return result, failure("EMAIL_CONNECTION_REVISION_CONFLICT")
	}
	account, err = readConnectionAccount(ctx, tx)
	if err != nil {
		return result, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	if textcompat.Fold(account) != values.account {
		return result, failure("EMAIL_CONNECTION_ACCOUNT_IMMUTABLE")
	}
	if problem = updateRotationRows(ctx, tx, before, intent.After.SecretName, values, input.SubjectPolicies != nil); problem != nil {
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
	result.Committed, result.Revision = true, intent.After.Revision
	if problem = checkBoundary(ctx, session.root, session.pins, session.db, *home, session.intentRaw, intent.After); problem != nil {
		return result, problem
	}
	if problem = verifyNewSecret(secrets, intent.After.SecretName, values.password); problem != nil {
		return result, problem
	}
	if !nonDeleting {
		found, readProblem := secretPresent(secrets, before.SecretName)
		if readProblem != nil {
			return result, readProblem
		}
		if found {
			if problem = checkBoundary(ctx, session.root, session.pins, session.db, *home, session.intentRaw, intent.After); problem != nil {
				return result, problem
			}
			result.OldDeleteAttempted = true
			if _, err := secrets.Delete(before.SecretName); err != nil {
				return result, credentialStoreProblem(err)
			}
		}
	}
	if problem = checkBoundary(ctx, session.root, session.pins, session.db, *home, session.intentRaw, intent.After); problem != nil {
		return result, problem
	}
	if !nonDeleting {
		found, readProblem := secretPresent(secrets, before.SecretName)
		if readProblem != nil || found {
			return result, failure("EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED")
		}
		result.OldDeleteConfirmed = true
	}
	if problem = verifyNewSecret(secrets, intent.After.SecretName, values.password); problem != nil {
		return result, problem
	}
	if hooks.beforeIntentRemove != nil {
		if problem = hooks.beforeIntentRemove(); problem != nil {
			return result, problem
		}
	}
	if problem = checkBoundary(ctx, session.root, session.pins, session.db, *home, session.intentRaw, intent.After); problem != nil {
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

// The first read is deliberately non-mutating and outside the operation gate.
// stageCredentialIntent rechecks the exact witness under both gates before it
// publishes anything; the transaction rechecks it again before commit.
func rotationSnapshot(ctx context.Context, dataRoot, account string, revision int64, expectedHome *datahome.Info, operation string) (home *datahome.Info, witness emailcredentials.RowWitness, problem *Error) {
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
	if operation == emailcredentials.PromoteLegacy {
		if witness.SecretName != emailcredentials.LegacyName {
			return nil, witness, failure("EMAIL_CREDENTIAL_LEGACY_REQUIRED")
		}
	} else if operation == "reconnect" {
		scope, e := root.CredentialScope()
		if e != nil {
			return nil, witness, failure("EMAIL_CREDENTIAL_SCOPE_UNAVAILABLE")
		}
		reconnect, err := emailcredentials.RequiresReauthorization(*home, scope, witness.SecretName)
		if err != nil {
			return nil, witness, failure("EMAIL_CONNECTION_INVALID")
		}
		if !reconnect {
			return nil, witness, failure("EMAIL_CREDENTIAL_RECONNECT_NOT_REQUIRED")
		}
	} else if operation == emailcredentials.Rotate {
		if problem := boundTargetProblem(root, *home, witness.SecretName); problem != nil {
			return nil, witness, problem
		}
	} else {
		return nil, witness, failure("EMAIL_CONNECTION_INVALID")
	}
	storedAccount, err := readConnectionAccount(ctx, db)
	if err != nil {
		return nil, witness, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	if textcompat.Fold(storedAccount) != account {
		return nil, witness, failure("EMAIL_CONNECTION_ACCOUNT_IMMUTABLE")
	}
	if pins.Check() != nil {
		return nil, witness, failure("EMAIL_CREDENTIAL_DATABASE_PATH_CHANGED")
	}
	return home, witness, nil
}

func updateRotationRows(ctx context.Context, tx *sql.Tx, before emailcredentials.RowWitness, newName string, values normalizedNewConnection, overridePolicies bool) *Error {
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	scheduled, oldEnabled := 0, 0
	if values.scheduled {
		scheduled = 1
	}
	if before.Enabled {
		oldEnabled = 1
	}
	updated, err := tx.ExecContext(ctx, `UPDATE job_email_connections
		SET secret_name=?,enabled=1,schedule_enabled=?,interval_minutes=?,
			last_tested_at=NULL,last_test_status='not_tested',last_error_code='',
			revision=revision+1,updated_at=?
		WHERE connection_id='primary' AND revision=? AND enabled=? AND secret_name=?`,
		newName, scheduled, values.interval, now, before.Revision, oldEnabled, before.SecretName)
	if err != nil {
		return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED")
	}
	count, err := updated.RowsAffected()
	if err != nil || count != 1 {
		return failure("EMAIL_CONNECTION_REVISION_CONFLICT")
	}
	return updateSenderRules(ctx, tx, values, overridePolicies, now)
}
