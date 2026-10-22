package applicationwrite

import (
	"context"
	"database/sql"
	"errors"
	"regexp"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emailcredentials"
	"careerhub.local/backend/internal/emailreceipts"
	"careerhub.local/backend/internal/emailrouting"
	"careerhub.local/backend/internal/emailworker"
	"careerhub.local/backend/internal/runtimepaths"
)

var emailStateCode = regexp.MustCompile(`^EMAIL_[A-Z0-9_]{1,80}$`)

const emailStateAck = `{}`

// EmailPrimarySecretName is the fixed legacy Gmail credential target. New
// target is metadata only; live lookups require a current-root v3 target.
const EmailPrimarySecretName = emailcredentials.LegacyName

type EmailConnectionConfig struct {
	Home               datahome.Info
	Scope              runtimepaths.CredentialScope
	Revision           int64
	AccountAddress     string
	Folder             string
	SecretName         string
	ScheduleEnabled    bool
	LookbackDays       int
	MaxMessagesPerSync int
}

// EmailSyncState implements the worker's fixed state operations through the
// same admitted Data Home, operation gate, SQLite path pins and schema check
// as all other native application writes. No method opens a mailbox or secret.
type EmailSyncState struct{ owner *Service }

var _ emailworker.State = (*EmailSyncState)(nil)

func (s *Service) EmailSyncState() *EmailSyncState { return &EmailSyncState{owner: s} }

// ConnectionConfig reads the one admitted connection inside the native owner.
// It returns no secret and performs no write or mailbox access.
func (state *EmailSyncState) ConnectionConfig(ctx context.Context) (EmailConnectionConfig, error) {
	return state.connectionConfig(ctx, nil, nil)
}

// WithConnectionConfig re-admits an exact previously observed configuration
// while retaining root/SQLite pins and the operation gate through action.
// It is for a bounded secret read or mailbox open, not a complete sync or a
// reentrant owner call. No callback runs for an unbound/foreign credential.
func (state *EmailSyncState) WithConnectionConfig(ctx context.Context, expected EmailConnectionConfig, action func(EmailConnectionConfig) error) error {
	if action == nil {
		return &emailworker.Error{Code: "EMAIL_CONNECTION_INVALID"}
	}
	_, err := state.connectionConfig(ctx, &expected, action)
	return err
}

func (state *EmailSyncState) connectionConfig(ctx context.Context, expected *EmailConnectionConfig, action func(EmailConnectionConfig) error) (EmailConnectionConfig, error) {
	var config EmailConnectionConfig
	err := state.runWithRoot(ctx, func(ctx context.Context, tx *sql.Tx, home datahome.Info, root *runtimepaths.Root) (bool, error) {
		config.Home = home
		var provider, secretName string
		var enabled, scheduled int
		err := tx.QueryRowContext(ctx, `SELECT provider, account_address, folder, secret_name,
			enabled, schedule_enabled, lookback_days, max_messages_per_sync, revision
			FROM job_email_connections WHERE connection_id='primary'`).Scan(
			&provider, &config.AccountAddress, &config.Folder, &secretName,
			&enabled, &scheduled, &config.LookbackDays, &config.MaxMessagesPerSync, &config.Revision)
		if errors.Is(err, sql.ErrNoRows) {
			return false, errors.New("EMAIL_CONNECTION_NOT_CONFIGURED")
		}
		if err != nil {
			return false, err
		}
		if enabled == 0 {
			return false, errors.New("EMAIL_SYNC_DISABLED")
		}
		if enabled != 1 || (scheduled != 0 && scheduled != 1) ||
			provider != "gmail_imap" || config.Folder != "INBOX" ||
			config.AccountAddress == "" ||
			config.LookbackDays < 1 || config.LookbackDays > emailworker.MaxLookbackDays ||
			config.MaxMessagesPerSync < 1 || config.MaxMessagesPerSync > emailworker.MaxMessagesPerSync ||
			config.Revision < 1 || config.Revision > emailcredentials.MaxRevision {
			return false, errors.New("EMAIL_CONNECTION_INVALID")
		}
		scope, scopeError := root.CredentialScope()
		if scopeError != nil {
			return false, errors.New("EMAIL_CREDENTIAL_SCOPE_UNAVAILABLE")
		}
		reconnect, err := emailcredentials.RequiresReauthorization(home, scope, secretName)
		if err != nil {
			return false, err
		}
		if reconnect {
			return false, errors.New("EMAIL_CREDENTIAL_RECONNECT_REQUIRED")
		}
		config.Scope = scope
		config.SecretName = secretName
		config.ScheduleEnabled = scheduled == 1
		if expected != nil && config != *expected {
			return false, errors.New("EMAIL_CONNECTION_CHANGED")
		}
		if action != nil {
			if err := action(config); err != nil {
				return false, err
			}
		}
		fresh, scopeError := root.CredentialScope()
		if scopeError != nil || !scope.Equal(fresh) {
			return false, errors.New("EMAIL_CREDENTIAL_SCOPE_CHANGED")
		}
		return false, nil
	})
	if err != nil {
		return EmailConnectionConfig{}, err
	}
	return config, nil
}

