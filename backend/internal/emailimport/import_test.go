package emailimport

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/emailalerts"
	"careerhub.local/backend/internal/emailcandidates"
	"careerhub.local/backend/internal/emailrouting"
	"careerhub.local/backend/internal/emailworker"
)

type fakeWriter struct {
	calls        []string
	captured     []discovery.CaptureRequest
	fingerprints []string
	observedAt   []string
	capture      func(discovery.CaptureRequest) (*applicationwrite.Outcome, *applicationwrite.Error)
	outcome      func(emailcandidates.ApplicationOutcome) (*applicationwrite.Outcome, *applicationwrite.Error)
}

func (f *fakeWriter) CaptureDiscovery(_ context.Context, request discovery.CaptureRequest) (*applicationwrite.Outcome, *applicationwrite.Error) {
	f.calls = append(f.calls, "candidate")
	f.captured = append(f.captured, request)
	if f.capture != nil {
		return f.capture(request)
	}
	return ownedJSON(map[string]any{"outcome": "created", "discovery": map[string]any{"discovery_id": "discovery_1"}}, true), nil
}

func (f *fakeWriter) RecordEmailApplicationOutcome(_ context.Context, fingerprint string, outcome emailcandidates.ApplicationOutcome, observed string) (*applicationwrite.Outcome, *applicationwrite.Error) {
	f.calls = append(f.calls, "outcome")
	f.fingerprints = append(f.fingerprints, fingerprint)
	f.observedAt = append(f.observedAt, observed)
	if f.outcome != nil {
		return f.outcome(outcome)
	}
	return ownedJSON(map[string]any{
		"outcome_id": "event_1", "resolution": "application_updated", "application_changed": true,
	}, true), nil
}

func ownedJSON(value any, changed bool) *applicationwrite.Outcome {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return &applicationwrite.Outcome{Result: raw, CommitAttempted: changed, Committed: changed}
}

func mixedAlert(t *testing.T) emailalerts.Result {
	t.Helper()
	parsed := parsedFixture(t)
	parsed.ApplicationOutcomes = []emailcandidates.ApplicationOutcome{{
		Ordinal: 1, Title: "Senior Data Analyst", Company: "Example Analytics",
		SourceProvider: "handshake", PostingIdentity: "handshake:123",
		ApplicationStatus: "applied", EvidenceCode: "handshake_application_sent",
	}}
	return parsed
}

func TestImporterRoutesMixedMessageAndPreservesOutcomeFirstOrder(t *testing.T) {
	parsed := mixedAlert(t)
	w := &fakeWriter{}
	got, err := (Importer{Writer: w}).Import(context.Background(), parsed, emailrouting.SenderPurposeBoth)
	if err != nil || got.Status != "imported" || got.CandidateCount != 1 || got.DiscoveryCount != 1 ||
		got.ApplicationOutcomeCount != 1 || got.ApplicationsUpdated != 1 {
		t.Fatalf("import=%+v err=%v", got, err)
	}
	if len(w.calls) != 2 || w.calls[0] != "outcome" || w.calls[1] != "candidate" {
		t.Fatalf("owner order=%v", w.calls)
	}
	if w.fingerprints[0] != parsed.MessageFingerprint || w.observedAt[0] != parsed.ReceivedAt {
		t.Fatal("source envelope was not carried to the outcome owner")
	}
	if w.captured[0]["request_fingerprint"] != parsed.MessageFingerprint+":v6:1" ||
		w.captured[0]["content_scope"] != "link_only" {
		t.Fatalf("unexpected Discovery projection: %v", w.captured[0])
	}
}

func TestImporterPurposeRoutingAndEmptyMessage(t *testing.T) {
	parsed := mixedAlert(t)
	for _, test := range []struct {
		name, route string
		wantCalls   string
		candidates  int
		outcomes    int
	}{
		{"jobs", emailrouting.SenderPurposeJobAlerts, "candidate", 1, 0},
		{"tracking", emailrouting.SenderPurposeApplicationTracking, "outcome", 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := &fakeWriter{}
			got, err := (Importer{Writer: w}).Import(context.Background(), parsed, test.route)
			if err != nil || got.Status != "imported" || got.CandidateCount != test.candidates ||
				got.ApplicationOutcomeCount != test.outcomes || len(w.calls) != 1 || w.calls[0] != test.wantCalls {
				t.Fatalf("import=%+v calls=%v err=%v", got, w.calls, err)
			}
		})
	}
	parsed.Candidates, parsed.ApplicationOutcomes = nil, nil
	w := &fakeWriter{}
	got, err := (Importer{Writer: w}).Import(context.Background(), parsed, emailrouting.SenderPurposeBoth)
	if err != nil || got.Status != "no_candidates" || len(w.calls) != 0 {
		t.Fatalf("empty import=%+v calls=%v err=%v", got, w.calls, err)
	}
}

