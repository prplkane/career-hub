package emailconnection

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emailcredentials"
	"careerhub.local/backend/internal/emailgate"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/secretstore"
	"careerhub.local/backend/internal/sqlitestore"
)

// credentialSession retains both operation gates and the checked Home/SQLite
// paths until the synchronous credential operation has finished. It is private
// to the future native mutator; merely publishing an intent is not a product
// action, and no installed route calls this code.
type credentialSession struct {
	root           *runtimepaths.Root
	pins           *runtimepaths.SQLiteFiles
	db             *sql.DB
	intentRaw      []byte
	releaseGeneral func()
	releaseEmail   func()
	closed         bool
}

func (session *credentialSession) Close() *Error {
	if session == nil || session.closed {
		return nil
	}
	session.closed = true
	uncertain := false
	if session.db != nil && session.db.Close() != nil {
		uncertain = true
	}
	if session.pins != nil && session.pins.Close() != nil {
		uncertain = true
	}
	if session.root != nil && session.root.Close() != nil {
		uncertain = true
	}
	if session.releaseGeneral != nil {
		session.releaseGeneral()
	}
	if session.releaseEmail != nil {
		session.releaseEmail()
	}
	if uncertain {
		return failure("EMAIL_CREDENTIAL_OPERATION_UNCONFIRMED")
	}
	return nil
}

