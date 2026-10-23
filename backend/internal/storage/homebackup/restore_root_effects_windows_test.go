//go:build windows

package homebackup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func runRestoreRootsHelper(t *testing.T, dir, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRestoreRootsHelper$")
	cmd.Env = append(os.Environ(), "CH_RESTORE_ROOT_HOME="+dir, "CH_RESTORE_ROOT_MODE="+mode)
	out, err := cmd.CombinedOutput()
	if strings.HasPrefix(mode, "crash-") {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 84 {
			return
		}
	}
	if err != nil || strings.HasPrefix(mode, "crash-") {
		t.Fatalf("roots %s: %v\n%s", mode, err, out)
	}
}

func TestRestoreRootEffectsNativeProcess(t *testing.T) {
	for _, mode := range []string{"normal", "new-root", "empty-original", "large-file", "deep-original", "deep-candidate", "physical-sqlite-sidecars", "hardlink-source", "no-confirm", "bad-late-root", "duplicate-placement",
		"source-drift", "journal-drift", "target-race", "not-moved-error", "moved-error", "unknown-move", "canceled-after-move", "extra-file", "missing-file"} {
		t.Run(mode, func(t *testing.T) { runRestoreRootsHelper(t, t.TempDir(), mode) })
	}
}

func TestRestoreRootCrashAndRestart(t *testing.T) {
	for _, direction := range []string{"activate", "rollback"} {
		for index := 0; index < 4; index++ {
			t.Run(fmt.Sprintf("%s-%d", direction, index), func(t *testing.T) {
				dir := t.TempDir()
				runRestoreRootsHelper(t, dir, fmt.Sprintf("crash-%s-%d", direction, index))
				runRestoreRootsHelper(t, dir, "resume-rollback")
			})
		}
	}
	// A second crash while undoing a previous process's partial activation must
	// remain recoverable, not require an in-memory progress counter.
	dir := t.TempDir()
	runRestoreRootsHelper(t, dir, "crash-activate-3")
	runRestoreRootsHelper(t, dir, "crash-resume-0")
	runRestoreRootsHelper(t, dir, "resume-rollback")
}

