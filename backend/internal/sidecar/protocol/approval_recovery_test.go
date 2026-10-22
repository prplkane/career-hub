package protocol

import "testing"

func TestApprovalRecoveryMaintenanceValidation(t *testing.T) {
	base := func() map[string]any {
		p := recoveryMaintenancePayload(ApprovalRecoveryCommand)
		p["confirm"] = true
		return p
	}
	got, err := ParseApprovalRecoveryMaintenance(base(), PathOptions{Style: Windows})
	requireCode(t, err, "")
	if got.Port != 0 || got.Channel != "dev" || got.DataRoot != `C:\data` || got.ResourceRoot != `C:\resources` {
		t.Fatal("incorrect recovery request")
	}
	for _, tt := range []struct {
		field string
		value any
		code  string
	}{
		{"confirm", false, "SIDECAR_MAINTENANCE_CONFIRMATION_INVALID"},
		{"confirm", "true", "SIDECAR_MAINTENANCE_CONFIRMATION_INVALID"},
		{"confirm", nil, "SIDECAR_MAINTENANCE_CONFIRMATION_INVALID"},
		{"extra", true, "SIDECAR_RECORD_INVALID_FIELDS"},
		{"protocol_version", true, "SIDECAR_PROTOCOL_UNSUPPORTED"},
		{"data_root", "relative", "SIDECAR_START_INVALID_PATH"},
		{"resource_root", "relative", "SIDECAR_START_INVALID_PATH"},
		{"channel", "other", "SIDECAR_START_INVALID_CHANNEL"},
	} {
		p := base()
		p[tt.field] = tt.value
		_, err := ParseApprovalRecoveryMaintenance(p, PathOptions{Style: Windows})
		requireCode(t, err, tt.code)
	}
	p := base()
	delete(p, "confirm")
	_, err = ParseApprovalRecoveryMaintenance(p, PathOptions{Style: Windows})
	requireCode(t, err, "SIDECAR_RECORD_INVALID_FIELDS")
}
