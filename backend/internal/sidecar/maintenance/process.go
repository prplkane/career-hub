package maintenance

import (
	"context"
	"io"
	"time"

	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/startup"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documentmigration"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/secretstore"
)

type documentBackendFactory func() (keys.Backend, error)
type executeDocument func(context.Context, protocol.DocumentMigrationMaintenanceRequest) (map[string]any, string)
type documentRecoveryInventory interface {
	RequireProcessOwnership() *runtimepaths.Error
	Exists(string) (bool, *runtimepaths.Error)
}

type processDependencies struct {
	clock        func() time.Time
	openRecovery openRecovery
	document     executeDocument
	approval     executeApprovalRecovery
	email        executeEmailRecovery
	inspect      inspectRecovery
	backup       executeBackupCreate
	restore      executeBackupRestore
}

func defaultProcessDependencies() processDependencies {
	return processDependencies{
		clock:    time.Now,
		approval: startup.RecoverApprovalForProcess,
		email:    startup.RecoverEmailCredentialForProcess,
		inspect:  startup.InspectRecoveryForProcess,
		backup:   startup.CreateBackupForProcess,
		restore:  startup.RunBackupRestoreForProcess,
		openRecovery: func(ctx context.Context, start protocol.StartRecord) (recoveryWorkspace, error) {
			return startup.OpenNewJobRecoveryForProcess(ctx, start)
		},
		document: func(ctx context.Context, request protocol.DocumentMigrationMaintenanceRequest) (map[string]any, string) {
			return executeOwnedDocumentRequest(ctx, request, runtimepaths.Open, func() (keys.Backend, error) {
				return secretstore.NewWindowsBackend()
			})
		},
	}
}

// Run owns one maintenance child's two pipe endpoints. It consumes one bounded
// record, emits one record, starts no listener, and returns so the executable
// can exit and release its process-lifetime Data Home claim. Electron owns the
// hard deadline and final reap; this child never detaches background work.
func Run(input io.ReadCloser, output io.WriteCloser) int {
	if input == nil {
		if output == nil {
			return 6
		}
		defer output.Close()
		return emitFailure(output, "SIDECAR_MAINTENANCE_READ_FAILED", 3)
	}
	defer input.Close()
	if output == nil {
		return 6
	}
	defer output.Close()
	return runProcess(context.Background(), input, output, defaultProcessDependencies())
}

func runProcess(ctx context.Context, input io.Reader, output io.Writer, dependencies processDependencies) int {
	if ctx == nil || input == nil || output == nil {
		return emitFailure(output, "SIDECAR_MAINTENANCE_FAILED", 4)
	}
	payload, err := protocol.ReadRecord(input)
	if err != nil {
		return emitFailure(output, requestErrorCode(err), 3)
	}
	record, code, status := executeMaintenancePayload(ctx, payload, dependencies)
	if code != "" {
		if record != nil {
			if emitSuccess(output, record) != 0 {
				return 6
			}
			return status
		}
		return emitFailure(output, code, status)
	}
	return emitSuccess(output, record)
}

