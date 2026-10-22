//go:build windows

package homebackup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

func runRestoreFinishHelper(t *testing.T, dir, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRestoreFinishHelper$")
	cmd.Env = append(os.Environ(), "CH_RESTORE_FINISH_HOME="+dir, "CH_RESTORE_FINISH_MODE="+mode)
	out, err := cmd.CombinedOutput()
	if strings.HasPrefix(mode, "crash-") {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 85 {
			return
		}
	}
	if err != nil || strings.HasPrefix(mode, "crash-") {
		t.Fatalf("finish %s: %v\n%s", mode, err, out)
	}
}

func TestRestoreFinishNativeProcess(t *testing.T) {
	for _, mode := range []string{"normal", "rollback", "same-marker", "no-validator", "no-confirm", "incomplete-roots", "validation-failure",
		"marker-moved-error", "marker-unknown", "receipt-error", "receipt-unknown", "receipt-conflict", "receipt-retry",
		"remove-error", "remove-unknown", "not-removed-error", "after-commit-error", "cancel-after-marker", "drift-before-commit", "marker-drift-before-commit"} {
		t.Run(mode, func(t *testing.T) { runRestoreFinishHelper(t, t.TempDir(), mode) })
	}
}

func TestRestoreFinishCrashAndRestart(t *testing.T) {
	for _, stage := range []string{"before_marker", "after_marker", "after_validation", "after_receipt", "before_commit", "after_commit"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			runRestoreFinishHelper(t, dir, "crash-"+stage)
			if stage == "after_commit" {
				runRestoreFinishHelper(t, dir, "inspect-committed")
			} else {
				runRestoreFinishHelper(t, dir, "recover-pending")
			}
		})
	}
}

