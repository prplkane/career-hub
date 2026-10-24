package startup

import (
	"context"

	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/homebackup"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/secretstore"
)

// RunBackupRestoreForProcess is a one-shot maintenance boundary, never ordinary
// bootstrap. The supervisor must stop/reap its app child and obtain the user's
// confirmation about legacy/file writers before dispatch; ownership ends only
// at process exit. No schema initialization, listener or mail work occurs here.
func RunBackupRestoreForProcess(ctx context.Context, request protocol.BackupRestoreRequest) (homebackup.RestoreResult, error) {
	return runBackupRestoreForProcess(ctx, request, secretstore.NewWindowsBackend)
}

func runBackupRestoreForProcess(ctx context.Context, request protocol.BackupRestoreRequest, factory func() (secretstore.Backend, error)) (out homebackup.RestoreResult, problem error) {
	out = homebackup.UnstartedRestoreResult()
	if ctx == nil || ctx.Err() != nil {
		return out, fail("DATA_HOME_STARTUP_CANCELLED")
	}
	if !request.ConfirmWritersStopped {
		return out, &homebackup.Error{Code: "BACKUP_WRITER_CONFIRMATION_REQUIRED"}
	}
	if request.Command != protocol.BackupRestoreCommand && request.Command != protocol.BackupRestoreRecoverCommand {
		return out, fail("SIDECAR_COMMAND_UNSUPPORTED")
	}
	if request.Command == protocol.BackupRestoreCommand && !protocol.ValidBackupArchivePath(request.ArchiveRelativePath) || request.Command == protocol.BackupRestoreRecoverCommand && request.ArchiveRelativePath != "" {
		return out, &homebackup.Error{Code: "BACKUP_SOURCE_UNAVAILABLE"}
	}
	if err := readonly.Preflight(request.Start); err != nil {
		return out, workspaceError(err)
	}
	root, q := runtimepaths.Open(request.Start.DataRoot)
	if q != nil {
		return out, fail("DATA_HOME_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil && problem == nil {
			problem = fail("DATA_HOME_OWNERSHIP_UNAVAILABLE")
		}
	}()
	if q := root.ClaimForProcess(); q != nil {
		return out, recoveryOwnershipError(q)
	}
	if request.Command == protocol.BackupRestoreRecoverCommand {
		classification, p := datahome.ClassifyOwnedRecovery(root, &request.Start.Channel)
		if p != nil {
			return out, recoveryAdmissionError(p)
		}
		if err := requireRecoveryKind(classification, datahome.RecoveryRestore); err != nil {
			return out, err
		}
	} else if _, p := datahome.Inspect(request.Start.DataRoot, &request.Start.Channel); p != nil {
		return out, recoveryAdmissionError(p)
	}
	if ctx.Err() != nil {
		return out, fail("DATA_HOME_STARTUP_CANCELLED")
	}
	if factory == nil {
		return out, &homebackup.Error{Code: "BACKUP_DOCUMENT_KEY_UNAVAILABLE"}
	}
	backend, err := factory()
	if err != nil || secretstore.IsNilBackend(backend) {
		return out, &homebackup.Error{Code: "BACKUP_DOCUMENT_KEY_UNAVAILABLE"}
	}
	// Factory construction is stateless; RecoverOwned checks scope/journal
	// evidence before any key read. Neither domain owner writes/deletes keys.
	var p *homebackup.Error
	if request.Command == protocol.BackupRestoreRecoverCommand {
		out, p = homebackup.RecoverOwned(ctx, homebackup.RecoverRequest{DataRoot: request.Start.DataRoot, Channel: request.Start.Channel, ConfirmWritersStopped: true}, backend)
	} else {
		out, p = homebackup.RestoreOwned(ctx, homebackup.RestoreRequest{DataRoot: request.Start.DataRoot, Channel: request.Start.Channel, ArchiveRelativePath: request.ArchiveRelativePath, ConfirmWritersStopped: true}, backend)
	}
	if p != nil {
		return out, p
	}
	return out, nil
}
