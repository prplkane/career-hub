//go:build windows

package homebackup

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func runRestoreJournalHelper(t *testing.T, home, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRestoreJournalHelper$")
	cmd.Env = append(os.Environ(), "CH_RESTORE_JOURNAL_HOME="+home, "CH_RESTORE_JOURNAL_MODE="+mode)
	if out, err := cmd.CombinedOutput(); err != nil {
		if exited, ok := err.(*exec.ExitError); ok && strings.HasPrefix(mode, "crash-") && exited.ExitCode() == 83 {
			return
		}
		t.Fatalf("journal %s: %v\n%s", mode, err, out)
	}
	if strings.HasPrefix(mode, "crash-") {
		t.Fatal("fixture did not exit at crash boundary")
	}
}

func TestRestoreJournalRestartNativeProcess(t *testing.T) {
	for _, boundary := range []string{"evidence", "journal"} {
		t.Run(boundary, func(t *testing.T) {
			dir := t.TempDir()
			runRestoreJournalHelper(t, dir, "crash-"+boundary)
			runRestoreJournalHelper(t, dir, "restart-"+boundary)
		})
	}
}

func TestRestoreJournalNativeProcess(t *testing.T) {
	for _, mode := range []string{"normal", "foreign-scope", "marker-drift", "stage-missing", "active-drift", "active-missing", "other-journal", "existing-recovery",
		"interrupt-before-journal", "evidence-drift", "journal-collision", "published-error", "unknown-publication", "cancel-after-journal",
		"read-corrupt-evidence", "read-wrong-channel", "read-legacy", "read-valid-legacy", "read-other-journal", "read-marker-drift", "unowned"} {
		t.Run(mode, func(t *testing.T) { runRestoreJournalHelper(t, t.TempDir(), mode) })
	}
}

