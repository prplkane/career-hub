package main

import (
	"encoding/json"
	"testing"

	"careerhub.local/backend/internal/applications/applicationwrite"
)

func candidateOwnerOutcome(raw string, committed bool) *applicationwrite.Outcome {
	return &applicationwrite.Outcome{Result: json.RawMessage(raw), CommitAttempted: committed, Committed: committed}
}

func TestContactCandidateResultSelectsCreatedAndValidatesProjection(t *testing.T) {
	preview := `{"contract_version":1,"source":{},"candidate":{},"defaults":{},"matches":{},"claimed_contact":null}`
	out, problem := contactCandidateResult(candidateOwnerOutcome(preview, false), nil, false)
	if problem != nil || out == nil || out.Created || string(out.Result) != preview {
		t.Fatal("valid preview projection changed", out, problem)
	}
	for _, test := range []struct {
		outcome string
		created bool
	}{{"created", true}, {"provenance_match", false}, {"linked", false}, {"unchanged", false}} {
		raw := `{"contract_version":1,"outcome":"` + test.outcome + `","contact":{}}`
		out, problem = contactCandidateResult(candidateOwnerOutcome(raw, test.created), nil, true)
		if problem != nil || out == nil || out.Created != test.created || out.Committed != test.created {
			t.Fatal("confirmation status projection changed", test, out, problem)
		}
	}
	for _, test := range []struct {
		raw          string
		confirmation bool
		expected     string
	}{
		{`{}`, false, "CONTACT_CANDIDATE_PREVIEW_FAILED"},
		{`{"contract_version":2,"outcome":"created","contact":{}}`, true, "CONTACT_CANDIDATE_SAVE_FAILED"},
		{`{"contract_version":1,"outcome":"other","contact":{}}`, true, "CONTACT_CANDIDATE_SAVE_FAILED"},
	} {
		_, problem = contactCandidateResult(candidateOwnerOutcome(test.raw, false), nil, test.confirmation)
		if problem == nil || problem.Code != test.expected {
			t.Fatal("invalid owner projection escaped", test, problem)
		}
	}
	for _, test := range []struct {
		value        *applicationwrite.Outcome
		confirmation bool
		expected     string
	}{
		{candidateOwnerOutcome(preview, true), false, "CONTACT_CANDIDATE_PREVIEW_FAILED"},
		{candidateOwnerOutcome(`{"contract_version":1,"outcome":"created","contact":{}}`, false), true, "CONTACT_CANDIDATE_SAVE_FAILED"},
	} {
		_, problem = contactCandidateResult(test.value, nil, test.confirmation)
		if problem == nil || problem.Code != test.expected {
			t.Fatal("contradictory success facts escaped", test.expected, problem)
		}
	}
}

func TestContactCandidateResultMapsOnlyPublicErrorsAndKeepsFacts(t *testing.T) {
	tests := []struct{ owner, class, public string }{
		{"CONTACT_CANDIDATE_STALE", "ContactCandidateRequestError", "CONTACT_CANDIDATE_STALE"},
		{"CONTACT_EXACT_MATCH_REQUIRES_LINK", "ContactCandidateRequestError", "CONTACT_EXACT_MATCH_REQUIRES_LINK"},
		{"private", "ApplicationContactNotFound", "CONTACT_NOT_FOUND"},
		{"private", "ApplicationContactRevisionConflict", "CONTACT_CONFLICT"},
		{"private", "ApplicationContactError", "CONTACT_REQUEST_INVALID"},
		{"APPLICATION_LEDGER_CANCELED", "", "LOCAL_OPERATION_CANCELED"},
		{"APPLICATION_LEDGER_COMMIT_UNCONFIRMED", "", "CONTACT_CANDIDATE_SAVE_UNCONFIRMED"},
		{"private", "", "CONTACT_CANDIDATE_SAVE_FAILED"},
	}
	for _, test := range tests {
		value := &applicationwrite.Outcome{CommitAttempted: true, Committed: false, CleanupFailed: true, RecoveryRequired: true}
		out, problem := contactCandidateResult(value, &applicationwrite.Error{Code: test.owner, SourceClass: test.class, SourceMessage: "private candidate and path"}, true)
		if problem == nil || problem.Code != test.public || out == nil || !out.CommitUnknown || !out.CleanupFailed || !out.RecoveryRequired {
			t.Fatal("candidate error mapping or facts changed", test, out, problem)
		}
	}
}
