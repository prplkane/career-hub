//go:build windows

package homebackup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestValidationCleanupNative(t *testing.T) {
	for _, mode := range []string{"complete", "closed-reservation", "nil-reservation", "extra-file", "invalid-normalized", "physical-drift", "directory-drift", "scope", "recovery", "unfinished", "commit-unknown", "receipt-unknown", "semantic-missing", "unknown-remove", "late-remove-error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestValidationCleanupHelper$")
			cmd.Env = append(os.Environ(), "CH_VALIDATION_CLEANUP_HOME="+t.TempDir(), "CH_VALIDATION_CLEANUP_MODE="+mode)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
		})
	}
}

func TestValidationCleanupHelper(t *testing.T) {
	dir, mode := os.Getenv("CH_VALIDATION_CLEANUP_HOME"), os.Getenv("CH_VALIDATION_CLEANUP_MODE")
	if mode == "" {
		t.Skip("subprocess only")
	}
	ctx := context.Background()
	write := func(name, content string) {
		t.Helper()
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	marker, markerProblem := datahome.MarkerBytes("stable", testHomeID)
	if markerProblem != nil {
		t.Fatal(markerProblem)
	}
	write(datahome.MarkerPath, string(marker))
	root, q := runtimepaths.Open(dir)
	if q != nil {
		t.Fatal(q)
	}
	defer root.Close()
	if q := root.ClaimForProcess(); q != nil {
		t.Fatal(q)
	}
	snapshot := snapshotPrefix + strings.Repeat("c", 32)
	if _, q := root.EnsureDirectory(strings.TrimSuffix(snapshotPrefix, "/")); q != nil {
		t.Fatal(q)
	}
	reservation, outcome, q := root.ReserveDirectory(snapshot)
	if q != nil || outcome != runtimepaths.DirectoryCreated {
		t.Fatal(q)
	}
	defer reservation.Close()
	normalized := []File{testFile(databasePath, "normalized SQLite fixture"), testFile("jobs/note.txt", "validation fixture")}
	for _, file := range normalized {
		content := "normalized SQLite fixture"
		if file.RelativePath != databasePath {
			content = "validation fixture"
		}
		write(snapshot+"/"+file.RelativePath, content)
	}
	write(snapshot+"/.physical/"+databasePath, "private copy changed by SQLite")
	write(snapshot+"/.physical/"+databasePath+"-wal", "private WAL fixture")
	for _, name := range []string{"jobs/keep.txt", "backups/keep.zip", "backups/restore-recovery/keep.txt", restorePrefix + strings.Repeat("d", 32) + "/jobs/candidate.txt", snapshotPrefix + strings.Repeat("e", 32) + "/keep.txt"} {
		write(name, "protected fixture")
	}
	protected := validationFixtureFiles(t, dir, []string{snapshot})
	defer func() {
		// Recovery is deliberately injected by this test, never deleted by cleanup.
		if mode == "recovery" {
			protected[datahome.RestoreRecoveryPath] = "pending fixture"
		}
		if !reflect.DeepEqual(protected, validationFixtureFiles(t, dir, []string{snapshot})) {
			t.Error("cleanup changed active/archive/recovery/neighbor fixtures")
		}
	}()
	held := reservation
	if mode == "closed-reservation" {
		if q := reservation.Close(); q != nil {
			t.Fatal(q)
		}
	}
	if mode == "nil-reservation" {
		held = nil
	}
	if mode == "extra-file" {
		write(snapshot+"/.physical/unexpected.txt", "not produced")
	}
	if mode == "invalid-normalized" {
		normalized[0].SHA256 = strings.Repeat("0", 64)
	}
	witness, p := captureValidationCleanup(ctx, root, snapshot, held, normalized, true)
	if mode == "closed-reservation" || mode == "nil-reservation" || mode == "extra-file" || mode == "invalid-normalized" {
		if witness != nil || p == nil {
			t.Fatal("invalid producer witness accepted")
		}
		return
	}
	if p != nil || witness == nil || len(witness.files) != 4 {
		t.Fatal("valid copy witness not captured", p)
	}
	if q := reservation.Close(); q != nil {
		t.Fatal(q)
	}
	normalized[0].SHA256 = "caller mutated source slice"
	finish := restoreFinishResult{Complete: true, SemanticValidated: true, JournalRemoval: restoreRemoved,
		MarkerPublication: runtimepaths.FileNotPublished, Receipt: runtimepaths.StreamPublication{Outcome: runtimepaths.FilePublished, CleanupConfirmed: true}}
	ops := snapshotCleanupOperations{}
	want := ""
	switch mode {
	case "physical-drift":
		write(snapshot+"/.physical/"+databasePath, "foreign replacement")
		want = "BACKUP_CLEANUP_TREE_CHANGED"
	case "directory-drift":
		if err := os.Mkdir(filepath.Join(dir, filepath.FromSlash(snapshot+"/unexpected-empty")), 0700); err != nil {
			t.Fatal(err)
		}
		want = "BACKUP_CLEANUP_TREE_CHANGED"
	case "scope":
		witness.scope = strings.Repeat("0", 64)
		want = "BACKUP_CLEANUP_SCOPE_CHANGED"
	case "recovery":
		write(datahome.RestoreRecoveryPath, "pending fixture")
		want = "BACKUP_CLEANUP_RECOVERY_REQUIRED"
	case "unfinished":
		finish.Complete = false
		want = "BACKUP_RESTORE_COMMIT_UNCONFIRMED"
	case "commit-unknown":
		finish.JournalRemoval = restoreRemovalUnknown
		want = "BACKUP_RESTORE_COMMIT_UNCONFIRMED"
	case "receipt-unknown":
		finish.Receipt.Outcome = runtimepaths.FilePublicationUnknown
		want = "BACKUP_RESTORE_COMMIT_UNCONFIRMED"
	case "semantic-missing":
		finish.SemanticValidated = false
		want = "BACKUP_RESTORE_COMMIT_UNCONFIRMED"
	case "unknown-remove":
		ops.file = func(context.Context, *runtimepaths.Root, string, File) (runtimepaths.BackupRemovalOutcome, *runtimepaths.Error) {
			return runtimepaths.BackupRemovalUnknown, nil
		}
		want = "BACKUP_CLEANUP_UNCONFIRMED"
	case "late-remove-error":
		ops.file = func(ctx context.Context, root *runtimepaths.Root, name string, f File) (runtimepaths.BackupRemovalOutcome, *runtimepaths.Error) {
			outcome, q := root.RemoveBackupFile(ctx, name, f.SizeBytes, f.SHA256)
			if q != nil || outcome != runtimepaths.BackupRemoved {
				t.Fatal(q)
			}
			return outcome, &runtimepaths.Error{Code: "BACKUP_REMOVE_CANCELED"}
		}
		want = "BACKUP_CLEANUP_UNCONFIRMED"
	}
	before := validationFixtureFiles(t, dir, nil)
	result, p := cleanupValidationCopy(ctx, root, "stable", witness, finish, ops)
	if want == "" {
		if p != nil || !result.Complete || result.RemovedFiles != 4 || result.SnapshotRemoval != runtimepaths.BackupRemoved {
			t.Fatal("cleanup incomplete", result, p)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(snapshot))); !os.IsNotExist(err) {
			t.Fatal("snapshot retained after complete cleanup")
		}
	} else if p == nil || p.Code != want || result.Complete {
		t.Fatal("incorrect refusal", result, p, want)
	} else if mode == "late-remove-error" {
		if result.RemovedFiles != 1 || result.LastRemoval != runtimepaths.BackupRemoved || result.SnapshotRemoval != runtimepaths.BackupNotRemoved {
			t.Fatal("late removal facts lost", result)
		}
	} else if !reflect.DeepEqual(before, validationFixtureFiles(t, dir, nil)) {
		t.Fatal("preflight/unknown seam changed data")
	}
	if retried, p := cleanupValidationCopy(ctx, root, "stable", witness, finish, snapshotCleanupOperations{}); p == nil || p.Code != "BACKUP_CLEANUP_PERMIT_INVALID" || retried.Attempted {
		t.Fatal("consumed witness reused", retried, p)
	}
}

