//go:build windows

package homebackup

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestSnapshotCleanupNative(t *testing.T) {
	for _, mode := range []string{
		"complete", "empty", "unowned", "canceled", "invalid-channel", "missing-marker", "invalid-marker",
		"invalid-target", "invalid-witness", "prepare-extra", "prepare-missing", "prepare-hash",
		"drift-file", "drift-directory", "scope", "nil-permit", "late-recovery", "late-cancel",
		"late-file", "late-child", "unknown-file", "committed-file-error", "unknown-directory", "terminal-cancel",
		"recovery-document", "recovery-restore", "recovery-registry", "recovery-evidence", "recovery-discovery",
		"recovery-resume", "recovery-intake", "recovery-new-job", "recovery-email", "recovery-approval",
	} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSnapshotCleanupHelper$")
			cmd.Env = append(os.Environ(), "CH_SNAPSHOT_CLEANUP_MODE="+mode, "CH_SNAPSHOT_CLEANUP_HOME="+t.TempDir())
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
		})
	}
}

func TestSnapshotCleanupHelper(t *testing.T) {
	mode, dir := os.Getenv("CH_SNAPSHOT_CLEANUP_MODE"), os.Getenv("CH_SNAPSHOT_CLEANUP_HOME")
	if mode == "" {
		t.Skip("subprocess only")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	assertText := func(name, text string) {
		t.Helper()
		bytes, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil || string(bytes) != text {
			t.Fatalf("retained fixture %s changed: %v", name, err)
		}
	}
	marker, p := datahome.MarkerBytes("stable", testHomeID)
	if p != nil {
		t.Fatal(p)
	}
	if mode != "missing-marker" {
		write(datahome.MarkerPath, string(marker))
	}
	if mode == "invalid-marker" {
		write(datahome.MarkerPath, "{}")
	}
	// All destruction is confined to this generated fixture. Neighboring stages,
	// originals, archives, and rollback history must remain byte-identical.
	protected := []string{"jobs/active.txt", "backups/keep.zip", "backups/restore-recovery/keep/original.txt", snapshotPrefix + strings.Repeat("b", 32) + "/keep.txt"}
	for _, name := range protected {
		write(name, "protected fixture")
	}
	t.Cleanup(func() {
		for _, name := range protected {
			assertText(name, "protected fixture")
		}
	})
	snapshot := snapshotPrefix + strings.Repeat("a", 32)
	files := []File{}
	if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(snapshot+"/empty/nested")), 0700); err != nil {
		t.Fatal(err)
	}
	if mode != "empty" {
		for _, name := range []string{"b.txt", "a.txt"} { // Deliberately unsorted witness.
			write(snapshot+"/"+name, name)
			files = append(files, File{RelativePath: name, SizeBytes: int64(len(name)), SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(name)))})
		}
	}
	root, q := runtimepaths.Open(dir)
	if q != nil {
		t.Fatal(q)
	}
	defer root.Close()
	if mode != "unowned" {
		if q := root.ClaimForProcess(); q != nil {
			t.Fatal(q)
		}
	}
	channel, target := "stable", snapshot
	wantPrepare := ""
	switch mode {
	case "unowned":
		wantPrepare = "BACKUP_OWNERSHIP_REQUIRED"
	case "canceled":
		cancel()
		wantPrepare = "BACKUP_CANCELED"
	case "invalid-channel":
		channel, wantPrepare = "other", "BACKUP_CHANNEL_INVALID"
	case "missing-marker", "invalid-marker":
		wantPrepare = "BACKUP_CLEANUP_RECOVERY_REQUIRED"
	case "invalid-target":
		for _, invalid := range []string{"jobs", "backups/restore-recovery", snapshot + "/../other", snapshot + "/", snapshotPrefix + strings.Repeat("A", 32)} {
			permit, problem := prepareSnapshotCleanup(ctx, root, channel, invalid, files)
			if permit != nil || problem == nil || problem.Code != "BACKUP_CLEANUP_TARGET_INVALID" {
				t.Fatalf("accepted target %q: %v", invalid, problem)
			}
		}
		return
	case "invalid-witness":
		for _, invalid := range [][]File{
			nil, {files[0], files[0]}, {{RelativePath: "../escape", SizeBytes: 1, SHA256: files[0].SHA256}},
			{{RelativePath: "a.txt", SizeBytes: -1, SHA256: files[0].SHA256}},
			{{RelativePath: "a.txt", SizeBytes: MaxEntryBytes + 1, SHA256: files[0].SHA256}},
			{{RelativePath: "a.txt", SizeBytes: 5, SHA256: "invalid"}},
		} {
			permit, problem := prepareSnapshotCleanup(ctx, root, channel, target, invalid)
			if permit != nil || problem == nil {
				t.Fatal("accepted invalid witness")
			}
		}
		return
	case "prepare-extra":
		write(snapshot+"/foreign", "retain")
		wantPrepare = "BACKUP_CLEANUP_TREE_CHANGED"
	case "prepare-missing":
		files[0].RelativePath = "missing.txt"
		wantPrepare = "BACKUP_CLEANUP_TREE_CHANGED"
	case "prepare-hash":
		files[0].SHA256 = strings.Repeat("0", 64)
		wantPrepare = "BACKUP_CLEANUP_TREE_CHANGED"
	}
	recovery := map[string]string{
		"recovery-document": datahome.DocumentRecoveryPath, "recovery-restore": datahome.RestoreRecoveryPath,
		"recovery-registry": datahome.RegistryRecoveryPath, "recovery-evidence": datahome.EvidenceRecoveryPath,
		"recovery-discovery": datahome.DiscoveryRecoveryPath, "recovery-resume": datahome.ResumeRecoveryPath,
		"recovery-intake": datahome.IntakeRecoveryPath, "recovery-new-job": datahome.NewJobRecoveryPath,
		"recovery-email": datahome.EmailCredentialRecoveryPath, "recovery-approval": datahome.ApprovalRecoveryPath,
	}
	permit, problem := prepareSnapshotCleanup(ctx, root, channel, target, files)
	if wantPrepare != "" {
		if permit != nil || problem == nil || problem.Code != wantPrepare {
			t.Fatalf("prepare: %v, want %s", problem, wantPrepare)
		}
		assertText(snapshot+"/a.txt", "a.txt")
		assertText(snapshot+"/b.txt", "b.txt")
		return
	}
	if problem != nil {
		t.Fatal(problem)
	}
	// Caller mutation cannot change the prepared witness.
	if len(files) != 0 {
		files[0].SHA256 = "caller changed"
	}
	ops := snapshotCleanupOperations{}
	wantCode, wantFiles, wantDirs := "", 2, 3
	wantSnapshot := runtimepaths.BackupRemoved
	if mode == "empty" {
		wantFiles = 0
	}
	if path, ok := recovery[mode]; ok {
		write(path, "pending recovery")
		wantCode, wantFiles, wantDirs, wantSnapshot = "BACKUP_CLEANUP_RECOVERY_REQUIRED", 0, 0, runtimepaths.BackupNotRemoved
		t.Cleanup(func() { assertText(path, "pending recovery") })
	}
	switch mode {
	case "drift-file":
		write(snapshot+"/a.txt", "other")
		wantCode = "BACKUP_CLEANUP_TREE_CHANGED"
	case "drift-directory":
		if err := os.Mkdir(filepath.Join(dir, filepath.FromSlash(snapshot+"/new")), 0700); err != nil {
			t.Fatal(err)
		}
		wantCode = "BACKUP_CLEANUP_TREE_CHANGED"
	case "scope":
		permit.scope = "different physical Home"
		wantCode = "BACKUP_CLEANUP_SCOPE_CHANGED"
	case "nil-permit":
		permit = nil
		wantCode = "BACKUP_CLEANUP_PERMIT_INVALID"
	case "late-recovery", "late-cancel", "late-file":
		wantCode, wantFiles = "BACKUP_CLEANUP_UNCONFIRMED", 1
		if mode == "late-recovery" {
			wantCode = "BACKUP_CLEANUP_RECOVERY_REQUIRED"
		} else if mode == "late-cancel" {
			wantCode = "BACKUP_CANCELED"
		}
		ops.beforeRemove = func(index int) *Error {
			if index == 1 {
				switch mode {
				case "late-recovery":
					write(datahome.RestoreRecoveryPath, "pending recovery")
				case "late-cancel":
					cancel()
				case "late-file":
					write(snapshot+"/b.txt", "other")
				}
			}
			return nil
		}
	case "late-child":
		wantCode = "BACKUP_CLEANUP_UNCONFIRMED"
		ops.beforeRemove = func(index int) *Error {
			if index == 2 {
				write(snapshot+"/empty/nested/late", "retain")
			}
			return nil
		}
	case "unknown-file", "committed-file-error":
		wantCode, wantFiles = "BACKUP_CLEANUP_UNCONFIRMED", 0
		ops.file = func(ctx context.Context, r *runtimepaths.Root, path string, file File) (runtimepaths.BackupRemovalOutcome, *runtimepaths.Error) {
			if mode == "unknown-file" {
				return runtimepaths.BackupRemovalUnknown, nil // Composition only; native ambiguity tested in runtimepaths.
			}
			outcome, q := r.RemoveBackupFile(ctx, path, file.SizeBytes, file.SHA256)
			if q != nil {
				t.Fatal(q)
			}
			return outcome, &runtimepaths.Error{Code: "SYNTHETIC_LATE_ERROR"}
		}
		if mode == "committed-file-error" {
			wantFiles = 1
		}
	case "unknown-directory", "terminal-cancel":
		ops.directory = func(ctx context.Context, r *runtimepaths.Root, path string) (runtimepaths.BackupRemovalOutcome, *runtimepaths.Error) {
			if path == snapshot && mode == "unknown-directory" {
				return runtimepaths.BackupRemovalUnknown, nil
			}
			outcome, q := r.RemoveEmptyBackupDirectory(ctx, path)
			if path == snapshot {
				cancel()
			}
			return outcome, q
		}
		wantCode = "BACKUP_CANCELED"
		if mode == "unknown-directory" {
			wantCode, wantDirs, wantSnapshot = "BACKUP_CLEANUP_UNCONFIRMED", 2, runtimepaths.BackupRemovalUnknown
		}
	}
	switch mode {
	case "drift-file", "drift-directory", "scope", "nil-permit":
		wantFiles, wantDirs, wantSnapshot = 0, 0, runtimepaths.BackupNotRemoved
	case "late-recovery", "late-cancel", "late-file", "late-child", "unknown-file", "committed-file-error":
		wantDirs, wantSnapshot = 0, runtimepaths.BackupNotRemoved
	}
	result, problem := consumeSnapshotCleanup(ctx, root, permit, ops)
	if (problem == nil) != (wantCode == "") || problem != nil && problem.Code != wantCode {
		t.Fatalf("consume: %v, want %s; facts %+v", problem, wantCode, result)
	}
	if result.Complete != (wantCode == "") || result.RemovedFiles != wantFiles || result.RemovedDirectories != wantDirs || result.SnapshotRemoval != wantSnapshot {
		t.Fatalf("incorrect facts: %+v; want files=%d dirs=%d snapshot=%s", result, wantFiles, wantDirs, wantSnapshot)
	}
	if result.Attempted != (wantFiles > 0 || wantDirs > 0 || mode == "unknown-file") {
		t.Fatalf("incorrect attempt fact: %+v", result)
	}
	if wantFiles == 0 && mode != "empty" && mode != "drift-file" {
		assertText(snapshot+"/a.txt", "a.txt")
	}
	if wantFiles < 2 && mode != "empty" {
		text := "b.txt"
		if mode == "late-file" {
			text = "other"
		}
		assertText(snapshot+"/b.txt", text)
	}
	if wantSnapshot == runtimepaths.BackupRemoved {
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(snapshot))); !os.IsNotExist(err) {
			t.Fatalf("snapshot still exists: %v", err)
		}
	}
	// Neither a successful nor a failed/ambiguous attempt may reuse its permit.
	repeated, problem := consumeSnapshotCleanup(context.Background(), root, permit, snapshotCleanupOperations{})
	if problem == nil || problem.Code != "BACKUP_CLEANUP_PERMIT_INVALID" || repeated != unstartedSnapshotCleanup() {
		t.Fatalf("permit reused: %+v, %v", repeated, problem)
	}
}
