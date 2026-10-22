package startup

import (
	"context"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emailconnection"
	"careerhub.local/backend/internal/secretstore"
	"careerhub.local/backend/internal/sidecar/protocol"
)

// RecoverEmailCredentialForProcess claims the pending Home until process exit.
// It does not bootstrap, migrate SQLite, create secrets, or start an HTTP server.
func RecoverEmailCredentialForProcess(ctx context.Context, start protocol.StartRecord) (emailconnection.Result, error) {
	return recoverEmailForProcess(ctx, start, secretstore.NewWindowsBackend)
}

func recoverEmailForProcess(ctx context.Context, start protocol.StartRecord, factory func() (secretstore.Backend, error)) (emailconnection.Result, error) {
	home, err := admitRecoveryForProcess(ctx, start, datahome.RecoveryEmailCredential)
	if err != nil {
		return emailconnection.Result{}, err
	}
	if ctx.Err() != nil || factory == nil {
		return emailconnection.Result{}, &emailconnection.Error{Code: "EMAIL_CREDENTIAL_RECOVERY_CANCELED"}
	}
	backend, err := factory()
	if err != nil || secretstore.IsNilBackend(backend) {
		return emailconnection.Result{}, &emailconnection.Error{Code: "EMAIL_SECRET_STORE_FAILED"}
	}
	out, problem := emailconnection.RecoverPinned(ctx, start.DataRoot, backend, *home)
	if problem != nil {
		return out, problem
	}
	return out, nil
}
