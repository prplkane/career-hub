//go:build windows

package homebackup

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

func runRestoreOwnerHelper(t *testing.T, dir, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRestoreOwnerHelper$")
	cmd.Env = append(os.Environ(), "CH_RESTORE_OWNER_HOME="+dir, "CH_RESTORE_OWNER_MODE="+mode)
	out, err := cmd.CombinedOutput()
	if strings.HasPrefix(mode, "crash-") {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 87 {
			return
		}
	}
	if err != nil || strings.HasPrefix(mode, "crash-") {
		t.Fatalf("%s: %v\n%s", mode, err, out)
	}
}

func TestRestoreOwnedNativeProcess(t *testing.T) {
	for _, mode := range []string{"normal", "wal", "encrypted", "empty", "archive-wal", "archive-empty", "archive-wal-rollback", "archive-empty-rollback", "archive-schema", "no-confirm", "no-owner", "wrong-channel", "invalid-source", "invalid-candidate", "missing-key", "source-drift", "stage-drift", "journal-error", "roots-error", "validation-error", "commit-error", "recover-missing-key"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if mode == "wal" || strings.HasPrefix(mode, "archive-wal") {
				runRestoreValidationHelper(t, dir, "seed-wal")
			}
			runRestoreOwnerHelper(t, dir, mode)
		})
	}
}

func TestRestoreOwnedCrashRecovery(t *testing.T) {
	for _, stage := range []string{"after_journal", "after_move", "after_roots", "after_marker", "after_receipt", "after_commit"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			runRestoreValidationHelper(t, dir, "seed-wal")
			runRestoreOwnerHelper(t, dir, "crash-"+stage)
			mode := "recover"
			if stage == "after_commit" {
				mode = "inspect-committed"
			}
			runRestoreOwnerHelper(t, dir, mode)
		})
	}
}

func TestRestoreOwnedCleanupOutcomes(t *testing.T) {
	for _, prefix := range []string{"cleanup-", "rollback-cleanup-"} {
		for _, mode := range []string{"unknown", "late-error", "root-late-error", "cancel", "recovery"} {
			t.Run(prefix+mode, func(t *testing.T) { runRestoreOwnerHelper(t, t.TempDir(), prefix+mode) })
		}
	}
}

