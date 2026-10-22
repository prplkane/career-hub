package main

import (
	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/previewcache"
	"careerhub.local/backend/internal/sidecar/assessment"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"testing"
)

func TestFeedWriterPreservesCommitAndRecovery(t *testing.T) {
	for _, input := range []applicationwrite.Outcome{{}, {CommitAttempted: true}, {CommitAttempted: true, Committed: true}, {CommitAttempted: true, Committed: true, RecoveryRequired: true, CleanupFailed: true}} {
		out := feedWriteOutcome(&input)
		if out.Committed != input.Committed || out.CommitUnknown != (input.CommitAttempted && !input.Committed) || out.RecoveryRequired != input.RecoveryRequired || out.CleanupFailed != input.CleanupFailed {
			t.Fatalf("lost publication facts %+v -> %+v", input, out)
		}
	}
}

func TestFeedMutationAndReviewRetainBothPublicationFacts(t *testing.T) {
	failedOpen, err := feedReviewFailure(nil, &assessment.Error{Code: "ASSESSMENT_CLEANUP_FAILED"})
	if err == nil || failedOpen.Committed || !failedOpen.CleanupFailed {
		t.Fatal("lost admission cleanup failure")
	}
	for _, report := range []previewcache.WriteReport{{}, {DirectoriesCreated: true}, {Attempted: true}, {Attempted: true, Committed: true}} {
		out := feedReviewOutcome(&assessment.Result{Write: report, CleanupFailed: true})
		if out.Committed != report.Committed || out.CommitUnknown != (report.Attempted && !report.Committed) || !out.CleanupFailed {
			t.Fatal("lost cache publication facts")
		}
		confirmed := &httpapi.ApplicationOutcome{Committed: true}
		failed := &httpapi.ApplicationError{Code: "DISCOVERY_READ_UNAVAILABLE"}
		merged, err := mergeFeedMutationReview(confirmed, out, failed)
		if err != failed || !merged.Committed || !merged.Partial || merged.Result != nil || merged.CommitUnknown != out.CommitUnknown || !merged.CleanupFailed {
			t.Fatal("late review failure erased a successful mutation")
		}
	}
}
