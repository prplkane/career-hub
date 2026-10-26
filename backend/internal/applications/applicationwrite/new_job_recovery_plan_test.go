package applicationwrite

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPlanIncompleteNewJobRecoveryClassifiesExactStates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, initial, files, ledger, receipt string
		disposition, step, reason             string
		advance                               bool
	}{
		{"no files", "captured", "none", "not_checked", "not_checked", "manual_review", "", "JOB_INTAKE_RECOVERY_FILES_INCOMPLETE", false},
		{"raw only", "captured", "raw_only", "not_checked", "not_checked", "manual_review", "", "JOB_INTAKE_RECOVERY_FILES_INCOMPLETE", false},
		{"file conflict", "captured", "conflict", "not_checked", "not_checked", "manual_review", "", "JOB_INTAKE_RECOVERY_STATE_CONFLICT", false},
		{"schema required", "captured", "complete", "database_missing", "missing", "schema_required", "", "SCHEMA_PREPARATION_REQUIRED", false},
		{"ledger", "captured", "complete", "job_missing", "missing", "advance", "ledger", "JOB_INTAKE_RECOVERY_STEP_AVAILABLE", true},
		{"assessment", "captured", "complete", "captured", "missing", "advance", "assessment", "JOB_INTAKE_RECOVERY_STEP_AVAILABLE", true},
		{"captured terminal", "captured", "complete", "assessed", "missing", "advance", "terminal", "JOB_INTAKE_RECOVERY_STEP_AVAILABLE", true},
		{"applied transition", "applied", "complete", "assessed", "missing", "advance", "applied", "JOB_INTAKE_RECOVERY_STEP_AVAILABLE", true},
		{"applied terminal", "applied", "complete", "applied", "missing", "advance", "terminal", "JOB_INTAKE_RECOVERY_STEP_AVAILABLE", true},
		{"settle captured receipt", "captured", "complete", "assessed", "exact", "advance", "terminal", "JOB_INTAKE_RECOVERY_STEP_AVAILABLE", true},
		{"settle applied receipt", "applied", "complete", "applied", "exact", "advance", "terminal", "JOB_INTAKE_RECOVERY_STEP_AVAILABLE", true},
		{"receipt before ledger", "captured", "complete", "job_missing", "exact", "manual_review", "", "JOB_INTAKE_RECOVERY_STATE_CONFLICT", false},
		{"captured ledger after applied intent", "applied", "complete", "captured", "exact", "manual_review", "", "JOB_INTAKE_RECOVERY_STATE_CONFLICT", false},
		{"applied ledger after captured intent", "captured", "complete", "applied", "missing", "manual_review", "", "JOB_INTAKE_RECOVERY_STATE_CONFLICT", false},
		{"frozen content conflict", "captured", "complete", "conflict", "not_checked", "manual_review", "", "JOB_INTAKE_RECOVERY_STATE_CONFLICT", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			observed := recoveryPlanObservation(tt.initial, tt.files, tt.ledger, tt.receipt)
			plan, problem := planIncompleteNewJobRecovery(observed)
			if problem != nil {
				t.Fatalf("plan failed: %s/%s", problem.Stage, problem.Code)
			}
			if plan.Version != 1 || !strings.HasPrefix(plan.PlanID, "sha256:") || len(plan.PlanID) != 71 {
				t.Fatalf("invalid plan identity: %#v", plan)
			}
			if plan.Disposition != tt.disposition || plan.NextStep != tt.step ||
				plan.ReasonCode != tt.reason || plan.CanAdvance != tt.advance {
				t.Fatalf("wrong classification: %#v", plan)
			}
			if plan.OperationID != observed.OperationID || plan.JobID != observed.JobID ||
				plan.InitialStatus != tt.initial || plan.FileState != tt.files ||
				plan.LedgerState != tt.ledger || plan.ReceiptState != tt.receipt {
				t.Fatalf("observation facts changed: %#v", plan)
			}
		})
	}
}

