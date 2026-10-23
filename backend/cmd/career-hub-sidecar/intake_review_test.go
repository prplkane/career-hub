package main

import (
	"encoding/json"
	"testing"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/intake/intakereview"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func TestIntakeReviewCompositionHelpers(t *testing.T) {
	write := intakeWriteOutcome(&applicationwrite.Outcome{Result: json.RawMessage(`{"ok":true}`), CommitAttempted: true, Committed: true, CleanupFailed: true, RecoveryRequired: true})
	if write == nil || !write.Committed || write.CommitUnknown || !write.CleanupFailed || !write.RecoveryRequired || !write.Partial {
		t.Fatal("review write facts changed", write)
	}
	unknown := intakeWriteOutcome(&applicationwrite.Outcome{CommitAttempted: true})
	if unknown == nil || !unknown.CommitUnknown || unknown.Committed {
		t.Fatal("unknown review commit was collapsed", unknown)
	}
	for private, public := range map[string]string{
		"REVIEW_BUILD_REQUEST_INVALID":        "INTAKE_REVIEW_BUILD_REQUEST_INVALID",
		"REVIEW_BUILD_REVISION_CONFLICT":      "INTAKE_REVIEW_BUILD_REVISION_CONFLICT",
		"REVIEW_BUILD_WRITE_UNCERTAIN":        "INTAKE_REVIEW_BUILD_WRITE_UNCERTAIN",
		"REVIEW_BUILD_COMMITTED_UNVERIFIED":   "INTAKE_REVIEW_BUILD_COMMITTED_UNVERIFIED",
		"DATA_HOME_OWNERSHIP_REQUIRED":        "INTAKE_REVIEW_BUILD_UNAVAILABLE",
		"private-unrecognized-error-sentinel": "INTAKE_REVIEW_BUILD_UNAVAILABLE",
	} {
		if got := intakeBuildCode(private); got != public {
			t.Fatalf("review error mapping %q: got %q want %q", private, got, public)
		}
	}
	for private, public := range map[string]string{
		"REVIEW_SUGGEST_REQUEST_INVALID":      "INTAKE_REVIEW_SUGGEST_REQUEST_INVALID",
		"REVIEW_SUGGEST_REVISION_CONFLICT":    "INTAKE_REVIEW_SUGGEST_REVISION_CONFLICT",
		"REVIEW_SUGGEST_WRITE_UNCERTAIN":      "INTAKE_REVIEW_SUGGEST_WRITE_UNCERTAIN",
		"REVIEW_SUGGEST_COMMITTED_UNVERIFIED": "INTAKE_REVIEW_SUGGEST_COMMITTED_UNVERIFIED",
		"DATA_HOME_OWNERSHIP_REQUIRED":        "INTAKE_REVIEW_SUGGEST_UNAVAILABLE",
		"private-unrecognized-error-sentinel": "INTAKE_REVIEW_SUGGEST_UNAVAILABLE",
	} {
		if got := intakeSuggestCode(private); got != public {
			t.Fatalf("suggestion error mapping %q: got %q want %q", private, got, public)
		}
	}
	revision := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if !validIntakeBuildReceipt(intakeReviewBuildReceipt{"src_fictional_1234567890", revision, 1, 2}, "src_fictional_1234567890") ||
		validIntakeBuildReceipt(intakeReviewBuildReceipt{"src_other_1234567890", revision, 1, 2}, "src_fictional_1234567890") {
		t.Fatal("review receipt boundary changed")
	}
	stats := intakereview.SuggestionStats{CardCount: 1, NewCardCount: 1, ExperienceCardCount: 1, AutoAssignedLinkCount: 2, StructuralBlockCount: 1}
	if !validIntakeSuggestReceipt(intakeReviewSuggestReceipt{"src_fictional_1234567890", revision, stats}, "src_fictional_1234567890") {
		t.Fatal("valid suggestion receipt was refused")
	}
	stats.CardCount = 2
	if validIntakeSuggestReceipt(intakeReviewSuggestReceipt{"src_fictional_1234567890", revision, stats}, "src_fictional_1234567890") {
		t.Fatal("inconsistent suggestion receipt was accepted")
	}
	if _, problem := intakeReadResult(nil, &readonly.Error{Code: "APPLICATION_READ_CANCELED"}); problem == nil || problem.Code != "INTAKE_READ_CANCELED" || problem.Category != "intake_review" {
		t.Fatal("read cancellation mapping changed", problem)
	}
}
