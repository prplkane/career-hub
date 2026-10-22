// Package maintenance composes bounded one-shot sidecar maintenance actions.
// It starts no listener and owns no desktop or executable routing policy.
package maintenance

import (
	"context"
	"errors"
	"io"
	"time"

	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/startup"
)

type recoveryWorkspace interface {
	Plan(context.Context) (*applicationwrite.NewJobRecoveryPlan, error)
	Advance(context.Context, string, assessmentcontext.Month) (*applicationwrite.NewJobRecoveryStepOutcome, error)
}

type openRecovery func(context.Context, protocol.StartRecord) (recoveryWorkspace, error)

// RunRecovery preserves the independently callable new-job maintenance core.
// The shared process Run owns real pipes and dispatches an already-read payload.
func RunRecovery(ctx context.Context, input io.Reader, output io.Writer) int {
	return runRecovery(ctx, input, output, time.Now, func(ctx context.Context, start protocol.StartRecord) (recoveryWorkspace, error) {
		return startup.OpenNewJobRecoveryForProcess(ctx, start)
	})
}

func runRecovery(ctx context.Context, input io.Reader, output io.Writer, clock func() time.Time, open openRecovery) int {
	if ctx == nil || input == nil || output == nil || clock == nil || open == nil {
		return emitFailure(output, "SIDECAR_MAINTENANCE_FAILED", 4)
	}
	payload, err := protocol.ReadRecord(input)
	if err != nil {
		return emitFailure(output, requestErrorCode(err), 3)
	}
	record, code, status := executeRecoveryPayload(ctx, payload, clock, open)
	if code != "" {
		return emitFailure(output, code, status)
	}
	return emitSuccess(output, record)
}

// executeRecoveryPayload lets the shared one-shot process dispatch an already
// bounded record without reading a second line. It retains the standalone
// runner's parsing order, owner admission and exact error projection.
func executeRecoveryPayload(ctx context.Context, payload map[string]any, clock func() time.Time,
	open openRecovery) (map[string]any, string, int) {
	if ctx == nil || payload == nil || clock == nil || open == nil {
		return nil, "SIDECAR_MAINTENANCE_FAILED", 4
	}
	request, err := protocol.ParseRecoveryMaintenance(payload, protocol.PathOptions{})
	if err != nil {
		return nil, requestErrorCode(err), 3
	}
	if request.Command == protocol.DataHomeRecoveryInspectCommand {
		return nil, "SIDECAR_COMMAND_UNSUPPORTED", 3
	}
	workspace, err := open(ctx, protocol.StartRecord{Port: 0, ResourceRoot: request.ResourceRoot,
		DataRoot: request.DataRoot, Channel: request.Channel})
	if err != nil {
		return nil, recoveryErrorCode(err), 4
	}
	if workspace == nil {
		return nil, "SIDECAR_MAINTENANCE_FAILED", 4
	}
	if request.Command == protocol.DataHomeRecoveryPlanCommand {
		plan, planErr := workspace.Plan(ctx)
		if planErr != nil {
			return nil, recoveryErrorCode(planErr), 4
		}
		record, recordErr := recoveryPlanRecord(plan)
		if recordErr != nil {
			return nil, "SIDECAR_MAINTENANCE_FAILED", 4
		}
		return record, "", 0
	}

	plan, planErr := workspace.Plan(ctx)
	if planErr != nil {
		return nil, recoveryErrorCode(planErr), 4
	}
	month := assessmentcontext.Month{}
	if plan != nil && plan.PlanID == request.ConfirmPlan && plan.NextStep == "assessment" {
		now := clock().UTC()
		month = assessmentcontext.Month{Year: now.Year(), Number: int(now.Month())}
	}
	outcome, advanceErr := workspace.Advance(ctx, request.ConfirmPlan, month)
	if advanceErr != nil {
		return nil, recoveryErrorCode(advanceErr), 4
	}
	if outcome == nil || outcome.Plan == nil || outcome.Plan.PlanID != request.ConfirmPlan ||
		outcome.Step == "" || outcome.Step != outcome.Plan.NextStep {
		return nil, "SIDECAR_MAINTENANCE_FAILED", 4
	}
	record, recordErr := protocol.DataHomeRecoveryAdvancedRecord(request.ConfirmPlan, outcome.Step, outcome.Step == "terminal")
	if recordErr != nil {
		return nil, "SIDECAR_MAINTENANCE_FAILED", 4
	}
	return record, "", 0
}