func emailStateProblem(err error) *Error {
	if err == nil {
		return nil
	}
	code := err.Error()
	if !emailStateCode.MatchString(code) {
		code = "EMAIL_SYNC_LOCAL_WRITE_FAILED"
	}
	return failed("email_state", code)
}

func emailStateFailure(problem *Error) error {
	code := "EMAIL_SYNC_LOCAL_WRITE_FAILED"
	if problem != nil {
		if emailStateCode.MatchString(problem.Code) {
			code = problem.Code
		} else if problem.Code == "APPLICATION_LEDGER_CANCELED" {
			code = "EMAIL_SYNC_CANCELED"
		}
	}
	return &emailworker.Error{Code: code}
}

// run commits each state write before returning a result. Read-only and
// unchanged operations roll back. Typed results stay in the closure because
// sender policies contain private fields that cannot survive JSON encoding.
func (state *EmailSyncState) run(ctx context.Context, operation func(context.Context, *sql.Tx) (bool, error)) error {
	if operation == nil {
		return &emailworker.Error{Code: "EMAIL_CONNECTION_INVALID"}
	}
	return state.runWithHome(ctx, func(ctx context.Context, tx *sql.Tx, _ datahome.Info) (bool, error) {
		return operation(ctx, tx)
	})
}

func (state *EmailSyncState) runWithHome(ctx context.Context, operation func(context.Context, *sql.Tx, datahome.Info) (bool, error)) error {
	if operation == nil {
		return &emailworker.Error{Code: "EMAIL_CONNECTION_INVALID"}
	}
	return state.runWithRoot(ctx, func(ctx context.Context, tx *sql.Tx, home datahome.Info, _ *runtimepaths.Root) (bool, error) {
		return operation(ctx, tx, home)
	})
}

func (state *EmailSyncState) runWithRoot(ctx context.Context, operation func(context.Context, *sql.Tx, datahome.Info, *runtimepaths.Root) (bool, error)) error {
	if state == nil || state.owner == nil || ctx == nil || operation == nil {
		return &emailworker.Error{Code: "EMAIL_CONNECTION_INVALID"}
	}
	out, problem := state.owner.run(ctx, "", func(reads *sourceReads) (mutation, *Error) {
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			changed, err := operation(ctx, tx, reads.home, reads.root)
			if err != nil {
				return nil, emailStateProblem(err)
			}
			return &applications.MutationResult{Result: []byte(emailStateAck), Changed: changed}, nil
		}, nil
	})
	if out != nil && (out.CleanupFailed || out.RecoveryRequired ||
		(out.CommitAttempted && (!out.Committed || len(out.Result) == 0)) ||
		(out.Committed && !out.CommitAttempted)) {
		return emailworker.ErrCommitUnknown
	}
	if problem != nil && out != nil && out.CommitAttempted {
		return emailworker.ErrCommitUnknown
	}
	if problem != nil || out == nil || len(out.Result) == 0 {
		return emailStateFailure(problem)
	}
	return nil
}