func TestRestoreOwnerHelper(t *testing.T) {
	dir, mode := os.Getenv("CH_RESTORE_OWNER_HOME"), os.Getenv("CH_RESTORE_OWNER_MODE")
	if dir == "" || mode == "" {
		return
	}
	ctx := context.Background()
	root, q := runtimepaths.Open(dir)
	if q != nil {
		t.Fatal(q)
	}
	defer root.Close()
	if mode != "no-owner" {
		if q := root.ClaimForProcess(); q != nil {
			t.Fatal(q)
		}
	}
	write := func(name string, raw []byte) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	backend := &syntheticValidationKeys{t: t, missing: mode == "missing-key"}
	afterID := "dh_" + strings.Repeat("e", 32)
	if mode == "encrypted" || mode == "missing-key" || mode == "empty" || mode == "recover-missing-key" {
		afterID = testHomeID
	}
	recoveryCrash := strings.HasPrefix(mode, "crash-recover-archive-")
	inspectRecovered := mode == "inspect-recovered-archive"
	if mode == "recover" || mode == "recover-archive" || recoveryCrash || inspectRecovered {
		var result RestoreResult
		if inspectRecovered {
			// Journal removal is the commit boundary even if the previous process
			// exited before returning its result. A new rollback must not replay.
			refused, p := RecoverOwned(ctx, RecoverRequest{dir, "stable", true}, backend)
			if p == nil || p.Code != "BACKUP_RESTORE_RECOVERY_REQUIRED" || refused.Finish.Complete {
				t.Fatal("committed rollback replayed", refused, p)
			}
			entries, err := os.ReadDir(filepath.Join(dir, "backups", "restore-recovery"))
			if err != nil || len(entries) != 1 || !entries[0].IsDir() || !strings.HasPrefix(entries[0].Name(), "restore_") ||
				!lowerHex(strings.TrimPrefix(entries[0].Name(), "restore_"), 32) {
				t.Fatal("unexpected retained recovery fixture", entries, err)
			}
			result.RecoveryRelativePath = "backups/restore-recovery/" + entries[0].Name()
		} else {
			if _, p := datahome.Inspect(dir, nil); p == nil {
				t.Fatal("interrupted restore admitted startup")
			}
			ops := restoreOperations{newID: newBackupID}
			stop := func(stage string) *Error {
				if mode == "crash-recover-archive-"+stage {
					os.Exit(87)
				}
				return nil
			}
			ops.checkpoint, ops.finish.checkpoint = stop, stop
			ops.roots.afterMove = func(_ int, _ restoreRootMove) *Error { return stop("after_move") }
			var p *Error
			result, p = recoverOwned(ctx, RecoverRequest{dir, "stable", true}, backend, ops)
			if recoveryCrash || p != nil || !result.Finish.Complete || result.Finish.JournalRemoval != restoreRemoved {
				t.Fatal("recovery failed or missed crash boundary", result, p)
			}
		}
		home, admission := datahome.Inspect(dir, nil)
		if admission != nil || home.DataHomeID != testHomeID {
			t.Fatal("original identity not restored", admission)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "jobs/old.txt"))
		if err != nil || string(raw) != "original" {
			t.Fatal("original job not restored", err)
		}
		// The original journal manifest binds real quiescent WAL/SHM bytes.
		jraw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(result.RecoveryRelativePath+"/original_manifest.json")))
		if err != nil {
			t.Fatal(err)
		}
		manifest, p := ParseManifest(jraw)
		if p != nil {
			t.Fatal(p)
		}
		for _, file := range manifest.Files {
			raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file.RelativePath)))
			if err != nil || testFile(file.RelativePath, string(raw)) != file {
				t.Fatal("original physical bytes changed", file.RelativePath, err)
			}
		}
		if mode == "recover-archive" || inspectRecovered {
			raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(result.RecoveryRelativePath+"/target_manifest.json")))
			if err != nil {
				t.Fatal(err)
			}
			candidate, p := ParseManifest(raw)
			if p != nil {
				t.Fatal(p)
			}
			for _, file := range candidate.Files {
				raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(result.RecoveryRelativePath+"/interrupted_candidate/"+file.RelativePath)))
				if err != nil || testFile(file.RelativePath, string(raw)) != file {
					t.Fatal("candidate physical bytes changed after crash", file.RelativePath, err)
				}
			}
		}
		return
	}
	if mode == "inspect-committed" {
		home, p := datahome.Inspect(dir, nil)
		if p != nil || home.DataHomeID != afterID {
			t.Fatal("committed startup failed", p)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "jobs/new.txt"))
		if err != nil || string(raw) != "candidate" {
			t.Fatal("committed payload lost", err)
		}
		return
	}
	marker, p := datahome.MarkerBytes("stable", testHomeID)
	if p != nil {
		t.Fatal(p)
	}
	write(datahome.MarkerPath, marker)
	write("jobs/old.txt", []byte("original"))
	write("outputs/retained.txt", []byte("unselected"))
	if mode == "invalid-source" {
		write("career_vault/profile.yaml", []byte("name: partial"))
	}
	if mode == "recover-missing-key" {
		writeRestoreValidationDocument(t, write)
	}
	candidate := map[string][]byte{"jobs/new.txt": []byte("candidate"), "documents/note.txt": []byte("new identity"), "applications/note.txt": []byte("candidate applications")}
	if mode == "empty" {
		candidate = map[string][]byte{}
	}
	if mode == "encrypted" || mode == "missing-key" {
		delete(candidate, "documents/note.txt")
		writeRestoreValidationDocument(t, func(name string, raw []byte) { candidate[name] = raw })
	}
	if mode == "invalid-candidate" {
		candidate["jobs/normalized/bad.yaml"] = []byte("id: incomplete")
	}
	manifest := testManifest()
	archiveCrash := strings.HasPrefix(mode, "crash-archive-wal-")
	if strings.HasPrefix(mode, "archive-") || archiveCrash {
		version := int64(0)
		candidate[databasePath] = nil
		if strings.HasPrefix(mode, "archive-wal") || archiveCrash {
			version = 1
			for _, suffix := range []string{"", "-wal", "-shm"} {
				raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(databasePath+suffix)))
				if err != nil {
					t.Fatal(err)
				}
				candidate[databasePath+suffix] = raw
			}
		}
		if mode == "archive-schema" {
			version = 1
		}
		manifest.SQLiteSchemaVersion = &version
	}
	manifest.DataHomeID = afterID
	if mode == "wrong-channel" {
		manifest.DataHomeChannel = "dev"
	}
	names := make([]string, 0, len(candidate))
	for name := range candidate {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := []fixtureEntry{}
	for _, name := range names {
		manifest.Files = append(manifest.Files, testFile(name, string(candidate[name])))
		entries = append(entries, fixtureEntry{name, string(candidate[name]), 0600})
	}
	archive := fixtureZIP(t, manifestJSON(t, manifest), entries...)
	write("backups/input.zip", archive)
	ops := restoreOperations{newID: newBackupID}
	cleanupFault := strings.HasPrefix(mode, "cleanup-")
	rollbackCleanupFault := strings.HasPrefix(mode, "rollback-cleanup-")
	if cleanupFault {
		ops.cleanup = restoreCleanupFaultOperations(t, dir, strings.TrimPrefix(mode, "cleanup-"))
	}
	lastID := ""
	ops.newID = func() (string, error) { id, err := newBackupID(); lastID = id; return id, err }
	ops.checkpoint = func(stage string) *Error {
		if mode == "crash-"+stage || mode == "crash-archive-wal-"+stage {
			os.Exit(87)
		}
		if mode == "source-drift" && stage == "after_initial_validation" {
			write("jobs/old.txt", []byte("drift"))
		}
		if mode == "stage-drift" && stage == "after_extraction" {
			write(restorePrefix+lastID+"/jobs/new.txt", []byte("drift"))
		}
		if mode == "journal-error" && stage == "after_journal" || (mode == "roots-error" || mode == "recover-missing-key" || strings.HasSuffix(mode, "-rollback") || rollbackCleanupFault) && stage == "after_roots" {
			return failure("BACKUP_FIXTURE_STOP")
		}
		return nil
	}
	ops.roots.afterMove = func(_ int, _ restoreRootMove) *Error {
		if mode == "crash-after_move" {
			os.Exit(87)
		}
		return nil
	}
	ops.finish.checkpoint = func(stage string) *Error {
		if mode == "crash-"+stage || mode == "crash-archive-wal-"+stage {
			os.Exit(87)
		}
		if mode == "validation-error" && stage == "after_marker" {
			write("career_vault/profile.yaml", []byte("name: incomplete"))
		}
		if mode == "commit-error" && stage == "after_commit" {
			return failure("BACKUP_FIXTURE_LATE_ERROR")
		}
		return nil
	}
	result, problem := restoreOwned(ctx, RestoreRequest{dir, "stable", "backups/input.zip", mode != "no-confirm"}, backend, ops)
	want := mode == "normal" || mode == "wal" || mode == "encrypted" || mode == "empty" || mode == "archive-wal" || mode == "archive-empty"
	if (problem == nil) != want {
		t.Fatal(mode, result, problem)
	}
	if strings.HasPrefix(mode, "archive-") {
		if result.ArchiveValidation.SnapshotRelativePath == "" || result.ArchiveValidation.Preparation.Namespace != runtimepaths.DirectoryCreated {
			t.Fatal("copy facts missing", result.ArchiveValidation)
		}
		if mode == "archive-schema" {
			if problem == nil || problem.Code != "BACKUP_SCHEMA_UNSUPPORTED" || result.Journal.Journal.Outcome != runtimepaths.FileNotPublished {
				t.Fatal("schema refusal crossed intent", result, problem)
			}
		} else if result.ArchiveValidation.Summary == nil {
			t.Fatal("archive not validated")
		}
	}
	gotArchive, err := os.ReadFile(filepath.Join(dir, "backups/input.zip"))
	if err != nil || !bytes.Equal(gotArchive, archive) {
		t.Fatal("input archive modified", err)
	}
	retained, err := os.ReadFile(filepath.Join(dir, "outputs/retained.txt"))
	if err != nil || string(retained) != "unselected" {
		t.Fatal("unselected root lost", err)
	}
	if cleanupFault {
		assertRestoreCleanupFault(t, dir, strings.TrimPrefix(mode, "cleanup-"), result, problem, false)
		for name, expected := range candidate {
			raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
			if err != nil || !bytes.Equal(raw, expected) {
				t.Fatal("cleanup changed activated candidate", name, err)
			}
		}
		return
	}
	if want || mode == "commit-error" {
		if want && !result.Finish.Complete || result.Finish.JournalRemoval != restoreRemoved {
			t.Fatal("missing commit facts", result.Finish)
		}
		home, p := datahome.Inspect(dir, nil)
		if p != nil || home.DataHomeID != afterID {
			t.Fatal("committed Home not admitted", p)
		}
		for name, expected := range candidate {
			raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
			if err != nil || !bytes.Equal(raw, expected) {
				t.Fatal("candidate changed", name, err)
			}
		}
		if mode == "encrypted" && result.FinalValidation.Summary.DocumentsValidated != 1 {
			t.Fatal("encrypted final validation skipped")
		}
		if want {
			cleanupRestoreValidationFixtures(t, ctx, root, dir, result)
		} else {
			refused, p := cleanupValidationCopy(ctx, root, "stable", result.InitialValidation.cleanup, result.Finish, snapshotCleanupOperations{})
			if p == nil || refused.Attempted || p.Code != "BACKUP_RESTORE_COMMIT_UNCONFIRMED" {
				t.Fatal("late-error restore allowed cleanup", refused, p)
			}
		}
		return
	}
	if result.Finish.Complete {
		t.Fatal("failed restore marked complete")
	}
	if result.Journal.Journal.Outcome == runtimepaths.FilePublished {
		if _, p := datahome.Inspect(dir, nil); p == nil {
			t.Fatal("partial restore admitted startup")
		}
		if mode == "journal-error" || mode == "roots-error" || mode == "recover-missing-key" || strings.HasSuffix(mode, "-rollback") || rollbackCleanupFault {
			backend.missing = mode == "recover-missing-key"
			if rollbackCleanupFault {
				fault := strings.TrimPrefix(mode, "rollback-cleanup-")
				before := validationFixtureFiles(t, dir, nil)
				recovered, p := recoverOwned(ctx, RecoverRequest{dir, "stable", true}, backend, restoreOperations{newID: newBackupID, cleanup: restoreCleanupFaultOperations(t, dir, fault)})
				assertRestoreCleanupFault(t, dir, fault, recovered, p, true)
				after := validationFixtureFiles(t, dir, nil)
				for name, raw := range before {
					if strings.HasPrefix(name, snapshotPrefix) && after[name] != raw {
						t.Fatal("rollback cleaned previous invocation copy", name)
					}
				}
				if after["jobs/old.txt"] != "original" {
					t.Fatal("rollback original lost")
				}
				return
			}
			recovered, p := RecoverOwned(ctx, RecoverRequest{dir, "stable", true}, backend)
			if mode == "recover-missing-key" {
				if p == nil || p.Code != "BACKUP_DOCUMENT_KEY_UNAVAILABLE" || recovered.Finish.JournalRemoval != restoreNotRemoved {
					t.Fatal("recovery bypassed missing key", recovered, p)
				}
			} else if p != nil || !recovered.Finish.Complete {
				t.Fatal("explicit rollback failed", recovered, p)
			} else {
				cleanupRestoreValidationFixtures(t, ctx, root, dir, recovered)
			}
			if strings.HasSuffix(mode, "-rollback") {
				for name, raw := range candidate {
					got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(recovered.RecoveryRelativePath+"/interrupted_candidate/"+name)))
					if err != nil || !bytes.Equal(got, raw) {
						t.Fatal("archived candidate not preserved", name, err)
					}
				}
				got, err := os.ReadFile(filepath.Join(dir, "jobs/old.txt"))
				if err != nil || string(got) != "original" {
					t.Fatal("original not restored", err)
				}
			}
		}
	} else {
		if _, err := os.Stat(filepath.Join(dir, datahome.RestoreRecoveryPath)); !os.IsNotExist(err) {
			t.Fatal("pre-admission failure published journal", err)
		}
	}
}

func TestRestoreArchivedWALCrashRecovery(t *testing.T) {
	for _, stage := range []string{"after_roots", "after_marker", "after_receipt"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			runRestoreValidationHelper(t, dir, "seed-wal")
			runRestoreOwnerHelper(t, dir, "crash-archive-wal-"+stage)
			runRestoreOwnerHelper(t, dir, "recover-archive")
		})
	}
}

func TestRestoreArchivedWALRecoveryInterruptedAgain(t *testing.T) {
	for _, stage := range []string{"after_move", "after_roots", "after_marker", "after_receipt", "after_commit"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			runRestoreValidationHelper(t, dir, "seed-wal")
			// First fail after forward identity/receipt publication, then fail a
			// separate rollback process. No in-memory progress survives either.
			runRestoreOwnerHelper(t, dir, "crash-archive-wal-after_receipt")
			runRestoreOwnerHelper(t, dir, "crash-recover-archive-"+stage)
			next := "recover-archive"
			if stage == "after_commit" {
				next = "inspect-recovered-archive"
			}
			runRestoreOwnerHelper(t, dir, next)
		})
	}
}