func recoveryPlanRecord(plan *applicationwrite.NewJobRecoveryPlan) (map[string]any, error) {
	if plan == nil || plan.Version != 1 {
		return nil, errors.New("invalid recovery plan")
	}
	return protocol.DataHomeRecoveryPlanRecord(protocol.NewJobRecoveryPlanOutput{
		PlanID: plan.PlanID, JobID: plan.JobID, InitialStatus: plan.InitialStatus,
		FileState: plan.FileState, LedgerState: plan.LedgerState, ReceiptState: plan.ReceiptState,
		Disposition: plan.Disposition, NextStep: plan.NextStep, ReasonCode: plan.ReasonCode,
		CanAdvance: plan.CanAdvance,
	})
}

func emitSuccess(output io.Writer, record map[string]any) int {
	if err := protocol.WriteRecord(output, record); err != nil {
		return 6
	}
	return 0
}

func emitFailure(output io.Writer, code string, status int) int {
	if output == nil || protocol.WriteRecord(output, protocol.ErrorRecord(code)) != nil {
		return 6
	}
	return status
}

func requestErrorCode(err error) string {
	var protocolError *protocol.Error
	if errors.As(err, &protocolError) {
		return protocolError.Code
	}
	var compatibility *protocol.CompatibilityError
	if errors.As(err, &compatibility) {
		return "SIDECAR_INPUT_UNSUPPORTED"
	}
	return "SIDECAR_MAINTENANCE_READ_FAILED"
}

func recoveryErrorCode(err error) string {
	var startupError *startup.Error
	if errors.As(err, &startupError) && allowedStartupCode(startupError.Code) {
		return startupError.Code
	}
	var applicationError *applicationwrite.Error
	if errors.As(err, &applicationError) && allowedApplicationCode(applicationError.Code) {
		return applicationError.Code
	}
	return "JOB_INTAKE_RECOVERY_FAILED"
}

func allowedStartupCode(code string) bool {
	switch code {
	case "DATA_HOME_STARTUP_CANCELLED", "SIDECAR_START_INVALID_PORT", "SIDECAR_START_INVALID_CHANNEL",
		"SIDECAR_START_INVALID_PATH", "SIDECAR_PATH_BOUNDARY_INVALID", "SIDECAR_RESOURCE_INVALID",
		"DATA_HOME_UNAVAILABLE", "INVALID_DATA_HOME", "DATA_HOME_NEWER_THAN_APP", "DATA_HOME_MIGRATION_REQUIRED",
		"DATA_HOME_RECOVERY_REQUIRED", "DATA_HOME_CHANNEL_MISMATCH", "DATA_HOME_INPUT_UNSUPPORTED",
		"DATA_HOME_OWNERSHIP_REQUIRED", "DATA_HOME_IN_USE", "DATA_HOME_OWNERSHIP_UNAVAILABLE",
		"DATA_HOME_OWNERSHIP_UNSUPPORTED", "DATA_HOME_RECOVERY_MARKER_REQUIRED",
		"DATA_HOME_RECOVERY_NOT_REQUIRED", "DATA_HOME_RECOVERY_AMBIGUOUS", "DATA_HOME_RECOVERY_KIND_UNSUPPORTED":
		return true
	default:
		return false
	}
}

func allowedApplicationCode(code string) bool {
	switch code {
	case "JOB_INTAKE_RECOVERY_UNAVAILABLE", "JOB_INTAKE_RECOVERY_CLOSE_FAILED",
		"JOB_INTAKE_RECOVERY_INTENT_MISSING", "JOB_INTAKE_RECOVERY_INTENT_UNAVAILABLE",
		"JOB_INTAKE_RECOVERY_SOURCE_UNAVAILABLE", "JOB_INTAKE_RECOVERY_RECEIPT_UNAVAILABLE",
		"JOB_INTAKE_RECOVERY_CHANGED", "JOB_INTAKE_RECOVERY_STATE_INVALID",
		"JOB_INTAKE_RECOVERY_PLAN_INVALID", "JOB_INTAKE_RECOVERY_PLAN_STALE",
		"JOB_INTAKE_RECOVERY_FILES_INCOMPLETE", "JOB_INTAKE_RECOVERY_STATE_CONFLICT",
		"SCHEMA_PREPARATION_REQUIRED":
		return true
	default:
		return false
	}
}
