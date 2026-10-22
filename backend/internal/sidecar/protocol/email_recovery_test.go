package protocol

import "testing"

func TestEmailCredentialRecoveryMaintenanceValidation(t *testing.T) {
	base := func() map[string]any {
		p := recoveryMaintenancePayload(EmailCredentialRecoveryCommand)
		p["confirm"] = true
		return p
	}
	got, err := ParseEmailCredentialRecoveryMaintenance(base(), PathOptions{Style: Windows})
	requireCode(t, err, "")
	if got.Channel != "dev" || got.DataRoot != `C:\data` || got.ResourceRoot != `C:\resources` || got.Port != 0 {
		t.Fatal("recovery identity changed")
	}
	for _, tt := range []struct {
		field string
		value any
		code  string
	}{
		{"confirm", false, "SIDECAR_MAINTENANCE_CONFIRMATION_INVALID"},
		{"confirm", "true", "SIDECAR_MAINTENANCE_CONFIRMATION_INVALID"},
		{"confirm", nil, "SIDECAR_MAINTENANCE_CONFIRMATION_INVALID"},
		{"secret_name", "not-authority", "SIDECAR_RECORD_INVALID_FIELDS"},
		{"password", "not-accepted", "SIDECAR_RECORD_INVALID_FIELDS"},
		{"protocol_version", true, "SIDECAR_PROTOCOL_UNSUPPORTED"},
		{"channel", "other", "SIDECAR_START_INVALID_CHANNEL"},
		{"data_root", "relative", "SIDECAR_START_INVALID_PATH"},
		{"resource_root", "relative", "SIDECAR_START_INVALID_PATH"},
		{"command", ApprovalRecoveryCommand, "SIDECAR_COMMAND_UNSUPPORTED"},
	} {
		p := base()
		p[tt.field] = tt.value
		_, err := ParseEmailCredentialRecoveryMaintenance(p, PathOptions{Style: Windows})
		requireCode(t, err, tt.code)
	}
	p := base()
	delete(p, "confirm")
	_, err = ParseEmailCredentialRecoveryMaintenance(p, PathOptions{Style: Windows})
	requireCode(t, err, "SIDECAR_RECORD_INVALID_FIELDS")
}
