package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/storage/homebackup"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func restoreResultFixture() homebackup.RestoreResult {
	r := homebackup.UnstartedRestoreResult()
	r.RestoreID = strings.Repeat("a", 32)
	r.StagingRelativePath = ".career_hub_staging/restores/" + r.RestoreID
	r.RecoveryRelativePath = "backups/restore-recovery/restore_" + r.RestoreID
	for _, phase := range []*homebackup.RestoreValidationPhase{&r.InitialValidation, &r.FinalValidation, &r.ArchiveValidation} {
		phase.SnapshotRelativePath = ".career_hub_staging/backups/" + strings.Repeat("b", 32)
		phase.Preparation.Namespace = runtimepaths.DirectoryCreated
		phase.Summary = &homebackup.PayloadValidation{}
		phase.Cleanup = homebackup.SnapshotCleanupResult{Attempted: true, Complete: true, RemovedDirectories: 1, LastRemoval: runtimepaths.BackupRemoved, SnapshotRemoval: runtimepaths.BackupRemoved}
	}
	r.Extracted.Namespace = runtimepaths.DirectoryCreated
	r.StagedValidation = &homebackup.PayloadValidation{}
	r.Journal.Namespace = runtimepaths.DirectoryCreated
	r.Journal.EvidenceFiles = 4
	r.Journal.LastEvidence = runtimepaths.StreamPublication{Outcome: runtimepaths.FilePublished, CleanupConfirmed: true}
	r.Journal.Journal = r.Journal.LastEvidence
	r.Roots.RootPhaseComplete = true
	r.Finish.SemanticValidated = true
	r.Finish.Receipt = r.Journal.LastEvidence
	r.Finish.JournalRemoval = "removed"
	r.Finish.Complete = true
	return r
}

func TestBackupRestoreProcessFactsAndRedaction(t *testing.T) {
	for _, command := range []string{protocol.BackupRestoreCommand, protocol.BackupRestoreRecoverCommand} {
		for _, mode := range []string{"success", "late-error", "unknown", "panic", "private", "private-code", "unconfirmed", "extra", "invalid", "unsafe-path", "bad-count", "no-validation", "not-removed", "cleanup", "cancel-after"} {
			t.Run(command+"/"+mode, func(t *testing.T) {
				payload := map[string]any{"command": command, "protocol_version": 1, "data_root": fixtureDataRoot, "resource_root": fixtureResourceRoot, "channel": "dev", "confirm_writers_stopped": mode != "unconfirmed"}
				if command == protocol.BackupRestoreCommand {
					payload["archive_relative_path"] = "backups/input.zip"
				}
				if mode == "extra" {
					payload["password"] = "private"
				}
				raw, _ := json.Marshal(payload)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls := 0
				deps := processDependencies{restore: func(ctx context.Context, r protocol.BackupRestoreRequest) (homebackup.RestoreResult, error) {
					calls++
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > 10*time.Minute || r.Command != command {
						t.Fatal("request/deadline changed")
					}
					out := restoreResultFixture()
					switch mode {
					case "late-error":
						return out, &homebackup.Error{Code: "BACKUP_SOURCE_CLOSE_FAILED"}
					case "unknown":
						out.Finish.JournalRemoval = "unknown"
						return out, &homebackup.Error{Code: "BACKUP_RESTORE_COMMIT_UNCONFIRMED"}
					case "panic":
						panic("private native password")
					case "private":
						return out, errors.New("private native password")
					case "private-code":
						return out, &homebackup.Error{Code: "BACKUP_RESTORE_private_password"}
					case "invalid":
						return homebackup.RestoreResult{}, nil
					case "unsafe-path":
						out.FinalValidation.SnapshotRelativePath = `C:\private\stage`
					case "bad-count":
						out.Roots.CompletedMoves = 999
					case "no-validation":
						out.FinalValidation.Summary = nil
					case "not-removed":
						out.Finish.JournalRemoval = "not_removed"
					case "cleanup":
						out.Finish.Receipt.CleanupConfirmed = false
					case "cancel-after":
						cancel()
					}
					return out, nil
				}}
				var output bytes.Buffer
				status := runProcess(ctx, bytes.NewReader(append(raw, '\n')), &output, deps)
				record := decodeOutput(t, &output)
				if strings.Contains(output.String(), "private") {
					t.Fatal("private output", output.String())
				}
				if mode == "unconfirmed" || mode == "extra" {
					if calls != 0 || status != 3 {
						t.Fatal(calls, status)
					}
					return
				}
				if calls != 1 {
					t.Fatal("dispatch repeated", calls)
				}
				event := "backup_restore_result"
				if command == protocol.BackupRestoreRecoverCommand {
					event = "backup_restore_recovery_result"
				}
				if record["event"] != event || len(record) != 20 {
					t.Fatal("open result contract", record)
				}
				if (status == 0) != (mode == "success") || record["success"] != (mode == "success") {
					t.Fatal("false success", record)
				}
				if mode == "late-error" || mode == "private" || mode == "private-code" || mode == "cancel-after" {
					if record["journal_removal"] != "removed" || record["receipt_publication"] != "published" || record["complete"] != true {
						t.Fatal("late committed facts lost", record)
					}
				}
				if mode == "panic" || mode == "invalid" {
					if record["journal_removal"] != "unknown" || record["marker_publication"] != "unknown" || record["receipt_publication"] != "unknown" || record["complete"] != false {
						t.Fatal("unknown concealed", record)
					}
				}
			})
		}
	}
}

