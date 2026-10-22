package startup

import (
	"context"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/homebackup"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/secretstore"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

// CreateBackupForProcess is a dedicated maintenance entry: the parent must stop
// and reap its app child first, keep it stopped until this process exits, and
// obtain explicit legacy/file-writer quiescence confirmation. ClaimForProcess
// lasts until OS process exit; it is not exclusion of old Python/CLI writers.
func CreateBackupForProcess(ctx context.Context, request protocol.BackupCreateRequest) (homebackup.CreationResult, error) {
	return createBackupForProcess(ctx, request, secretstore.NewWindowsBackend)
}

func createBackupForProcess(ctx context.Context, request protocol.BackupCreateRequest, factory func() (secretstore.Backend, error)) (out homebackup.CreationResult, problem error) {
	out.SnapshotNamespace = runtimepaths.DirectoryNotCreated
	out.LastStagedFile = runtimepaths.StreamPublication{Outcome: runtimepaths.FileNotPublished, CleanupConfirmed: true}
	out.ArchivePublication = out.LastStagedFile
	out.SnapshotCleanup = homebackup.SnapshotCleanupResult{LastRemoval: runtimepaths.BackupNotRemoved, SnapshotRemoval: runtimepaths.BackupNotRemoved}
	if ctx == nil || ctx.Err() != nil {
		return out, fail("DATA_HOME_STARTUP_CANCELLED")
	}
	if !request.ConfirmWritersStopped {
		return out, &homebackup.Error{Code: "BACKUP_WRITER_CONFIRMATION_REQUIRED"}
	}
	if err := readonly.Preflight(request.Start); err != nil {
		return out, workspaceError(err)
	}
	root, pathProblem := runtimepaths.Open(request.Start.DataRoot)
	if pathProblem != nil {
		return out, fail("DATA_HOME_UNAVAILABLE")
	}
	defer func() {
		// Unlike ordinary startup, never discard known/unknown publication facts.
		if err := root.Close(); err != nil && problem == nil {
			problem = fail("DATA_HOME_OWNERSHIP_UNAVAILABLE")
		}
	}()
	if p := root.ClaimForProcess(); p != nil {
		return out, recoveryOwnershipError(p)
	}
	if _, p := datahome.Inspect(request.Start.DataRoot, &request.Start.Channel); p != nil {
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
	result, p := homebackup.CreateOwned(ctx, homebackup.CreateRequest{
		DataRoot: request.Start.DataRoot, Channel: request.Start.Channel, ConfirmWritersStopped: true,
		ArchiveRelativePath: request.ArchiveRelativePath, IncludeRoots: request.IncludeRoots,
	}, backend)
	if p != nil {
		return result, p
	}
	return result, nil
}
