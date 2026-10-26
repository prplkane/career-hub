package emailconnection

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"time"

	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/email/emailgate"
	"careerhub.local/backend/internal/email/emailrouting"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/secretstore"
	"careerhub.local/backend/internal/storage/sqlitestore"
	"careerhub.local/backend/internal/textcompat"
)

// EditSettingsInput cannot carry a password: an existing enabled connection
// retains its exact credential target, test state, cursor and message receipts.
type EditSettingsInput struct {
	// Optional already-admitted startup Home; never populated from HTTP JSON.
	ExpectedHome       *datahome.Info
	AccountAddress     string
	JobSenders         []string
	ApplicationSenders []string
	SubjectPolicies    map[string]string
	ScheduleEnabled    bool
	IntervalMinutes    *int
	ExpectedRevision   int64
}

type EditSettingsResult struct {
	CommitAttempted         bool
	Committed               bool
	RevisionRefreshRequired bool
	Revision                int64
}

type settingsHooks struct {
	beforeCommit func() *Error
	commit       func(*sql.Tx) error
}

func normalizeEditSettings(input EditSettingsInput) (normalizedNewConnection, *Error) {
	if input.ExpectedRevision < 1 || input.ExpectedRevision >= emailcredentials.MaxRevision {
		return normalizedNewConnection{}, failure("EMAIL_CONNECTION_INVALID")
	}
	return normalizeConnectionFields(ConfigureNewInput{
		AccountAddress: input.AccountAddress, JobSenders: input.JobSenders,
		ApplicationSenders: input.ApplicationSenders, SubjectPolicies: input.SubjectPolicies,
		ScheduleEnabled: input.ScheduleEnabled, IntervalMinutes: input.IntervalMinutes,
	}, false)
}

// EditSettings is an unconnected native owner operation. It changes only
// schema-28 SQLite state, so it must not publish a credential intent. An
// uncertain commit requires a fresh revision read, never blind replay.
func EditSettings(ctx context.Context, dataRoot string, backend secretstore.Backend, input EditSettingsInput) (EditSettingsResult, *Error) {
	return editSettingsWithHooks(ctx, dataRoot, backend, input, settingsHooks{})
}

