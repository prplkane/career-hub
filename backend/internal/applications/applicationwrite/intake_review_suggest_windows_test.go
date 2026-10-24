//go:build windows

package applicationwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/intake/intakeread"
	"careerhub.local/backend/internal/intake/intakereview"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const reviewSuggestTestHome = "CAREERHUB_TEST_REVIEW_SUGGEST_HOME"
const reviewSuggestTestCase = "CAREERHUB_TEST_REVIEW_SUGGEST_CASE"

func reviewSuggestExtraction() []byte {
	return []byte(`{
  "schema_version":2,"status":"extracted","extractor_version":"vault-intake-0.3","registry_revision":"revision",
  "source":{"id":"src_fictional_1234567890","path":"career_vault/raw/fictional.txt","file_type":"txt","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "blocks":[
    {"id":"b0","order":0,"kind":"paragraph","text":"EXPERIENCE","style":null,"is_bullet":false},
    {"id":"b1","order":1,"kind":"paragraph","text":"Example Systems / Software Engineer  Feb 2026 - Present","style":null,"is_bullet":false},
    {"id":"b2","order":2,"kind":"paragraph","text":"Built a local workflow.","style":null,"is_bullet":true}
  ],
  "section_hints":[{"section_type":"experience","source_text":"EXPERIENCE","source_ref":{"source_id":"src_fictional_1234567890","block_id":"b0"}}],
  "technology_ids":[],"technology_mentions":[],"technology_resolution_issues":[],
  "date_candidates":[{"source_text":"Feb 2026 - Present","start_text":"Feb 2026","end_text":"Present","current":true,"source_ref":{"source_id":"src_fictional_1234567890","block_id":"b1"}}],
  "bullet_candidates":[],"unresolved_fields":[]
}`)
}

func TestIntakeReviewSuggestOwnedProcess(t *testing.T) {
	if home := os.Getenv(reviewSuggestTestHome); home != "" {
		runIntakeReviewSuggestOwned(t, home, os.Getenv(reviewSuggestTestCase))
		return
	}
	for _, scenario := range []string{"success", "replace-effect", "replace-no-effect"} {
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
			extraction := reviewSuggestExtraction()
			if err := os.WriteFile(filepath.Join(runDir, "extracted.yaml"), extraction, 0600); err != nil {
				t.Fatal(err)
			}
			built, failure := intakereview.Build(extraction, nil)
			if failure != nil || os.WriteFile(filepath.Join(runDir, "review.yaml"), built.Bytes, 0600) != nil {
				t.Fatal("review fixture unavailable", failure)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIntakeReviewSuggestOwnedProcess$")
			cmd.Env = append(os.Environ(), reviewSuggestTestHome+"="+home, reviewSuggestTestCase+"="+scenario)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.WaitDelay = time.Second
			output, err := cmd.CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("review suggest child failed: %v %.1200s", err, output)
			}
			reviewRaw, readErr := os.ReadFile(filepath.Join(runDir, "review.yaml"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			parsed, parseFailure := intakeread.ParseReview(reviewRaw)
			wantEntities := scenario != "replace-no-effect"
			if parseFailure != nil || wantEntities != (len(parsed.Entities) == 1) {
				t.Fatal("unexpected suggestion publication state", scenario, len(parsed.Entities), parseFailure)
			}
			currentExtraction, readErr := os.ReadFile(filepath.Join(runDir, "extracted.yaml"))
			if readErr != nil || !bytes.Equal(currentExtraction, extraction) {
				t.Fatal("review suggestion changed extraction")
			}
		})
	}
}

func runIntakeReviewSuggestOwned(t *testing.T, home, scenario string) {
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
	reviewPath := filepath.Join(home, "career_vault", "intake_runs", "src_fictional_1234567890", "review.yaml")
	reviewRaw, readErr := os.ReadFile(reviewPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	revision, failure := intakereview.Revision(reviewRaw)
	if failure != nil {
		t.Fatal(failure.Code)
	}
	if out, problem := owner.SuggestIntakeReview(context.Background(), "src_fictional_1234567890", revision); problem == nil || problem.Code != "DATA_HOME_OWNERSHIP_REQUIRED" || out.CommitAttempted {
		t.Fatal("unowned review suggestion accepted", out, problem)
	}
	if claimErr := root.ClaimForProcess(); claimErr != nil {
		t.Fatal(claimErr.Code)
	}
	if scenario != "success" {
		effect := scenario == "replace-effect"
		owner.reviewMutations.replace = func(r *runtimepaths.Root, path string, before, after []byte) (bool, *runtimepaths.Error) {
			if effect {
				if committed, problem := r.ReplaceFile(path, before, after); problem != nil || !committed {
					return committed, problem
				}
			}
			return false, &runtimepaths.Error{Code: "FILE_AMBIGUOUS_TEST"}
		}
	}
	out, problem := owner.SuggestIntakeReview(context.Background(), "src_fictional_1234567890", revision)
	if scenario == "success" {
		if problem != nil || out == nil || !out.CommitAttempted || !out.Committed || len(out.Result) == 0 {
			t.Fatal("owned review suggestion failed", out, problem)
		}
		var receipt struct {
			SourceID       string                       `json:"source_id"`
			ReviewRevision string                       `json:"review_revision"`
			Suggestion     intakereview.SuggestionStats `json:"suggestion"`
		}
		if json.Unmarshal(out.Result, &receipt) != nil || receipt.SourceID != "src_fictional_1234567890" || receipt.Suggestion.ExperienceCardCount != 1 {
			t.Fatal("suggestion receipt changed", string(out.Result))
		}
		if repeated, repeatedProblem := owner.SuggestIntakeReview(context.Background(), "src_fictional_1234567890", revision); repeatedProblem == nil || repeatedProblem.Code != "REVIEW_SUGGEST_REVISION_CONFLICT" || repeated.CommitAttempted {
			t.Fatal("stale suggestion revision was accepted", repeated, repeatedProblem)
		}
		return
	}
	if problem == nil || problem.Code != "REVIEW_SUGGEST_WRITE_UNCERTAIN" || out == nil || !out.CommitAttempted || out.Committed || len(out.Result) != 0 {
		t.Fatal("ambiguous suggestion was presented as safe", out, problem)
	}
}
