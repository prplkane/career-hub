package emailoutcomes

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"careerhub.local/backend/internal/emailalerts"
	"careerhub.local/backend/internal/emailcandidates"
)

func parsedFixture(t *testing.T, name string) emailalerts.Result {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "email_alerts", name))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := emailalerts.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestSourceConfirmedOutcomes(t *testing.T) {
	t.Parallel()
	const recorded = "2026-08-20T10:00:00Z"
	for _, example := range []struct {
		name, id, provider, identity, status, observed string
	}{
		{"linkedin_rejection_with_suggestions.eml", "email_outcome_484d82b25d24cdd580d71637063d54287c9456eeca80b26a09d8fa8e3fd10b91", "linkedin", "linkedin:4451000001", "rejected", "2026-08-15T23:11:02Z"},
		{"handshake_application_sent_with_suggestions.eml", "email_outcome_4c15dd8235350779e47b218847feb8b2040151a602d31402b568df4401ee45cb", "handshake", "", "applied", "2026-08-18T22:45:00Z"},
	} {
		t.Run(example.provider, func(t *testing.T) {
			parsed := parsedFixture(t, example.name)
			if len(parsed.ApplicationOutcomes) != 1 {
				t.Fatalf("outcomes=%d", len(parsed.ApplicationOutcomes))
			}
			got, err := Normalize(parsed.MessageFingerprint, parsed.ApplicationOutcomes[0], parsed.ReceivedAt, recorded)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != example.id || got.Provider != example.provider || got.PostingIdentity != example.identity ||
				got.ApplicationStatus != example.status || got.ObservedAt != example.observed {
				t.Fatalf("normalized=%+v", got)
			}
		})
	}
}

func TestOutcomeValidationFailsClosed(t *testing.T) {
	t.Parallel()
	parsed := parsedFixture(t, "linkedin_rejection_with_suggestions.eml")
	base := parsed.ApplicationOutcomes[0]
	for name, mutate := range map[string]func(*emailcandidates.ApplicationOutcome){
		"status":   func(o *emailcandidates.ApplicationOutcome) { o.ApplicationStatus = "applied" },
		"provider": func(o *emailcandidates.ApplicationOutcome) { o.SourceProvider = "unknown" },
		"evidence": func(o *emailcandidates.ApplicationOutcome) { o.EvidenceCode = "" },
		"identity": func(o *emailcandidates.ApplicationOutcome) { o.PostingIdentity = "linkedin:999" },
		"url":      func(o *emailcandidates.ApplicationOutcome) { o.SourceURL = "https://www.linkedin.com/jobs/view/999/" },
		"control":  func(o *emailcandidates.ApplicationOutcome) { o.Company = "unsafe\x01name" },
		"too_long": func(o *emailcandidates.ApplicationOutcome) { o.Title = strings.Repeat("x", 501) },
		"bad_utf8": func(o *emailcandidates.ApplicationOutcome) { o.Location = "\xff" },
	} {
		t.Run(name, func(t *testing.T) {
			outcome := base
			mutate(&outcome)
			if _, err := Normalize(parsed.MessageFingerprint, outcome, parsed.ReceivedAt, "2026-08-20T10:00:00Z"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	for _, fingerprint := range []string{"", "sha256:ABC", strings.Repeat("a", 64)} {
		if _, err := Normalize(fingerprint, base, parsed.ReceivedAt, "2026-08-20T10:00:00Z"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("fingerprint %q: %v", fingerprint, err)
		}
	}
	for _, observed := range []string{"not a timestamp", "2026-99-20"} {
		if _, err := Normalize(parsed.MessageFingerprint, base, observed, "2026-08-20T10:00:00Z"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("observed %q: %v", observed, err)
		}
	}
}

func TestHandshakeOpaqueEventCannotClaimPosting(t *testing.T) {
	t.Parallel()
	parsed := parsedFixture(t, "handshake_application_sent_with_suggestions.eml")
	base := parsed.ApplicationOutcomes[0]
	for _, sourceURL := range []string{"https://email.notifications.joinhandshake.com/c/opaque", "https://app.joinhandshake.com/job-search/99"} {
		outcome := base
		outcome.SourceURL = sourceURL
		if _, err := Normalize(parsed.MessageFingerprint, outcome, parsed.ReceivedAt, "2026-08-20T10:00:00Z"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("sourceURL %q: %v", sourceURL, err)
		}
	}
	matching := base
	matching.PostingIdentity = "handshake:99"
	matching.SourceURL = "https://app.joinhandshake.com/job-search/99"
	if _, err := Normalize(parsed.MessageFingerprint, matching, "", "2026-08-20T10:00:00Z"); err != nil {
		t.Fatalf("exact provider URL: %v", err)
	}
	missingCompany := base
	missingCompany.Company = ""
	if _, err := Normalize(parsed.MessageFingerprint, missingCompany, parsed.ReceivedAt, "2026-08-20T10:00:00Z"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing company: %v", err)
	}
}

func TestOutcomeTimeAndTextNormalization(t *testing.T) {
	t.Parallel()
	parsed := parsedFixture(t, "linkedin_rejection_with_suggestions.eml")
	outcome := parsed.ApplicationOutcomes[0]
	outcome.SourceProvider = " LINKEDIN "
	outcome.PostingIdentity = " LINKEDIN:4451000001 "
	outcome.ApplicationStatus = " REJECTED "
	outcome.Title = " Data\tEngineer "
	got, err := Normalize(parsed.MessageFingerprint, outcome, "2026-08-18", "2026-08-20T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "linkedin" || got.Title != "Data Engineer" || got.ObservedAt != "2026-08-18T00:00:00Z" {
		t.Fatalf("normalized=%+v", got)
	}
	got, err = Normalize(parsed.MessageFingerprint, outcome, "", "2026-08-20T10:00:00Z")
	if err != nil || got.ObservedAt != "2026-08-20T10:00:00Z" {
		t.Fatalf("fallback=%+v err=%v", got, err)
	}
}