func editSettingsWithHooks(ctx context.Context, dataRoot string, backend secretstore.Backend, input EditSettingsInput, hooks settingsHooks) (result EditSettingsResult, problem *Error) {
	values, problem := normalizeEditSettings(input)
	if problem != nil {
		return result, problem
	}
	if ctx == nil || dataRoot == "" || secretstore.IsNilBackend(backend) {
		return result, failure("EMAIL_CONNECTION_INVALID")
	}
	session, home, before, problem := acquireSettingsSession(ctx, dataRoot, backend, values.account, input.ExpectedRevision, input.ExpectedHome)
	if problem != nil {
		return result, problem
	}
	defer func() {
		if closeProblem := session.Close(); closeProblem != nil && problem == nil {
			problem = closeProblem
		}
	}()
	if problem = checkSettingsBoundary(ctx, dataRoot, session, *home, before, values.account); problem != nil {
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
	defer func() {
		if rollbackError := tx.Rollback(); rollbackError != nil && !errors.Is(rollbackError, sql.ErrTxDone) && !result.Committed {
			result.RevisionRefreshRequired = true
			problem = failure("EMAIL_CREDENTIAL_OPERATION_UNCONFIRMED")
		}
	}()
	current, witnessProblem := readWitnessFrom(ctx, tx)
	if witnessProblem != nil {
		return result, witnessProblem
	}
	if current != before || session.pins.Check() != nil {
		return result, failure("EMAIL_CONNECTION_REVISION_CONFLICT")
	}
	account, err := readConnectionAccount(ctx, tx)
	if err != nil {
		return result, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	if textcompat.Fold(account) != values.account {
		return result, failure("EMAIL_CONNECTION_ACCOUNT_IMMUTABLE")
	}
	if problem = updateSettingsRows(ctx, tx, before, values, input.SubjectPolicies != nil); problem != nil {
		return result, problem
	}
	if hooks.beforeCommit != nil {
		if problem = hooks.beforeCommit(); problem != nil {
			return result, problem
		}
	}
	if problem = checkSettingsBoundary(ctx, dataRoot, session, *home, before, values.account); problem != nil {
		return result, problem
	}
	result.CommitAttempted = true
	if hooks.commit != nil {
		err = hooks.commit(tx)
	} else {
		err = tx.Commit()
	}
	if err != nil {
		result.RevisionRefreshRequired = true
		return result, failure("EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED")
	}
	result.Committed, result.Revision = true, before.Revision+1
	after := before
	after.Revision++
	if problem = checkSettingsBoundary(ctx, dataRoot, session, *home, after, values.account); problem != nil {
		result.RevisionRefreshRequired = true
		return result, problem
	}
	secrets, err := newBoundSecrets(ctx, session.root, *home, backend, nil)
	if err != nil {
		result.RevisionRefreshRequired = true
		return result, credentialStoreProblem(err)
	}
	value, found, err := secrets.Get(after.SecretName)
	if err != nil {
		result.RevisionRefreshRequired = true
		return result, credentialStoreProblem(err)
	}
	if !found || value == "" {
		result.RevisionRefreshRequired = true
		return result, failure("EMAIL_CREDENTIAL_MISSING")
	}
	if problem = checkSettingsBoundary(ctx, dataRoot, session, *home, after, values.account); problem != nil {
		result.RevisionRefreshRequired = true
		return result, problem
	}
	return result, nil
}

func acquireSettingsSession(ctx context.Context, dataRoot string, backend secretstore.Backend, account string, revision int64, expectedHome *datahome.Info) (owned *credentialSession, home *datahome.Info, witness emailcredentials.RowWitness, problem *Error) {
	gateCtx, cancel := context.WithTimeout(ctx, gateWait)
	defer cancel()
	session := &credentialSession{}
	defer func() {
		if problem != nil {
			if closeProblem := session.Close(); closeProblem != nil {
				problem = closeProblem
			}
		}
	}()
	var err error
	session.releaseEmail, err = emailgate.Acquire(gateCtx)
	if err != nil {
		return nil, nil, witness, failure("EMAIL_CREDENTIAL_OPERATION_CANCELED")
	}
	session.releaseGeneral, err = operationgate.Acquire(gateCtx)
	if err != nil {
		return nil, nil, witness, failure("EMAIL_CREDENTIAL_OPERATION_CANCELED")
	}
	var pathProblem *runtimepaths.Error
	session.root, pathProblem = runtimepaths.Open(dataRoot)
	if pathProblem != nil || session.root == nil {
		return nil, nil, witness, failure("EMAIL_CREDENTIAL_PATH_UNAVAILABLE")
	}
	if session.root.RequireProcessOwnership() != nil {
		return nil, nil, witness, failure("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	home, admission := datahome.Inspect(dataRoot, nil)
	if admission != nil || home == nil {
		if admission != nil {
			return nil, nil, witness, failure(admission.Code)
		}
		return nil, nil, witness, failure("DATA_HOME_UNAVAILABLE")
	}
	if expectedHome != nil && *home != *expectedHome {
		return nil, nil, witness, failure("DATA_HOME_CHANGED")
	}
	session.pins, pathProblem = session.root.GuardSQLiteFiles(databasePath)
	if pathProblem != nil || session.pins == nil {
		return nil, nil, witness, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	session.db, err = sqlitestore.OpenReadOnly(ctx, filepath.Join(dataRoot, filepath.FromSlash(databasePath)))
	if err != nil {
		return nil, nil, witness, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	witness, problem = readWitness(ctx, session.db)
	if problem != nil {
		return nil, nil, witness, problem
	}
	if !witness.Present {
		return nil, nil, witness, failure("EMAIL_CONNECTION_NOT_CONFIGURED")
	}
	if witness.Revision != revision {
		return nil, nil, witness, failure("EMAIL_CONNECTION_REVISION_CONFLICT")
	}
	if !witness.Enabled {
		return nil, nil, witness, failure("EMAIL_CREDENTIAL_MISSING")
	}
	if problem := boundTargetProblem(session.root, *home, witness.SecretName); problem != nil {
		return nil, nil, witness, problem
	}
	storedAccount, err := readConnectionAccount(ctx, session.db)
	if err != nil {
		return nil, nil, witness, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	if textcompat.Fold(storedAccount) != account {
		return nil, nil, witness, failure("EMAIL_CONNECTION_ACCOUNT_IMMUTABLE")
	}
	secrets, err := newBoundSecrets(ctx, session.root, *home, backend, nil)
	if err != nil {
		return nil, nil, witness, credentialStoreProblem(err)
	}
	value, found, err := secrets.Get(witness.SecretName)
	if err != nil {
		return nil, nil, witness, credentialStoreProblem(err)
	}
	if !found || value == "" {
		return nil, nil, witness, failure("EMAIL_CREDENTIAL_MISSING")
	}
	if session.pins.Check() != nil {
		return nil, nil, witness, failure("EMAIL_CREDENTIAL_DATABASE_PATH_CHANGED")
	}
	return session, home, witness, nil
}

type connectionQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readConnectionAccount(ctx context.Context, db connectionQueryer) (string, error) {
	var account string
	err := db.QueryRowContext(ctx, `SELECT account_address FROM job_email_connections WHERE connection_id='primary'`).Scan(&account)
	return account, err
}

func checkSettingsBoundary(ctx context.Context, dataRoot string, session *credentialSession, home datahome.Info, expected emailcredentials.RowWitness, account string) *Error {
	if ctx.Err() != nil || session.root.RequireProcessOwnership() != nil {
		return failure("EMAIL_CREDENTIAL_OPERATION_CANCELED")
	}
	admitted, admission := datahome.Inspect(dataRoot, nil)
	if admission != nil || admitted == nil || *admitted != home || session.pins.Check() != nil {
		return failure("EMAIL_CREDENTIAL_HOME_CHANGED")
	}
	current, problem := readWitness(ctx, session.db)
	if problem != nil || current != expected || session.pins.Check() != nil {
		return failure("EMAIL_CREDENTIAL_DATABASE_STATE_CHANGED")
	}
	storedAccount, err := readConnectionAccount(ctx, session.db)
	if err != nil {
		return failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	if textcompat.Fold(storedAccount) != account {
		return failure("EMAIL_CONNECTION_ACCOUNT_IMMUTABLE")
	}
	if problem := boundTargetProblem(session.root, home, expected.SecretName); problem != nil {
		return problem
	}
	return nil
}

type existingSenderRule struct {
	createdAt string
	policy    emailrouting.Policy
}

func updateSettingsRows(ctx context.Context, tx *sql.Tx, before emailcredentials.RowWitness, values normalizedNewConnection, overridePolicies bool) *Error {
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	scheduled := 0
	if values.scheduled {
		scheduled = 1
	}
	updated, err := tx.ExecContext(ctx, `UPDATE job_email_connections
		SET enabled=1,schedule_enabled=?,interval_minutes=?,revision=revision+1,updated_at=?
		WHERE connection_id='primary' AND revision=? AND enabled=1 AND secret_name=?`,
		scheduled, values.interval, now, before.Revision, before.SecretName)
	if err != nil {
		return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED")
	}
	count, err := updated.RowsAffected()
	if err != nil || count != 1 {
		return failure("EMAIL_CONNECTION_REVISION_CONFLICT")
	}
	return updateSenderRules(ctx, tx, values, overridePolicies, now)
}

// updateSenderRules is shared by settings-only edits and password rotation.
// All history changes occur in the caller's one immediate transaction.
func updateSenderRules(ctx context.Context, tx *sql.Tx, values normalizedNewConnection, overridePolicies bool, now string) *Error {
	rows, err := tx.QueryContext(ctx, `SELECT sender_address,created_at,subject_policy_json FROM job_email_sender_rules WHERE connection_id='primary'`)
	if err != nil {
		return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED")
	}
	existing := make(map[string]existingSenderRule)
	for rows.Next() {
		var address, createdAt, rawPolicy string
		if err := rows.Scan(&address, &createdAt, &rawPolicy); err != nil {
			rows.Close()
			return failure("EMAIL_CONNECTION_INVALID")
		}
		policy, err := emailrouting.ParsePolicyJSON(rawPolicy)
		if err != nil {
			rows.Close()
			return failure("EMAIL_CONNECTION_INVALID")
		}
		existing[address] = existingSenderRule{createdAt, policy}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED")
	}
	if err := rows.Close(); err != nil {
		return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE job_email_sender_rules SET disposition='disabled',updated_at=? WHERE connection_id='primary'`, now); err != nil {
		return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED")
	}
	for _, sender := range values.senders {
		createdAt := now
		policy := sender.policyJSON
		if previous, exists := existing[sender.address]; exists {
			createdAt = previous.createdAt
			if !overridePolicies {
				policy = previous.policy.CanonicalJSON()
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO job_email_sender_rules
			(connection_id,sender_address,disposition,purpose,subject_policy_json,created_at,updated_at)
			VALUES ('primary',?,'allowed',?,?,?,?)
			ON CONFLICT(connection_id,sender_address) DO UPDATE SET
				disposition='allowed',purpose=excluded.purpose,
				subject_policy_json=excluded.subject_policy_json,updated_at=excluded.updated_at`,
			sender.address, sender.purpose, policy, createdAt, now)
		if err != nil {
			return failure("EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED")
		}
	}
	return nil
}
