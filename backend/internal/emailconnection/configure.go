package emailconnection

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"path/filepath"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emailcredentials"
	"careerhub.local/backend/internal/secretstore"
	"careerhub.local/backend/internal/sqlitestore"
)

// ConfigureResult reports only safe operation facts. In particular, a commit
// attempt that returns an error is unknown, even if SQLite actually committed.
type ConfigureResult struct {
	IntentPublished      bool
	SecretWriteAttempted bool
	CommitAttempted      bool
	Committed            bool
	RecoveryRequired     bool
	IntentRemoved        bool
	Revision             int64
}

type configureHooks struct {
	generateName       func(datahome.Info) (string, error)
	afterSecretWrite   func() *Error
	beforeCommit       func() *Error
	commit             func(*sql.Tx) error
	beforeIntentRemove func() *Error
}

// ConfigureNew is an unconnected, process-owned native primitive. No product
// route invokes it. It requires an already prepared schema-27 Data Home and an
// injected backend; it never opens Gmail or selects the Windows secret store.
func ConfigureNew(ctx context.Context, dataRoot string, backend secretstore.Backend, input ConfigureNewInput) (ConfigureResult, *Error) {
	return configureNewWithHooks(ctx, dataRoot, backend, input, configureHooks{})
}

func configureNewWithHooks(ctx context.Context, dataRoot string, backend secretstore.Backend, input ConfigureNewInput, hooks configureHooks) (result ConfigureResult, problem *Error) {
	values, problem := normalizeNewConnection(input)
	if problem != nil {
		return result, problem
	}
	if ctx == nil || dataRoot == "" || secretstore.IsNilBackend(backend) {
		return result, failure("EMAIL_CONNECTION_INVALID")
	}
	home, admission := datahome.Inspect(dataRoot, nil)
	if admission != nil || home == nil {
		if admission != nil {
			return result, failure(admission.Code)
		}
		return result, failure("DATA_HOME_UNAVAILABLE")
	}
	if input.ExpectedHome != nil && *home != *input.ExpectedHome {
		return result, failure("DATA_HOME_CHANGED")
	}
	scope, problem := scopeAtPath(dataRoot)
	if problem != nil {
		return result, problem
	}
	var session *credentialSession
	var intent emailcredentials.ScopedIntent
	for attempt := 0; attempt < 8; attempt++ {
		generateName := func(h datahome.Info) (string, error) { return emailcredentials.NewScopedGenerationName(h, scope) }
		if hooks.generateName != nil {
			generateName = hooks.generateName
		}
		name, err := generateName(*home)
		if err != nil {
			return result, failure("EMAIL_SECRET_TARGET_GENERATION_FAILED")
		}
		intent = emailcredentials.ScopedIntent{
			Version: 2, Home: *home, RootFingerprint: scope.Fingerprint(), Operation: emailcredentials.InitialConfigure,
			After: emailcredentials.RowWitness{Present: true, Revision: 1, Enabled: true, SecretName: name},
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
	result.IntentPublished = true
	result.RecoveryRequired = true
	defer func() {
		if closeProblem := session.Close(); closeProblem != nil && problem == nil {
			problem = closeProblem
		}
	}()
	secrets, err := newBoundSecrets(ctx, session.root, *home, backend, session.intentRaw)
	if err != nil {
		return result, credentialStoreProblem(err)
	}
	if problem = checkBoundary(ctx, session.root, session.pins, session.db, *home, session.intentRaw, intent.Before); problem != nil {
		return result, problem
	}
	result.SecretWriteAttempted = true
	if err = secrets.Set(intent.After.SecretName, values.password); err != nil {
		return result, credentialStoreProblem(err)
	}
	if hooks.afterSecretWrite != nil {
		if problem = hooks.afterSecretWrite(); problem != nil {
			return result, problem
		}
	}
	if problem = verifyNewSecret(secrets, intent.After.SecretName, values.password); problem != nil {
		return result, problem
	}
	if problem = checkBoundary(ctx, session.root, session.pins, session.db, *home, session.intentRaw, intent.Before); problem != nil {
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
	transaction, err := writeDB.BeginTx(ctx, nil)
	if err != nil {
		return result, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	defer transaction.Rollback() // Harmless after a successful or unknown commit.
	current, witnessProblem := readWitnessFrom(ctx, transaction)
	if witnessProblem != nil {
		return result, witnessProblem
	}
	if current != intent.Before || session.pins.Check() != nil {
		return result, failure("EMAIL_CREDENTIAL_DATABASE_STATE_CHANGED")
	}
	if problem = insertNewConnection(ctx, transaction, values, intent.After.SecretName); problem != nil {
		return result, problem
	}
	if hooks.beforeCommit != nil {
		if problem = hooks.beforeCommit(); problem != nil {
			return result, problem
		}
	}
	if problem = checkBoundary(ctx, session.root, session.pins, session.db, *home, session.intentRaw, intent.Before); problem != nil {
		return result, problem
	}
	result.CommitAttempted = true
	if hooks.commit != nil {
		err = hooks.commit(transaction)
	} else {
		err = transaction.Commit()
	}
	if err != nil {
		// A driver error may be returned after durable commit. Do not infer
		// rollback or delete the newly written credential here.
		return result, failure("EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED")
	}
	result.Committed, result.Revision = true, 1
	if problem = checkBoundary(ctx, session.root, session.pins, session.db, *home, session.intentRaw, intent.After); problem != nil {
		return result, problem
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

func verifyNewSecret(secrets *boundSecrets, name, expected string) *Error {
	value, found, err := secrets.Get(name)
	if err != nil {
		return credentialStoreProblem(err)
	}
	if !found || subtle.ConstantTimeCompare([]byte(value), []byte(expected)) != 1 {
		return failure("EMAIL_CREDENTIAL_RECOVERY_REQUIRED")
	}
	return nil
}

func insertNewConnection(ctx context.Context, tx *sql.Tx, values normalizedNewConnection, secretName string) *Error {
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	scheduled := 0
	if values.scheduled {
		scheduled = 1
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO job_email_connections
		(connection_id,provider,account_address,folder,secret_name,enabled,lookback_days,max_messages_per_sync,
		 last_test_status,last_error_code,revision,created_at,updated_at,schedule_enabled,interval_minutes)
		VALUES ('primary','gmail_imap',?,'INBOX',?,1,14,250,'not_tested','',1,?,?,?,?)`,
		values.account, secretName, now, now, scheduled, values.interval)
	if err != nil {
		return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO job_email_sync_state (connection_id,folder) VALUES ('primary','INBOX')`); err != nil {
		return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED")
	}
	for _, sender := range values.senders {
		_, err = tx.ExecContext(ctx, `INSERT INTO job_email_sender_rules
			(connection_id,sender_address,disposition,purpose,created_at,updated_at,subject_policy_json)
			VALUES ('primary',?,'allowed',?,?,?,?)`, sender.address, sender.purpose, now, now, sender.policyJSON)
		if err != nil {
			return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED")
		}
	}
	return nil
}