func TestPlanIncompleteNewJobRecoveryBindsPrivateRevision(t *testing.T) {
	t.Parallel()
	observed := recoveryPlanObservation("captured", "complete", "captured", "missing")
	first, problem := planIncompleteNewJobRecovery(observed)
	if problem != nil {
		t.Fatalf("first plan failed: %s/%s", problem.Stage, problem.Code)
	}
	second, problem := planIncompleteNewJobRecovery(observed)
	if problem != nil || second.PlanID != first.PlanID {
		t.Fatalf("same observation was not stable: %#v / %#v / %#v", first, second, problem)
	}

	changedIntent := *observed
	changedIntent.intentSHA256[0] ^= 0xff
	third, problem := planIncompleteNewJobRecovery(&changedIntent)
	if problem != nil || third.PlanID == first.PlanID {
		t.Fatalf("private intent change did not advance revision: %#v / %#v", third, problem)
	}
	changedState := *observed
	changedState.LedgerState = "assessed"
	fourth, problem := planIncompleteNewJobRecovery(&changedState)
	if problem != nil || fourth.PlanID == first.PlanID {
		t.Fatalf("durable state change did not advance revision: %#v / %#v", fourth, problem)
	}
	changedSource := *observed
	changedSource.SourceKeySHA256[0] ^= 0xff
	fifth, problem := planIncompleteNewJobRecovery(&changedSource)
	if problem != nil || fifth.PlanID == first.PlanID {
		t.Fatalf("source-key change did not advance revision: %#v / %#v", fifth, problem)
	}
}

func TestPlanIncompleteNewJobRecoveryRejectsImpossibleObservations(t *testing.T) {
	t.Parallel()
	tests := map[string]*NewJobRecoveryObservation{
		"nil":                 nil,
		"unknown files":       recoveryPlanObservation("captured", "other", "not_checked", "not_checked"),
		"incomplete with SQL": recoveryPlanObservation("captured", "raw_only", "captured", "missing"),
		"complete unchecked":  recoveryPlanObservation("captured", "complete", "captured", "not_checked"),
		"unknown ledger":      recoveryPlanObservation("captured", "complete", "other", "missing"),
		"unknown receipt":     recoveryPlanObservation("captured", "complete", "captured", "other"),
		"unknown status":      recoveryPlanObservation("other", "complete", "captured", "missing"),
	}
	zeroIntent := recoveryPlanObservation("captured", "complete", "captured", "missing")
	zeroIntent.intentSHA256 = [32]byte{}
	tests["missing intent witness"] = zeroIntent
	zeroSource := recoveryPlanObservation("captured", "complete", "captured", "missing")
	zeroSource.SourceKeySHA256 = [32]byte{}
	tests["missing source witness"] = zeroSource

	for name, observed := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			plan, problem := planIncompleteNewJobRecovery(observed)
			if plan != nil || problem == nil || problem.Stage != "observation" || problem.Code != "JOB_INTAKE_RECOVERY_STATE_INVALID" {
				t.Fatalf("unexpected result: %#v / %#v", plan, problem)
			}
		})
	}
}

func TestPlanIncompleteNewJobRecoveryPublicShapeIsContentMinimized(t *testing.T) {
	t.Parallel()
	plan, problem := planIncompleteNewJobRecovery(recoveryPlanObservation("applied", "complete", "assessed", "missing"))
	if problem != nil {
		t.Fatalf("plan failed: %s/%s", problem.Stage, problem.Code)
	}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"intent_sha256", "source_key_sha256", "source_url", "raw_path", "card_path", "title", "company"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("public plan leaked %q: %s", forbidden, payload)
		}
	}
	if got := fmt.Sprintf("%+v", plan); got != "[redacted new job recovery plan]" {
		t.Fatalf("format leaked plan facts: %q", got)
	}
}

func TestPlanIncompleteNewJobRecoveryIDIsCanonical(t *testing.T) {
	t.Parallel()
	plan, problem := planIncompleteNewJobRecovery(recoveryPlanObservation("captured", "complete", "captured", "missing"))
	if problem != nil || !validNewJobRecoveryPlanID(plan.PlanID) {
		t.Fatalf("generated plan ID is invalid: %#v / %#v", plan, problem)
	}
	for name, value := range map[string]string{
		"empty":       "",
		"bare digest": strings.Repeat("a", 64),
		"short":       "sha256:" + strings.Repeat("a", 63),
		"upper":       "sha256:" + strings.Repeat("A", 64),
		"non hex":     "sha256:" + strings.Repeat("z", 64),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if validNewJobRecoveryPlanID(value) {
				t.Fatalf("accepted noncanonical plan ID %q", value)
			}
		})
	}
}

func recoveryPlanObservation(initial, files, ledger, receipt string) *NewJobRecoveryObservation {
	return &NewJobRecoveryObservation{
		OperationID:     "123e4567e89b42d3a456426614174000",
		JobID:           "fictional_recovery_job",
		InitialStatus:   initial,
		FileState:       files,
		LedgerState:     ledger,
		ReceiptState:    receipt,
		SourceKeySHA256: [32]byte{1, 2, 3},
		intentSHA256:    [32]byte{4, 5, 6},
	}
}
