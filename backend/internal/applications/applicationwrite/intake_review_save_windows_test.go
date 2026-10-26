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

	"careerhub.local/backend/internal/intake/intakereview"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestIntakeReviewSaveOwnedFaults(t *testing.T) {
	if home := os.Getenv("CAREERHUB_TEST_REVIEW_SAVE_HOME"); home != "" {
		runReviewSave(t, home, os.Getenv("CAREERHUB_TEST_REVIEW_SAVE_CASE"))
		return
	}
	for _, scenario := range []string{"success", "replace-effect", "replace-no-effect", "replace-true-error", "review-drift", "extraction-drift", "approval-drift", "after-commit", "terminal-diagnostic"} {
		t.Run(scenario, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home")
			dir := filepath.Join(home, "career_vault/intake_runs/src_fictional_1234567890")
			if e := os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			built, e := intakereview.Build(reviewBuildExtraction(), nil)
			if e != nil {
				t.Fatal(e)
			}
			for p, b := range map[string][]byte{datahome.MarkerPath: []byte(`{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`), "career_vault/intake_runs/src_fictional_1234567890/extracted.yaml": reviewBuildExtraction(), "career_vault/intake_runs/src_fictional_1234567890/review.yaml": built.Bytes} {
				if e := os.WriteFile(filepath.Join(home, p), b, 0600); e != nil {
					t.Fatal(e)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIntakeReviewSaveOwnedFaults$")
			cmd.Env = append(os.Environ(), "CAREERHUB_TEST_REVIEW_SAVE_HOME="+home, "CAREERHUB_TEST_REVIEW_SAVE_CASE="+scenario)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.WaitDelay = time.Second
			if raw, e := cmd.CombinedOutput(); e != nil {
				t.Fatalf("native child %v: %.1700s", e, raw)
			}
		})
	}
}

func runReviewSave(t *testing.T, home, scenario string) {
	root, e := runtimepaths.Open(home)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e := root.ClaimForProcess(); e != nil {
		t.Fatal(e)
	}
	info, e2 := datahome.Inspect(home, nil)
	if e2 != nil {
		t.Fatal(e2)
	}
	owner := New(home).PinnedToHome(*info)
	id := "src_fictional_1234567890"
	extractionPath, reviewPath, approvalPath := intakeReviewPaths(id)
	previous, e3 := os.ReadFile(filepath.Join(home, reviewPath))
	if e3 != nil {
		t.Fatal(e3)
	}
	revision, e4 := intakereview.Revision(previous)
	if e4 != nil {
		t.Fatal(e4)
	}
	var document map[string]any
	if json.Unmarshal(previous, &document) != nil {
		t.Fatal("fixture decode")
	}
	document["block_reviews"].([]any)[0].(map[string]any)["user_note"] = "Keep exact fictional note"
	candidate, _ := json.Marshal(document)
	normalized, e4 := intakereview.Normalize(candidate)
	if e4 != nil {
		t.Fatal(e4)
	}
	if strings.HasPrefix(scenario, "replace-") {
		owner.reviewMutations.replace = func(r *runtimepaths.Root, p string, a, b []byte) (bool, *runtimepaths.Error) {
			if scenario != "replace-no-effect" {
				if changed, e := r.ReplaceFile(p, a, b); e != nil || !changed {
					return changed, e
				}
			}
			return scenario == "replace-true-error", &runtimepaths.Error{Code: "TEST_AMBIGUOUS"}
		}
	}
	if scenario == "terminal-diagnostic" {
		owner.intakeMutations.replace = func(*runtimepaths.Root, string, []byte, []byte) (bool, *runtimepaths.Error) {
			return false, &runtimepaths.Error{Code: "TEST_DIAGNOSTIC"}
		}
	}
	owner.checkpoint = func(stage string) *Error {
		if stage == "intake_review_saved" && scenario == "after-commit" {
			return failed("test", "TEST_VERIFY")
		}
		if stage != "intake_review_save_prepared" {
			return nil
		}
		var p string
		var content []byte
		switch scenario {
		case "review-drift":
			p, content = reviewPath, append(bytes.Clone(previous), ' ')
		case "extraction-drift":
			p, content = extractionPath, append(reviewBuildExtraction(), ' ')
		case "approval-drift":
			p, content = approvalPath, []byte("approved")
		default:
			return nil
		}
		if e := os.WriteFile(filepath.Join(home, p), content, 0600); e != nil {
			t.Fatal(e)
		}
		return nil
	}
	out, err := owner.SaveIntakeReview(context.Background(), id, revision, candidate)
	if out == nil {
		t.Fatal("missing outcome")
	}
	if scenario == "success" {
		if err != nil || !out.Committed || len(out.Result) == 0 {
			t.Fatal("save failed", err)
		}
	} else {
		if err == nil || len(out.Result) != 0 {
			t.Fatal("false success")
		}
		if strings.HasSuffix(scenario, "drift") {
			if out.CommitAttempted || err.Code != "REVIEW_REVISION_CONFLICT" {
				t.Fatal("drift not refused", err)
			}
		} else if !out.CommitAttempted {
			t.Fatal("write attempt hidden")
		}
	}
	if scenario == "replace-effect" || scenario == "replace-no-effect" {
		if out.Committed || err.Code != "REVIEW_UI_SAVE_WRITE_UNCERTAIN" {
			t.Fatal("unknown treated as settled", err)
		}
	}
	if scenario == "replace-true-error" || scenario == "after-commit" || scenario == "terminal-diagnostic" {
		if !out.Committed {
			t.Fatal("committed effect lost")
		}
	}
	want := previous
	if scenario == "review-drift" {
		want = append(bytes.Clone(previous), ' ')
	}
	if scenario == "success" || scenario == "replace-effect" || scenario == "replace-true-error" || scenario == "after-commit" || scenario == "terminal-diagnostic" {
		want = normalized
	}
	actual, e3 := os.ReadFile(filepath.Join(home, reviewPath))
	if e3 != nil || !bytes.Equal(actual, want) {
		t.Fatal("disk evidence differs", e3)
	}
}