func (state *EmailSyncState) Acquire(ctx context.Context, started, owner string, scheduled bool) (string, error) {
	var deferred string
	err := state.run(ctx, func(ctx context.Context, tx *sql.Tx) (bool, error) {
		var e error
		deferred, e = emailreceipts.AcquireLease(ctx, tx, started, owner, scheduled)
		return deferred == "" && e == nil, e
	})
	if err != nil {
		return "", err
	}
	return deferred, nil
}

func (state *EmailSyncState) SetUIDValidity(ctx context.Context, epoch string) (bool, error) {
	var reset bool
	err := state.run(ctx, func(ctx context.Context, tx *sql.Tx) (bool, error) {
		previous, _, e := emailreceipts.Position(ctx, tx)
		if e != nil {
			return false, e
		}
		reset, e = emailreceipts.SetUIDValidity(ctx, tx, epoch)
		return previous != epoch && e == nil, e
	})
	if err != nil {
		return false, err
	}
	return reset, nil
}

func (state *EmailSyncState) Position(ctx context.Context) (string, uint32, error) {
	var epoch string
	var uid uint32
	err := state.run(ctx, func(ctx context.Context, tx *sql.Tx) (bool, error) {
		var e error
		epoch, uid, e = emailreceipts.Position(ctx, tx)
		return false, e
	})
	if err != nil {
		return "", 0, err
	}
	return epoch, uid, nil
}

func (state *EmailSyncState) AllowedRules(ctx context.Context) (map[string]emailrouting.Rule, error) {
	var rules map[string]emailrouting.Rule
	err := state.run(ctx, func(ctx context.Context, tx *sql.Tx) (bool, error) {
		var e error
		rules, e = emailrouting.LoadAllowed(ctx, tx)
		return false, e
	})
	if err != nil {
		return nil, err
	}
	return rules, nil
}

func (state *EmailSyncState) PendingRetryUIDs(ctx context.Context, epoch string, allowed map[string]bool) ([]uint32, error) {
	var uids []uint32
	err := state.run(ctx, func(ctx context.Context, tx *sql.Tx) (bool, error) {
		var e error
		uids, e = emailreceipts.PendingRetryUIDs(ctx, tx, epoch, allowed)
		return false, e
	})
	if err != nil {
		return nil, err
	}
	return uids, nil
}

func (state *EmailSyncState) Receipt(ctx context.Context, epoch string, uid uint32) (*emailreceipts.Receipt, error) {
	var receipt *emailreceipts.Receipt
	err := state.run(ctx, func(ctx context.Context, tx *sql.Tx) (bool, error) {
		var e error
		receipt, e = emailreceipts.ByTransport(ctx, tx, epoch, uid)
		return false, e
	})
	if err != nil {
		return nil, err
	}
	return receipt, nil
}

func (state *EmailSyncState) IdentityProcessed(ctx context.Context, field, value, epoch string, uid uint32) (bool, error) {
	var processed bool
	err := state.run(ctx, func(ctx context.Context, tx *sql.Tx) (bool, error) {
		var e error
		processed, e = emailreceipts.IdentityWasProcessed(ctx, tx, field, value, epoch, uid)
		return false, e
	})
	if err != nil {
		return false, err
	}
	return processed, nil
}

func (state *EmailSyncState) Record(ctx context.Context, input emailreceipts.RecordInput) (int, error) {
	var attempts int
	err := state.run(ctx, func(ctx context.Context, tx *sql.Tx) (bool, error) {
		var e error
		attempts, e = emailreceipts.Record(ctx, tx, input)
		return e == nil, e
	})
	if err != nil {
		return 0, err
	}
	return attempts, nil
}

func (state *EmailSyncState) Complete(ctx context.Context, owner, at string, issues, retry bool) error {
	return state.run(ctx, func(ctx context.Context, tx *sql.Tx) (bool, error) {
		return emailreceipts.MarkCompleted(ctx, tx, owner, at, issues, retry)
	})
}

func (state *EmailSyncState) Fail(ctx context.Context, owner, code, at string) error {
	return state.run(ctx, func(ctx context.Context, tx *sql.Tx) (bool, error) {
		return emailreceipts.MarkFailed(ctx, tx, owner, code, at)
	})
}
