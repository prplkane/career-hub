package protocol

import (
	"reflect"
	"strings"
	"testing"
)

func documentMigrationPayload(command string) map[string]any {
	payload := map[string]any{
		"command": command, "protocol_version": 1, "data_root": `C:\CareerHub\dev`,
	}
	if command == DocumentMigrationApplyCommand {
		payload["confirm_plan"] = "sha256:" + strings.Repeat("a", 64)
	}
	return payload
}

func TestDocumentMigrationMaintenanceValidation(t *testing.T) {
	t.Parallel()
	for _, command := range []string{DocumentMigrationPlanCommand, DocumentMigrationApplyCommand, DocumentMigrationRecoverCommand} {
		request, err := ParseDocumentMigrationMaintenance(documentMigrationPayload(command), PathOptions{Style: Windows})
		requireCode(t, err, "")
		if request.Command != command || request.DataRoot != `C:\CareerHub\dev` ||
			command == DocumentMigrationApplyCommand && request.ConfirmPlan == "" ||
			command != DocumentMigrationApplyCommand && request.ConfirmPlan != "" {
			t.Fatalf("wrong parsed request: %#v", request)
		}
	}

	tests := []struct {
		name, command, field string
		value                any
		code                 string
	}{
		{"unknown command", "other", "", nil, "SIDECAR_COMMAND_UNSUPPORTED"},
		{"missing field", DocumentMigrationPlanCommand, "delete:data_root", nil, "SIDECAR_RECORD_INVALID_FIELDS"},
		{"extra field", DocumentMigrationRecoverCommand, "extra", true, "SIDECAR_RECORD_INVALID_FIELDS"},
		{"version", DocumentMigrationPlanCommand, "protocol_version", 2, "SIDECAR_PROTOCOL_UNSUPPORTED"},
		{"relative root", DocumentMigrationRecoverCommand, "data_root", "relative", "SIDECAR_START_INVALID_PATH"},
		{"missing confirmation", DocumentMigrationApplyCommand, "delete:confirm_plan", nil, "SIDECAR_RECORD_INVALID_FIELDS"},
		{"bad confirmation before path", DocumentMigrationApplyCommand, "confirm_plan", "bad", "SIDECAR_MAINTENANCE_CONFIRMATION_INVALID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload := documentMigrationPayload(test.command)
			if test.name == "bad confirmation before path" {
				payload["data_root"] = "relative"
			}
			if strings.HasPrefix(test.field, "delete:") {
				delete(payload, strings.TrimPrefix(test.field, "delete:"))
			} else if test.field != "" {
				payload[test.field] = test.value
			}
			_, err := ParseDocumentMigrationMaintenance(payload, PathOptions{Style: Windows})
			requireCode(t, err, test.code)
		})
	}
}

func TestDocumentMigrationMaintenanceRecordsMatchInstalledShape(t *testing.T) {
	t.Parallel()
	planID := "sha256:" + strings.Repeat("b", 64)
	plan := DocumentMigrationPlanOutput{PlanID: planID, SourceFormatVersion: 1, TargetFormatVersion: 2,
		DocumentCount: 2, CiphertextFileCount: 3, TotalCiphertextBytes: 512}
	record, err := DocumentMigrationPlanRecord(plan)
	requireCode(t, err, "")
	want := map[string]any{
		"event": DocumentMigrationPlanCommand, "protocol_version": Version, "plan_id": planID,
		"source_format_version": 1, "target_format_version": 2, "document_count": 2,
		"ciphertext_file_count": 3, "total_ciphertext_bytes": 512,
	}
	if !reflect.DeepEqual(record, want) {
		t.Fatalf("wrong plan record: %#v", record)
	}
	complete, err := DocumentMigrationCompleteRecord(planID, 2)
	requireCode(t, err, "")
	if !reflect.DeepEqual(complete, map[string]any{
		"event": DocumentMigrationCompleteEvent, "protocol_version": Version, "plan_id": planID, "document_count": 2,
	}) {
		t.Fatalf("wrong completion record: %#v", complete)
	}
	if recovered := DocumentMigrationRecoveredRecord(true); !reflect.DeepEqual(recovered, map[string]any{
		"event": DocumentMigrationRecoveredEvent, "protocol_version": Version, "recovered": true,
	}) {
		t.Fatalf("wrong recovery record: %#v", recovered)
	}

	encoded, encodeErr := EncodeRecord(complete)
	wantBytes := `{"document_count":2,"event":"document_migration_complete","plan_id":"` + planID + `","protocol_version":1}` + "\n"
	if encodeErr != nil || string(encoded) != wantBytes {
		t.Fatalf("completion bytes drifted: %q / %v", encoded, encodeErr)
	}
	for _, invalid := range []DocumentMigrationPlanOutput{
		{},
		{PlanID: planID, SourceFormatVersion: 2, TargetFormatVersion: 2},
		{PlanID: planID, SourceFormatVersion: 1, TargetFormatVersion: 2, DocumentCount: 2, CiphertextFileCount: 1},
	} {
		if record, problem := DocumentMigrationPlanRecord(invalid); record != nil || problem == nil {
			t.Fatalf("invalid plan became output: %#v / %v", record, problem)
		}
	}
}
