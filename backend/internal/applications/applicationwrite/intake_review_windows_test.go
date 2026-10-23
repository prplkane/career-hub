//go:build windows

package applicationwrite

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/intake/intakeread"
	"careerhub.local/backend/internal/intake/intakereview"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const reviewBuildTestHome = "CAREERHUB_TEST_REVIEW_BUILD_HOME"
const reviewBuildTestCase = "CAREERHUB_TEST_REVIEW_BUILD_CASE"

func reviewBuildExtraction() []byte {
	return []byte(`{
  "schema_version":2,"status":"extracted","extractor_version":"vault-intake-0.3","registry_revision":"revision",
  "source":{"id":"src_fictional_1234567890","path":"career_vault/raw/fictional.txt","file_type":"txt","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "blocks":[{"id":"b1","order":0,"kind":"paragraph","text":"Example Systems","style":null,"is_bullet":false}],
  "section_hints":[],"technology_ids":[],"technology_mentions":[],"technology_resolution_issues":[],
  "date_candidates":[],"bullet_candidates":[],"unresolved_fields":[]
}`)
}

func TestIntakeReviewBuildRejectsInputBeforeHomeAccess(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	owner := New(missing)
	for _, sourceID := range []string{"", "../escape", "src_UPPER", "src_has-hyphen"} {
		out, problem := owner.BuildIntakeReview(context.Background(), sourceID, nil)
		if problem == nil || problem.Code != "REVIEW_BUILD_REQUEST_INVALID" || out.CommitAttempted || out.Committed {
			t.Fatal("invalid source reached Home", sourceID, out, problem)
		}
	}
	badRevision := "not-a-revision"
	if out, problem := owner.BuildIntakeReview(context.Background(), "src_fictional_1234567890", &badRevision); problem == nil || problem.Code != "REVIEW_BUILD_REQUEST_INVALID" || out.CommitAttempted {
		t.Fatal("invalid revision reached Home", out, problem)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("invalid request created a Data Home")
	}
}

func TestIntakeReviewBuildOwnedProcess(t *testing.T) {
	if home := os.Getenv(reviewBuildTestHome); home != "" {
		runIntakeReviewBuildOwned(t, home, os.Getenv(reviewBuildTestCase))
		return
	}
	for _, scenario := range []string{"success", "create-effect", "create-no-effect", "replace-effect"} {
		t.Run(scenario, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home")
			runDir := filepath.Join(home, "career_vault", "intake_runs", "src_fictional_1234567890")
			if err := os.MkdirAll(runDir, 0700); err != nil {
				t.Fatal(err)
			}
			marker := []byte(`{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`)
			if err := os.WriteFile(filepath.Join(home, datahome.MarkerPath), marker, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(runDir, "extracted.yaml"), reviewBuildExtraction(), 0600); err != nil {
				t.Fatal(err)
			}
			if scenario == "replace-effect" {
				built, failure := intakereview.Build(reviewBuildExtraction(), nil)
				if failure != nil || os.WriteFile(filepath.Join(runDir, "review.yaml"), append([]byte(" \n"), built.Bytes...), 0600) != nil {
					t.Fatal("existing review fixture unavailable", failure)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIntakeReviewBuildOwnedProcess$")
			cmd.Env = append(os.Environ(), reviewBuildTestHome+"="+home, reviewBuildTestCase+"="+scenario)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.WaitDelay = time.Second
			output, err := cmd.CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("review build child failed: %v %.1200s", err, output)
			}
			reviewRaw, readErr := os.ReadFile(filepath.Join(runDir, "review.yaml"))
			wantReview := scenario != "create-no-effect"
			if (wantReview && (readErr != nil || len(reviewRaw) == 0)) || (!wantReview && !os.IsNotExist(readErr)) {
				t.Fatal("unexpected review publication state", readErr)
			}
			if wantReview {
				parsed, failure := intakeread.ParseReview(reviewRaw)
				if failure != nil || len(parsed.BlockReviews) != 1 || parsed.BlockReviews[0].BlockID != "b1" {
					t.Fatal("published review is invalid", failure)
				}
			}
			currentExtraction, readErr := os.ReadFile(filepath.Join(runDir, "extracted.yaml"))
			if readErr != nil || !bytes.Equal(currentExtraction, reviewBuildExtraction()) {
				t.Fatal("review build changed extraction")
			}
		})
	}
}

func runIntakeReviewBuildOwned(t *testing.T, home, scenario string) {
	t.Helper()
	root, openErr := runtimepaths.Open(home)
	if openErr != nil {
		t.Fatal(openErr.Code)
	}
	defer root.Close()
	homeInfo, inspectErr := datahome.Inspect(home, nil)
	if inspectErr != nil {
		t.Fatal(inspectErr.Code)
	}
	owner := New(home).PinnedToHome(*homeInfo)
	if out, problem := owner.BuildIntakeReview(context.Background(), "src_fictional_1234567890", nil); problem == nil || problem.Code != "DATA_HOME_OWNERSHIP_REQUIRED" || out.CommitAttempted {
		t.Fatal("unowned review build accepted", out, problem)
	}
	if claimErr := root.ClaimForProcess(); claimErr != nil {
		t.Fatal(claimErr.Code)
	}
	var expected *string
	if scenario == "replace-effect" {
		raw, readErr := os.ReadFile(filepath.Join(home, "career_vault", "intake_runs", "src_fictional_1234567890", "review.yaml"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		revision, failure := intakereview.Revision(raw)
		if failure != nil {
			t.Fatal(failure.Code)
		}
		expected = &revision
	}
	if strings.HasPrefix(scenario, "create-") {
		effect := scenario == "create-effect"
		owner.reviewMutations.create = func(r *runtimepaths.Root, path string, content []byte) (bool, *runtimepaths.Error) {
			if effect {
				if created, problem := r.CreateFile(path, content); problem != nil || !created {
					return created, problem
				}
			}
			return false, &runtimepaths.Error{Code: "FILE_AMBIGUOUS_TEST"}
		}
	}
	if scenario == "replace-effect" {
		owner.reviewMutations.replace = func(r *runtimepaths.Root, path string, before, after []byte) (bool, *runtimepaths.Error) {
			if committed, problem := r.ReplaceFile(path, before, after); problem != nil || !committed {
				return committed, problem
			}
			return false, &runtimepaths.Error{Code: "FILE_AMBIGUOUS_TEST"}
		}
	}
	out, problem := owner.BuildIntakeReview(context.Background(), "src_fictional_1234567890", expected)
	if scenario == "success" {
		if problem != nil || out == nil || !out.CommitAttempted || !out.Committed || len(out.Result) == 0 {
			t.Fatal("owned review build failed", out, problem)
		}
		if repeated, repeatedProblem := owner.BuildIntakeReview(context.Background(), "src_fictional_1234567890", nil); repeatedProblem == nil || repeatedProblem.Code != "REVIEW_BUILD_REVISION_REQUIRED" || repeated.CommitAttempted {
			t.Fatal("existing review accepted without its displayed revision", repeated, repeatedProblem)
		}
		return
	}
	if problem == nil || problem.Code != "REVIEW_BUILD_WRITE_UNCERTAIN" || out == nil || !out.CommitAttempted || out.Committed || len(out.Result) != 0 {
		t.Fatal("ambiguous file result was presented as safe", out, problem)
	}
}
