package runtime

import (
	"fmt"
	"testing"
)

func TestOwnershipStartupCodesAreExplicit(t *testing.T) {
	for _, code := range []string{"DATA_HOME_IN_USE", "DATA_HOME_OWNERSHIP_UNAVAILABLE", "DATA_HOME_OWNERSHIP_UNSUPPORTED"} {
		if got := initializationCode(fmt.Errorf("private native context: %w", &StartupError{Code: code})); got != code {
			t.Fatal("known ownership failure lost its stable category")
		}
	}
	if got := initializationCode(&StartupError{Code: "private ownership detail"}); got != "SIDECAR_STARTUP_FAILED" {
		t.Fatal("unknown ownership detail escaped the public allowlist")
	}
}
