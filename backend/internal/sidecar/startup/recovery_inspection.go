package startup

import (
	"context"

	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// InspectRecoveryForProcess routes a confirmed loading-page action without
// trying mutation owners in sequence. It reads only marker/sentinel metadata,
// opens no credentials/database and retains process ownership until exit.
func InspectRecoveryForProcess(ctx context.Context, start protocol.StartRecord) (kind datahome.RecoveryKind, failure error) {
	if ctx == nil || ctx.Err() != nil {
		return "", fail("DATA_HOME_STARTUP_CANCELLED")
	}
	if err := readonly.Preflight(start); err != nil {
		return "", workspaceError(err)
	}
	root, problem := runtimepaths.Open(start.DataRoot)
	if problem != nil {
		return "", fail("DATA_HOME_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil && failure == nil {
			kind, failure = "", fail("DATA_HOME_OWNERSHIP_UNAVAILABLE")
		}
	}()
	if problem := root.ClaimForProcess(); problem != nil {
		return "", recoveryOwnershipError(problem)
	}
	classification, admission := datahome.ClassifyOwnedRecovery(root, &start.Channel)
	if admission != nil {
		return "", recoveryAdmissionError(admission)
	}
	if classification == nil {
		return "", fail("DATA_HOME_UNAVAILABLE")
	}
	if err := requireRecoveryKind(classification, classification.Kind); err != nil {
		return "", err
	}
	if ctx.Err() != nil {
		return "", fail("DATA_HOME_STARTUP_CANCELLED")
	}
	switch classification.Kind {
	case datahome.RecoveryNewJob, datahome.RecoveryEmailCredential, datahome.RecoveryRestore:
		return classification.Kind, nil
	default:
		return "", fail("DATA_HOME_RECOVERY_KIND_UNSUPPORTED")
	}
}
