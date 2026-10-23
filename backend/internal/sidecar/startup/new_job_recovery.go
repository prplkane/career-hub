package startup

import (
	"context"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// NewJobRecoveryWorkspace is admitted only while the dedicated process owns
// the exact pending Data Home. It exposes no ordinary workspace reads and does
// not prepare a schema, repair files or advance a phase automatically.
type NewJobRecoveryWorkspace struct {
	owner        *applicationwrite.Service
	resourceRoot string
	start        protocol.StartRecord
}

// OpenNewJobRecoveryForProcess validates the normal deployment/data boundary,
// claims the exact Data Home for this process, and bypasses only the new-job
// recovery sentinel. The process must exit to release ownership.
func OpenNewJobRecoveryForProcess(ctx context.Context, start protocol.StartRecord) (workspace *NewJobRecoveryWorkspace, failure error) {
	owner, err := openRecoveryOwnerForProcess(ctx, start, datahome.RecoveryNewJob)
	if err != nil {
		return nil, err
	}
	return &NewJobRecoveryWorkspace{owner: owner, resourceRoot: start.ResourceRoot, start: start}, nil
}

func openRecoveryOwnerForProcess(ctx context.Context, start protocol.StartRecord, kind datahome.RecoveryKind) (*applicationwrite.Service, error) {
	home, err := admitRecoveryForProcess(ctx, start, kind)
	if err != nil {
		return nil, err
	}
	return applicationwrite.New(start.DataRoot).PinnedToHome(*home).PinnedToResources(start.ResourceRoot), nil
}

// Shares admission only: recovery domains retain their own owners and journals.
func admitRecoveryForProcess(ctx context.Context, start protocol.StartRecord, kind datahome.RecoveryKind) (admitted *datahome.Info, failure error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, fail("DATA_HOME_STARTUP_CANCELLED")
	}
	if err := readonly.Preflight(start); err != nil {
		return nil, workspaceError(err)
	}
	if ctx.Err() != nil {
		return nil, fail("DATA_HOME_STARTUP_CANCELLED")
	}
	root, pathErr := runtimepaths.Open(start.DataRoot)
	if pathErr != nil {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil && failure == nil {
			admitted = nil
			failure = fail("DATA_HOME_OWNERSHIP_UNAVAILABLE")
		}
	}()
	if claimErr := root.ClaimForProcess(); claimErr != nil {
		return nil, recoveryOwnershipError(claimErr)
	}
	if ctx.Err() != nil {
		return nil, fail("DATA_HOME_STARTUP_CANCELLED")
	}
	classification, classificationErr := datahome.ClassifyOwnedRecovery(root, &start.Channel)
	if classificationErr != nil {
		return nil, recoveryAdmissionError(classificationErr)
	}
	if err := requireRecoveryKind(classification, kind); err != nil {
		return nil, err
	}
	inspect := datahome.InspectOwnedForNewJobRecovery
	switch kind {
	case datahome.RecoveryNewJob:
	case datahome.RecoveryApproval:
		inspect = datahome.InspectOwnedForApprovalRecovery
	case datahome.RecoveryEmailCredential:
		inspect = datahome.InspectOwnedForEmailCredentialRecovery
	default:
		return nil, fail("DATA_HOME_RECOVERY_KIND_UNSUPPORTED")
	}
	home, admissionErr := inspect(root, &start.Channel)
	if admissionErr != nil {
		return nil, recoveryAdmissionError(admissionErr)
	}
	if home == nil || !home.Initialized || ctx.Err() != nil {
		if ctx.Err() != nil {
			return nil, fail("DATA_HOME_STARTUP_CANCELLED")
		}
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	if err := readonly.Preflight(start); err != nil {
		return nil, workspaceError(err)
	}
	if ctx.Err() != nil {
		return nil, fail("DATA_HOME_STARTUP_CANCELLED")
	}
	return home, nil
}

func requireNewJobRecovery(classification *datahome.RecoveryClassification) error {
	return requireRecoveryKind(classification, datahome.RecoveryNewJob)
}

func requireRecoveryKind(classification *datahome.RecoveryClassification, kind datahome.RecoveryKind) error {
	if classification == nil {
		return fail("DATA_HOME_UNAVAILABLE")
	}
	if classification.Marker != datahome.RecoveryMarkerInitialized {
		return fail("DATA_HOME_RECOVERY_MARKER_REQUIRED")
	}
	switch classification.Cardinality {
	case datahome.RecoveryNone:
		return fail("DATA_HOME_RECOVERY_NOT_REQUIRED")
	case datahome.RecoveryMultiple:
		return fail("DATA_HOME_RECOVERY_AMBIGUOUS")
	case datahome.RecoverySingle:
		if classification.Kind == kind {
			return nil
		}
		return fail("DATA_HOME_RECOVERY_KIND_UNSUPPORTED")
	default:
		return fail("DATA_HOME_UNAVAILABLE")
	}
}

func (w *NewJobRecoveryWorkspace) Plan(ctx context.Context) (*applicationwrite.NewJobRecoveryPlan, error) {
	if w == nil || w.owner == nil || ctx == nil {
		return nil, fail("JOB_INTAKE_RECOVERY_UNAVAILABLE")
	}
	if err := w.check(ctx); err != nil {
		return nil, err
	}
	plan, problem := w.owner.PlanIncompleteNewJobRecovery(ctx)
	if problem != nil {
		return nil, problem
	}
	return plan, nil
}

func (w *NewJobRecoveryWorkspace) Advance(ctx context.Context, expectedPlanID string,
	month assessmentcontext.Month) (*applicationwrite.NewJobRecoveryStepOutcome, error) {
	if w == nil || w.owner == nil || w.resourceRoot == "" || ctx == nil {
		return nil, fail("JOB_INTAKE_RECOVERY_UNAVAILABLE")
	}
	if err := w.check(ctx); err != nil {
		return nil, err
	}
	outcome, problem := w.owner.RecoverIncompleteNewJobStep(ctx, expectedPlanID, w.resourceRoot, month)
	if problem != nil {
		return outcome, problem
	}
	return outcome, nil
}

func (w *NewJobRecoveryWorkspace) check(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		return fail("DATA_HOME_STARTUP_CANCELLED")
	}
	if err := readonly.Preflight(w.start); err != nil {
		return workspaceError(err)
	}
	if ctx.Err() != nil {
		return fail("DATA_HOME_STARTUP_CANCELLED")
	}
	return nil
}

func recoveryOwnershipError(problem *runtimepaths.Error) error {
	if problem != nil {
		switch problem.Code {
		case "OWNERSHIP_IN_USE":
			return fail("DATA_HOME_IN_USE")
		case "OWNERSHIP_UNSUPPORTED":
			return fail("DATA_HOME_OWNERSHIP_UNSUPPORTED")
		}
	}
	return fail("DATA_HOME_OWNERSHIP_UNAVAILABLE")
}

func recoveryAdmissionError(problem *datahome.Error) error {
	if problem != nil {
		switch problem.Code {
		case "DATA_HOME_UNAVAILABLE", "INVALID_DATA_HOME", "DATA_HOME_NEWER_THAN_APP",
			"DATA_HOME_MIGRATION_REQUIRED", "DATA_HOME_RECOVERY_REQUIRED", "DATA_HOME_CHANNEL_MISMATCH",
			"DATA_HOME_INPUT_UNSUPPORTED", "DATA_HOME_OWNERSHIP_REQUIRED":
			return fail(problem.Code)
		}
	}
	return fail("DATA_HOME_UNAVAILABLE")
}