func TestBackupRestoreArchiveValidationProof(t *testing.T) {
	for _, mode := range []string{"copy", "no-database", "missing-proof", "false-skipped", "unsafe-path", "partial", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			out := restoreResultFixture()
			code := ""
			switch mode {
			case "no-database", "false-skipped":
				out.ArchiveValidation = homebackup.UnstartedRestoreResult().ArchiveValidation
				out.ArchiveValidation.Summary = &homebackup.PayloadValidation{}
				if mode == "false-skipped" {
					out.ArchiveValidation.Preparation.CopiedFiles = 1
				}
			case "missing-proof":
				out.ArchiveValidation.Summary = nil
			case "unsafe-path":
				out.ArchiveValidation.SnapshotRelativePath = `C:\private`
			case "partial":
				out.ArchiveValidation.Summary = nil
				out.ArchiveValidation.Preparation.CopiedFiles = 2
				out.ArchiveValidation.Preparation.LastFile = runtimepaths.StreamPublication{Outcome: runtimepaths.FilePublished, CleanupConfirmed: true}
				code = "BACKUP_DATABASE_COPY_FAILED"
			case "unknown":
				out = homebackup.RestoreResult{}
				code = "BACKUP_RESTORE_UNCONFIRMED"
			}
			record, _, status := backupRestoreRecord(protocol.BackupRestoreCommand, out, code)
			if (status == 0) != (mode == "copy" || mode == "no-database") {
				t.Fatal(mode, record)
			}
			facts := record["archive_validation"].(map[string]any)
			if mode == "partial" && (facts["copied_files"] != 2 || facts["validated"] != false || facts["namespace"] != "created") {
				t.Fatal("lost partial facts", facts)
			}
			if mode == "unknown" && (facts["namespace"] != "unknown" || facts["last_file_publication"] != "unknown" || facts["temporary_cleanup_confirmed"] != false) {
				t.Fatal("unknown became absent", facts)
			}
		})
	}
}

func TestBackupRestoreLegacyRefusalProjection(t *testing.T) {
	code := backupRestoreCode(&homebackup.Error{Code: "BACKUP_RESTORE_LEGACY_RECOVERY_REQUIRED"})
	if code != "BACKUP_RESTORE_LEGACY_RECOVERY_REQUIRED" {
		t.Fatal("lost explicit legacy refusal", code)
	}
	record, _, status := backupRestoreRecord(protocol.BackupRestoreRecoverCommand, homebackup.UnstartedRestoreResult(), code)
	if status != 4 || record["success"] != false || record["complete"] != false || record["journal_removal"] != "not_removed" || record["error_code"] != code {
		t.Fatal("legacy refusal implied native replay", record)
	}
}

func TestBackupRestoreCleanupPreservesCommit(t *testing.T) {
	for _, command := range []string{protocol.BackupRestoreCommand, protocol.BackupRestoreRecoverCommand} {
		for _, mode := range []string{"unstarted", "unknown", "removed-file", "unknown-file", "removed-root", "invalid-count", "invalid-complete"} {
			t.Run(command+"/"+mode, func(t *testing.T) {
				out := restoreResultFixture()
				phase := &out.FinalValidation
				phase.CleanupFiles = 2
				phase.Cleanup = homebackup.UnstartedRestoreResult().FinalValidation.Cleanup
				want := "incomplete"
				switch mode {
				case "unstarted":
					want = "not_started"
				case "unknown":
					phase.CleanupFiles = 0
					phase.Cleanup = homebackup.SnapshotCleanupResult{}
					want = "unknown"
				case "removed-file":
					phase.Cleanup.Attempted = true
					phase.Cleanup.RemovedFiles = 1
					phase.Cleanup.LastRemoval = runtimepaths.BackupRemoved
				case "unknown-file":
					phase.Cleanup.Attempted = true
					phase.Cleanup.LastRemoval = runtimepaths.BackupRemovalUnknown
				case "removed-root":
					phase.Cleanup.Attempted = true
					phase.Cleanup.RemovedFiles = 2
					phase.Cleanup.RemovedDirectories = 1
					phase.Cleanup.LastRemoval = runtimepaths.BackupRemoved
					phase.Cleanup.SnapshotRemoval = runtimepaths.BackupRemoved
				case "invalid-count":
					phase.Cleanup.RemovedFiles = 3
					want = "unknown"
				case "invalid-complete":
					phase.Cleanup.Complete = true
					want = "unknown"
				}
				record, _, status := backupRestoreRecord(command, out, "")
				if status != 4 || record["success"] != false || record["complete"] != true || record["journal_removal"] != "removed" {
					t.Fatal("cleanup concealed committed restore", record)
				}
				c := record["final_validation"].(map[string]any)["cleanup"].(map[string]any)
				if len(c) != 6 || c["status"] != want {
					t.Fatal("incorrect closed cleanup projection", c)
				}
				if mode == "removed-file" && (c["removed_files"] != 1 || c["last_removal"] != "removed") {
					t.Fatal("lost deletion fact", c)
				}
				if mode == "removed-root" && c["snapshot_removal"] != "removed" {
					t.Fatal("lost root deletion fact", c)
				}
			})
		}
	}
}
