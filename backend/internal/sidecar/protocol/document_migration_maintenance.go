package protocol

const (
	DocumentMigrationPlanCommand    = "document_migration_plan"
	DocumentMigrationApplyCommand   = "document_migration_apply"
	DocumentMigrationRecoverCommand = "document_migration_recover"
	DocumentMigrationCompleteEvent  = "document_migration_complete"
	DocumentMigrationRecoveredEvent = "document_migration_recovered"
)

// DocumentMigrationMaintenanceRequest matches the installed Python one-shot
// maintenance request. Path admission here is lexical; the future process
// owner must still open and claim the exact managed root.
type DocumentMigrationMaintenanceRequest struct {
	Command, DataRoot, ConfirmPlan string
}

func ParseDocumentMigrationMaintenance(payload map[string]any, paths PathOptions) (DocumentMigrationMaintenanceRequest, error) {
	var request DocumentMigrationMaintenanceRequest
	command, ok := payload["command"].(string)
	if !ok || (command != DocumentMigrationPlanCommand && command != DocumentMigrationApplyCommand &&
		command != DocumentMigrationRecoverCommand) {
		return request, failure("SIDECAR_COMMAND_UNSUPPORTED")
	}
	fields := []string{"command", "protocol_version", "data_root"}
	if command == DocumentMigrationApplyCommand {
		fields = append(fields, "confirm_plan")
	}
	if !exactFields(payload, fields...) {
		return request, failure("SIDECAR_RECORD_INVALID_FIELDS")
	}
	if !integerEquals(payload["protocol_version"], Version) {
		return request, failure("SIDECAR_PROTOCOL_UNSUPPORTED")
	}
	confirmPlan := ""
	if command == DocumentMigrationApplyCommand {
		confirmPlan, ok = payload["confirm_plan"].(string)
		if !ok || !canonicalRecoveryPlanID(confirmPlan) {
			return request, failure("SIDECAR_MAINTENANCE_CONFIRMATION_INVALID")
		}
	}
	dataRoot, err := absolutePath(payload["data_root"], paths)
	if err != nil {
		return request, err
	}
	return DocumentMigrationMaintenanceRequest{Command: command, DataRoot: dataRoot, ConfirmPlan: confirmPlan}, nil
}

type DocumentMigrationPlanOutput struct {
	PlanID                                   string
	SourceFormatVersion, TargetFormatVersion int
	DocumentCount, CiphertextFileCount       int
	TotalCiphertextBytes                     int
}

func DocumentMigrationPlanRecord(plan DocumentMigrationPlanOutput) (map[string]any, error) {
	if !validDocumentMigrationPlan(plan) {
		return nil, failure("SIDECAR_OUTPUT_INVALID")
	}
	return map[string]any{
		"event": DocumentMigrationPlanCommand, "protocol_version": Version,
		"plan_id": plan.PlanID, "source_format_version": plan.SourceFormatVersion,
		"target_format_version": plan.TargetFormatVersion, "document_count": plan.DocumentCount,
		"ciphertext_file_count": plan.CiphertextFileCount, "total_ciphertext_bytes": plan.TotalCiphertextBytes,
	}, nil
}

func DocumentMigrationCompleteRecord(planID string, documentCount int) (map[string]any, error) {
	if !canonicalRecoveryPlanID(planID) || documentCount < 0 {
		return nil, failure("SIDECAR_OUTPUT_INVALID")
	}
	return map[string]any{
		"event":            DocumentMigrationCompleteEvent,
		"protocol_version": Version, "plan_id": planID, "document_count": documentCount,
	}, nil
}

func DocumentMigrationRecoveredRecord(recovered bool) map[string]any {
	return map[string]any{
		"event": DocumentMigrationRecoveredEvent, "protocol_version": Version, "recovered": recovered,
	}
}

func validDocumentMigrationPlan(plan DocumentMigrationPlanOutput) bool {
	return canonicalRecoveryPlanID(plan.PlanID) && plan.SourceFormatVersion == 1 && plan.TargetFormatVersion == 2 &&
		plan.DocumentCount >= 0 && plan.CiphertextFileCount >= 0 && plan.TotalCiphertextBytes >= 0 &&
		plan.CiphertextFileCount >= plan.DocumentCount
}
