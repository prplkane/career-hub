package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func TestFeedContextFailureDoesNotHideIntegrityErrors(t *testing.T) {
	for _, code := range []string{"EVIDENCE_VAULT_INVALID", "EVIDENCE_CATALOG_INVALID", "DISCOVERY_PREVIEW_POLICY_INVALID"} {
		if !feedContextUnavailable(&readonly.Error{Code: code}) {
			t.Fatal(code)
		}
	}
	for _, code := range []string{"EVIDENCE_INPUT_CHANGED", "DISCOVERY_SOURCE_HASH_MISMATCH", "DATA_HOME_UNAVAILABLE", "DISCOVERY_CLEANUP_FAILED", "ASSESSMENT_INPUT_CANCELED"} {
		if feedContextUnavailable(&readonly.Error{Code: code}) {
			t.Fatal("hidden integrity failure", code)
		}
	}
	if feedContextUnavailable(context.Canceled) {
		t.Fatal("hidden cancellation")
	}
}

func TestFeedUnavailableContextStillShowsExactExtraSteps(t *testing.T) {
	detail := &discovery.Detail{Record: discovery.Record{ContentScope: "confirmed_full"}, SourceText: "Software Engineer\nHow to apply\nEmail your resume to recruiting@example.com.\n"}
	preview, err := unavailableFeedPreview(detail)
	if err != nil || preview.Receipt.Availability != "context_unavailable" || preview.Receipt.BlockerCount != 0 || !preview.Instructions.Eligible {
		t.Fatalf("bad missing-context receipt: %+v %v", preview, err)
	}
	if preview.Receipt.ExtraStepCount != preview.Instructions.ItemCount || preview.Receipt.ContactCandidateCount != preview.Contacts.CandidateCount {
		t.Fatal("lost extraction counts")
	}
	out, err := encodeReview(context.Background(), detail, detail, &Result{Preview: preview})
	if err != nil || !json.Valid(out) {
		t.Fatal("missing context must retain a bounded, honest detail", err)
	}
}

func TestFeedReviewRejectsSourceDriftAndCancellation(t *testing.T) {
	detail := &discovery.Detail{Record: discovery.Record{ContentScope: "confirmed_full", Title: "Engineer"}}
	preview, err := unavailableFeedPreview(detail)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*discovery.Detail){
		func(d *discovery.Detail) { d.Title = "Changed role" },
		func(d *discovery.Detail) { d.ContentScope = "link_only" },
		func(d *discovery.Detail) { d.SourceText = "Changed source" },
		func(d *discovery.Detail) { d.Revision++ },
	} {
		after := *detail
		change(&after)
		if raw, err := encodeReview(context.Background(), detail, &after, &Result{Preview: preview}); raw != nil || err == nil {
			t.Fatal("mixed capture witness exposed a preview")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if raw, err := encodeReview(ctx, detail, detail, &Result{Preview: preview}); raw != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled review succeeded")
	}
}
