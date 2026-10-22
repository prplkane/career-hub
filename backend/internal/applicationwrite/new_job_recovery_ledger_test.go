package applicationwrite

import (
	"testing"

	"careerhub.local/backend/internal/applicationreview"
	"careerhub.local/backend/internal/cataloginput"
)

func TestNewJobLedgerRecoveryClassification(t *testing.T) {
	card, parseErr := cataloginput.ParseYAML([]byte("company: Fictional\ntitle: Engineer\nsource_url: https://example.test/job\n"))
	if parseErr != nil {
		t.Fatal(parseErr.Code)
	}
	intent := testNewJobIntent([]byte("fictional JD"), []byte("fictional card"))
	facts := newJobLedgerFacts{company: "Fictional", title: "Engineer", sourceURL: "https://example.test/job",
		fingerprint: intent.ContentFingerprint, status: "captured", allEvents: 1, capturedEvents: 1}
	if state := classifyNewJobLedger(intent, card, facts); state != "captured" {
		t.Fatal("unassessed Captured was misclassified", state)
	}
	facts.assessment = &applicationreview.VersionedAssessment{JobID: intent.JobID, JobFingerprint: intent.CardSHA256, Artifact: "{}"}
	if state := classifyNewJobLedger(intent, card, facts); state != "assessed" {
		t.Fatal("assessed Captured was misclassified", state)
	}
	facts.status, facts.allEvents, facts.appliedEvents, facts.resumeRecorded, facts.resumeSnapshots, facts.initialResumeSnapshots = "applied", 2, 1, 1, 1, 1
	if state := classifyNewJobLedger(intent, card, facts); state != "conflict" {
		t.Fatal("Captured intent adopted Applied", state)
	}
	intent.InitialStatus = "applied"
	if state := classifyNewJobLedger(intent, card, facts); state != "applied" {
		t.Fatal("explicit Applied was misclassified", state)
	}
	facts.title = "Other employer's role"
	if state := classifyNewJobLedger(intent, card, facts); state != "conflict" {
		t.Fatal("wrong SQLite identity was adopted", state)
	}
	facts.title = "Engineer"
	facts.assessment.JobFingerprint = "wrong"
	if state := classifyNewJobLedger(intent, card, facts); state != "conflict" {
		t.Fatal("wrong assessment was adopted", state)
	}
	facts.assessment.JobFingerprint = intent.CardSHA256
	facts.initialResumeSnapshots = 0
	if state := classifyNewJobLedger(intent, card, facts); state != "conflict" {
		t.Fatal("unrelated resume event was adopted as initial Applied snapshot", state)
	}
	facts.initialResumeSnapshots = 1
	facts.allEvents = 3
	if state := classifyNewJobLedger(intent, card, facts); state != "conflict" {
		t.Fatal("extra status history was adopted", state)
	}
}
