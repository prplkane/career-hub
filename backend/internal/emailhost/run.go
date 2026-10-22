// Package emailhost composes the native email worker for an opt-in Go preview.
// The caller must supply an already-admitted Data Home owner and an explicit
// provider-secret backend; this package never discovers either one.
package emailhost

import (
	"context"
	"errors"

	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/emailgate"
	"careerhub.local/backend/internal/emailimport"
	"careerhub.local/backend/internal/emailworker"
	"careerhub.local/backend/internal/mailtransport"
	"careerhub.local/backend/internal/secretstore"
)

type mailboxOpener func(context.Context, string, string, string) (emailworker.Mailbox, error)

func fail(code string) error { return &emailworker.Error{Code: code} }

// Run reads a current-root v3 Gmail credential, then
// invokes one bounded mailbox sync pass. The installed application does not call it.
func Run(ctx context.Context, owner *applicationwrite.Service, backend secretstore.Backend, trigger string) (emailworker.Result, error) {
	return run(ctx, owner, backend, trigger, func(ctx context.Context, account, password, folder string) (emailworker.Mailbox, error) {
		return mailtransport.Open(ctx, account, password, folder)
	})
}

func run(ctx context.Context, owner *applicationwrite.Service, backend secretstore.Backend, trigger string, open mailboxOpener) (emailworker.Result, error) {
	if ctx == nil || owner == nil || secretstore.IsNilBackend(backend) || open == nil ||
		(trigger != "manual" && trigger != "scheduled") {
		return emailworker.Result{}, fail("EMAIL_CONNECTION_INVALID")
	}
	release, gateError := emailgate.TryAcquire(ctx)
	if errors.Is(gateError, emailgate.ErrBusy) {
		return emailworker.Result{}, fail("EMAIL_SYNC_ALREADY_RUNNING")
	}
	if gateError != nil {
		return emailworker.Result{}, fail("EMAIL_SYNC_CANCELED")
	}
	defer release()
	config, err := owner.EmailSyncState().ConnectionConfig(ctx)
	if err != nil {
		return emailworker.Result{}, err
	}
	pinnedOwner := owner.PinnedToHome(config.Home)
	state := pinnedOwner.EmailSyncState()
	// Re-admit the exact row and physical scope with native pins retained
	// through the secret read; returning ConnectionConfig alone is insufficient.
	var password string
	err = state.WithConnectionConfig(ctx, config, func(current applicationwrite.EmailConnectionConfig) error {
		secrets, err := secretstore.New(current.Home.Channel, backend)
		if err != nil {
			return fail("EMAIL_SECRET_STORE_FAILED")
		}
		value, found, err := secrets.Get(current.SecretName)
		if err != nil {
			return fail("EMAIL_SECRET_STORE_FAILED")
		}
		if !found || value == "" {
			return fail("EMAIL_CREDENTIAL_MISSING")
		}
		password = value
		return nil
	})
	if err != nil {
		return emailworker.Result{}, err
	}
	return emailworker.Run(ctx, state,
		func(ctx context.Context) (emailworker.Mailbox, error) {
			var mailbox emailworker.Mailbox
			var openErr error
			err := state.WithConnectionConfig(ctx, config, func(current applicationwrite.EmailConnectionConfig) error {
				mailbox, openErr = open(ctx, current.AccountAddress, password, current.Folder)
				// Preserve transport error identity for the worker, while still
				// checking the local boundary after even a failed network open.
				return nil
			})
			if err == nil {
				err = openErr
			}
			if err != nil && mailbox != nil {
				if closeErr := mailbox.Close(); closeErr != nil {
					return nil, fail("EMAIL_SYNC_MAILBOX_CLEANUP_FAILED")
				}
				mailbox = nil
			}
			return mailbox, err
		}, emailimport.Importer{Writer: pinnedOwner}, emailworker.Config{
			Trigger: trigger, ScheduleEnabled: config.ScheduleEnabled,
			LookbackDays: config.LookbackDays, MaxMessages: config.MaxMessagesPerSync,
		})
}
