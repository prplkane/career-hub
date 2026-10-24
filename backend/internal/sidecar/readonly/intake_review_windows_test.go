//go:build windows

package readonly

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

	"careerhub.local/backend/internal/sidecar/protocol"
)

const intakeViewHelper = "CAREERHUB_INTAKE_VIEW_HELPER"

func TestIntakeReviewReadOwner(t *testing.T) {
	if os.Getenv(intakeViewHelper) == "1" {
		runIntakeReviewReadOwner(t)
		return
	}
	base, start := fixture(t)
	runDir := filepath.Join(start.DataRoot, "career_vault", "intake_runs", "src_fictional_1234567890")
	if err := os.MkdirAll(runDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, start.DataRoot, "career_vault/intake_runs/src_fictional_1234567890/extracted.yaml", intakeViewExtraction())
	writeFixture(t, start.DataRoot, "career_vault/raw/Z.TXT", "fictional source")
	writeFixture(t, start.DataRoot, "career_vault/raw/a.docx", "metadata only; never decoded")
	writeFixture(t, start.DataRoot, "career_vault/raw/ignored.pdf", "unsupported")
	reviewPath := filepath.Join(runDir, "review.yaml")
	writeFixture(t, start.DataRoot, "career_vault/intake_runs/src_fictional_1234567890/review.yaml", intakeViewReview())
	if err := os.Chtimes(reviewPath, time.Unix(1700000000, 123456700), time.Unix(1700000000, 123456700)); err != nil {
		t.Fatal(err)
	}
	before := tree(t, base)
	raw, _ := json.Marshal(start)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIntakeReviewReadOwner$")
	cmd.Env = append(os.Environ(), intakeViewHelper+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = bytes.NewReader(raw)
	output, err := cmd.CombinedOutput()
	cancel()
	if err != nil {
		t.Fatalf("intake view child failed: %v %.1200s", err, output)
	}
	if after := tree(t, base); !equalTrees(before, after) {
		t.Fatal("intake read changed the fixture")
	}
}

func runIntakeReviewReadOwner(t *testing.T) {
	t.Helper()
	var start protocol.StartRecord
	if json.NewDecoder(os.Stdin).Decode(&start) != nil {
		t.Fatal("invalid child request")
	}
	workspace, err := OpenForProcess(start)
	if err != nil {
		t.Fatal(err)
	}
	sources, sourceErr := workspace.IntakeSources(context.Background(), 0, 1)
	if sourceErr != nil || sources.Total != 2 || len(sources.Items) != 1 || sources.Items[0].Name != "a.docx" || sources.NextCursor == nil || *sources.NextCursor != "offset:1" {
		t.Fatal("source metadata page failed", sources, sourceErr)
	}
	extraction, err := workspace.IntakeExtraction(context.Background(), "src_fictional_1234567890")
	if err != nil || extraction == nil || extraction.SourceName != "fictional.txt" || !extraction.ReviewExists || len(extraction.Blocks) != 1 {
		t.Fatal("extraction view failed", extraction, err)
	}
	review, err := workspace.IntakeReview(context.Background(), "src_fictional_1234567890")
	if err != nil || review == nil || review.ModifiedAt != "2023-11-14T22:13:20.123457+00:00" || review.Validation.Status != "success_with_warnings" || review.Completion.UnresolvedBlockCount != 1 {
		t.Fatal("review view failed", review, err)
	}
	if missing, missingErr := workspace.IntakeReview(context.Background(), "src_missing_1234567890"); missing != nil || errorCode(missingErr) != "INTAKE_EXTRACTION_NOT_FOUND" {
		t.Fatal("missing run did not stay missing", missing, missingErr)
	}
}

func equalTrees(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
