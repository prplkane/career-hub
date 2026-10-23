package emailconnection

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/email/emailgate"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/secretstore"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

// ReadOwnedSnapshot binds the source-shaped email read model to a process-owned
// Data Home and an explicitly supplied startup Home identity and credential
// backend. It neither creates a Home/schema nor opens a mailbox. No target
// name or credential is returned.
func ReadOwnedSnapshot(ctx context.Context, dataRoot string, expected datahome.Info, backend secretstore.Backend) (snapshot ConnectionSnapshot, problem *Error) {
	if ctx == nil || ctx.Err() != nil {
		return ConnectionSnapshot{}, failure("EMAIL_CONNECTION_CANCELED")
	}
	if dataRoot == "" || secretstore.IsNilBackend(backend) {
		return ConnectionSnapshot{}, failure("EMAIL_CONNECTION_INVALID")
	}
	// The connection row and credential presence form one public snapshot.
	// Hold the same operation gate as native credential mutations and sync
	// until all SQL, path and Home cleanup has finished.
	release, err := emailgate.Acquire(ctx)
	if err != nil {
		return ConnectionSnapshot{}, failure("EMAIL_CONNECTION_CANCELED")
	}
	defer release()
	root, pathProblem := runtimepaths.Open(dataRoot)
	if pathProblem != nil || root == nil {
		return ConnectionSnapshot{}, failure("DATA_HOME_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			snapshot, problem = ConnectionSnapshot{}, failure("EMAIL_CONNECTION_CLEANUP_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return ConnectionSnapshot{}, failure("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	home, admission := datahome.Inspect(dataRoot, &expected.Channel)
	if admission != nil || home == nil {
		if admission != nil {
			return ConnectionSnapshot{}, failure(admission.Code)
		}
		return ConnectionSnapshot{}, failure("DATA_HOME_UNAVAILABLE")
	}
	if *home != expected {
		return ConnectionSnapshot{}, failure("DATA_HOME_CHANGED")
	}
	pins, pathProblem := root.GuardSQLiteFiles(databasePath)
	if pathProblem != nil || pins == nil {
		return ConnectionSnapshot{}, failure("EMAIL_CONNECTION_DATABASE_UNAVAILABLE")
	}
	defer func() {
		if pins.Close() != nil {
			snapshot, problem = ConnectionSnapshot{}, failure("EMAIL_CONNECTION_CLEANUP_FAILED")
		}
	}()
	db, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(dataRoot, filepath.FromSlash(databasePath)))
	if err != nil {
		return ConnectionSnapshot{}, failure("EMAIL_CONNECTION_DATABASE_UNAVAILABLE")
	}
	defer func() {
		if db.Close() != nil {
			snapshot, problem = ConnectionSnapshot{}, failure("EMAIL_CONNECTION_CLEANUP_FAILED")
		}
	}()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ConnectionSnapshot{}, failure("EMAIL_CONNECTION_DATABASE_UNAVAILABLE")
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return ConnectionSnapshot{}, failure("EMAIL_CONNECTION_DATABASE_UNAVAILABLE")
	}
	if version < applications.SchemaVersion {
		return ConnectionSnapshot{}, failure("EMAIL_CONNECTION_SCHEMA_PREPARATION_REQUIRED")
	}
	if version > applications.SchemaVersion {
		return ConnectionSnapshot{}, failure("EMAIL_CONNECTION_SCHEMA_TOO_NEW")
	}
	// The target comes from the same transaction as the complete read model.
	// Missing rows never probe the shared legacy target. An unbound/copied
	// row is displayable reconnect metadata, not authority to read a secret.
	var target string
	err = tx.QueryRowContext(ctx, `SELECT secret_name FROM job_email_connections
		WHERE connection_id = ?`, "primary").Scan(&target)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ConnectionSnapshot{}, failure("EMAIL_CONNECTION_DATABASE_UNAVAILABLE")
	}
	present, reconnect := false, false
	if err == nil {
		var code string
		present, reconnect, code = ownedCredentialPresent(root, *home, target, backend)
		if code != "" {
			return ConnectionSnapshot{}, failure(code)
		}
	}
	snapshot, err = ReadSnapshot(ctx, tx, present)
	if err != nil {
		return ConnectionSnapshot{}, failure("EMAIL_CONNECTION_FAILED")
	}
	if reconnect {
		// Derived state only; do not overwrite the stored test result or claim
		// the configured account is missing. Sync/mutation owners must refuse too.
		snapshot.LastErrorCode = "EMAIL_CREDENTIAL_RECONNECT_REQUIRED"
	}
	if ctx.Err() != nil {
		return ConnectionSnapshot{}, failure("EMAIL_CONNECTION_CANCELED")
	}
	if pins.Check() != nil || root.RequireProcessOwnership() != nil {
		return ConnectionSnapshot{}, failure("EMAIL_CONNECTION_PATH_CHANGED")
	}
	fresh, admission := datahome.Inspect(dataRoot, &expected.Channel)
	if admission != nil || fresh == nil || *fresh != expected {
		return ConnectionSnapshot{}, failure("DATA_HOME_CHANGED")
	}
	if err := tx.Commit(); err != nil {
		return ConnectionSnapshot{}, failure("EMAIL_CONNECTION_DATABASE_UNAVAILABLE")
	}
	fresh, admission = datahome.Inspect(dataRoot, &expected.Channel)
	if admission != nil || fresh == nil || *fresh != expected {
		return ConnectionSnapshot{}, failure("DATA_HOME_CHANGED")
	}
	return snapshot, nil
}

func ownedCredentialPresent(root *runtimepaths.Root, home datahome.Info, target string, backend secretstore.Backend) (bool, bool, string) {
	scope, scopeError := root.CredentialScope()
	if scopeError != nil {
		return false, false, "EMAIL_CREDENTIAL_SCOPE_UNAVAILABLE"
	}
	reconnect, err := emailcredentials.RequiresReauthorization(home, scope, target)
	if err != nil {
		return false, false, "EMAIL_CONNECTION_INVALID"
	}
	if reconnect {
		return false, true, ""
	}
	store, err := secretstore.New(home.Channel, backend)
	if err != nil {
		return false, false, "EMAIL_SECRET_STORE_FAILED"
	}
	_, found, err := store.Get(target)
	if err != nil {
		return false, false, "EMAIL_SECRET_STORE_FAILED"
	}
	current, scopeError := root.CredentialScope()
	if scopeError != nil || !scope.Equal(current) {
		return false, false, "EMAIL_CREDENTIAL_SCOPE_CHANGED"
	}
	return found, false, ""
}
