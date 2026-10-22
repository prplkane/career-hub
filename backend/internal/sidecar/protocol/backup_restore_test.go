package protocol

import (
	"strings"
	"testing"
)

func TestBackupRestoreMaintenanceValidation(t *testing.T) {
	for _, command := range []string{BackupRestoreCommand, BackupRestoreRecoverCommand} {
		base := func() map[string]any {
			p := recoveryMaintenancePayload(command)
			p["confirm_writers_stopped"] = true
			if command == BackupRestoreCommand {
				p["archive_relative_path"] = "backups/input.zip"
			}
			return p
		}
		got, err := ParseBackupRestoreMaintenance(base(), PathOptions{Style: Windows})
		requireCode(t, err, "")
		if got.Command != command || !got.ConfirmWritersStopped || got.Start.DataRoot != `C:\data` || got.Start.Port != 0 {
			t.Fatal(got)
		}
		for _, tc := range []struct {
			field string
			value any
			code  string
		}{
			{"command", "restore", "SIDECAR_COMMAND_UNSUPPORTED"}, {"confirm_writers_stopped", false, "SIDECAR_MAINTENANCE_CONFIRMATION_INVALID"}, {"confirm_writers_stopped", "true", "SIDECAR_MAINTENANCE_CONFIRMATION_INVALID"},
			{"protocol_version", true, "SIDECAR_PROTOCOL_UNSUPPORTED"}, {"channel", "nightly", "SIDECAR_START_INVALID_CHANNEL"}, {"data_root", "relative", "SIDECAR_START_INVALID_PATH"},
			{"resource_root", "relative", "SIDECAR_START_INVALID_PATH"}, {"password", "private", "SIDECAR_RECORD_INVALID_FIELDS"}, {"include_roots", []any{"jobs"}, "SIDECAR_RECORD_INVALID_FIELDS"}, {"port", 8766, "SIDECAR_RECORD_INVALID_FIELDS"},
		} {
			p := base()
			p[tc.field] = tc.value
			_, err := ParseBackupRestoreMaintenance(p, PathOptions{Style: Windows})
			requireCode(t, err, tc.code)
		}
		for field := range base() {
			p := base()
			delete(p, field)
			_, err := ParseBackupRestoreMaintenance(p, PathOptions{Style: Windows})
			if err == nil {
				t.Fatal("missing field accepted", command, field)
			}
		}
		for _, path := range []any{nil, "", `C:\private\file.zip`, "backups/../file.zip", "backups/file.zip\x00", "backups/file.txt", "backups/input.zip"} {
			p := base()
			p["archive_relative_path"] = path
			_, err := ParseBackupRestoreMaintenance(p, PathOptions{Style: Windows})
			want := "SIDECAR_BACKUP_SOURCE_INVALID"
			if command == BackupRestoreRecoverCommand {
				want = "SIDECAR_RECORD_INVALID_FIELDS"
			} else if path == "backups/input.zip" {
				want = ""
			}
			requireCode(t, err, want)
		}
	}
	id := strings.Repeat("a", 32)
	if !ValidRestoreStagePath(".career_hub_staging/restores/"+id, id) || !ValidRestoreRecoveryPath("backups/restore-recovery/restore_"+id, id) {
		t.Fatal("canonical paths refused")
	}
	for _, bad := range []string{"", strings.Repeat("A", 32), strings.Repeat("a", 31), "../" + strings.Repeat("a", 29)} {
		if ValidRestoreID(bad) || ValidRestoreStagePath(".career_hub_staging/restores/"+bad, bad) || ValidRestoreRecoveryPath("backups/restore-recovery/restore_"+bad, bad) {
			t.Fatal(bad)
		}
	}
}
