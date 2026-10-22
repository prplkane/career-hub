package startup

import (
	"testing"

	"careerhub.local/backend/internal/datahome"
)

func TestRequireNewJobRecoveryClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   *datahome.RecoveryClassification
		code string
	}{
		{"nil", nil, "DATA_HOME_UNAVAILABLE"},
		{"markerless", &datahome.RecoveryClassification{Cardinality: datahome.RecoverySingle, Kind: datahome.RecoveryNewJob, Marker: datahome.RecoveryMarkerless}, "DATA_HOME_RECOVERY_MARKER_REQUIRED"},
		{"none", &datahome.RecoveryClassification{Cardinality: datahome.RecoveryNone, Marker: datahome.RecoveryMarkerInitialized}, "DATA_HOME_RECOVERY_NOT_REQUIRED"},
		{"multiple", &datahome.RecoveryClassification{Cardinality: datahome.RecoveryMultiple, Marker: datahome.RecoveryMarkerInitialized}, "DATA_HOME_RECOVERY_AMBIGUOUS"},
		{"unsupported", &datahome.RecoveryClassification{Cardinality: datahome.RecoverySingle, Kind: datahome.RecoveryResume, Marker: datahome.RecoveryMarkerInitialized}, "DATA_HOME_RECOVERY_KIND_UNSUPPORTED"},
		{"new job", &datahome.RecoveryClassification{Cardinality: datahome.RecoverySingle, Kind: datahome.RecoveryNewJob, Marker: datahome.RecoveryMarkerInitialized}, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := requireNewJobRecovery(test.in)
			if test.code == "" {
				if err != nil {
					t.Fatalf("supported classification failed: %v", err)
				}
				return
			}
			typed, ok := err.(*Error)
			if !ok || typed.Code != test.code {
				t.Fatalf("wrong refusal: %#v", err)
			}
		})
	}
}
