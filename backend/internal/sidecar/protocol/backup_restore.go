package protocol

const (
	BackupRestoreCommand        = "backup_restore"
	BackupRestoreRecoverCommand = "backup_restore_recover"
)

type BackupRestoreRequest struct {
	Command               string
	Start                 StartRecord
	ConfirmWritersStopped bool
	ArchiveRelativePath   string
}

// Recovery settles only the already-present scoped intent. It cannot choose an
// archive, roots, Home identity, secret target, resume phase or external path.
func ParseBackupRestoreMaintenance(payload map[string]any, paths PathOptions) (BackupRestoreRequest, error) {
	var request BackupRestoreRequest
	command, ok := payload["command"].(string)
	if !ok || command != BackupRestoreCommand && command != BackupRestoreRecoverCommand {
		return request, failure("SIDECAR_COMMAND_UNSUPPORTED")
	}
	fields := []string{"command", "protocol_version", "data_root", "resource_root", "channel", "confirm_writers_stopped"}
	if command == BackupRestoreCommand {
		fields = append(fields, "archive_relative_path")
	}
	if !exactFields(payload, fields...) {
		return request, failure("SIDECAR_RECORD_INVALID_FIELDS")
	}
	base := map[string]any{"confirm": payload["confirm_writers_stopped"]}
	for _, field := range fields[:5] {
		base[field] = payload[field]
	}
	start, err := parseConfirmedRecoveryMaintenance(base, paths, command)
	if err != nil {
		return request, err
	}
	request = BackupRestoreRequest{Command: command, Start: start, ConfirmWritersStopped: true}
	if command == BackupRestoreCommand {
		value, ok := payload["archive_relative_path"].(string)
		if !ok || !ValidBackupArchivePath(value) {
			return BackupRestoreRequest{}, failure("SIDECAR_BACKUP_SOURCE_INVALID")
		}
		request.ArchiveRelativePath = value
	}
	return request, nil
}

func ValidRestoreID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func ValidRestoreStagePath(value, id string) bool {
	return ValidRestoreID(id) && value == ".career_hub_staging/restores/"+id
}
func ValidRestoreRecoveryPath(value, id string) bool {
	return ValidRestoreID(id) && value == "backups/restore-recovery/restore_"+id
}