func TestRestoreFinishHelper(t *testing.T) {
	dir, mode := os.Getenv("CH_RESTORE_FINISH_HOME"), os.Getenv("CH_RESTORE_FINISH_MODE")
	if dir == "" || mode == "" {
		return
	}
	root, q := runtimepaths.Open(dir)
	if q != nil {
		t.Fatal(q)
	}
	defer root.Close()
	if q = root.ClaimForProcess(); q != nil {
		t.Fatal(q)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	write := func(name, value string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	afterID := "dh_" + strings.Repeat("f", 32)
	if mode == "same-marker" {
		afterID = testHomeID
	}
	if mode == "inspect-committed" {
		home, p := datahome.Inspect(dir, nil)
		if p != nil || home.DataHomeID != afterID {
			t.Fatal("committed Home did not restart", p)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "jobs/new.txt"))
		if err != nil || string(raw) != "new" {
			t.Fatal("committed data lost", err)
		}
		if _, err := os.Stat(filepath.Join(dir, datahome.RestoreRecoveryPath)); !os.IsNotExist(err) {
			t.Fatal("committed journal still present", err)
		}
		return
	}
	var j restoreJournal
	var evidence restoreEvidence
	var p *Error
	if mode == "recover-pending" {
		if _, p := datahome.Inspect(dir, nil); p == nil || p.Code != "DATA_HOME_RECOVERY_REQUIRED" {
			t.Fatal("partial restore admitted ordinary startup", p)
		}
		j, evidence, p = readRestoreJournal(ctx, root, "stable")
		if p != nil {
			t.Fatal(p)
		}
		if result, p := moveRestoreRoots(ctx, root, "stable", true, true, restoreRootOperations{}); p != nil || !result.RootPhaseComplete {
			t.Fatal("restart root rollback", p)
		}
	} else {
		marker, markerProblem := datahome.MarkerBytes("stable", testHomeID)
		if markerProblem != nil {
			t.Fatal(markerProblem)
		}
		write(datahome.MarkerPath, string(marker))
		write("jobs/old.txt", "old")
		before := testManifest(testFile("jobs/old.txt", "old"))
		after := testManifest(testFile("jobs/new.txt", "new"), testFile("documents/note.txt", "synthetic note"))
		after.DataHomeID = afterID
		scope, q := root.CredentialScope()
		if q != nil {
			t.Fatal(q)
		}
		j, evidence, p = makeRestoreJournal(strings.Repeat("a", 32), scope.Fingerprint(), marker, before, after, map[string]bool{"jobs": true, "documents": false})
		if p != nil {
			t.Fatal(p)
		}
		write(j.stagePath()+"/jobs/new.txt", "new")
		write(j.stagePath()+"/documents/note.txt", "synthetic note")
		if _, p := publishRestoreJournal(ctx, root, j, evidence, restoreJournalOperations{}); p != nil {
			t.Fatal(p)
		}
		if mode != "incomplete-roots" {
			if result, p := moveRestoreRoots(ctx, root, "stable", true, false, restoreRootOperations{}); p != nil || !result.RootPhaseComplete {
				t.Fatal(p)
			}
		}
		if mode == "rollback" {
			// Simulate an interrupted forward marker transition before rollback.
			if changed, q := root.ReplaceFile(datahome.MarkerPath, evidence.beforeMarker, evidence.afterMarker); q != nil || !changed {
				t.Fatal(q)
			}
			if result, p := moveRestoreRoots(ctx, root, "stable", true, true, restoreRootOperations{}); p != nil || !result.RootPhaseComplete {
				t.Fatal(p)
			}
		}
	}
	rollback := mode == "rollback" || mode == "recover-pending"
	validationCalls := 0
	validate := func(ctx context.Context, _ *runtimepaths.Root, home datahome.Info) *Error {
		validationCalls++
		marker, _, q := root.ReadObjectFile(datahome.MarkerPath, datahome.MaxMarkerBytes)
		if q != nil {
			t.Fatal(q)
		}
		current, p := datahome.ParseMarkerForDocumentMigration(marker)
		if p != nil || *current != home {
			t.Fatal("validator ran under wrong marker", p)
		}
		if mode == "validation-failure" {
			return failure("BACKUP_FIXTURE_SEMANTIC_FAILURE")
		}
		// Full semantic validation through an isolated physical copy.
		// No database or OS credentials in this terminal-state fixture: WAL and
		// encryption qualification belong to the full orchestration tests.
		id, err := newBackupID()
		if err != nil {
			t.Fatal(err)
		}
		stage := snapshotPrefix + id
		_, problem := validateRestoreCopy(ctx, dir, stage, home, nil)
		return problem
	}
	ops := restoreFinishOperations{}
	if mode == "no-validator" {
		validate = nil
	}
	if mode == "receipt-conflict" {
		write(j.recoveryPath()+"/completed.json", "foreign receipt")
	}
	ops.checkpoint = func(stage string) *Error {
		if mode == "crash-"+stage {
			os.Exit(85)
		}
		if mode == "cancel-after-marker" && stage == "after_marker" {
			cancel()
		}
		if mode == "after-commit-error" && stage == "after_commit" {
			return failure("BACKUP_FIXTURE_LATE_ERROR")
		}
		if mode == "receipt-retry" && stage == "after_receipt" {
			return failure("BACKUP_FIXTURE_INTERRUPTED")
		}
		if stage == "before_commit" {
			if mode == "drift-before-commit" {
				write("jobs/new.txt", "bad")
			}
			if mode == "marker-drift-before-commit" {
				other, _ := datahome.MarkerBytes("stable", "dh_"+strings.Repeat("e", 32))
				write(datahome.MarkerPath, string(other))
			}
		}
		return nil
	}
	if mode == "marker-moved-error" || mode == "marker-unknown" {
		ops.replace = func(r *runtimepaths.Root, path string, before, after []byte) (bool, *runtimepaths.Error) {
			changed, q := r.ReplaceFile(path, before, after)
			if q != nil || !changed {
				t.Fatal(q)
			}
			if mode == "marker-unknown" {
				changed = false
			}
			return changed, &runtimepaths.Error{Code: "FIXTURE"}
		}
	}
	if mode == "receipt-error" || mode == "receipt-unknown" {
		ops.publish = func(ctx context.Context, r *runtimepaths.Root, path string, raw []byte) (runtimepaths.StreamPublication, *Error) {
			result, p := publishRestoreBytes(ctx, r, path, raw)
			if p != nil {
				t.Fatal(p)
			}
			if mode == "receipt-unknown" {
				result.Outcome = runtimepaths.FilePublicationUnknown
			}
			return result, failure("BACKUP_FIXTURE_RECEIPT_ERROR")
		}
	}
	if mode == "remove-error" || mode == "remove-unknown" || mode == "not-removed-error" {
		ops.remove = func(r *runtimepaths.Root, path string, raw []byte) (bool, *runtimepaths.Error) {
			removed := false
			if mode != "not-removed-error" {
				var q *runtimepaths.Error
				removed, q = r.RemoveFile(path, raw)
				if q != nil || !removed {
					t.Fatal(q)
				}
			}
			if mode == "remove-unknown" {
				removed = false
			}
			return removed, &runtimepaths.Error{Code: "FIXTURE"}
		}
	}
	result, p := finishRestore(ctx, root, "stable", mode != "no-confirm", rollback, validate, ops)
	expected := map[string]string{"no-validator": "BACKUP_RESTORE_VALIDATOR_REQUIRED", "no-confirm": "BACKUP_WRITER_CONFIRMATION_REQUIRED",
		"incomplete-roots": "BACKUP_RESTORE_ROOT_PHASE_INCOMPLETE", "validation-failure": "BACKUP_FIXTURE_SEMANTIC_FAILURE",
		"marker-moved-error": "BACKUP_RESTORE_MARKER_UNCONFIRMED", "marker-unknown": "BACKUP_RESTORE_MARKER_UNCONFIRMED",
		"receipt-error": "BACKUP_FIXTURE_RECEIPT_ERROR", "receipt-unknown": "BACKUP_FIXTURE_RECEIPT_ERROR", "receipt-conflict": "BACKUP_RESTORE_RECEIPT_CONFLICT",
		"receipt-retry": "BACKUP_FIXTURE_INTERRUPTED", "remove-error": "BACKUP_RESTORE_COMMIT_UNCONFIRMED", "remove-unknown": "BACKUP_RESTORE_COMMIT_UNCONFIRMED",
		"not-removed-error": "BACKUP_RESTORE_COMMIT_UNCONFIRMED", "after-commit-error": "BACKUP_FIXTURE_LATE_ERROR", "cancel-after-marker": "BACKUP_CANCELED",
		"drift-before-commit": "BACKUP_RESTORE_TREE_CHANGED", "marker-drift-before-commit": "BACKUP_RESTORE_HOME_CHANGED"}[mode]
	if expected != "" {
		if p == nil || p.Code != expected || result.Complete {
			t.Fatal("wrong terminal refusal", expected, result, p)
		}
		if mode == "marker-moved-error" && result.MarkerPublication != runtimepaths.FilePublished {
			t.Fatal("lost marker fact")
		}
		if mode == "marker-unknown" && result.MarkerPublication != runtimepaths.FilePublicationUnknown {
			t.Fatal("lost unknown marker fact")
		}
		if mode == "receipt-error" && result.Receipt.Outcome != runtimepaths.FilePublished {
			t.Fatal("lost receipt fact")
		}
		if mode == "receipt-unknown" && result.Receipt.Outcome != runtimepaths.FilePublicationUnknown {
			t.Fatal("lost unknown receipt fact")
		}
		committed := mode == "remove-error" || mode == "remove-unknown" || mode == "after-commit-error"
		_, journalError := os.Stat(filepath.Join(dir, datahome.RestoreRecoveryPath))
		if committed != os.IsNotExist(journalError) {
			t.Fatal("incorrect commit facts", result, journalError)
		}
		if mode == "remove-error" || mode == "after-commit-error" {
			if result.JournalRemoval != restoreRemoved {
				t.Fatal("lost removal fact")
			}
		}
		if mode == "remove-unknown" || mode == "not-removed-error" {
			if result.JournalRemoval != restoreRemovalUnknown {
				t.Fatal("lost unknown removal")
			}
		}
		if mode == "no-validator" || mode == "no-confirm" || mode == "incomplete-roots" {
			if validationCalls != 0 {
				t.Fatal("validator called before admission")
			}
		}
		if mode != "receipt-retry" {
			return
		}
		// A matching existing receipt is reusable only after fresh validation.
		result, p = finishRestore(ctx, root, "stable", true, false, validate, restoreFinishOperations{})
		if validationCalls != 2 {
			t.Fatal("receipt skipped semantic validation")
		}
	}
	if p != nil || !result.Complete || !result.SemanticValidated || result.Receipt.Outcome != runtimepaths.FilePublished || result.JournalRemoval != restoreRemoved {
		t.Fatal("terminal result", result, p)
	}
	if mode == "same-marker" && result.MarkerPublication != runtimepaths.FileNotPublished {
		t.Fatal("unchanged marker rewritten")
	}
	home, pHome := datahome.Inspect(dir, nil)
	want := afterID
	if rollback {
		want = testHomeID
	}
	if pHome != nil || home.DataHomeID != want {
		t.Fatal("final Home admission", home, pHome)
	}
	name, state := "completed.json", "restored"
	if rollback {
		name, state = "recovered.json", "rolled_back"
	}
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(j.recoveryPath()+"/"+name)))
	var receipt restoreReceipt
	if err != nil || json.Unmarshal(raw, &receipt) != nil || receipt.State != state || receipt.RestoreID != j.RestoreID {
		t.Fatal("receipt missing", err)
	}
	if rollback {
		old, err := os.ReadFile(filepath.Join(dir, "jobs/old.txt"))
		if err != nil || !bytes.Equal(old, []byte("old")) {
			t.Fatal("original lost", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "documents")); !os.IsNotExist(err) {
			t.Fatal("new root survived rollback")
		}
	}
}