func executeMaintenancePayload(ctx context.Context, payload map[string]any,
	dependencies processDependencies) (record map[string]any, code string, status int) {
	defer func() {
		if recover() != nil {
			record, code, status = nil, "SIDECAR_MAINTENANCE_FAILED", 4
		}
	}()
	command, ok := payload["command"].(string)
	if !ok {
		return nil, "SIDECAR_COMMAND_UNSUPPORTED", 3
	}
	switch command {
	case protocol.BackupCreateCommand:
		return executeBackupCreatePayload(ctx, payload, dependencies.backup)
	case protocol.BackupRestoreCommand, protocol.BackupRestoreRecoverCommand:
		return executeBackupRestorePayload(ctx, payload, dependencies.restore)
	case protocol.DataHomeRecoveryInspectCommand:
		return executeRecoveryInspection(ctx, payload, dependencies.inspect)
	case protocol.EmailCredentialRecoveryCommand:
		return executeEmailRecoveryPayload(ctx, payload, dependencies.email)
	case protocol.ApprovalRecoveryCommand:
		return executeApprovalRecoveryPayload(ctx, payload, dependencies.approval)
	case protocol.DataHomeRecoveryPlanCommand, protocol.DataHomeRecoveryAdvanceCommand:
		return executeRecoveryPayload(ctx, payload, dependencies.clock, dependencies.openRecovery)
	case protocol.DocumentMigrationPlanCommand, protocol.DocumentMigrationApplyCommand,
		protocol.DocumentMigrationRecoverCommand:
		request, err := protocol.ParseDocumentMigrationMaintenance(payload, protocol.PathOptions{})
		if err != nil {
			return nil, requestErrorCode(err), 3
		}
		if dependencies.document == nil {
			return nil, "SIDECAR_MAINTENANCE_FAILED", 4
		}
		record, code := dependencies.document(ctx, request)
		if code != "" {
			return nil, code, 4
		}
		return record, "", 0
	default:
		return nil, "SIDECAR_COMMAND_UNSUPPORTED", 3
	}
}

func executeOwnedDocumentRequest(ctx context.Context, request protocol.DocumentMigrationMaintenanceRequest,
	openRoot func(string) (*runtimepaths.Root, *runtimepaths.Error),
	backendFactory documentBackendFactory) (record map[string]any, code string) {
	if ctx == nil || ctx.Err() != nil || openRoot == nil {
		return nil, documentProcessFailure(request.Command)
	}
	root, pathProblem := openRoot(request.DataRoot)
	if pathProblem != nil || root == nil {
		if root != nil {
			_ = root.Close()
		}
		return nil, documentmigration.CodePathInvalid
	}
	defer func() {
		if root.Close() != nil && code == "" {
			record, code = nil, documentProcessFailure(request.Command)
		}
	}()
	if claimProblem := root.ClaimForProcess(); claimProblem != nil {
		return nil, documentProcessFailure(request.Command)
	}
	if admissionCode := admitDocumentRecoveryKind(root, request.Command); admissionCode != "" {
		return nil, admissionCode
	}

	var backend keys.Backend
	if request.Command != protocol.DocumentMigrationPlanCommand {
		if backendFactory == nil {
			return nil, documentProcessFailure(request.Command)
		}
		var err error
		backend, err = backendFactory()
		if err != nil || backend == nil {
			return nil, documentProcessFailure(request.Command)
		}
	}
	return executeDocumentMigrationOwned(ctx, request, root, backend)
}

func admitDocumentRecoveryKind(root documentRecoveryInventory, command string) string {
	if root == nil || root.RequireProcessOwnership() != nil {
		return documentProcessFailure(command)
	}
	paths := [...]string{
		datahome.DocumentRecoveryPath,
		datahome.RestoreRecoveryPath,
		datahome.RegistryRecoveryPath,
		datahome.EvidenceRecoveryPath,
		datahome.DiscoveryRecoveryPath,
		datahome.ResumeRecoveryPath,
		datahome.IntakeRecoveryPath,
		datahome.NewJobRecoveryPath,
		datahome.EmailCredentialRecoveryPath,
		datahome.ApprovalRecoveryPath,
	}
	presentCount := 0
	documentPresent := false
	for index, path := range paths {
		present, problem := root.Exists(path)
		if problem != nil {
			return documentProcessFailure(command)
		}
		if present {
			presentCount++
			documentPresent = documentPresent || index == 0
		}
	}
	if command == protocol.DocumentMigrationRecoverCommand {
		if presentCount == 0 || presentCount == 1 && documentPresent {
			return ""
		}
		return documentmigration.CodeRecoveryFailed
	}
	if presentCount == 0 {
		return ""
	}
	if presentCount == 1 && documentPresent {
		return documentmigration.CodeRecoveryRequired
	}
	return documentmigration.CodeDataHomeInvalid
}

func documentProcessFailure(command string) string {
	if command == protocol.DocumentMigrationRecoverCommand {
		return documentmigration.CodeRecoveryFailed
	}
	return documentmigration.CodeApplyFailed
}
