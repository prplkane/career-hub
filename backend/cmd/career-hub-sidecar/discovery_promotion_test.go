package main

import (
	"careerhub.local/backend/internal/sidecar/httpapi"
	"testing"
)

func TestPromotionPreservesPriorAndUncertainEffects(t *testing.T) {
	prior := &httpapi.ApplicationOutcome{Committed: true, RunID: "retained"}
	out := mergePromotionFacts(prior, &httpapi.ApplicationOutcome{CommitUnknown: true, RecoveryRequired: true, CleanupFailed: true})
	out, err := promotionFailure(out, "DISCOVERY_WRITE_FAILED")
	if err == nil || !out.Committed || !out.CommitUnknown || !out.RecoveryRequired || !out.CleanupFailed || !out.Partial || out.Result != nil || out.RunID != "retained" {
		t.Fatal("promotion failure erased previous effects")
	}
}
