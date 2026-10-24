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
	"careerhub.local/backend/internal/sidecar/startup"
	"careerhub.local/backend/internal/storage/homebackup"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestBackupCreateProcessFactsAndRedaction(t *testing.T) {
	for _, mode := range []string{"success", "empty", "late-error", "unknown", "cleanup", "invalid", "private", "private-code", "admission", "unconfirmed", "panic", "unsafe-path", "bad-count", "extra", "cancel-after"} {
		t.Run(mode, func(t *testing.T) {
			payload := map[string]any{"command": protocol.BackupCreateCommand, "protocol_version": 1, "data_root": fixtureDataRoot, "resource_root": fixtureResourceRoot, "channel": "dev", "confirm_writers_stopped": mode != "unconfirmed", "include_roots": []string{"jobs"}}
			if mode == "extra" {
				payload["password"] = "private"
			}
			raw, _ := json.Marshal(payload)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			deps := processDependencies{backup: func(ctx context.Context, r protocol.BackupCreateRequest) (homebackup.CreationResult, error) {
				calls++
				d, ok := ctx.Deadline()
				if !ok || time.Until(d) > 5*time.Minute || len(r.IncludeRoots) != 1 || r.IncludeRoots[0] != "jobs" {
					t.Fatal("request/deadline changed")
				}
				out := homebackup.CreationResult{SnapshotRelativePath: ".career_hub_staging/backups/" + strings.Repeat("a", 32), SnapshotNamespace: runtimepaths.DirectoryCreated, StagedFiles: 2, ArchiveFiles: 1, ArchiveRelativePath: "backups/created.zip", FullSourceValidation: &homebackup.PayloadValidation{}, LastStagedFile: runtimepaths.StreamPublication{Outcome: runtimepaths.FilePublished, CleanupConfirmed: true}, ArchivePublication: runtimepaths.StreamPublication{Outcome: runtimepaths.FilePublished, CleanupConfirmed: true}}
				out.SnapshotCleanup = homebackup.SnapshotCleanupResult{Attempted: true, RemovedFiles: 2, RemovedDirectories: 1, LastRemoval: runtimepaths.BackupRemoved, SnapshotRemoval: runtimepaths.BackupRemoved, Complete: true}
				switch mode {
				case "empty":
					out.StagedFiles, out.ArchiveFiles = 0, 0
					out.LastStagedFile.Outcome = runtimepaths.FileNotPublished
					out.SnapshotCleanup.RemovedFiles = 0
				case "late-error":
					return out, &homebackup.Error{Code: "BACKUP_SOURCE_CLOSE_FAILED"}
				case "unknown":
					out.SnapshotCleanup = homebackup.SnapshotCleanupResult{LastRemoval: runtimepaths.BackupNotRemoved, SnapshotRemoval: runtimepaths.BackupNotRemoved}
					out.ArchivePublication = runtimepaths.StreamPublication{Outcome: runtimepaths.FilePublicationUnknown}
					return out, &homebackup.Error{Code: "BACKUP_PUBLICATION_FAILED"}
				case "cleanup":
					out.ArchivePublication.CleanupConfirmed = false
				case "invalid":
					return homebackup.CreationResult{}, nil
				case "private":
					return out, errors.New("private password native path")
				case "private-code":
					return out, &homebackup.Error{Code: "BACKUP_private_password"}
				case "admission":
					return homebackup.CreationResult{SnapshotNamespace: runtimepaths.DirectoryNotCreated, ArchivePublication: runtimepaths.StreamPublication{Outcome: runtimepaths.FileNotPublished, CleanupConfirmed: true}}, &startup.Error{Code: "DATA_HOME_IN_USE"}
				case "panic":
					panic("private password")
				case "unsafe-path":
					out.ArchiveRelativePath = "C:\\private\\archive.zip"
				case "bad-count":
					out.ArchiveFiles = -1
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
			if calls != 1 || record["event"] != "backup_create_result" {
				t.Fatal("dispatch/retry changed", record)
			}
			success := mode == "success" || mode == "empty"
			if (status == 0) != success || record["success"] != success {
				t.Fatal("false success", record)
			}
			if mode == "late-error" || mode == "cleanup" || mode == "private" || mode == "private-code" || mode == "cancel-after" {
				if record["archive_publication"] != "published" {
					t.Fatal("publication facts lost", record)
				}
			}
			if mode == "unknown" || mode == "panic" || mode == "invalid" {
				if record["archive_publication"] != "unknown" || record["archive_temporary_cleanup_confirmed"] != false {
					t.Fatal("unknown outcome concealed", record)
				}
			}
			if mode == "admission" && (record["error_code"] != "DATA_HOME_IN_USE" || record["archive_publication"] != "not_published") {
				t.Fatal(record)
			}
		})
	}
}

func TestBackupCreateCleanupProjection(t *testing.T) {
	for _, mode := range []string{"complete", "partial", "removed-canceled", "not-started", "unknown", "negative", "excess", "unpublished", "invalid-complete"} {
		t.Run(mode, func(t *testing.T) {
			out := homebackup.CreationResult{SnapshotNamespace: runtimepaths.DirectoryCreated,
				SnapshotRelativePath: ".career_hub_staging/backups/" + strings.Repeat("a", 32),
				StagedFiles:          2, ArchiveFiles: 1, ArchiveRelativePath: "backups/test.zip", FullSourceValidation: &homebackup.PayloadValidation{},
				LastStagedFile:     runtimepaths.StreamPublication{Outcome: runtimepaths.FilePublished, CleanupConfirmed: true},
				ArchivePublication: runtimepaths.StreamPublication{Outcome: runtimepaths.FilePublished, CleanupConfirmed: true},
				SnapshotCleanup: homebackup.SnapshotCleanupResult{Attempted: true, RemovedFiles: 2, RemovedDirectories: 3,
					LastRemoval: runtimepaths.BackupRemoved, SnapshotRemoval: runtimepaths.BackupRemoved, Complete: true}}
			code, wantStatus := "BACKUP_CLEANUP_UNCONFIRMED", "unknown"
			switch mode {
			case "complete":
				code, wantStatus = "", "complete"
			case "partial":
				out.SnapshotCleanup = homebackup.SnapshotCleanupResult{Attempted: true, RemovedFiles: 1, LastRemoval: runtimepaths.BackupRemovalUnknown, SnapshotRemoval: runtimepaths.BackupNotRemoved}
				wantStatus = "incomplete"
			case "removed-canceled":
				out.SnapshotCleanup.Complete = false
				code, wantStatus = "BACKUP_CANCELED", "incomplete"
			case "not-started":
				out.SnapshotCleanup = homebackup.SnapshotCleanupResult{LastRemoval: runtimepaths.BackupNotRemoved, SnapshotRemoval: runtimepaths.BackupNotRemoved}
				wantStatus = "not_started"
			case "unknown":
				out.SnapshotCleanup = homebackup.SnapshotCleanupResult{}
			case "negative":
				out.SnapshotCleanup.RemovedFiles = -1
			case "excess":
				out.SnapshotCleanup.RemovedDirectories = 50010
			case "unpublished":
				out.ArchivePublication.Outcome = runtimepaths.FileNotPublished
			case "invalid-complete":
				out.SnapshotCleanup.SnapshotRemoval = runtimepaths.BackupRemovalUnknown
			}
			record, gotCode, status := backupCreateRecord(out, code)
			cleanup := record["snapshot_cleanup"].(map[string]any)
			if len(record) != 15 || len(cleanup) != 5 || cleanup["status"] != wantStatus || gotCode != code || (status == 0) != (mode == "complete") {
				t.Fatal(record, gotCode, status)
			}
			if mode == "partial" && (cleanup["removed_files"] != 1 || cleanup["last_removal"] != "unknown") || mode == "removed-canceled" && cleanup["snapshot_removal"] != "removed" {
				t.Fatal("partial facts lost", record)
			}
			if mode != "unpublished" && record["archive_publication"] != "published" {
				t.Fatal("lost valid archive", record)
			}
			if code != "" && backupCreateCode(&homebackup.Error{Code: code}) != code {
				t.Fatal("cleanup error code lost")
			}
			if mode != "complete" {
				_, code, status := backupCreateRecord(out, "")
				if code != "BACKUP_CREATE_UNCONFIRMED" || status != 4 {
					t.Fatal("false cleanup success")
				}
			}
		})
	}
}
