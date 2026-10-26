package jobcard

import (
	"context"
	"strings"
	"testing"

	"careerhub.local/backend/internal/jobs/jobdigest"
	"careerhub.local/backend/internal/skills/cataloginput"
)

func intakeFixture(t *testing.T) (IntakeDraftInput, *jobdigest.Result) {
	t.Helper()
	_, fresh := reanalysisFixture(t)
	return IntakeDraftInput{
		ID: "fictional_job", RawPath: "jobs/incoming/fictional_job.txt", RawText: reanalysisSource,
		SourceURL: "https://example.test/jobs/fictional/", PostingID: " ABC ",
		IdempotencyKey: " retry-key-123 ",
	}, fresh
}

func TestPrepareIntakeBuildsOneValidatedDraftWithoutWriting(t *testing.T) {
	input, fresh := intakeFixture(t)
	card, err := PrepareIntake(context.Background(), input, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if get(card, "id").Text != input.ID || get(card, "status").Text != "draft" ||
		get(card, "company").Text != fresh.Fields["company"].Value ||
		get(card, "title").Text != fresh.Fields["title"].Value ||
		get(card, "content_fingerprint").Text != fresh.ContentFingerprint ||
		get(card, "posting_identity").Text != "posting_id:abc" ||
		get(card, "intake_idempotency_key").Text != "retry-key-123" {
		t.Fatal("fresh digest or explicit intake identity missing")
	}
	if get(card, "required_capabilities").Kind != "sequence" || len(get(card, "required_capabilities").Items) != 0 ||
		get(card, "notes").Items[0].Text != "Draft created from the raw job description; manual review required." {
		t.Fatal("new draft acquired stale evidence or lost its review marker")
	}
	encoded, encodeErr := EncodeCard(context.Background(), card)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	reopened, parseErr := cataloginput.ParseYAML(encoded)
	if parseErr != nil || !sameCardValue(card, reopened) {
		t.Fatal("new draft did not round-trip through stored YAML")
	}
	input.RawText = "\ufeff" + strings.ReplaceAll(reanalysisSource, "\n", "\r\n")
	canonical, normalizeErr := IntakeDigestSource(input.RawText)
	if normalizeErr != nil || canonical != reanalysisSource {
		t.Fatal("retained UTF-8-sig/universal-newline analysis source changed", normalizeErr)
	}
	withBOM, bomErr := PrepareIntake(context.Background(), input, fresh)
	if bomErr != nil || get(withBOM, "content_fingerprint").Text != fresh.ContentFingerprint {
		t.Fatal("BOM/CRLF intake was not compatible with later owned reanalysis", bomErr)
	}
}

func TestPrepareIntakeRejectsInvalidSourceIdentityAndDigest(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		change     func(*IntakeDraftInput, *jobdigest.Result)
	}{
		{"bad-id", "JOB_INTAKE_REQUEST_INVALID", func(in *IntakeDraftInput, _ *jobdigest.Result) { in.ID = "../escape" }},
		{"wrong-stem", "JOB_INTAKE_REQUEST_INVALID", func(in *IntakeDraftInput, _ *jobdigest.Result) { in.RawPath = "jobs/incoming/other.txt" }},
		{"nested-source", "JOB_INTAKE_REQUEST_INVALID", func(in *IntakeDraftInput, _ *jobdigest.Result) { in.RawPath = "jobs/incoming/nested/fictional_job.txt" }},
		{"retry-key", "JOB_INTAKE_REQUEST_INVALID", func(in *IntakeDraftInput, _ *jobdigest.Result) { in.IdempotencyKey = "bad" }},
		{"empty-source", "JOB_INTAKE_SOURCE_INVALID", func(in *IntakeDraftInput, _ *jobdigest.Result) { in.RawText = " \r\n " }},
		{"nul-source", "JOB_INTAKE_SOURCE_INVALID", func(in *IntakeDraftInput, _ *jobdigest.Result) { in.RawText += "\x00" }},
		{"wrong-fingerprint", "JOB_INTAKE_DIGEST_INVALID", func(_ *IntakeDraftInput, fresh *jobdigest.Result) { fresh.ContentFingerprint = strings.Repeat("0", 64) }},
		{"missing-digest-fields", "JOB_INTAKE_DIGEST_INVALID", func(_ *IntakeDraftInput, fresh *jobdigest.Result) { fresh.Fields = nil }},
		{"invalid-url", "JOB_INTAKE_RESULT_INVALID", func(in *IntakeDraftInput, _ *jobdigest.Result) { in.SourceURL = "https://example.test/has space" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, fresh := intakeFixture(t)
			tc.change(&input, fresh)
			card, err := PrepareIntake(context.Background(), input, fresh)
			if err == nil || err.Code != tc.code || card.Kind != "" {
				t.Fatalf("wanted %s with no card, got %#v %#v", tc.code, card, err)
			}
		})
	}
	input, fresh := intakeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if card, err := PrepareIntake(ctx, input, fresh); err == nil || err.Code != "JOB_CARD_CANCELED" || card.Kind != "" {
		t.Fatal("cancellation retained a partial draft", card, err)
	}
}
