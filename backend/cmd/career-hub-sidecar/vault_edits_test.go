package main

import (
	"testing"

	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/skills/catalogtransaction"
	"careerhub.local/backend/internal/skills/catalogwrite"
)

func TestCatalogRenamePreservesPublicationFacts(t *testing.T) {
	for _, tc := range []struct {
		name               string
		value              *catalogwrite.Result
		failure            *catalogwrite.Error
		committed, cleanup bool
	}{
		{name: "success", value: &catalogwrite.Result{}, committed: true},
		{name: "rejected", failure: &catalogwrite.Error{Stage: "input"}},
		{name: "readback", failure: &catalogwrite.Error{Stage: "readback", Committed: true}, committed: true},
		{name: "close", failure: &catalogwrite.Error{Stage: "close", Committed: true}, committed: true, cleanup: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, failure := catalogRenameResult(tc.value, tc.failure)
			if out == nil || out.Committed != tc.committed || out.CleanupFailed != tc.cleanup || (failure != nil) != (tc.failure != nil) {
				t.Fatalf("publication facts: out=%+v failure=%+v", out, failure)
			}
		})
	}
}

func TestVaultWriteFactsPreservePartialPublicationWithoutPaths(t *testing.T) {
	out := &httpapi.ApplicationOutcome{}
	applyWriteFacts(out, catalogtransaction.Report{IntentRecorded: true, AppliedFiles: []string{"private/record.yaml"}})
	if !out.Committed || !out.Partial || out.Write == nil || out.Write.AppliedFiles != 1 || out.Write.Completed {
		t.Fatalf("partial publication lost: %+v", out)
	}
}