func TestRestoreJournalCopiedHomeRefused(t *testing.T) {
	source, copied := t.TempDir(), t.TempDir()
	runRestoreJournalHelper(t, source, "normal")
	// Copy only this test's generated files after its helper exited. The second
	// process owns the clone but must not treat its copied journal as authority.
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(copied, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	runRestoreJournalHelper(t, copied, "read-clone")
}

func TestRestoreJournalHelper(t *testing.T) {
	dir, mode := os.Getenv("CH_RESTORE_JOURNAL_HOME"), os.Getenv("CH_RESTORE_JOURNAL_MODE")
	if dir == "" || mode == "" {
		return
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
	root, pathProblem := runtimepaths.Open(dir)
	if pathProblem != nil {
		t.Fatal(pathProblem)
	}
	defer root.Close()
	if mode != "unowned" {
		if pathProblem = root.ClaimForProcess(); pathProblem != nil {
			t.Fatal(pathProblem)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if strings.HasPrefix(mode, "restart-") {
		j, _ := journalFixture(t)
		for _, item := range []string{"original_marker.json", "target_marker.json", "original_manifest.json", "target_manifest.json"} {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(j.recoveryPath()+"/"+item))); err != nil {
				t.Fatal("lost crash evidence", err)
			}
		}
		if mode == "restart-evidence" {
			if _, err := os.Stat(filepath.Join(dir, datahome.RestoreRecoveryPath)); !os.IsNotExist(err) {
				t.Fatal("journal published before boundary", err)
			}
			if _, p := datahome.Inspect(dir, nil); p != nil {
				t.Fatal("pre-intent preparation blocked unchanged Home", p)
			}
		} else {
			got, _, p := readRestoreJournal(ctx, root, "stable")
			if p != nil || got.RestoreID != j.RestoreID {
				t.Fatal("restart lost scoped intent", p)
			}
			if _, p := datahome.Inspect(dir, nil); p == nil || p.Code != "DATA_HOME_RECOVERY_REQUIRED" {
				t.Fatal("pending restore admitted", p)
			}
		}
		active, err := os.ReadFile(filepath.Join(dir, "jobs/original.txt"))
		if err != nil || string(active) != "original" {
			t.Fatal("crash changed active data", err)
		}
		return
	}
	if mode == "read-clone" {
		original, err := os.ReadFile(filepath.Join(dir, datahome.RestoreRecoveryPath))
		if err != nil {
			t.Fatal(err)
		}
		j, e, p := readRestoreJournal(ctx, root, "stable")
		if p == nil || p.Code != "BACKUP_RESTORE_SCOPE_MISMATCH" || j.RestoreID != "" || e.beforeMarker != nil {
			t.Fatal("clone admitted", p)
		}
		after, err := os.ReadFile(filepath.Join(dir, datahome.RestoreRecoveryPath))
		if err != nil || !bytes.Equal(original, after) {
			t.Fatal("clone evidence changed", err)
		}
		return
	}
	j, evidence := journalFixture(t)
	if mode != "unowned" {
		scope, p := root.CredentialScope()
		if p != nil {
			t.Fatal(p)
		}
		j.RootFingerprint = scope.Fingerprint()
	}
	write(datahome.MarkerPath, evidence.beforeMarker)
	write("jobs/original.txt", []byte("original"))
	if mode != "stage-missing" {
		write(j.stagePath()+"/jobs/new.txt", []byte("new"))
	}
	if mode == "foreign-scope" {
		j.RootFingerprint = strings.Repeat("c", 64)
	}
	if mode == "marker-drift" {
		other, _ := datahome.MarkerBytes("stable", "dh_"+strings.Repeat("f", 32))
		write(datahome.MarkerPath, other)
	}
	if mode == "active-drift" {
		j.Roots[0].HadActive = false
	}
	activePath := "jobs/original.txt"
	if mode == "active-missing" {
		if err := os.Rename(filepath.Join(dir, "jobs"), filepath.Join(dir, "fixture-retained-jobs")); err != nil {
			t.Fatal(err)
		}
		activePath = "fixture-retained-jobs/original.txt"
	}
	if mode == "other-journal" {
		write(datahome.RegistryRecoveryPath, []byte("pending"))
	}
	if mode == "existing-recovery" {
		write(j.recoveryPath()+"/foreign.txt", []byte("retain"))
	}
	ops := restoreJournalOperations{}
	if mode == "interrupt-before-journal" {
		ops.afterEvidence = func() *Error { return failure("BACKUP_FIXTURE_INTERRUPTED") }
	}
	if mode == "crash-evidence" {
		ops.afterEvidence = func() *Error { os.Exit(83); return nil }
	}
	if mode == "evidence-drift" {
		ops.afterEvidence = func() *Error { write(j.recoveryPath()+"/original_manifest.json", []byte("{}")); return nil }
	}
	ops.publish = func(ctx context.Context, r *runtimepaths.Root, path string, raw []byte) (runtimepaths.StreamPublication, *Error) {
		// The production implementation must hold no-write leases on all evidence.
		for _, item := range restoreEvidenceFiles(evidence) {
			f, err := os.OpenFile(filepath.Join(dir, filepath.FromSlash(j.recoveryPath()+"/"+item.name)), os.O_WRONLY, 0600)
			if err == nil {
				f.Close()
				t.Fatal("evidence writer admitted during journal publication")
			}
		}
		if f, err := os.OpenFile(filepath.Join(dir, datahome.MarkerPath), os.O_WRONLY, 0600); err == nil {
			f.Close()
			t.Fatal("marker writer admitted during journal publication")
		}
		if mode == "journal-collision" {
			write(path, []byte("foreign journal"))
		}
		result, p := publishRestoreBytes(ctx, r, path, raw)
		if p != nil {
			return result, p
		}
		if mode == "crash-journal" {
			os.Exit(83)
		}
		if mode == "published-error" {
			return result, failure("BACKUP_FIXTURE_LATE_ERROR")
		}
		if mode == "unknown-publication" {
			result.Outcome = runtimepaths.FilePublicationUnknown
			return result, failure("BACKUP_FIXTURE_UNKNOWN")
		}
		if mode == "cancel-after-journal" {
			cancel()
		}
		return result, nil
	}
	result, p := publishRestoreJournal(ctx, root, j, evidence, ops)
	expected := map[string]string{"foreign-scope": "BACKUP_RESTORE_SCOPE_MISMATCH", "marker-drift": "BACKUP_RESTORE_HOME_CHANGED",
		"stage-missing": "BACKUP_RESTORE_STAGE_CHANGED", "active-drift": "BACKUP_RESTORE_EVIDENCE_INVALID", "active-missing": "BACKUP_RESTORE_HOME_CHANGED", "other-journal": "BACKUP_RESTORE_RECOVERY_REQUIRED",
		"existing-recovery": "BACKUP_RESTORE_RESERVATION_FAILED", "interrupt-before-journal": "BACKUP_FIXTURE_INTERRUPTED",
		"evidence-drift": "BACKUP_RESTORE_EVIDENCE_CHANGED", "journal-collision": "BACKUP_RESTORE_PUBLICATION_FAILED",
		"published-error": "BACKUP_FIXTURE_LATE_ERROR", "unknown-publication": "BACKUP_FIXTURE_UNKNOWN", "cancel-after-journal": "BACKUP_CANCELED",
		"unowned": "BACKUP_OWNERSHIP_REQUIRED"}[mode]
	if expected == "" && p != nil || expected != "" && (p == nil || p.Code != expected) {
		t.Fatalf("want %s, got %v", expected, p)
	}
	published := expected == "" || mode == "published-error" || mode == "unknown-publication" || mode == "cancel-after-journal"
	prepared := published || mode == "interrupt-before-journal" || mode == "evidence-drift" || mode == "journal-collision"
	if prepared {
		if result.Namespace != runtimepaths.DirectoryCreated || result.EvidenceFiles != 4 || result.LastEvidence.Outcome != runtimepaths.FilePublished {
			t.Fatal("lost evidence facts", result)
		}
	} else if result.Namespace != runtimepaths.DirectoryNotCreated || result.EvidenceFiles != 0 {
		t.Fatal("unexpected preparation", result)
	}
	wantOutcome := runtimepaths.FileNotPublished
	if published {
		wantOutcome = runtimepaths.FilePublished
	}
	if mode == "unknown-publication" {
		wantOutcome = runtimepaths.FilePublicationUnknown
	}
	if result.Journal.Outcome != wantOutcome {
		t.Fatal("lost journal outcome", result)
	}
	if !prepared && mode != "existing-recovery" {
		if _, err := os.Stat(filepath.Join(dir, "backups/restore-recovery")); !os.IsNotExist(err) {
			t.Fatal("preflight wrote recovery directory", err)
		}
	}
	if published {
		if _, p := datahome.Inspect(dir, nil); p == nil || p.Code != "DATA_HOME_RECOVERY_REQUIRED" {
			t.Fatal("normal startup admitted pending restore", p)
		}
		readChannel := "stable"
		readCode := ""
		switch mode {
		case "read-corrupt-evidence":
			write(j.recoveryPath()+"/original_manifest.json", []byte("{}"))
			readCode = "BACKUP_RESTORE_EVIDENCE_INVALID"
		case "read-wrong-channel":
			readChannel = "dev"
			readCode = "BACKUP_RESTORE_RECOVERY_REQUIRED"
		case "read-legacy":
			write(datahome.RestoreRecoveryPath, []byte(`{"version":1}`))
			readCode = "BACKUP_RESTORE_JOURNAL_INVALID"
		case "read-valid-legacy":
			write(datahome.RestoreRecoveryPath, legacyRestoreFixture())
			readCode = "BACKUP_RESTORE_LEGACY_RECOVERY_REQUIRED"
		case "read-other-journal":
			write(datahome.RegistryRecoveryPath, []byte("pending"))
			readCode = "BACKUP_RESTORE_RECOVERY_REQUIRED"
		case "read-marker-drift":
			other, _ := datahome.MarkerBytes("stable", "dh_"+strings.Repeat("f", 32))
			write(datahome.MarkerPath, other)
			readCode = "BACKUP_RESTORE_HOME_CHANGED"
		}
		got, e, p := readRestoreJournal(context.Background(), root, readChannel)
		if readCode == "" {
			if p != nil || got.RestoreID != j.RestoreID || !bytes.Equal(e.beforeMarker, evidence.beforeMarker) {
				t.Fatal("journal reread failed", p)
			}
		} else if p == nil || p.Code != readCode || got.RestoreID != "" || e.beforeMarker != nil {
			t.Fatal("bad journal read accepted", p)
		}
	}
	active, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(activePath)))
	if err != nil || string(active) != "original" {
		t.Fatal("active payload changed", err)
	}
	if mode != "marker-drift" && mode != "read-marker-drift" {
		marker, err := os.ReadFile(filepath.Join(dir, datahome.MarkerPath))
		if err != nil || !bytes.Equal(marker, evidence.beforeMarker) {
			t.Fatal("active marker changed", err)
		}
	}
	if prepared {
		// Every journal/read path must release native handles, without deleting evidence.
		f, err := os.OpenFile(filepath.Join(dir, filepath.FromSlash(j.recoveryPath()+"/original_marker.json")), os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal("evidence lease leaked", err)
		}
		f.Close()
	}
}
