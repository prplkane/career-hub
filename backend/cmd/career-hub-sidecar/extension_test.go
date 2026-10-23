package main

import (
	"encoding/json"
	"testing"

	"careerhub.local/backend/internal/applications/applicationhelper"
	"careerhub.local/backend/internal/applications/applicationwrite"
)

func TestExtensionSessionOutcomePreservesFacts(t *testing.T) {
	for _, tc := range []struct{ class, code string }{
		{"ApplicationSessionUnauthorized", "APPLICATION_SESSION_UNAUTHORIZED"},
		{"ApplicationSessionNotFound", "APPLICATION_SESSION_NOT_FOUND"},
		{"ApplicationSessionExpired", "APPLICATION_SESSION_EXPIRED"},
		{"ApplicationSessionClaimUnavailable", "APPLICATION_SESSION_NOT_PENDING"},
		{"ApplicationSessionClaimAmbiguous", "APPLICATION_SESSION_CLAIM_AMBIGUOUS"},
		{"ApplicationSessionRevisionConflict", "APPLICATION_SESSION_REVISION_CONFLICT"},
		{"ApplicationSessionEventConflict", "APPLICATION_SESSION_EVENT_CONFLICT"},
		{"ApplicationNotFound", "APPLICATION_JOB_NOT_FOUND"},
		{"ApplicationSessionError", "APPLICATION_SESSION_INVALID"},
		{"private class", "APPLICATION_SESSION_FAILED"},
	} {
		out, problem := extensionSessionOutcome(&applicationwrite.Outcome{CommitAttempted: true, CleanupFailed: true, RecoveryRequired: true}, &applicationwrite.Error{SourceClass: tc.class, Code: "private internal cause", SourceMessage: "private token"})
		if problem == nil || problem.Code != tc.code || out == nil || !out.CommitUnknown || !out.CleanupFailed || !out.RecoveryRequired || out.Committed {
			t.Fatal("session error/facts changed")
		}
	}
	raw := json.RawMessage(`{"session_token":"purpose-limited"}`)
	out, problem := extensionSessionOutcome(&applicationwrite.Outcome{Committed: true, Result: raw}, nil)
	if problem != nil || !out.Committed || string(out.Result) != string(raw) {
		t.Fatal("authorized claim reply changed")
	}
}

func TestExtensionHelperOutcomePreservesUncertainty(t *testing.T) {
	for _, committed := range []bool{false, true} {
		out, problem := extensionHelperOutcome(&applicationhelper.Outcome{Committed: committed, CleanupFailed: true, Result: json.RawMessage(`{"private":"value"}`)}, &applicationhelper.Error{Stage: "save", Code: "FILE_PRIVATE_CAUSE"})
		if problem == nil || problem.Code != "APPLICATION_HELPER_OPERATION_FAILED" || out.Committed != committed || out.CommitUnknown == committed || !out.CleanupFailed || out.Result != nil {
			t.Fatal("helper failure lost facts or exposed partial content")
		}
	}
	for _, code := range []string{"APPLICATION_ANSWER_INVALID", "APPLICATION_ANSWER_REVISION_CONFLICT", "APPLICATION_HELPER_SOURCE_UNAVAILABLE", "APPLICATION_HELPER_CONFIGURATION_INVALID"} {
		out, problem := extensionHelperOutcome(nil, &applicationhelper.Error{Code: code})
		if problem == nil || problem.Code != code || out.CommitUnknown {
			t.Fatal("helper read/validation failure misclassified")
		}
	}
}
