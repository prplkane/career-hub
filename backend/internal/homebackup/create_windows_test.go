//go:build windows

package homebackup

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/documents/keys"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sqlitestore"
)

func TestCreateOwnedNative(t *testing.T) {
	for _, scenario := range []string{"default", "wal", "selected", "unowned", "no-confirm", "channel", "missing-marker", "invalid-marker", "recovery", "partial-invalid", "partial-missing-key", "existing", "collision", "id-error", "marker-pin", "recovery-before-publish", "recovery-after-publish", "race", "committed-error", "unknown", "close-after-publish", "cancel-after-publish",
		"cleanup-pin", "cleanup-cancel", "cleanup-recovery", "cleanup-drift", "cleanup-unknown", "cleanup-terminal-cancel", "archive-other"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCreateOwnedHelper$")
			cmd.Env = append(os.Environ(), "CH_CREATE_HOME="+dir, "CH_CREATE_SCENARIO="+scenario)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", scenario, err, out)
			}
			if scenario != "wal" && scenario != "selected" {
				return
			}

		})
	}
}

func TestCreateOwnedHelper(t *testing.T) {
	scenario := os.Getenv("CH_CREATE_SCENARIO")
	if scenario == "" {
		return
	}
	dir := os.Getenv("CH_CREATE_HOME")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	write := func(name string, raw []byte) {
		t.Helper()
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	markerBytes, markerError := datahome.MarkerBytes("stable", testHomeID)
	if markerError != nil {
		t.Fatal(markerError)
	}
	if scenario != "missing-marker" {
		write(datahome.MarkerPath, markerBytes)
	}
	if scenario == "invalid-marker" {
		write(datahome.MarkerPath, []byte("{}"))
	}
	write("jobs/a.txt", []byte("synthetic job"))
	write("resume_versions/a.txt", []byte("synthetic resume"))
	// These exclusions must never become archive members.
	write("secrets/synthetic.txt", []byte("excluded synthetic key material"))
	request := CreateRequest{DataRoot: dir, Channel: "stable", ArchiveRelativePath: "backups/nested/created.zip", ConfirmWritersStopped: true}
	ops := createOperations{newID: func() (string, error) { return strings.Repeat("c", 32), nil }, publish: PublishSnapshot}
	backend := &syntheticValidationKeys{t: t, missing: scenario == "partial-missing-key"}
	if scenario == "default" {
		request.ArchiveRelativePath = ""
	}
	if scenario == "no-confirm" {
		request.ConfirmWritersStopped = false
	}
	if scenario == "channel" {
		request.Channel = "dev"
	}
	if scenario == "recovery" {
		write(datahome.RestoreRecoveryPath, []byte("opaque synthetic recovery"))
	}
	if scenario == "partial-invalid" {
		write("career_vault/profile.yaml", []byte("name: Synthetic"))
		request.IncludeRoots = []string{"jobs"}
	}
	if scenario == "partial-missing-key" {
		state := keys.State{Version: 1, Algorithm: keys.Algorithm, DataHomeID: testHomeID, ActiveKeyID: "key_" + strings.Repeat("a", 32)}
		raw, err := state.CanonicalBytes()
		if err != nil {
			t.Fatal(err)
		}
		write("documents/key_state.json", raw)
		request.IncludeRoots = []string{"jobs"}
	}
	if scenario == "wal" || scenario == "selected" || scenario == "close-after-publish" {
		write(databasePath, nil)
		db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(dir, filepath.FromSlash(databasePath)))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; PRAGMA user_version=1; CREATE TABLE fixture(value TEXT); INSERT INTO fixture VALUES ('committed WAL value')"); err != nil {
			t.Fatal(err)
		}
		if scenario == "selected" {
			request.IncludeRoots = []string{"jobs"}
		}
	}
	if scenario == "existing" {
		write(request.ArchiveRelativePath, []byte("preserve existing"))
	}
	if scenario == "collision" {
		write(snapshotPrefix+strings.Repeat("c", 32)+"/foreign", []byte("preserve namespace"))
	}
	if scenario == "id-error" {
		ops.newID = func() (string, error) { return "", errors.New("synthetic private details") }
	}
	if scenario == "marker-pin" {
		ops.afterValidation = func() {
			if err := os.WriteFile(filepath.Join(dir, datahome.MarkerPath), []byte("replacement"), 0600); err == nil {
				t.Fatal("marker was writable during backup")
			}
		}
	}
	if scenario == "recovery-before-publish" {
		ops.afterValidation = func() { write(datahome.EmailCredentialRecoveryPath, []byte("synthetic intent")) }
	}
	if scenario == "archive-other" {
		ops.publish = func(ctx context.Context, root *runtimepaths.Root, stage, target string, manifest Manifest) (runtimepaths.StreamPublication, *Error) {
			manifest.Files = manifest.Files[:1]
			return PublishSnapshot(ctx, root, stage, target, manifest)
		}
	}
	if strings.HasPrefix(scenario, "cleanup-") {
		ops.cleanup.beforeRemove = func(index int) *Error {
			if scenario == "cleanup-pin" {
				archive := filepath.Join(dir, filepath.FromSlash(request.ArchiveRelativePath))
				if err := os.WriteFile(archive, []byte("must refuse"), 0600); err == nil {
					t.Fatal("archive was writable during cleanup")
				}
				if err := os.Rename(archive, archive+".moved"); err == nil {
					t.Fatal("archive could be replaced during cleanup")
				}
			}
			if index == 1 {
				switch scenario {
				case "cleanup-cancel":
					cancel()
				case "cleanup-recovery":
					write(datahome.EmailCredentialRecoveryPath, []byte("pending recovery"))
				case "cleanup-drift":
					write(snapshotPrefix+strings.Repeat("c", 32)+"/resume_versions/a.txt", []byte("changed staged copy"))
				}
			}
			return nil
		}
		if scenario == "cleanup-unknown" {
			ops.cleanup.file = func(context.Context, *runtimepaths.Root, string, File) (runtimepaths.BackupRemovalOutcome, *runtimepaths.Error) {
				return runtimepaths.BackupRemovalUnknown, nil
			}
		}
		if scenario == "cleanup-terminal-cancel" {
			ops.cleanup.directory = func(ctx context.Context, r *runtimepaths.Root, path string) (runtimepaths.BackupRemovalOutcome, *runtimepaths.Error) {
				out, p := r.RemoveEmptyBackupDirectory(ctx, path)
				if path == snapshotPrefix+strings.Repeat("c", 32) {
					cancel()
				}
				return out, p
			}
		}
	}
	if scenario == "race" || scenario == "committed-error" || scenario == "unknown" || scenario == "close-after-publish" || scenario == "cancel-after-publish" || scenario == "recovery-after-publish" {
		ops.publish = func(ctx context.Context, root *runtimepaths.Root, stage, target string, manifest Manifest) (runtimepaths.StreamPublication, *Error) {
			if scenario == "race" {
				write(target, []byte("preserve racing archive"))
			}
			if scenario == "unknown" {
				// This only tests composition of lower-layer facts. Real ambiguous
				// rename fault coverage lives in runtimepaths' native stream tests.
				return runtimepaths.StreamPublication{Outcome: runtimepaths.FilePublicationUnknown, CleanupConfirmed: false}, failure("BACKUP_PUBLICATION_FAILED")
			}
			facts, p := PublishSnapshot(ctx, root, stage, target, manifest)
			if p != nil {
				return facts, p
			}
			switch scenario {
			case "committed-error":
				return facts, failure("BACKUP_PUBLICATION_FAILED")
			case "close-after-publish":
				if err := root.Close(); err != nil {
					t.Fatal(err)
				}
			case "cancel-after-publish":
				cancel()
			case "recovery-after-publish":
				write(datahome.EmailCredentialRecoveryPath, []byte("synthetic intent"))
			}
			return facts, nil
		}
	}
	root, pathProblem := runtimepaths.Open(dir)
	if pathProblem != nil {
		t.Fatal(pathProblem)
	}
	defer root.Close()
	if scenario != "unowned" {
		if p := root.ClaimForProcess(); p != nil {
			t.Fatal(p)
		}
	}
	var got CreationResult
	var problem *Error
	if scenario == "default" {
		got, problem = CreateOwned(ctx, request, backend) // Exercise real random-ID entry point.
	} else {
		got, problem = createOwned(ctx, request, backend, ops)
	}
	codes := map[string]string{
		"unowned": "BACKUP_OWNERSHIP_REQUIRED", "no-confirm": "BACKUP_WRITER_CONFIRMATION_REQUIRED", "channel": "DATA_HOME_CHANNEL_MISMATCH",
		"missing-marker": "BACKUP_HOME_UNAVAILABLE", "invalid-marker": "INVALID_DATA_HOME", "recovery": "DATA_HOME_RECOVERY_REQUIRED",
		"partial-invalid": "BACKUP_VAULT_INCOMPLETE", "partial-missing-key": "BACKUP_DOCUMENT_KEY_UNAVAILABLE",
		"existing": "BACKUP_DESTINATION_EXISTS", "collision": "BACKUP_SNAPSHOT_RESERVATION_FAILED", "id-error": "BACKUP_ID_UNAVAILABLE",
		"recovery-before-publish": "DATA_HOME_RECOVERY_REQUIRED", "recovery-after-publish": "DATA_HOME_RECOVERY_REQUIRED",
		"race": "BACKUP_PUBLICATION_FAILED", "committed-error": "BACKUP_PUBLICATION_FAILED", "unknown": "BACKUP_PUBLICATION_FAILED", "cancel-after-publish": "BACKUP_CANCELED",
		"cleanup-cancel": "BACKUP_CANCELED", "cleanup-recovery": "BACKUP_CLEANUP_RECOVERY_REQUIRED", "cleanup-drift": "BACKUP_CLEANUP_UNCONFIRMED",
		"cleanup-unknown": "BACKUP_CLEANUP_UNCONFIRMED", "cleanup-terminal-cancel": "BACKUP_CANCELED", "archive-other": "BACKUP_CLEANUP_ARCHIVE_CHANGED",
	}
	wantCode := codes[scenario]
	if scenario == "close-after-publish" {
		if problem == nil {
			t.Fatal("closed consumer Root succeeded")
		}
	} else if wantCode == "" && problem != nil || wantCode != "" && (problem == nil || problem.Code != wantCode) {
		t.Fatalf("%s: %+v problem=%v want=%s", scenario, got, problem, wantCode)
	}
	wantPublished := problem == nil || scenario == "committed-error" || scenario == "close-after-publish" || scenario == "cancel-after-publish" || scenario == "recovery-after-publish" || strings.HasPrefix(scenario, "cleanup-") || scenario == "archive-other"
	wantOutcome := runtimepaths.FileNotPublished
	if wantPublished {
		wantOutcome = runtimepaths.FilePublished
	}
	if scenario == "unknown" {
		wantOutcome = runtimepaths.FilePublicationUnknown
	}
	if got.ArchivePublication.Outcome != wantOutcome {
		t.Fatal("lost publication facts", got, problem)
	}
	if scenario == "unknown" && got.ArchivePublication.CleanupConfirmed {
		t.Fatal("invented cleanup confirmation")
	}
	if wantPublished {
		manifest, p := VerifyLocal(context.Background(), root, got.ArchiveRelativePath)
		if p != nil {
			t.Fatal("published ZIP invalid", p)
		}
		wantArchiveFiles := got.ArchiveFiles
		if scenario == "archive-other" {
			wantArchiveFiles = 1
		}
		if manifest.DataHomeID != testHomeID || manifest.DataHomeChannel != "stable" || len(manifest.Files) != wantArchiveFiles || got.FullSourceValidation == nil {
			t.Fatal(manifest, got)
		}
		for _, file := range manifest.Files {
			if strings.HasPrefix(file.RelativePath, "secrets/") || file.RelativePath == datahome.MarkerPath {
				t.Fatal("excluded content exported")
			}
		}
		if scenario == "selected" && (got.StagedFiles != 3 || got.ArchiveFiles != 1 || manifest.SQLiteSchemaVersion != nil || got.FullSourceValidation.SQLiteSchemaVersion == nil || *got.FullSourceValidation.SQLiteSchemaVersion != 1) {
			t.Fatal("partial export skipped full validation", got, manifest)
		}
	} else if scenario != "existing" && scenario != "race" && got.ArchiveRelativePath != "" {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(got.ArchiveRelativePath))); !os.IsNotExist(err) {
			t.Fatal("unexpected archive", err)
		}
	}
	if scenario == "existing" || scenario == "race" {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(request.ArchiveRelativePath)))
		want := "preserve existing"
		if scenario == "race" {
			want = "preserve racing archive"
		}
		if err != nil || string(raw) != want {
			t.Fatal("overwritten destination", err)
		}
	}
	if got.SnapshotNamespace == runtimepaths.DirectoryCreated {
		stage := filepath.Join(dir, filepath.FromSlash(got.SnapshotRelativePath))
		if problem == nil {
			if !got.SnapshotCleanup.Complete || got.SnapshotCleanup.SnapshotRemoval != runtimepaths.BackupRemoved || got.SnapshotCleanup.RemovedFiles != got.StagedFiles {
				t.Fatal("successful backup did not clean its working copy", got)
			}
		}
		if got.SnapshotCleanup.SnapshotRemoval == runtimepaths.BackupRemoved {
			if _, err := os.Lstat(stage); !os.IsNotExist(err) {
				t.Fatal("completed snapshot retained", err)
			}
		} else if err := os.Rename(stage, stage+".released"); err != nil {
			t.Fatal("snapshot handles leaked", err)
		}
	} else if scenario != "collision" {
		if _, err := os.Stat(filepath.Join(dir, ".career_hub_staging")); !os.IsNotExist(err) {
			t.Fatal("preflight refusal created staging", err)
		}
	}
	if strings.HasPrefix(scenario, "cleanup-") {
		if !got.SnapshotCleanup.Attempted {
			t.Fatal("lost cleanup dispatch", got)
		}
		if (scenario == "cleanup-cancel" || scenario == "cleanup-recovery" || scenario == "cleanup-drift") && got.SnapshotCleanup.RemovedFiles != 1 {
			t.Fatal("lost partial cleanup", got)
		}
		if scenario == "cleanup-terminal-cancel" && (got.SnapshotCleanup.Complete || got.SnapshotCleanup.SnapshotRemoval != runtimepaths.BackupRemoved) {
			t.Fatal("lost terminal cancellation effects", got)
		}
	} else if problem != nil && got.SnapshotCleanup != unstartedSnapshotCleanup() {
		t.Fatal("failed publication must not authorize cleanup", got)
	}
	if scenario == "collision" {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(snapshotPrefix+strings.Repeat("c", 32)+"/foreign")))
		if err != nil || string(raw) != "preserve namespace" {
			t.Fatal("adopted or removed existing namespace", err)
		}
	}
	if backend.reads != 0 && scenario != "partial-missing-key" {
		t.Fatal("unexpected credential read")
	}
	if scenario == "partial-missing-key" && backend.reads != 1 {
		t.Fatal("did not validate unselected document key")
	}
	if scenario != "missing-marker" && scenario != "invalid-marker" {
		marker := filepath.Join(dir, datahome.MarkerPath)
		raw, err := os.ReadFile(marker)
		if err != nil || string(raw) != string(markerBytes) {
			t.Fatal("source marker changed", err)
		}
		if err := os.Rename(marker, marker+".released"); err != nil {
			t.Fatal("marker lease leaked", err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "jobs", "a.txt")); err != nil || string(raw) != "synthetic job" {
		t.Fatal("active job changed", err)
	}
}
