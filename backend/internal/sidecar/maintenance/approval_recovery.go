package maintenance

import (
	"context"
	"errors"
	"time"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/email/emaildiagnostics"
	"careerhub.local/backend/internal/intake/sourceupload"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/startup"
)

type executeApprovalRecovery func(context.Context, protocol.StartRecord) (*applicationwrite.Outcome, error)

func executeApprovalRecoveryPayload(ctx context.Context, payload map[string]any, execute executeApprovalRecovery) (map[string]any, string, int) {
	start, err := protocol.ParseApprovalRecoveryMaintenance(payload, protocol.PathOptions{})
	if err != nil {
		return nil, requestErrorCode(err), 3
	}
	if ctx == nil || execute == nil {
		return nil, "SIDECAR_MAINTENANCE_FAILED", 4
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := execute(ctx, start)
	code := ""
	if err != nil {
		code = "APPROVAL_UNAVAILABLE"
		var admission *startup.Error
		var problem *applicationwrite.Error
		if errors.As(err, &admission) && allowedStartupCode(admission.Code) {
			code = admission.Code
		} else if errors.As(err, &problem) && sourceupload.ValidError(problem.Code) {
			code = problem.Code
		}
	}
	if out == nil {
		if code == "" {
			code = "APPROVAL_UNAVAILABLE"
		}
		return nil, code, 4
	}
	if code == "" && (!out.ApprovalWrite.Completed || out.RecoveryRequired || out.CleanupFailed) {
		code = "APPROVAL_COMMITTED_UNVERIFIED"
	}
	runID := out.RunID
	if !emaildiagnostics.ValidRunID(runID) {
		runID = ""
	}
	// No raw source IDs, paths, result, target filenames or caller error strings.
	// A failed replay still returns known publication facts and a nonzero exit.
	record := map[string]any{"event": "resume_approval_recovered", "protocol_version": protocol.Version,
		"success": code == "", "committed": out.Committed, "commit_attempted": out.CommitAttempted,
		"files_complete": out.ApprovalWrite.FilesComplete, "recovery_required": out.RecoveryRequired,
		"cleanup_failed": out.CleanupFailed, "run_id": runID, "error_code": code}
	if code != "" {
		return record, code, 4
	}
	return record, "", 0
}
