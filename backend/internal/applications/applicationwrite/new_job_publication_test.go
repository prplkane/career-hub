package applicationwrite

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/datahome"
)

func frozenNewJobFixture(t *testing.T) (newJobIntent, []byte, []byte) {
	t.Helper()
	raw := []byte("Build Go services and document the production interfaces.")
	processed, sourceErr := jobcard.IntakeDigestSource(string(raw))
	if sourceErr != nil {
		t.Fatal(sourceErr.Code)
	}
	fingerprint, fingerprintErr := applicationanalysis.SourceFingerprint(processed)
	if fingerprintErr != nil {
		t.Fatal(fingerprintErr)
	}
	cardText := fmt.Sprintf(`id: synthetic
status: draft
company: Fictional
title: Software Engineer
source_url: https://example.test/jobs/1
location: ''
remote_policy: ''
seniority: ''
required_skills: [Go]
required_platforms: []
required_capabilities: []
preferred_skills: []
experience_requirements: []
education_requirements: []
responsibilities: [Build Go services.]
visa_signals:
  citizenship_required: null
  clearance_required: null
  sponsorship_mentioned: null
  no_visa_or_opt: null
raw_jd_path: jobs/incoming/synthetic.txt
content_fingerprint: %s
`, fingerprint)
	parsed, parseErr := cataloginput.ParseYAML([]byte(cardText))
	if parseErr != nil {
		t.Fatal(parseErr.Code)
	}
	card, encodeErr := jobcard.EncodeCard(context.Background(), parsed)
	if encodeErr != nil {
		t.Fatal(encodeErr.Code)
	}
	intent := newJobIntent{Version: 1,
		Home:        datahome.Info{FormatVersion: 2, Channel: "dev", Initialized: true, DataHomeID: "dh_" + strings.Repeat("a", 32)},
		OperationID: strings.Repeat("e", 32), InitialStatus: "captured", JobID: "synthetic",
		RawPath: "jobs/incoming/synthetic.txt", CardPath: "jobs/normalized/synthetic.yaml",
		RawSize: len(raw), RawSHA256: digestBytes(raw), CardSize: len(card), CardSHA256: digestBytes(card), ContentFingerprint: fingerprint}
	if err := validateFrozenNewJob(context.Background(), intent, raw, card); err != nil {
		t.Fatal(err.Code)
	}
	return intent, raw, card
}

func TestNewJobPublicationPreflightRejectsChangedDraft(t *testing.T) {
	intent, raw, card := frozenNewJobFixture(t)
	changed := bytes.Replace(card, []byte("draft"), []byte("ready"), 1)
	if bytes.Equal(card, changed) {
		t.Fatal("draft fixture did not change")
	}
	intent.CardSize, intent.CardSHA256 = len(changed), digestBytes(changed)
	if err := validateFrozenNewJob(context.Background(), intent, raw, changed); err == nil || err.Code != "JOB_INTAKE_CARD_INVALID" {
		t.Fatal("non-draft card passed prepublication validation")
	}
}