// Test-only recursive reads of this process's generated fixture. Exact excluded
// snapshot paths are the only bytes whose removal the calling test authorizes.
func validationFixtureFiles(t *testing.T, dir string, excluded []string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(dir, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, skip := range excluded {
			if rel == skip {
				return filepath.SkipDir
			}
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		result[rel] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// Verify production owner cleanup facts against physical copy absence. This does
// not call cleanup again: a consumed witness must not authorize a second attempt.
func cleanupRestoreValidationFixtures(t *testing.T, ctx context.Context, root *runtimepaths.Root, dir string, result RestoreResult) {
	t.Helper()
	phases := []RestoreValidationPhase{result.InitialValidation, result.ArchiveValidation, result.FinalValidation}
	excluded := []string{}
	for _, phase := range phases {
		if phase.cleanup != nil {
			if phase.cleanup.snapshot != phase.SnapshotRelativePath {
				t.Fatal("copy witness bound to wrong phase")
			}
			excluded = append(excluded, phase.SnapshotRelativePath)
		}
	}
	for _, phase := range phases {
		if phase.cleanup == nil {
			if phase.SnapshotRelativePath != "" && phase.Summary != nil {
				t.Fatal("completed validation lost its cleanup witness")
			}
			continue
		}
		out := phase.Cleanup
		if !phase.cleanup.used || !out.Complete || out.RemovedFiles != len(phase.cleanup.files) {
			t.Fatal("composed cleanup failed", out)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(phase.SnapshotRelativePath))); !os.IsNotExist(err) {
			t.Fatal("completed copy was not removed", err)
		}
	}
	if len(validationFixtureFiles(t, dir, excluded)["backups/input.zip"]) == 0 {
		t.Fatal("input archive protection was not exercised")
	}
}

// The callback runs only when the production owner reaches terminal cleanup.
// Preserve every byte outside validation copies (including recovery evidence).
func restoreCleanupFaultOperations(t *testing.T, dir, mode string) snapshotCleanupOperations {
	t.Helper()
	var protected map[string]string
	checkProtection := func() {
		t.Helper()
		got := validationFixtureFiles(t, dir, []string{strings.TrimSuffix(snapshotPrefix, "/")})
		if mode == "recovery" {
			delete(got, datahome.RestoreRecoveryPath)
		}
		if !reflect.DeepEqual(protected, got) {
			t.Error("cleanup changed protected active/history/archive bytes")
		}
	}
	ops := snapshotCleanupOperations{}
	ops.beforeRemove = func(index int) *Error {
		if protected == nil {
			protected = validationFixtureFiles(t, dir, []string{strings.TrimSuffix(snapshotPrefix, "/")})
			t.Cleanup(checkProtection)
		}
		if mode == "cancel" {
			return failure("BACKUP_CANCELED")
		}
		if mode == "recovery" {
			if err := os.WriteFile(filepath.Join(dir, datahome.RestoreRecoveryPath), []byte("pending synthetic recovery"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	if mode == "unknown" || mode == "late-error" {
		ops.file = func(ctx context.Context, root *runtimepaths.Root, name string, file File) (runtimepaths.BackupRemovalOutcome, *runtimepaths.Error) {
			// Exercise unknown AFTER a real deletion, not merely a no-op seam.
			out, q := root.RemoveBackupFile(ctx, name, file.SizeBytes, file.SHA256)
			if q != nil || out != runtimepaths.BackupRemoved {
				t.Fatal("fixture removal failed", out, q)
			}
			if mode == "unknown" {
				return runtimepaths.BackupRemovalUnknown, nil
			}
			return out, &runtimepaths.Error{Code: "BACKUP_REMOVE_CANCELED"}
		}
	}
	if mode == "root-late-error" {
		ops.directory = func(ctx context.Context, root *runtimepaths.Root, name string) (runtimepaths.BackupRemovalOutcome, *runtimepaths.Error) {
			out, q := root.RemoveEmptyBackupDirectory(ctx, name)
			if q != nil || out != runtimepaths.BackupRemoved {
				t.Fatal("fixture directory removal failed", out, q)
			}
			if len(strings.TrimPrefix(name, snapshotPrefix)) == 32 {
				return out, &runtimepaths.Error{Code: "BACKUP_REMOVE_CANCELED"}
			}
			return out, nil
		}
	}
	return ops
}

func assertRestoreCleanupFault(t *testing.T, dir, mode string, result RestoreResult, problem *Error, recovery bool) {
	t.Helper()
	want := "BACKUP_CLEANUP_UNCONFIRMED"
	if mode == "cancel" {
		want = "BACKUP_CANCELED"
	}
	if mode == "recovery" {
		want = "BACKUP_CLEANUP_RECOVERY_REQUIRED"
	}
	if problem == nil || problem.Code != want || !result.Finish.Complete || result.Finish.JournalRemoval != restoreRemoved {
		t.Fatal("cleanup failure lost independent commit facts", result, problem)
	}
	phase := result.InitialValidation
	if recovery {
		phase = result.FinalValidation
	}
	c := phase.Cleanup
	if c.Complete || phase.cleanup == nil || !phase.cleanup.used {
		t.Fatal("failed cleanup reported complete or reusable", c)
	}
	switch mode {
	case "unknown":
		if !c.Attempted || c.RemovedFiles != 0 || c.LastRemoval != runtimepaths.BackupRemovalUnknown {
			t.Fatal("unknown deletion fabricated counts", c)
		}
	case "late-error":
		if !c.Attempted || c.RemovedFiles != 1 || c.LastRemoval != runtimepaths.BackupRemoved {
			t.Fatal("known deletion lost", c)
		}
	case "root-late-error":
		if c.SnapshotRemoval != runtimepaths.BackupRemoved || c.RemovedFiles != phase.CleanupFiles {
			t.Fatal("root removal lost", c)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(phase.SnapshotRelativePath))); !os.IsNotExist(err) {
			t.Fatal("root removal not real", err)
		}
	default:
		if c.Attempted || c.RemovedFiles != 0 || c.LastRemoval != runtimepaths.BackupNotRemoved {
			t.Fatal("pre-delete refusal lost", c)
		}
	}
	if !recovery {
		for _, later := range []RestoreValidationPhase{result.ArchiveValidation, result.FinalValidation} {
			if later.Cleanup.Attempted || later.Cleanup.Complete {
				t.Fatal("cleanup continued after uncertain outcome")
			}
			if later.cleanup != nil {
				if later.cleanup.used {
					t.Fatal("unused witness consumed after failure")
				}
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(later.SnapshotRelativePath))); err != nil {
					t.Fatal("later copy lost", err)
				}
			}
		}
	}
}
