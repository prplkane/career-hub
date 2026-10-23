//go:build windows

package applicationwrite

import (
	"fmt"
	"strings"
	"testing"
)

const sessionFixtureNow = "2026-09-09T12:00:00Z"

func TestSessionCapabilityDiagnosticFormatting(t *testing.T) {
	cap := SessionCapability{SessionID: "private-id", Token: "private-token", TabID: 1, PageOrigin: "private-origin"}
	signal := SubmissionSignal{Capability: cap, EventKey: "private-event", SuccessRule: "private-rule", SubmitObservedAt: "private-time", SuccessObservedAt: "private-time"}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		if strings.Contains(fmt.Sprintf(format, cap), "private") || strings.Contains(fmt.Sprintf(format, signal), "private") {
			t.Fatal("capability leaked in diagnostic formatting")
		}
	}
}