func TestImporterReplayCountsOnlyChangedOutcomes(t *testing.T) {
	parsed := mixedAlert(t)
	w := &fakeWriter{}
	first := true
	w.outcome = func(emailcandidates.ApplicationOutcome) (*applicationwrite.Outcome, *applicationwrite.Error) {
		if first {
			return ownedJSON(map[string]any{"outcome_id": "event_1", "resolution": "application_updated", "application_changed": true}, true), nil
		}
		return ownedJSON(map[string]any{"outcome_id": "event_1", "resolution": "application_already_current", "duplicate": true}, false), nil
	}
	w.capture = func(discovery.CaptureRequest) (*applicationwrite.Outcome, *applicationwrite.Error) {
		if first {
			return ownedJSON(map[string]any{"outcome": "created", "discovery": map[string]any{"discovery_id": "discovery_1"}}, true), nil
		}
		return ownedJSON(map[string]any{"outcome": "duplicate", "discovery": map[string]any{"discovery_id": "discovery_1"}}, false), nil
	}
	for index := 0; index < 2; index++ {
		got, err := (Importer{Writer: w}).Import(context.Background(), parsed, emailrouting.SenderPurposeBoth)
		if err != nil || got.Status != "imported" || got.DiscoveryCount != 1 || got.ApplicationOutcomeCount != 1 {
			t.Fatalf("replay %d = %+v err=%v", index, got, err)
		}
		if index == 1 && got.ApplicationsUpdated != 0 {
			t.Fatalf("replay counted an old status update: %+v", got)
		}
		first = false
	}
}

func TestImporterKnownUnitFailuresStayRetryableAndCountCommittedUnits(t *testing.T) {
	parsed := mixedAlert(t)
	w := &fakeWriter{outcome: func(emailcandidates.ApplicationOutcome) (*applicationwrite.Outcome, *applicationwrite.Error) {
		return &applicationwrite.Outcome{}, &applicationwrite.Error{Code: "EMAIL_IMPORT_OUTCOME_FAILED"}
	}}
	got, err := (Importer{Writer: w}).Import(context.Background(), parsed, emailrouting.SenderPurposeBoth)
	if err != nil || got.Status != "partial" || got.ErrorCode != "EMAIL_IMPORT_OUTCOME_FAILED" ||
		got.DiscoveryCount != 1 || got.ApplicationsUpdated != 0 {
		t.Fatalf("partial outcome=%+v err=%v", got, err)
	}
	w = &fakeWriter{capture: func(discovery.CaptureRequest) (*applicationwrite.Outcome, *applicationwrite.Error) {
		return &applicationwrite.Outcome{}, &applicationwrite.Error{Code: "DISCOVERY_WRITE_FAILED"}
	}}
	got, err = (Importer{Writer: w}).Import(context.Background(), parsed, emailrouting.SenderPurposeBoth)
	if err != nil || got.Status != "partial" || got.ErrorCode != "EMAIL_IMPORT_DISCOVERY_FAILED" ||
		got.DiscoveryCount != 0 || got.ApplicationsUpdated != 1 {
		t.Fatalf("partial Discovery=%+v err=%v", got, err)
	}
	w.outcome = func(emailcandidates.ApplicationOutcome) (*applicationwrite.Outcome, *applicationwrite.Error) {
		return &applicationwrite.Outcome{}, &applicationwrite.Error{Code: "EMAIL_IMPORT_OUTCOME_FAILED"}
	}
	got, err = (Importer{Writer: w}).Import(context.Background(), parsed, emailrouting.SenderPurposeBoth)
	if err != nil || got.Status != "failed" || got.ErrorCode != "EMAIL_IMPORT_DISCOVERY_FAILED" {
		t.Fatalf("all failed=%+v err=%v", got, err)
	}
}

func TestImporterOutcomeResolutionCounts(t *testing.T) {
	parsed := mixedAlert(t)
	parsed.ApplicationOutcomes = append(parsed.ApplicationOutcomes,
		emailcandidates.ApplicationOutcome{Ordinal: 2},
		emailcandidates.ApplicationOutcome{Ordinal: 3})
	resolutions := []struct {
		value, id string
		dismissed bool
	}{
		{"unmatched", "event_1", false},
		{"ambiguous", "event_2", false},
		{"discovery_dismissed", "event_3", true},
	}
	index := 0
	w := &fakeWriter{outcome: func(emailcandidates.ApplicationOutcome) (*applicationwrite.Outcome, *applicationwrite.Error) {
		item := resolutions[index]
		index++
		return ownedJSON(map[string]any{
			"outcome_id": item.id, "resolution": item.value, "discovery_changed": item.dismissed,
		}, true), nil
	}}
	got, err := (Importer{Writer: w}).Import(context.Background(), parsed, emailrouting.SenderPurposeApplicationTracking)
	if err != nil || got.Status != "imported" || got.ApplicationOutcomeCount != 3 ||
		got.ApplicationOutcomesUnlinked != 2 || got.ApplicationOutcomesNeedsReview != 1 ||
		got.DiscoveriesDismissed != 1 || got.CandidateCount != 0 || len(w.calls) != 3 {
		t.Fatalf("resolutions=%+v calls=%v err=%v", got, w.calls, err)
	}
}

