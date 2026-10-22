package protocol

import "testing"

func TestBackupCreateMaintenanceValidation(t *testing.T) {
	base := func() map[string]any {
		p := recoveryMaintenancePayload(BackupCreateCommand)
		p["confirm_writers_stopped"] = true
		return p
	}
	got, err := ParseBackupCreateMaintenance(base(), PathOptions{Style: Windows})
	requireCode(t, err, "")
	if got.Start.DataRoot != `C:\data` || got.Start.ResourceRoot != `C:\resources` || got.Start.Channel != "dev" || got.Start.Port != 0 || !got.ConfirmWritersStopped || got.IncludeRoots != nil || got.ArchiveRelativePath != "" {
		t.Fatal(got)
	}
	for _, tc := range []struct {
		field string
		value any
		code  string
	}{
		{"command", "restore", "SIDECAR_COMMAND_UNSUPPORTED"},
		{"confirm_writers_stopped", false, "SIDECAR_MAINTENANCE_CONFIRMATION_INVALID"},
		{"confirm_writers_stopped", "true", "SIDECAR_MAINTENANCE_CONFIRMATION_INVALID"},
		{"confirm", true, "SIDECAR_RECORD_INVALID_FIELDS"},
		{"password", "private", "SIDECAR_RECORD_INVALID_FIELDS"},
		{"port", 8766, "SIDECAR_RECORD_INVALID_FIELDS"},
		{"protocol_version", true, "SIDECAR_PROTOCOL_UNSUPPORTED"},
		{"data_root", "relative", "SIDECAR_START_INVALID_PATH"},
		{"resource_root", "relative", "SIDECAR_START_INVALID_PATH"},
		{"channel", "nightly", "SIDECAR_START_INVALID_CHANNEL"},
		{"archive_relative_path", nil, "SIDECAR_BACKUP_DESTINATION_INVALID"},
		{"archive_relative_path", "", "SIDECAR_BACKUP_DESTINATION_INVALID"},
		{"archive_relative_path", "backups/../x.zip", "SIDECAR_BACKUP_DESTINATION_INVALID"},
		{"archive_relative_path", "C:\\outside.zip", "SIDECAR_BACKUP_DESTINATION_INVALID"},
		{"include_roots", nil, "SIDECAR_BACKUP_SELECTION_INVALID"},
		{"include_roots", []any{}, "SIDECAR_BACKUP_SELECTION_INVALID"},
		{"include_roots", []any{"jobs", "jobs"}, "SIDECAR_BACKUP_SELECTION_INVALID"},
		{"include_roots", []any{"secrets"}, "SIDECAR_BACKUP_SELECTION_INVALID"},
		{"include_roots", []any{true}, "SIDECAR_BACKUP_SELECTION_INVALID"},
	} {
		p := base()
		p[tc.field] = tc.value
		_, err := ParseBackupCreateMaintenance(p, PathOptions{Style: Windows})
		requireCode(t, err, tc.code)
	}
	p := base()
	delete(p, "confirm_writers_stopped")
	_, err = ParseBackupCreateMaintenance(p, PathOptions{Style: Windows})
	requireCode(t, err, "SIDECAR_RECORD_INVALID_FIELDS")
	p = base()
	p["include_roots"] = []any{"logs", "jobs"}
	p["archive_relative_path"] = "backups/nested/selected.zip"
	got, err = ParseBackupCreateMaintenance(p, PathOptions{Style: Windows})
	requireCode(t, err, "")
	p["include_roots"].([]any)[0] = "secrets"
	if len(got.IncludeRoots) != 2 || got.IncludeRoots[0] != "logs" || got.IncludeRoots[1] != "jobs" || got.ArchiveRelativePath != "backups/nested/selected.zip" {
		t.Fatal(got)
	}
}
