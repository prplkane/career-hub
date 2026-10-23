//go:build windows

package applicationwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/intake/resumelibrary"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestApprovedImportNativeLifecycle(t *testing.T) {
	if home := os.Getenv("CAREERHUB_TEST_APPROVED_IMPORT_HOME"); home != "" {
		runApprovedImportLifecycle(t, home, os.Getenv("CAREERHUB_TEST_APPROVED_IMPORT_CASE"))
		return
	}
	for _, scenario := range []string{"keep", "keep-raw-drift", "keep-approval-drift", "keep-archived", "tampered", "missing", "unapproved", "remove", "remove-source-drift", "remove-approval-drift", "remove-error-after-delete", "remove-unknown", "remove-diagnostic"} {
		t.Run(scenario, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home")
			approvalOwnerFixture(t, home)
			raw := []byte("Synthetic approved resume\n")
			fixtureWrite(t, home, "career_vault/raw/fictional.txt", raw)
			extractionPath, reviewPath, _ := intakeReviewPaths(approvalTestID)
			for _, path := range []string{extractionPath, reviewPath} {
				full := filepath.Join(home, path)
				report, err := os.ReadFile(full)
				if err != nil {
					t.Fatal(err)
				}
				report = bytes.ReplaceAll(report, []byte(strings.Repeat("a", 64)), []byte(digestBytes(raw)))
				if err := os.WriteFile(full, report, 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestApprovedImportNativeLifecycle$")
			cmd.Env = append(os.Environ(), "CAREERHUB_TEST_APPROVED_IMPORT_HOME="+home, "CAREERHUB_TEST_APPROVED_IMPORT_CASE="+scenario)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.WaitDelay = time.Second
			if raw, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("approved import: %v %.1400s", err, raw)
			}
		})
	}
}

func runApprovedImportLifecycle(t *testing.T, home, scenario string) {
	runApprovalOwner(t, home, "success")
	if _, err := schema.PrepareOwned(context.Background(), home); err != nil {
		t.Fatal(err)
	}
	info, pe := datahome.Inspect(home, nil)
	if pe != nil {
		t.Fatal(pe)
	}
	owner := New(home).PinnedToHome(*info)
	rawPath := filepath.Join(home, "career_vault/raw/fictional.txt")
	extractionPath, reviewPath, approvalPath := intakeReviewPaths(approvalTestID)
	audit := map[string][]byte{}
	for _, path := range []string{extractionPath, reviewPath, approvalPath, "career_vault/project_blocks.yaml"} {
		raw, err := os.ReadFile(filepath.Join(home, path))
		if err != nil {
			t.Fatal(err)
		}
		audit[path] = raw
	}
	if scenario == "tampered" {
		if err := os.WriteFile(rawPath, []byte("changed"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if scenario == "missing" {
		if err := os.Remove(rawPath); err != nil {
			t.Fatal(err)
		}
	}
	if scenario == "unapproved" {
		if err := os.Remove(filepath.Join(home, approvalPath)); err != nil {
			t.Fatal(err)
		}
	}
	owner.checkpoint = func(stage string) *Error {
		if stage == "before_commit" && (scenario == "keep-raw-drift" || scenario == "keep-approval-drift") || stage == "approved_source_removal_prepared" && (scenario == "remove-source-drift" || scenario == "remove-approval-drift") {
			name, raw := rawPath, []byte("changed")
			if strings.Contains(scenario, "approval-drift") {
				name = filepath.Join(home, approvalPath)
				raw = append(bytes.Clone(audit[approvalPath]), ' ')
			}
			if err := os.WriteFile(name, raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	if scenario == "remove-error-after-delete" {
		owner.intakeMutations.remove = func(root *runtimepaths.Root, name string, raw []byte) (bool, *runtimepaths.Error) {
			removed, pe := root.RemoveFile(name, raw)
			if pe != nil || !removed {
				t.Fatal("delete fixture failed")
			}
			return true, &runtimepaths.Error{Code: "TEST_AFTER_DELETE"}
		}
	}
	if scenario == "remove-unknown" {
		owner.intakeMutations.remove = func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error) {
			return false, &runtimepaths.Error{Code: "FILE_REMOVE"}
		}
	}
	if scenario == "remove-diagnostic" {
		owner.intakeMutations.replace = func(*runtimepaths.Root, string, []byte, []byte) (bool, *runtimepaths.Error) {
			return false, &runtimepaths.Error{Code: "TEST_DIAGNOSTIC"}
		}
	}
	if strings.HasPrefix(scenario, "remove") {
		out, problem := owner.RemoveApprovedImport(context.Background(), approvalTestID)
		if scenario == "remove" {
			if problem != nil || !out.Committed || out.Result == nil {
				t.Fatal("remove failed", problem)
			}
			again, problem := owner.RemoveApprovedImport(context.Background(), approvalTestID)
			if problem != nil || again.Committed || again.CommitAttempted || !bytes.Contains(again.Result, []byte(`"already_removed":true`)) {
				t.Fatal("no-op removal failed", problem)
			}
			for path, expected := range audit {
				raw, err := os.ReadFile(filepath.Join(home, path))
				if err != nil || !bytes.Equal(raw, expected) {
					t.Fatal("audit was removed/changed")
				}
			}
		} else {
			if problem == nil || out.Result != nil {
				t.Fatal("remove error hidden")
			}
			wantCommit := scenario == "remove-error-after-delete" || scenario == "remove-diagnostic"
			if out.Committed != wantCommit || out.CommitAttempted != (wantCommit || scenario == "remove-unknown") {
				t.Fatal("delete facts lost")
			}
		}
		assertApprovalDiagnostics(t, home)
		return
	}
	out, problem := owner.KeepApprovedImport(context.Background(), approvalTestID)
	if scenario == "keep" || scenario == "keep-archived" {
		if problem != nil || out == nil || !out.Committed {
			t.Fatal("keep failed", problem)
		}
		var result struct {
			Resume struct {
				ID string `json:"id"`
			} `json:"resume"`
			Reused bool `json:"reused"`
		}
		if json.Unmarshal(out.Result, &result) != nil || result.Resume.ID == "" || result.Reused {
			t.Fatal("bad keep result")
		}
		if scenario == "keep-archived" {
			_, problem := owner.Resume(context.Background(), resumelibrary.Request{Operation: "archive", Fields: map[string]any{"resume_id": result.Resume.ID, "archived": true}})
			if problem != nil {
				t.Fatal(problem)
			}
			_, problem = owner.KeepApprovedImport(context.Background(), approvalTestID)
			if problem == nil || problem.SourceClass != "ResumeVersionArchived" {
				t.Fatal("archived resume reused")
			}
			return
		}
		again, problem := owner.KeepApprovedImport(context.Background(), approvalTestID)
		if problem != nil || again.Committed || !bytes.Contains(again.Result, []byte(`"reused":true`)) {
			t.Fatal("repeat keep failed", problem)
		}
		file, problem := owner.Resume(context.Background(), resumelibrary.Request{Operation: "version_file", Fields: map[string]any{"resume_id": result.Resume.ID, "require_active": true}})
		if problem != nil || !bytes.Equal(file.Bytes, []byte("Synthetic approved resume\n")) {
			t.Fatal("original bytes changed")
		}
	} else if problem == nil || out.Committed || out.Result != nil {
		t.Fatal("invalid keep accepted", problem)
	}
}
