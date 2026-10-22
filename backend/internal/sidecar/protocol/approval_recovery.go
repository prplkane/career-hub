package protocol

const ApprovalRecoveryCommand = "resume_approval_recover"

// Explicit recovery confirms completion of an already approved, pending intent;
// it cannot create a new approval or select arbitrary files to replay.
func ParseApprovalRecoveryMaintenance(payload map[string]any, paths PathOptions) (StartRecord, error) {
	return parseConfirmedRecoveryMaintenance(payload, paths, ApprovalRecoveryCommand)
}

func parseConfirmedRecoveryMaintenance(payload map[string]any, paths PathOptions, command string) (StartRecord, error) {
	var start StartRecord
	if payload["command"] != command {
		return start, failure("SIDECAR_COMMAND_UNSUPPORTED")
	}
	if !exactFields(payload, "command", "protocol_version", "data_root", "resource_root", "channel", "confirm") {
		return start, failure("SIDECAR_RECORD_INVALID_FIELDS")
	}
	if !integerEquals(payload["protocol_version"], Version) {
		return start, failure("SIDECAR_PROTOCOL_UNSUPPORTED")
	}
	if confirmed, ok := payload["confirm"].(bool); !ok || !confirmed {
		return start, failure("SIDECAR_MAINTENANCE_CONFIRMATION_INVALID")
	}
	channel, ok := payload["channel"].(string)
	if !ok || !oneOf(channel, "dev", "stable") {
		return start, failure("SIDECAR_START_INVALID_CHANNEL")
	}
	resources, err := absolutePath(payload["resource_root"], paths)
	if err != nil {
		return start, err
	}
	data, err := absolutePath(payload["data_root"], paths)
	if err != nil {
		return start, err
	}
	return StartRecord{ResourceRoot: resources, DataRoot: data, Channel: channel}, nil
}
