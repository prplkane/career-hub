package main

import (
	"encoding/json"
	"testing"

	"careerhub.local/backend/internal/applicationwrite"
)

func TestJobIntakeAssessmentProjection(t *testing.T) {
	wrapper := json.RawMessage(`{"assessment":{"fit":"strong"},"cache_hit":false}`)
	fresh, err := jobIntakeAssessment(&freshJobProgress{Assessment: &applicationwrite.Outcome{Result: wrapper}}, false)
	if err != nil || string(fresh) != `{"fit":"strong"}` {
		t.Fatal("fresh assessment projection changed", string(fresh), err)
	}
	duplicate, err := jobIntakeAssessment(&freshJobProgress{Duplicate: &applicationwrite.Outcome{Result: json.RawMessage(`{"duplicate":true,"assessment":{"assessment":{"fit":"strong"},"cache_hit":true}}`)}}, true)
	if err != nil || string(duplicate) != `{"fit":"strong"}` {
		t.Fatal("duplicate assessment wrapper leaked", string(duplicate), err)
	}
	for _, progress := range []*freshJobProgress{nil, {}, {Assessment: &applicationwrite.Outcome{Result: json.RawMessage(`{"cache_hit":false}`)}}} {
		if value, err := jobIntakeAssessment(progress, false); err == nil || value != nil {
			t.Fatal("invalid assessment accepted", progress, value)
		}
	}
}

func TestJobIntakeProgressFactsAndErrorClassification(t *testing.T) {
	progress := &freshJobProgress{RunID: "20260920T161500.000000Z_1234abcd", RecoveryRequired: true,
		Files:  &applicationwrite.NewJobFilePhaseOutcome{RawObserved: true, Unconfirmed: true, RecoveryRequired: true},
		Ledger: &applicationwrite.Outcome{CommitAttempted: true, Committed: true, CleanupFailed: true}}
	out := jobIntakeProgressOutcome(progress, true)
	if !out.Committed || !out.CommitUnknown || !out.RecoveryRequired || !out.CleanupFailed || !out.Partial || out.RunID != progress.RunID {
		t.Fatal("intake phase facts collapsed", out)
	}
	if got := jobIntakeError(out, &applicationwrite.Error{Stage: "source", Code: "JOB_INTAKE_RETRY_CONFLICT"}); got.Code != "JOB_INTAKE_RETRY_CONFLICT" {
		t.Fatal("retry conflict classification changed", got)
	}
	if got := jobIntakeError(out, &applicationwrite.Error{Stage: "files", Code: "JOB_INTAKE_FILE_UNCONFIRMED"}); got.Code != "JOB_INTAKE_RECOVERY_REQUIRED" {
		t.Fatal("unknown effect did not require recovery", got)
	}
	settled := jobIntakeProgressOutcome(&freshJobProgress{
		Files:        &applicationwrite.NewJobFilePhaseOutcome{RawObserved: true, CardObserved: true, Complete: true, RecoveryRequired: true},
		Assessment:   &applicationwrite.Outcome{Committed: true, RecoveryRequired: true},
		Finalization: &applicationwrite.NewJobTerminalOutcome{ReceiptObserved: true, RemovalObserved: true},
	}, false)
	if settled.RecoveryRequired || !settled.Committed || settled.Partial {
		t.Fatal("settled terminal intake retained an intermediate recovery flag", settled)
	}
}

func TestJobIntakeNamespaceEffectIsNotAJobCommit(t *testing.T) {
	progress := &freshJobProgress{Files: &applicationwrite.NewJobFilePhaseOutcome{NamespaceMayHaveChanged: true}}
	out := jobIntakeProgressOutcome(progress, true)
	if !out.Partial || out.Committed || out.CommitUnknown || out.RecoveryRequired {
		t.Fatal("directory-only effects confused with a job commit", out)
	}
	phase, recovery := intakeProgressState(progress)
	if phase != "namespace" || recovery {
		t.Fatal("namespace effect lost in diagnostics", phase, recovery)
	}
	for _, code := range []string{"JOB_INTAKE_STORAGE_UNAVAILABLE", "JOB_INTAKE_PATH_UNAVAILABLE", "JOB_INTAKE_PATH_CONFLICT"} {
		if got := jobIntakeError(out, &applicationwrite.Error{Stage: "preflight", Code: code}); got.Code != "JOB_INTAKE_STORAGE_UNAVAILABLE" {
			t.Fatal("storage failure blamed on user input", got)
		}
	}
}