// stageCredentialIntent publishes one durable, nonsecret intent before a future
// coordinator is allowed to change a credential or SQLite. A failed post-create
// check never removes the sentinel: explicit recovery must settle uncertainty.
func stageCredentialIntent(ctx context.Context, dataRoot string, backend secretstore.Backend, intent emailcredentials.ScopedIntent) (owned *credentialSession, problem *Error) {
	if ctx == nil || dataRoot == "" || secretstore.IsNilBackend(backend) {
		return nil, failure("EMAIL_CREDENTIAL_PUBLICATION_INVALID")
	}
	var err error
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
	session.releaseEmail, err = emailgate.Acquire(gateCtx)
	if err != nil {
		return nil, failure("EMAIL_CREDENTIAL_OPERATION_CANCELED")
	}
	session.releaseGeneral, err = operationgate.Acquire(gateCtx)
	if err != nil {
		return nil, failure("EMAIL_CREDENTIAL_OPERATION_CANCELED")
	}
	var pathProblem *runtimepaths.Error
	session.root, pathProblem = runtimepaths.Open(dataRoot)
	if pathProblem != nil || session.root == nil {
		return nil, failure("EMAIL_CREDENTIAL_PATH_UNAVAILABLE")
	}
	if session.root.RequireProcessOwnership() != nil {
		return nil, failure("DATA_HOME_OWNERSHIP_REQUIRED")
	}
	home, admission := datahome.Inspect(dataRoot, nil)
	if admission != nil {
		return nil, failure(admission.Code)
	}
	if *home != intent.Home {
		return nil, failure("EMAIL_CREDENTIAL_INTENT_HOME_CHANGED")
	}
	scope, scopeError := session.root.CredentialScope()
	if scopeError != nil {
		return nil, failure("EMAIL_CREDENTIAL_SCOPE_UNAVAILABLE")
	}
	raw, err := emailcredentials.EncodeScopedIntent(intent, scope)
	if err != nil {
		return nil, failure("EMAIL_CREDENTIAL_INTENT_INVALID")
	}
	session.pins, pathProblem = session.root.GuardSQLiteFiles(databasePath)
	if pathProblem != nil || session.pins == nil {
		return nil, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	session.db, err = sqlitestore.OpenReadOnly(ctx, filepath.Join(dataRoot, filepath.FromSlash(databasePath)))
	if err != nil {
		return nil, failure("EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE")
	}
	current, witnessProblem := readWitness(ctx, session.db)
	if witnessProblem != nil {
		return nil, witnessProblem
	}
	if session.pins.Check() != nil {
		return nil, failure("EMAIL_CREDENTIAL_DATABASE_STATE_CHANGED")
	}
	if current != intent.Before {
		if intent.Operation == emailcredentials.InitialConfigure && !intent.Before.Present && current.Present {
			return nil, failure("EMAIL_CONNECTION_REVISION_CONFLICT")
		}
		return nil, failure("EMAIL_CREDENTIAL_DATABASE_STATE_CHANGED")
	}
	if problem = preflightTargets(ctx, session.root, *home, backend, intent); problem != nil {
		return nil, problem
	}
	problem = publishIntent(session.root, raw, func() *Error {
		return session.verifyPublication(ctx, intent.Home, raw, intent.Before)
	})
	if problem != nil {
		return nil, problem
	}
	session.intentRaw = raw
	return session, nil
}

// preflightTargets runs only after Home, SQLite and row admission, with both
// gates held. A new generation is never allowed to replace an existing target.
// Legacy v1 retirement remains unavailable. Explicit non-deleting promotion
// can stage a new local v3 target without reading any unbound/foreign credential.
func preflightTargets(ctx context.Context, root *runtimepaths.Root, home datahome.Info, backend secretstore.Backend, intent emailcredentials.ScopedIntent) *Error {
	secrets, err := newBoundSecrets(ctx, root, home, backend, nil)
	if err != nil {
		return credentialStoreProblem(err)
	}
	if intent.Before.Present && !nonDeletingOperation(intent.Operation) {
		value, found, err := secrets.Get(intent.Before.SecretName)
		if err != nil {
			return credentialStoreProblem(err)
		}
		if found && value == "" {
			return failure("EMAIL_CREDENTIAL_RECOVERY_REQUIRED")
		}
		if intent.Before.Enabled && !found {
			return failure("EMAIL_CREDENTIAL_MISSING")
		}
	}
	if intent.Operation == emailcredentials.InitialConfigure || intent.Operation == emailcredentials.Rotate || nonDeletingOperation(intent.Operation) {
		_, found, err := secrets.Get(intent.After.SecretName)
		if err != nil {
			return credentialStoreProblem(err)
		}
		if found {
			return failure("EMAIL_CREDENTIAL_TARGET_COLLISION")
		}
	}
	return nil
}

func publishIntent(root *runtimepaths.Root, raw []byte, verify func() *Error) *Error {
	if root == nil || len(raw) == 0 || verify == nil {
		return failure("EMAIL_CREDENTIAL_PUBLICATION_INVALID")
	}
	created, pathProblem := root.CreateFile(datahome.EmailCredentialRecoveryPath, raw)
	if pathProblem != nil || !created {
		if created {
			return failure("EMAIL_CREDENTIAL_PUBLICATION_UNCONFIRMED")
		}
		if pathProblem != nil && pathProblem.Code == "FILE_EXISTS" {
			return failure("DATA_HOME_RECOVERY_REQUIRED")
		}
		return failure("EMAIL_CREDENTIAL_INTENT_UNAVAILABLE")
	}
	return verify()
}

func (session *credentialSession) verifyPublication(ctx context.Context, home datahome.Info, raw []byte, before emailcredentials.RowWitness) *Error {
	if ctx.Err() != nil || session.root.RequireProcessOwnership() != nil {
		return failure("EMAIL_CREDENTIAL_PUBLICATION_UNCONFIRMED")
	}
	admitted, admission := datahome.InspectOwnedForEmailCredentialRecovery(session.root, nil)
	if admission != nil || *admitted != home {
		return failure("EMAIL_CREDENTIAL_PUBLICATION_UNCONFIRMED")
	}
	observed, present, pathProblem := session.root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	if pathProblem != nil || !present || !bytes.Equal(observed, raw) || session.pins.Check() != nil {
		return failure("EMAIL_CREDENTIAL_PUBLICATION_UNCONFIRMED")
	}
	scope, e := session.root.CredentialScope()
	if e != nil {
		return failure("EMAIL_CREDENTIAL_PUBLICATION_UNCONFIRMED")
	}
	if _, err := emailcredentials.DecodeScopedIntent(observed, home, scope); err != nil {
		return failure("EMAIL_CREDENTIAL_PUBLICATION_UNCONFIRMED")
	}
	current, witnessProblem := readWitness(ctx, session.db)
	if witnessProblem != nil || current != before || session.pins.Check() != nil {
		return failure("EMAIL_CREDENTIAL_PUBLICATION_UNCONFIRMED")
	}
	return nil
}
