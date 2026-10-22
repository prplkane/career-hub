package protocol

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	DataHomeRecoveryInspectCommand = "data_home_recovery_inspect"
	DataHomeRecoveryPlanCommand    = "data_home_recovery_plan"
	DataHomeRecoveryAdvanceCommand = "data_home_recovery_advance"
)

// RecoveryMaintenanceRequest is a validated one-shot desktop-maintenance
// record. Paths still require native managed-root admission by the runner.
type RecoveryMaintenanceRequest struct {
	Command, DataRoot, ResourceRoot, Channel, ConfirmPlan string
}

func ParseRecoveryMaintenance(payload map[string]any, paths PathOptions) (RecoveryMaintenanceRequest, error) {
	var request RecoveryMaintenanceRequest
	command, ok := payload["command"].(string)
	if !ok || (command != DataHomeRecoveryPlanCommand && command != DataHomeRecoveryAdvanceCommand && command != DataHomeRecoveryInspectCommand) {
		return request, failure("SIDECAR_COMMAND_UNSUPPORTED")
	}
	fields := []string{"command", "protocol_version", "data_root", "resource_root", "channel"}
	if command == DataHomeRecoveryAdvanceCommand {
		fields = append(fields, "confirm_plan")
	}
	if !exactFields(payload, fields...) {
		return request, failure("SIDECAR_RECORD_INVALID_FIELDS")
	}
	if !integerEquals(payload["protocol_version"], Version) {
		return request, failure("SIDECAR_PROTOCOL_UNSUPPORTED")
	}
	channel, ok := payload["channel"].(string)
	if !ok || (channel != "dev" && channel != "stable") {
		return request, failure("SIDECAR_START_INVALID_CHANNEL")
	}
	resourceRoot, err := absolutePath(payload["resource_root"], paths)
	if err != nil {
		return request, err
	}
	dataRoot, err := absolutePath(payload["data_root"], paths)
	if err != nil {
		return request, err
	}
	confirmPlan := ""
	if command == DataHomeRecoveryAdvanceCommand {
		confirmPlan, ok = payload["confirm_plan"].(string)
		if !ok || !canonicalRecoveryPlanID(confirmPlan) {
			return request, failure("SIDECAR_MAINTENANCE_CONFIRMATION_INVALID")
		}
	}
	return RecoveryMaintenanceRequest{Command: command, DataRoot: dataRoot, ResourceRoot: resourceRoot,
		Channel: channel, ConfirmPlan: confirmPlan}, nil
}

// NewJobRecoveryPlanOutput is the content-minimized projection accepted by the
// desktop host. It deliberately omits paths, operation IDs and private hashes.
type NewJobRecoveryPlanOutput struct {
	PlanID, JobID, InitialStatus, FileState, LedgerState, ReceiptState string
	Disposition, NextStep, ReasonCode                                  string
	CanAdvance                                                         bool
}

func DataHomeRecoveryPlanRecord(plan NewJobRecoveryPlanOutput) (map[string]any, error) {
	if !validNewJobRecoveryPlanOutput(plan) {
		return nil, failure("SIDECAR_OUTPUT_INVALID")
	}
	return map[string]any{
		"event": DataHomeRecoveryPlanCommand, "protocol_version": Version, "recovery_kind": "new_job",
		"plan_id": plan.PlanID, "job_id": plan.JobID, "initial_status": plan.InitialStatus,
		"file_state": plan.FileState, "ledger_state": plan.LedgerState, "receipt_state": plan.ReceiptState,
		"disposition": plan.Disposition, "next_step": plan.NextStep, "reason_code": plan.ReasonCode,
		"can_advance": plan.CanAdvance,
	}, nil
}

func DataHomeRecoveryAdvancedRecord(confirmedPlan, step string, settled bool) (map[string]any, error) {
	if !canonicalRecoveryPlanID(confirmedPlan) || !oneOf(step, "ledger", "assessment", "applied", "terminal") || settled && step != "terminal" {
		return nil, failure("SIDECAR_OUTPUT_INVALID")
	}
	return map[string]any{"event": "data_home_recovery_advanced", "protocol_version": Version,
		"confirmed_plan": confirmedPlan, "step": step, "settled": settled}, nil
}

func validNewJobRecoveryPlanOutput(plan NewJobRecoveryPlanOutput) bool {
	if !canonicalRecoveryPlanID(plan.PlanID) || !canonicalRecoveryJobID(plan.JobID) ||
		!oneOf(plan.InitialStatus, "captured", "applied") ||
		!oneOf(plan.FileState, "none", "raw_only", "complete", "conflict") ||
		!oneOf(plan.LedgerState, "not_checked", "database_missing", "job_missing", "captured", "assessed", "applied", "conflict") ||
		!oneOf(plan.ReceiptState, "not_checked", "missing", "exact", "conflict") ||
		!oneOf(plan.Disposition, "advance", "schema_required", "manual_review") {
		return false
	}
	switch plan.Disposition {
	case "advance":
		return plan.CanAdvance && oneOf(plan.NextStep, "ledger", "assessment", "applied", "terminal") &&
			plan.ReasonCode == "JOB_INTAKE_RECOVERY_STEP_AVAILABLE"
	case "schema_required":
		return !plan.CanAdvance && plan.NextStep == "" && plan.ReasonCode == "SCHEMA_PREPARATION_REQUIRED"
	case "manual_review":
		return !plan.CanAdvance && plan.NextStep == "" &&
			oneOf(plan.ReasonCode, "JOB_INTAKE_RECOVERY_FILES_INCOMPLETE", "JOB_INTAKE_RECOVERY_STATE_CONFLICT")
	}
	return false
}

func canonicalRecoveryPlanID(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, r := range value[len("sha256:"):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func canonicalRecoveryJobID(value string) bool {
	if value == "" || len(value) > 200 || !utf8.ValidString(value) || strings.HasPrefix(value, "_") || strings.HasSuffix(value, "_") {
		return false
	}
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' {
			return false
		}
	}
	switch strings.ToUpper(value) {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return false
	}
	return true
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}