func TestImporterUncertainOwnerStopsBeforeFollowingUnits(t *testing.T) {
	parsed := mixedAlert(t)
	for _, out := range []*applicationwrite.Outcome{
		{CommitAttempted: true},
		{CommitAttempted: true, Committed: true},
		{CommitAttempted: true, Committed: true, CleanupFailed: true},
		{CleanupFailed: true},
		ownedJSON(map[string]any{"unexpected": true}, true),
	} {
		w := &fakeWriter{outcome: func(emailcandidates.ApplicationOutcome) (*applicationwrite.Outcome, *applicationwrite.Error) {
			return out, &applicationwrite.Error{Code: "APPLICATION_LEDGER_COMMIT_UNCONFIRMED"}
		}}
		_, err := (Importer{Writer: w}).Import(context.Background(), parsed, emailrouting.SenderPurposeBoth)
		if !errors.Is(err, emailworker.ErrCommitUnknown) || len(w.calls) != 1 || w.calls[0] != "outcome" {
			t.Fatalf("out=%+v err=%v calls=%v", out, err, w.calls)
		}
	}
	w := &fakeWriter{capture: func(discovery.CaptureRequest) (*applicationwrite.Outcome, *applicationwrite.Error) {
		return ownedJSON(map[string]any{"unexpected": true}, true), nil
	}}
	_, err := (Importer{Writer: w}).Import(context.Background(), parsed, emailrouting.SenderPurposeBoth)
	if !errors.Is(err, emailworker.ErrCommitUnknown) || len(w.calls) != 2 {
		t.Fatalf("malformed post-commit capture err=%v calls=%v", err, w.calls)
	}
	w = &fakeWriter{outcome: func(emailcandidates.ApplicationOutcome) (*applicationwrite.Outcome, *applicationwrite.Error) {
		return &applicationwrite.Outcome{CommitAttempted: true, Committed: true}, nil
	}}
	_, err = (Importer{Writer: w}).Import(context.Background(), parsed, emailrouting.SenderPurposeBoth)
	if !errors.Is(err, emailworker.ErrCommitUnknown) || len(w.calls) != 1 {
		t.Fatalf("missing committed response err=%v calls=%v", err, w.calls)
	}
}

func TestImporterRejectsMalformedProjectionWithoutDiscardingEarlierOutcome(t *testing.T) {
	parsed := mixedAlert(t)
	parsed.Candidates[0].Ordinal = 0
	w := &fakeWriter{}
	got, err := (Importer{Writer: w}).Import(context.Background(), parsed, emailrouting.SenderPurposeBoth)
	if err != nil || got.Status != "partial" || got.ErrorCode != "EMAIL_IMPORT_DISCOVERY_FAILED" ||
		got.CandidateCount != 1 || got.DiscoveryCount != 0 || got.ApplicationsUpdated != 1 ||
		len(w.calls) != 1 || w.calls[0] != "outcome" {
		t.Fatalf("invalid projection=%+v calls=%v err=%v", got, w.calls, err)
	}
	got, err = (Importer{Writer: w}).Import(context.Background(), parsed, emailrouting.SenderPurposeApplicationTracking)
	if err != nil || got.Status != "imported" || got.CandidateCount != 0 {
		t.Fatalf("tracking-only import=%+v err=%v", got, err)
	}
}

func TestImporterInvalidRouteAndEnvelopeNeverCallOwner(t *testing.T) {
	parsed := mixedAlert(t)
	w := &fakeWriter{}
	_, err := (Importer{Writer: w}).Import(context.Background(), parsed, "unapproved")
	if !errors.Is(err, errProtocol) || len(w.calls) != 0 {
		t.Fatalf("invalid route err=%v calls=%v", err, w.calls)
	}
	parsed.SchemaVersion = 0
	got, err := (Importer{Writer: w}).Import(context.Background(), parsed, emailrouting.SenderPurposeBoth)
	if err != nil || got.Status != "failed" || got.ErrorCode != "EMAIL_IMPORT_FILE_INVALID" || len(w.calls) != 0 {
		t.Fatalf("invalid envelope=%+v calls=%v err=%v", got, w.calls, err)
	}
}
