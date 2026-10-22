package emailimport

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/emailalerts"
)

func requireNativeJSON(t *testing.T, value any) {
	t.Helper()
	switch item := value.(type) {
	case nil, bool, string, int, int64, float64, json.Number:
		return
	case []any:
		for _, child := range item {
			requireNativeJSON(t, child)
		}
	case map[string]any:
		for _, child := range item {
			requireNativeJSON(t, child)
		}
	default:
		t.Fatalf("metadata contains non-JSON Go type %T", value)
	}
}

func parsedFixture(t *testing.T) emailalerts.Result {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "email_alerts", "single_great_match_html.eml"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := emailalerts.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestSourceCandidateProjection(t *testing.T) {
	t.Parallel()
	parsed := parsedFixture(t)
	requests, err := ProjectCandidates(parsed, ChannelMailbox)
	if err != nil || len(requests) != 1 {
		t.Fatalf("requests=%d err=%v", len(requests), err)
	}
	request := requests[0]
	requireNativeJSON(t, request["metadata"])
	want := map[string]any{
		"source_kind":         "email_file",
		"source_channel":      "gmail_imap_v1",
		"request_fingerprint": "sha256:f1fc4b671d3eefe7dd4944936e8ba48e0d8406ea60ce3767c809cfce0708601c:v6:1",
		"observed_url":        "https://jobs.example.test/jobs/123?campaign=alpha",
		"posting_identity":    "",
		"title":               "Senior Data Analyst",
		"company":             "Example Analytics",
		"location":            "New York, NY",
		"content_scope":       "link_only",
		"source_text":         "Great match Senior Data Analyst Company: Example Analytics Location: New York, NY Summary: Build dashboards for product partners.",
		"observed_at":         "2026-08-14T08:30:00-04:00",
	}
	for key, expected := range want {
		if request[key] != expected {
			t.Errorf("%s=%v, want %v", key, request[key], expected)
		}
	}
	if len(request) != len(want)+1 {
		t.Fatalf("unexpected capture fields: %v", request)
	}
	encoded, err := json.Marshal(request["metadata"])
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(encoded, &metadata); err != nil {
		t.Fatal(err)
	}
	for key, expected := range map[string]any{
		"parser_schema_version": float64(6),
		"email_content_scope":   "snippet",
		"needs_content":         true,
		"source_provider":       "generic",
		"email_section_kind":    "job_alert",
		"sender_domain":         "example.test",
		"message_fingerprint":   parsed.MessageFingerprint,
		"message_id_hash":       parsed.MessageIDHash,
	} {
		if metadata[key] != expected {
			t.Errorf("metadata.%s=%v, want %v", key, metadata[key], expected)
		}
	}
	recommendation, ok := metadata["source_recommendation"].(map[string]any)
	if !ok || recommendation["kind"] != "great_match" || recommendation["evidence_text"] != "Great match" {
		t.Fatalf("recommendation=%v", metadata["source_recommendation"])
	}
	facts, ok := metadata["capture_facts"].(map[string]any)
	if !ok || facts["version"] != float64(1) || facts["location"] != "New York, NY" {
		t.Fatalf("facts=%v", metadata["capture_facts"])
	}
}

func TestProjectionRefusesNonJSONFactNumberBeforeReturningRequests(t *testing.T) {
	parsed := parsedFixture(t)
	parsed.Candidates[0].CaptureFacts.Compensation.Ranges = []discovery.CompensationRange{{Minimum: json.Number("NaN")}}
	requests, err := ProjectCandidates(parsed, ChannelMailbox)
	if !errors.Is(err, ErrProjectionInvalid) || requests != nil {
		t.Fatalf("invalid facts returned requests=%v err=%v", requests, err)
	}
}

func TestProjectionRejectsInvalidEnvelopeAndOrdinals(t *testing.T) {
	t.Parallel()
	valid := parsedFixture(t)
	cases := map[string]func(*emailalerts.Result){
		"schema":      func(r *emailalerts.Result) { r.SchemaVersion-- },
		"fingerprint": func(r *emailalerts.Result) { r.MessageFingerprint = "not-a-fingerprint" },
		"ordinal":     func(r *emailalerts.Result) { r.Candidates[0].Ordinal = 0 },
		"duplicate": func(r *emailalerts.Result) {
			r.Candidates = append(r.Candidates, r.Candidates[0])
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			copy := valid
			copy.Candidates = append(valid.Candidates[:0:0], valid.Candidates...)
			change(&copy)
			if _, err := ProjectCandidates(copy, ChannelMailbox); !errors.Is(err, ErrProjectionInvalid) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, err := ProjectCandidates(valid, "arbitrary-channel"); !errors.Is(err, ErrProjectionInvalid) {
		t.Fatalf("unapproved channel: %v", err)
	}
}
