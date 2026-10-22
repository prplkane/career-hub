package protocol

import (
	"strings"
	"unicode/utf8"
)

const BackupCreateCommand = "backup_create"

type BackupCreateRequest struct {
	Start                 StartRecord
	ConfirmWritersStopped bool
	ArchiveRelativePath   string
	IncludeRoots          []string
}

// This record supplies no credentials, listener, restore authority or bypass.
// Confirmation states a maintenance precondition; it does not prove quiescence.
func ParseBackupCreateMaintenance(payload map[string]any, paths PathOptions) (BackupCreateRequest, error) {
	var request BackupCreateRequest
	if payload["command"] != BackupCreateCommand {
		return request, failure("SIDECAR_COMMAND_UNSUPPORTED")
	}
	fields := []string{"command", "protocol_version", "data_root", "resource_root", "channel", "confirm_writers_stopped"}
	for _, field := range []string{"archive_relative_path", "include_roots"} {
		if _, present := payload[field]; present {
			fields = append(fields, field)
		}
	}
	if !exactFields(payload, fields...) {
		return request, failure("SIDECAR_RECORD_INVALID_FIELDS")
	}
	base := map[string]any{"confirm": payload["confirm_writers_stopped"]}
	for _, field := range fields[:5] {
		base[field] = payload[field]
	}
	start, err := parseConfirmedRecoveryMaintenance(base, paths, BackupCreateCommand)
	if err != nil {
		return request, err
	}
	request.Start, request.ConfirmWritersStopped = start, true
	if raw, present := payload["archive_relative_path"]; present {
		value, ok := raw.(string)
		if !ok || !ValidBackupArchivePath(value) {
			return BackupCreateRequest{}, failure("SIDECAR_BACKUP_DESTINATION_INVALID")
		}
		request.ArchiveRelativePath = value
	}
	if raw, present := payload["include_roots"]; present {
		values, ok := raw.([]any)
		if !ok || len(values) == 0 || len(values) > 8 {
			return BackupCreateRequest{}, failure("SIDECAR_BACKUP_SELECTION_INVALID")
		}
		seen := map[string]bool{}
		for _, raw := range values {
			value, ok := raw.(string)
			if !ok || seen[value] || !oneOf(value, "applications", "career_vault", "documents", "jobs", "outputs", "resume_versions", "templates", "logs") {
				return BackupCreateRequest{}, failure("SIDECAR_BACKUP_SELECTION_INVALID")
			}
			seen[value] = true
			request.IncludeRoots = append(request.IncludeRoots, value)
		}
	}
	return request, nil
}

// Lexical protocol bound only. The owner independently enforces native path,
// link/alias/type and no-overwrite checks before and during publication.
func ValidBackupArchivePath(value string) bool {
	if len(value) > 4096 || !utf8.ValidString(value) || !strings.HasPrefix(value, "backups/") || !strings.HasSuffix(value, ".zip") || strings.ContainsAny(value, "\\:\x00") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func ValidBackupSnapshotPath(value string) bool {
	const prefix = ".career_hub_staging/backups/"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+32 {
		return false
	}
	for _, c := range value[len(prefix):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
