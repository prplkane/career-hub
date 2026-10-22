package protocol

import (
	"reflect"
	"strings"
	"testing"
)

func recoveryMaintenancePayload(command string) map[string]any {
	payload := map[string]any{"command": command, "protocol_version": 1, "data_root": `C:\data`,
		"resource_root": `C:\resources`, "channel": "dev"}
	if command == DataHomeRecoveryAdvanceCommand {
		payload["confirm_plan"] = "sha256:" + strings.Repeat("a", 64)
	}
	return payload
}

func TestRecoveryMaintenanceValidation(t *testing.T) {
	t.Parallel()
	for _, command := range []string{DataHomeRecoveryPlanCommand, DataHomeRecoveryAdvanceCommand} {
		request, err := ParseRecoveryMaintenance(recoveryMaintenancePayload(command), PathOptions{Style: Windows})
		requireCode(t, err, "")
		if request.Command != command || request.DataRoot != `C:\data` || request.ResourceRoot != `C:\resources` ||
			request.Channel != "dev" || command == DataHomeRecoveryAdvanceCommand && request.ConfirmPlan == "" ||
			command == DataHomeRecoveryPlanCommand && request.ConfirmPlan != "" {
			t.Fatalf("wrong parsed request: %#v", request)
		}
	}

	tests := []struct {
		name, command, field string
		value                any
		code                 string
	}{
		{"unknown command", "other", "", nil, "SIDECAR_COMMAND_UNSUPPORTED"},
		{"missing field", DataHomeRecoveryPlanCommand, "delete:data_root", nil, "SIDECAR_RECORD_INVALID_FIELDS"},
		{"extra field", DataHomeRecoveryPlanCommand, "extra", true, "SIDECAR_RECORD_INVALID_FIELDS"},
		{"version bool", DataHomeRecoveryPlanCommand, "protocol_version", true, "SIDECAR_PROTOCOL_UNSUPPORTED"},
		{"channel", DataHomeRecoveryPlanCommand, "channel", "nightly", "SIDECAR_START_INVALID_CHANNEL"},
		{"resource relative", DataHomeRecoveryPlanCommand, "resource_root", "relative", "SIDECAR_START_INVALID_PATH"},
		{"data relative", DataHomeRecoveryPlanCommand, "data_root", "relative", "SIDECAR_START_INVALID_PATH"},
		{"advance missing confirmation", DataHomeRecoveryAdvanceCommand, "delete:confirm_plan", nil, "SIDECAR_RECORD_INVALID_FIELDS"},
		{"advance uppercase confirmation", DataHomeRecoveryAdvanceCommand, "confirm_plan", "sha256:" + strings.Repeat("A", 64), "SIDECAR_MAINTENANCE_CONFIRMATION_INVALID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload := recoveryMaintenancePayload(test.command)
			if strings.HasPrefix(test.field, "delete:") {
				delete(payload, strings.TrimPrefix(test.field, "delete:"))
			} else if test.field != "" {
				payload[test.field] = test.value
			}
			_, err := ParseRecoveryMaintenance(payload, PathOptions{Style: Windows})
			requireCode(t, err, test.code)
		})
	}
}

func TestRecoveryMaintenanceRecords(t *testing.T) {
	t.Parallel()
	planID := "sha256:" + strings.Repeat("b", 64)
	plan := NewJobRecoveryPlanOutput{PlanID: planID, JobID: "fictional_job", InitialStatus: "applied",
		FileState: "complete", LedgerState: "assessed", ReceiptState: "missing",
		Disposition: "advance", NextStep: "applied", ReasonCode: "JOB_INTAKE_RECOVERY_STEP_AVAILABLE", CanAdvance: true}
	record, err := DataHomeRecoveryPlanRecord(plan)
	requireCode(t, err, "")
	want := map[string]any{"event": DataHomeRecoveryPlanCommand, "protocol_version": Version, "recovery_kind": "new_job",
		"plan_id": planID, "job_id": "fictional_job", "initial_status": "applied", "file_state": "complete",
		"ledger_state": "assessed", "receipt_state": "missing", "disposition": "advance", "next_step": "applied",
		"reason_code": "JOB_INTAKE_RECOVERY_STEP_AVAILABLE", "can_advance": true}
	if !reflect.DeepEqual(record, want) {
		t.Fatalf("wrong plan record: %#v", record)
	}
	if encoded, encodeErr := EncodeRecord(record); encodeErr != nil || strings.Contains(string(encoded), "operation_id") || strings.Contains(string(encoded), "path") {
		t.Fatalf("plan record was not content-minimized: %q / %v", encoded, encodeErr)
	}

	advanced, err := DataHomeRecoveryAdvancedRecord(planID, "terminal", true)
	requireCode(t, err, "")
	if !reflect.DeepEqual(advanced, map[string]any{"event": "data_home_recovery_advanced", "protocol_version": Version,
		"confirmed_plan": planID, "step": "terminal", "settled": true}) {
		t.Fatalf("wrong advanced record: %#v", advanced)
	}

	invalid := []NewJobRecoveryPlanOutput{
		{},
		func() NewJobRecoveryPlanOutput { value := plan; value.JobID = "../private"; return value }(),
		func() NewJobRecoveryPlanOutput { value := plan; value.Disposition = "manual_review"; return value }(),
		func() NewJobRecoveryPlanOutput { value := plan; value.ReasonCode = "PRIVATE_DETAIL"; return value }(),
	}
	for _, value := range invalid {
		if record, problem := DataHomeRecoveryPlanRecord(value); record != nil || problem == nil {
			t.Fatalf("invalid plan became output: %#v / %v", record, problem)
		}
	}
	for _, test := range []struct {
		plan, step string
		settled    bool
	}{
		{"bad", "terminal", true},
		{planID, "other", false},
		{planID, "assessment", true},
	} {
		if record, problem := DataHomeRecoveryAdvancedRecord(test.plan, test.step, test.settled); record != nil || problem == nil {
			t.Fatalf("invalid advance became output: %#v / %v", record, problem)
		}
	}
}
