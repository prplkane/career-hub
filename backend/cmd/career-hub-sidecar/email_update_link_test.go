package main

import (
	"testing"

	"careerhub.local/backend/internal/applicationwrite"
)

func TestEmailLinkFailureCategories(t *testing.T) {
	for _, tc := range []struct{ source, public string }{
		{"EMAIL_OUTCOME_LINK_INVALID", "EMAIL_APPLICATION_UPDATE_INVALID"},
		{"EMAIL_OUTCOME_LINK_ALREADY_RESOLVED", "EMAIL_APPLICATION_UPDATE_INVALID"},
		{"EMAIL_OUTCOME_LINK_NOT_FOUND", "EMAIL_APPLICATION_UPDATE_NOT_FOUND"},
		{"EMAIL_OUTCOME_LINK_REVISION_CONFLICT", "EMAIL_APPLICATION_UPDATE_REVISION_CONFLICT"},
		{"APPLICATION_LEDGER_CANCELED", "EMAIL_APPLICATION_UPDATE_LINK_CANCELED"},
		{"APPLICATION_LEDGER_COMMIT_UNCONFIRMED", "EMAIL_APPLICATION_UPDATE_LINK_COMMIT_UNCONFIRMED"},
		{"APPLICATION_LEDGER_HOME_CHANGED", "EMAIL_APPLICATION_UPDATE_LINK_UNAVAILABLE"},
		{"EMAIL_OUTCOME_WRITE_FAILED", "EMAIL_APPLICATION_UPDATE_LINK_FAILED"},
	} {
		got := emailLinkFailure(&applicationwrite.Error{Code: tc.source})
		if got.Code != tc.public || got.Category != "email_application_update_link" {
			t.Fatalf("%s mapped to %+v, want %s", tc.source, got, tc.public)
		}
	}
}
