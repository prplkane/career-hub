package emailconnection

import (
	"context"
	"testing"
)

func TestEditSettingsRefusesUnowned(t *testing.T) {
	input := EditSettingsInput{AccountAddress: "alerts@example.test", JobSenders: []string{"jobs@example.test"}, ExpectedRevision: 1}
	result, problem := EditSettings(context.Background(), t.TempDir(), &fakeSecrets{}, input)
	if result != (EditSettingsResult{}) || problem == nil || problem.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatalf("unowned settings edit admitted: %+v %v", result, problem)
	}
}
