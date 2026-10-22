package main

import (
	"testing"

	"careerhub.local/backend/internal/sidecar/readonly"
)

func TestEmailUpdatesFailureCategories(t *testing.T) {
	for _, tc := range []struct{ source, public string }{
		{"EMAIL_APPLICATION_UPDATE_INVALID", "EMAIL_APPLICATION_UPDATE_INVALID"},
		{"APPLICATION_READ_CANCELED", "EMAIL_APPLICATION_UPDATE_READ_CANCELED"},
		{"DATA_HOME_IDENTITY_CHANGED", "EMAIL_APPLICATION_UPDATE_READ_UNAVAILABLE"},
		{"APPLICATION_SCHEMA_TOO_NEW", "EMAIL_APPLICATION_UPDATE_READ_UNAVAILABLE"},
		{"EMAIL_APPLICATION_UPDATE_READ_FAILED", "EMAIL_APPLICATION_UPDATE_READ_FAILED"},
	} {
		got := emailUpdatesFailure(&readonly.Error{Code: tc.source})
		if got.Code != tc.public || got.Category != "email_application_updates_read" {
			t.Fatalf("%s mapped to %+v, want %s", tc.source, got, tc.public)
		}
	}
}
