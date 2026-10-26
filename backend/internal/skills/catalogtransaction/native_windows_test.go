//go:build windows

package catalogtransaction

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type nativeCrashRoot struct {
	*runtimepaths.Root
	stop string
}

func (r nativeCrashRoot) CreateFile(path string, raw []byte) (bool, *runtimepaths.Error) {
	saved, e := r.Root.CreateFile(path, raw)
	if e == nil && saved && ((path == JournalPath || path == EvidenceJournalPath || path == ApprovalJournalPath) && r.stop == "intent" || path == r.stop) {
		os.Exit(86)
	}
	return saved, e
}
func (r nativeCrashRoot) ReplaceFile(path string, before, after []byte) (bool, *runtimepaths.Error) {
	saved, e := r.Root.ReplaceFile(path, before, after)
	if e == nil && saved && path == r.stop {
		os.Exit(86)
	}
	if e == nil && saved && (path == vaultPaths[3] || path == vaultPaths[4]) && r.stop == "record" {
		os.Exit(86)
	}
	if e == nil && saved && ((path == catalogstore.CustomGroupsPath && r.stop == "groups") || (path == catalogstore.CustomSkillsPath && r.stop == "skills")) {
		os.Exit(86)
	}
	return saved, e
}
func (r nativeCrashRoot) RemoveFile(path string, raw []byte) (bool, *runtimepaths.Error) {
	removed, e := r.Root.RemoveFile(path, raw)
	if e == nil && removed && r.stop == "cleanup" {
		os.Exit(86)
	}
	return removed, e
}

type nativeTransactionRequest struct{ Root, Action string }

func TestNativeTransactionProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_TRANSACTION_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var request nativeTransactionRequest
	if json.NewDecoder(os.Stdin).Decode(&request) != nil {
		t.Fatal("request")
	}
	root, e := runtimepaths.Open(request.Root)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e := root.ClaimForProcess(); e != nil {
		t.Fatal(e)
	}
	var report Report
	var err error
	if strings.HasPrefix(request.Action, "approval:") {
		report, err = nativeApprovalAction(t, root, strings.TrimPrefix(request.Action, "approval:"))
	} else if strings.HasPrefix(request.Action, "evidence:") {
		action := strings.TrimPrefix(request.Action, "evidence:")
		_, data := evidenceFixture(false, false)
		switch action {
		case "recover":
			report, err = RecoverEvidenceOwned(context.Background(), root, testHomeID, accept)
		case "begin":
			report, err = BeginEvidenceOwned(context.Background(), root, testHomeID, data.Changes, data.Guards, accept)
		case "conflict":
			report, err = RecoverEvidenceOwned(context.Background(), root, testHomeID, accept)
			if err != ErrConflict || !report.IntentRecorded || len(report.AppliedFiles) != 0 {
				t.Fatal("native recovery overwrote a changed guard")
			}
			err = nil
		case "probe":
			if _, e := datahome.InspectOwnedForRegistryRecovery(root, nil); e == nil || e.Code != "DATA_HOME_RECOVERY_REQUIRED" {
				t.Fatal("registry ownership admitted pending evidence")
			}
			if _, e := BeginOwned(context.Background(), root, testHomeID, data.Changes[1:], accept); e != ErrInput {
				t.Fatal("registry writer admitted pending evidence")
			}
		default:
			report, err = beginIntent(context.Background(), nativeCrashRoot{root, action}, data, accept)
		}
	} else if request.Action == "recover" {
		report, err = RecoverOwned(context.Background(), root, testHomeID, accept)
	} else {
		_, changes := fixture(false)
		report, err = begin(context.Background(), nativeCrashRoot{root, request.Action}, testHomeID, changes, accept)
	}
	if err != nil {
		t.Fatal(err)
	}
	if json.NewEncoder(os.Stdout).Encode(report) != nil {
		t.Fatal("report")
	}
	os.Exit(0)
}
func runNativeTransaction(t *testing.T, root, action string, crash bool) Report {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeTransactionProcessHelper$")
	cmd.Env = append(os.Environ(), "CAREERHUB_TRANSACTION_HELPER=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = time.Second
	raw, _ := json.Marshal(nativeTransactionRequest{root, action})
	cmd.Stdin = bytes.NewReader(raw)
	// This helper emits only one small fixed-field report or a Go test failure.
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if crash {
		if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 86 || ctx.Err() != nil {
			t.Fatalf("wrong crash boundary %s: %v %.1500s", action, err, out.Bytes())
		}
		return Report{}
	}
	if err != nil {
		t.Fatalf("native helper %s: %v %.1500s", action, err, out.Bytes())
	}
	var report Report
	if json.Unmarshal(out.Bytes(), &report) != nil {
		t.Fatal("report")
	}
	return report
}
func TestNativeTransactionProcessExitAndRestart(t *testing.T) {
	for _, stop := range []string{"intent", "groups", "skills", "cleanup"} {
		t.Run(stop, func(t *testing.T) {
			dir, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			marker := fmt.Sprintf(`{"application":"CareerHub","channel":"dev","data_home_id":"%s","format_version":2}`, testHomeID)
			if os.WriteFile(filepath.Join(dir, datahome.MarkerPath), []byte(marker), 0600) != nil {
				t.Fatal("marker")
			}
			_, changes := fixture(false)
			for _, change := range changes {
				p := filepath.Join(dir, filepath.FromSlash(change.Path))
				if os.MkdirAll(filepath.Dir(p), 0700) != nil || os.WriteFile(p, change.Before, 0600) != nil {
					t.Fatal("fixture")
				}
			}
			unrelated := filepath.Join(dir, "history-do-not-change.txt")
			if os.WriteFile(unrelated, []byte("synthetic history"), 0600) != nil {
				t.Fatal("history")
			}
			runNativeTransaction(t, dir, stop, true)
			_, admissionErr := datahome.Inspect(dir, nil)
			if stop == "cleanup" {
				if admissionErr != nil {
					t.Fatal("completed cleanup blocked admission")
				}
			} else if admissionErr == nil || admissionErr.Code != "DATA_HOME_RECOVERY_REQUIRED" {
				t.Fatal("partial publication admitted")
			}
			report := runNativeTransaction(t, dir, "recover", false)
			if stop == "cleanup" {
				if report.IntentRecorded || report.Completed {
					t.Fatal("absent intent fabricated")
				}
			} else if !report.Completed || !report.IntentRecorded || !report.JournalRemoved {
				t.Fatalf("recovery incomplete: %#v", report)
			}
			for _, change := range changes {
				raw, e := os.ReadFile(filepath.Join(dir, filepath.FromSlash(change.Path)))
				if e != nil || !bytes.Equal(raw, change.After) {
					t.Fatal("restart lost after-state")
				}
			}
			again := runNativeTransaction(t, dir, "recover", false)
			if again.IntentRecorded || again.Completed || len(again.AppliedFiles) != 0 {
				t.Fatal("non-idempotent recovery")
			}
			raw, e := os.ReadFile(unrelated)
			if e != nil || string(raw) != "synthetic history" {
				t.Fatal("unrelated history changed")
			}
			if _, e := datahome.Inspect(dir, nil); e != nil {
				t.Fatal("recovered admission")
			}
		})
	}
}
