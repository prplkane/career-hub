package readonly

import (
	"testing"

	"careerhub.local/backend/internal/email/emailoutcomes"
)

func TestEmailUpdateReadErrorCategories(t *testing.T) {
	for _, tc := range []struct{ source, public string }{
		{"INPUT_INVALID", "EMAIL_APPLICATION_UPDATE_INVALID"},
		{"VALUE_INVALID", "EMAIL_APPLICATION_UPDATE_READ_FAILED"},
		{"SCHEMA_TOO_NEW", "APPLICATION_SCHEMA_TOO_NEW"},
		{"SCHEMA_PREPARATION_REQUIRED", "APPLICATION_SCHEMA_PREPARATION_REQUIRED"},
		{"CANCELED", "APPLICATION_READ_CANCELED"},
		{"READ_FAILED", "EMAIL_APPLICATION_UPDATE_READ_FAILED"},
	} {
		got := emailUpdateReadError(&emailoutcomes.UpdateReadError{Code: tc.source})
		if safe, ok := got.(*Error); !ok || safe.Code != tc.public {
			t.Fatalf("%s mapped to %v, want %s", tc.source, got, tc.public)
		}
	}
}