func TestRestoreRootsHelper(t *testing.T) {
	dir, mode := os.Getenv("CH_RESTORE_ROOT_HOME"), os.Getenv("CH_RESTORE_ROOT_MODE")
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
	resuming := mode == "resume-rollback" || strings.HasPrefix(mode, "crash-resume-")
	var j restoreJournal
	var evidence restoreEvidence
	original := map[string]string{"jobs/original.txt": "original job", "logs/original.txt": "original log"}
	candidate := map[string]string{"jobs/new.txt": "candidate job", "logs/new.txt": "candidate log"}
	if !resuming {
		if mode == "deep-original" {
			delete(original, "jobs/original.txt")
			original["jobs/"+strings.Repeat("d/", 62)+"original.txt"] = "deep original"
		}
		if mode == "deep-candidate" {
			delete(candidate, "jobs/new.txt")
			candidate["jobs/"+strings.Repeat("d/", 59)+"new.txt"] = "deep candidate"
		}
		if mode == "large-file" {
			original["jobs/original.txt"] = strings.Repeat("original ", 70000)
			candidate["jobs/new.txt"] = strings.Repeat("candidate ", 70000)
		}
		if mode == "new-root" {
			delete(original, "logs/original.txt")
		}
		if mode == "empty-original" {
			delete(original, "logs/original.txt")
			if err := os.MkdirAll(filepath.Join(dir, "logs/empty"), 0700); err != nil {
				t.Fatal(err)
			}
		}
		if mode == "physical-sqlite-sidecars" {
			original[databasePath], original[databasePath+"-wal"], original[databasePath+"-shm"] = "physical original database", "physical WAL", "physical SHM"
			candidate[databasePath] = "candidate standalone database"
		}
		before, after := testManifest(), testManifest()
		for name, value := range original {
			write(name, value)
			before.Files = append(before.Files, testFile(name, value))
		}
		id := strings.Repeat("a", 32)
		for name, value := range candidate {
			write(restorePrefix+id+"/"+name, value)
			after.Files = append(after.Files, testFile(name, value))
		}
		marker, p := datahome.MarkerBytes("stable", testHomeID)
		if p != nil {
			t.Fatal(p)
		}
		write(datahome.MarkerPath, string(marker))
		scope, pScope := root.CredentialScope()
		if pScope != nil {
			t.Fatal(pScope)
		}
		active := map[string]bool{"jobs": true, "logs": mode != "new-root"}
		if mode == "physical-sqlite-sidecars" {
			active["applications"] = true
		}
		var problem *Error
		j, evidence, problem = makeRestoreJournal(id, scope.Fingerprint(), marker, before, after, active)
		if problem != nil {
			t.Fatal(problem)
		}
		if result, p := publishRestoreJournal(ctx, root, j, evidence, restoreJournalOperations{}); p != nil || result.Journal.Outcome != runtimepaths.FilePublished {
			t.Fatal("journal setup", result, p)
		}
	} else {
		var p *Error
		j, evidence, p = readRestoreJournal(ctx, root, "stable")
		if p != nil {
			t.Fatal(p)
		}
	}
	assertRecovered := func() {
		t.Helper()
		for name, value := range original {
			raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
			if err != nil || string(raw) != value {
				t.Fatal("original not restored", name, err)
			}
		}
		if mode == "new-root" {
			if _, err := os.Stat(filepath.Join(dir, "logs")); !os.IsNotExist(err) {
				t.Fatal("new root survived rollback", err)
			}
		}
		if mode == "empty-original" {
			if info, err := os.Stat(filepath.Join(dir, "logs/empty")); err != nil || !info.IsDir() {
				t.Fatal("empty original directory lost", err)
			}
		}
		for name, value := range candidate {
			kept := 0
			for _, prefix := range []string{j.stagePath(), j.recoveryPath() + "/interrupted_candidate"} {
				raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(prefix+"/"+name)))
				if err == nil {
					if string(raw) != value {
						t.Fatal("candidate changed")
					}
					kept++
				}
			}
			if kept != 1 {
				t.Fatal("candidate lost or duplicated", name, kept)
			}
		}
		marker, err := os.ReadFile(filepath.Join(dir, datahome.MarkerPath))
		if err != nil || !bytes.Equal(marker, evidence.beforeMarker) {
			t.Fatal("root phase changed marker", err)
		}
		if _, p := datahome.Inspect(dir, nil); p == nil || p.Code != "DATA_HOME_RECOVERY_REQUIRED" {
			t.Fatal("root phase unblocked startup before final commit", p)
		}
	}
	rollbackOps := restoreRootOperations{}
	if strings.HasPrefix(mode, "crash-resume-") {
		rollbackOps.afterMove = func(index int, _ restoreRootMove) *Error {
			if index == 0 {
				os.Exit(84)
			}
			return nil
		}
	}
	if resuming {
		if result, p := moveRestoreRoots(ctx, root, "stable", true, true, rollbackOps); p != nil || !result.RootPhaseComplete {
			t.Fatal("restart rollback", result, p)
		}
		assertRecovered()
		return
	}
	if mode == "bad-late-root" {
		write(j.stagePath()+"/logs/new.txt", "wrong later root")
	}
	if mode == "hardlink-source" {
		if err := os.Link(filepath.Join(dir, "jobs/original.txt"), filepath.Join(dir, "fixture-link.txt")); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "duplicate-placement" {
		write(j.recoveryPath()+"/jobs/original.txt", "original job")
	}
	if mode == "extra-file" {
		write(j.stagePath()+"/jobs/extra.txt", "unexpected")
	}
	if mode == "missing-file" {
		if err := os.Rename(filepath.Join(dir, filepath.FromSlash(j.stagePath()+"/jobs/new.txt")), filepath.Join(dir, "fixture-retained-new.txt")); err != nil {
			t.Fatal(err)
		}
	}
	ops := restoreRootOperations{}
	if mode == "source-drift" || mode == "journal-drift" || mode == "target-race" {
		ops.beforeMove = func(index int, _ restoreRootMove) *Error {
			if index != 0 {
				return nil
			}
			switch mode {
			case "source-drift":
				write("jobs/original.txt", "altered job!")
			case "journal-drift":
				write(datahome.RestoreRecoveryPath, "{}")
			case "target-race":
				write(j.recoveryPath()+"/jobs/foreign.txt", "retain foreign bytes")
			}
			return nil
		}
	}
	if mode == "not-moved-error" || mode == "moved-error" || mode == "unknown-move" {
		ops.move = func(r *runtimepaths.Root, from, to string) (runtimepaths.DirectoryMoveOutcome, *runtimepaths.Error) {
			if mode == "not-moved-error" {
				return runtimepaths.DirectoryMoveNotMoved, &runtimepaths.Error{Code: "FIXTURE"}
			}
			outcome, p := r.MoveDirectoryNoReplace(from, to)
			if p != nil {
				t.Fatal(p)
			}
			if mode == "unknown-move" {
				outcome = runtimepaths.DirectoryMoveUnknown
			}
			return outcome, &runtimepaths.Error{Code: "FIXTURE"}
		}
	}
	crashDirection, crashIndex := "", -1
	if strings.HasPrefix(mode, "crash-") {
		parts := strings.Split(mode, "-")
		crashDirection = parts[1]
		crashIndex, _ = strconv.Atoi(parts[2])
	}
	ops.afterMove = func(index int, _ restoreRootMove) *Error {
		if crashDirection == "activate" && index == crashIndex {
			os.Exit(84)
		}
		if mode == "canceled-after-move" {
			cancel()
			return failure("BACKUP_CANCELED")
		}
		return nil
	}
	result, p := moveRestoreRoots(ctx, root, "stable", mode != "no-confirm", false, ops)
	expected := map[string]string{"no-confirm": "BACKUP_WRITER_CONFIRMATION_REQUIRED", "bad-late-root": "BACKUP_RESTORE_TREE_CHANGED",
		"deep-original": "BACKUP_RESTORE_PATH_LIMIT", "deep-candidate": "BACKUP_RESTORE_PATH_LIMIT",
		"hardlink-source":     "BACKUP_RESTORE_TREE_CHANGED",
		"duplicate-placement": "BACKUP_RESTORE_LAYOUT_INVALID", "source-drift": "BACKUP_RESTORE_TREE_CHANGED", "journal-drift": "BACKUP_RESTORE_JOURNAL_INVALID",
		"target-race": "BACKUP_RESTORE_LAYOUT_CHANGED", "not-moved-error": "BACKUP_RESTORE_MOVE_UNCONFIRMED", "moved-error": "BACKUP_RESTORE_MOVE_UNCONFIRMED",
		"unknown-move": "BACKUP_RESTORE_MOVE_UNCONFIRMED", "canceled-after-move": "BACKUP_CANCELED", "extra-file": "BACKUP_RESTORE_TREE_CHANGED", "missing-file": "BACKUP_RESTORE_TREE_CHANGED"}[mode]
	if expected != "" {
		if p == nil || p.Code != expected || result.RootPhaseComplete {
			t.Fatal("wrong refusal", result, p, expected)
		}
		if mode == "moved-error" || mode == "canceled-after-move" {
			if result.CompletedMoves != 1 || result.LastMove != runtimepaths.DirectoryMoveMoved {
				t.Fatal("lost committed move", result)
			}
		} else if result.CompletedMoves != 0 {
			t.Fatal("unexpected confirmed move", result)
		}
		if mode == "unknown-move" && result.LastMove != runtimepaths.DirectoryMoveUnknown {
			t.Fatal("lost unknown outcome")
		}
		if mode == "not-moved-error" || mode == "moved-error" || mode == "unknown-move" || mode == "canceled-after-move" || mode == "deep-original" || mode == "deep-candidate" {
			recovered, p := moveRestoreRoots(context.Background(), root, "stable", true, true, restoreRootOperations{})
			if p != nil || !recovered.RootPhaseComplete {
				t.Fatal("explicit recovery after uncertain call", recovered, p)
			}
			assertRecovered()
		} else {
			// These failures occur before any root move; no later root is changed.
			raw, err := os.ReadFile(filepath.Join(dir, "logs/original.txt"))
			if err != nil || string(raw) != "original log" {
				t.Fatal("late root changed", err)
			}
		}
		return
	}
	if p != nil || !result.RootPhaseComplete {
		t.Fatal("activation failed", result, p)
	}
	for name, value := range candidate {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil || string(raw) != value {
			t.Fatal("candidate not activated", name, err)
		}
	}
	if _, p := moveRestoreRoots(ctx, root, "stable", true, false, restoreRootOperations{}); p == nil || p.Code != "BACKUP_RESTORE_ROLLBACK_REQUIRED" {
		t.Fatal("activation retried", p)
	}
	if crashDirection == "rollback" {
		rollbackOps.afterMove = func(index int, _ restoreRootMove) *Error {
			if index == crashIndex {
				os.Exit(84)
			}
			return nil
		}
	}
	rolled, p := moveRestoreRoots(ctx, root, "stable", true, true, rollbackOps)
	if p != nil || !rolled.RootPhaseComplete {
		t.Fatal("rollback failed", rolled, p)
	}
	assertRecovered()
	again, p := moveRestoreRoots(ctx, root, "stable", true, true, restoreRootOperations{})
	if p != nil || !again.RootPhaseComplete || again.CompletedMoves != 0 {
		t.Fatal("rollback repeat not idempotent", again, p)
	}
}
