package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/jobs/jobdiagnostics"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/storage/datahome"
)

const diagnosticIntakeSource = "Company: Fictional Harbor\nRole: Systems Engineer\nRequirements\nBuild bounded services.\n"

type intakeDiagnosticFixture struct {
	coordinator  *intakeDiagnosticCoordinator
	last         *jobdiagnostics.Decoded
	events       []jobdiagnostics.Decoded
	rawEvents    [][]byte
	appendCalls  int
	failAppendAt int
	runCalls     int
	received     readonly.NewJobRequest
	progress     *freshJobProgress
	problem      *applicationwrite.Error
	observation  *applicationwrite.NewJobRecoveryObservation
	observeCalls int
}

func newIntakeDiagnosticFixture(t *testing.T) *intakeDiagnosticFixture {
	t.Helper()
	f := &intakeDiagnosticFixture{}
	f.progress = &freshJobProgress{
		Files:        &applicationwrite.NewJobFilePhaseOutcome{Complete: true, RecoveryRequired: true},
		Ledger:       &applicationwrite.Outcome{CommitAttempted: true, Committed: true, RecoveryRequired: true},
		Assessment:   &applicationwrite.Outcome{CommitAttempted: true, Committed: true, RecoveryRequired: true},
		Finalization: &applicationwrite.NewJobTerminalOutcome{ReceiptObserved: true, RemovalObserved: true},
	}
	f.coordinator = &intakeDiagnosticCoordinator{dataRoot: "fictional-home", ports: intakeDiagnosticPorts{
		home: func() (datahome.Info, error) { return datahome.Info{}, nil },
		last: func(context.Context, string, datahome.Info) (*jobdiagnostics.Decoded, *jobdiagnostics.StoreError) {
			return f.last, nil
		},
		append: func(_ context.Context, _ string, _ datahome.Info, event jobdiagnostics.Event) (jobdiagnostics.AppendOutcome, *jobdiagnostics.StoreError) {
			f.appendCalls++
			if f.failAppendAt == f.appendCalls {
				return jobdiagnostics.AppendOutcome{Uncertain: true}, &jobdiagnostics.StoreError{Code: "SYNTHETIC_APPEND"}
			}
			decoded, err := jobdiagnostics.Decode(event.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			f.events = append(f.events, decoded)
			f.rawEvents = append(f.rawEvents, event.Bytes())
			return jobdiagnostics.AppendOutcome{Committed: true}, nil
		},
		run: func(_ context.Context, request readonly.NewJobRequest, _, _ string, _ assessmentcontext.Month) (*freshJobProgress, *applicationwrite.Error) {
			f.runCalls++
			f.received = request
			return f.progress, f.problem
		},
		observe: func(context.Context, datahome.Info) (*applicationwrite.NewJobRecoveryObservation, *applicationwrite.Error) {
			f.observeCalls++
			return f.observation, nil
		},
	}}
	return f
}

func diagnosticIntakeRequest() readonly.NewJobRequest {
	return readonly.NewJobRequest{SourceName: "fictional.txt", RawText: diagnosticIntakeSource, IdempotencyKey: "fictional-key-123"}
}

func decodedIntakeTerminal(t *testing.T, recovery bool) *jobdiagnostics.Decoded {
	t.Helper()
	started, run := decodedIntakeStarted(t)
	var event jobdiagnostics.Event
	var err error
	if recovery {
		dispatched, dispatchErr := run.Dispatched()
		if dispatchErr != nil {
			t.Fatal(dispatchErr)
		}
		event, err = jobdiagnostics.IntakeInterrupted(decodedDiagnosticEvent(t, dispatched), "files", true)
	} else {
		event, err = jobdiagnostics.IntakeInterrupted(*started, "none", false)
	}
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodedDiagnosticEvent(t, event)
	return &decoded
}

func decodedIntakeStarted(t *testing.T) (*jobdiagnostics.Decoded, *jobdiagnostics.IntakeRun) {
	t.Helper()
	fingerprint := sha256.Sum256([]byte(diagnosticIntakeSource))
	run, err := jobdiagnostics.NewIntake(hex.EncodeToString(fingerprint[:]), "captured")
	if err != nil {
		t.Fatal(err)
	}
	started, err := run.Started()
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodedDiagnosticEvent(t, started)
	return &decoded, run
}

func decodedDiagnosticEvent(t *testing.T, event jobdiagnostics.Event) jobdiagnostics.Decoded {
	t.Helper()
	decoded, err := jobdiagnostics.Decode(event.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestIntakeDiagnosticCoordinatorBracketsOwner(t *testing.T) {
	f := newIntakeDiagnosticFixture(t)
	out, problem := f.coordinator.Intake(context.Background(), diagnosticIntakeRequest(), "Fictional Harbor", "Systems Engineer", assessmentcontext.Month{Year: 2026, Number: 9})
	if problem != nil || out.RunID == "" || out.RecoveryRequired || f.runCalls != 1 || f.received.InitialStatus != "captured" {
		t.Fatalf("successful intake diagnostic lifecycle invalid: %+v %+v", out, problem)
	}
	if len(f.events) != 3 || f.events[0].Operation != "intake" || f.events[0].Event != "started" ||
		f.events[0].InitialStatus != "captured" || f.events[1].Event != "dispatched" ||
		f.events[2].Event != "completed" || f.events[2].DurablePhase != "terminal" ||
		f.events[0].RunID != f.events[1].RunID || f.events[1].RunID != f.events[2].RunID {
		t.Fatal("owner was not bracketed by one exact run", f.events)
	}
	for _, raw := range f.rawEvents {
		if bytes.Contains(raw, []byte(diagnosticIntakeSource)) || bytes.Contains(raw, []byte("Fictional Harbor")) {
			t.Fatal("private request text reached diagnostics")
		}
	}

	f = newIntakeDiagnosticFixture(t)
	f.progress = &freshJobProgress{
		Files:     &applicationwrite.NewJobFilePhaseOutcome{Duplicate: &applicationwrite.NewJobFileDuplicate{JobID: "fictional_job"}},
		Duplicate: &applicationwrite.Outcome{Result: []byte(`{"duplicate":true}`)},
	}
	out, problem = f.coordinator.Intake(context.Background(), diagnosticIntakeRequest(), "", "", assessmentcontext.Month{Year: 2026, Number: 9})
	if problem != nil || out.RecoveryRequired || len(f.events) != 3 || f.events[2].DurablePhase != "duplicate" || !f.events[2].Duplicate {
		t.Fatalf("verified duplicate did not close its diagnostic run: %+v %+v %+v", out, problem, f.events)
	}
}

func TestIntakeDiagnosticCoordinatorRetainsFailureAndRecovery(t *testing.T) {
	f := newIntakeDiagnosticFixture(t)
	f.progress = &freshJobProgress{
		Files:  &applicationwrite.NewJobFilePhaseOutcome{Complete: true, RecoveryRequired: true},
		Ledger: &applicationwrite.Outcome{CommitAttempted: true, RecoveryRequired: true},
	}
	f.problem = &applicationwrite.Error{Stage: "commit", Code: "APPLICATION_LEDGER_COMMIT_UNCONFIRMED"}
	out, problem := f.coordinator.Intake(context.Background(), diagnosticIntakeRequest(), "", "", assessmentcontext.Month{Year: 2026, Number: 9})
	if problem != f.problem || !out.RecoveryRequired || len(f.events) != 3 || f.events[2].Event != "failed" ||
		f.events[2].ErrorCode != "JOB_INTAKE_LEDGER_FAILED" || f.events[2].DurablePhase != "files" || !f.events[2].RecoveryRequired {
		t.Fatalf("owner failure facts were hidden: %+v %+v %+v", out, problem, f.events)
	}

	f = newIntakeDiagnosticFixture(t)
	f.last = decodedIntakeTerminal(t, true)
	out, problem = f.coordinator.Intake(context.Background(), diagnosticIntakeRequest(), "", "", assessmentcontext.Month{Year: 2026, Number: 9})
	if problem == nil || problem.Code != "JOB_INTAKE_RECOVERY_REQUIRED" || !out.RecoveryRequired || f.runCalls != 0 || f.appendCalls != 0 {
		t.Fatalf("unresolved intake tail reached owner: %+v %+v", out, problem)
	}

	f = newIntakeDiagnosticFixture(t)
	f.last, _ = decodedIntakeStarted(t)
	out, problem = f.coordinator.Intake(context.Background(), diagnosticIntakeRequest(), "", "", assessmentcontext.Month{Year: 2026, Number: 9})
	if problem != nil || out.RecoveryRequired || f.runCalls != 1 || len(f.events) != 4 ||
		f.events[0].ErrorCode != "JOB_INTAKE_INTERRUPTED" || f.events[0].RecoveryRequired || f.events[1].Event != "started" {
		t.Fatalf("safe started-only tail did not close before new owner run: %+v %+v %+v", out, problem, f.events)
	}

	f = newIntakeDiagnosticFixture(t)
	_, dispatchedRun := decodedIntakeStarted(t)
	dispatched, err := dispatchedRun.Dispatched()
	if err != nil {
		t.Fatal(err)
	}
	dispatchedValue := decodedDiagnosticEvent(t, dispatched)
	f.last = &dispatchedValue
	f.observation = &applicationwrite.NewJobRecoveryObservation{
		FileState: "complete", LedgerState: "captured", ReceiptState: "missing",
		SourceKeySHA256: dispatchedValue.SourceKeySHA256,
	}
	out, problem = f.coordinator.Intake(context.Background(), diagnosticIntakeRequest(), "", "", assessmentcontext.Month{Year: 2026, Number: 9})
	if problem == nil || problem.Code != "JOB_INTAKE_RECOVERY_REQUIRED" || !out.RecoveryRequired || f.runCalls != 0 ||
		f.observeCalls != 1 || len(f.events) != 1 || f.events[0].ErrorCode != "JOB_INTAKE_INTERRUPTED" ||
		f.events[0].DurablePhase != "captured" || !f.events[0].RecoveryRequired {
		t.Fatalf("dispatched tail was not conservatively classified: %+v %+v %+v", out, problem, f.events)
	}
}

func TestIntakeDiagnosticCoordinatorTerminalAppendFailureStopsRetry(t *testing.T) {
	f := newIntakeDiagnosticFixture(t)
	f.failAppendAt = 3
	out, problem := f.coordinator.Intake(context.Background(), diagnosticIntakeRequest(), "", "", assessmentcontext.Month{Year: 2026, Number: 9})
	if problem == nil || problem.Code != "JOB_INTAKE_DIAGNOSTIC_FAILED" || !out.RecoveryRequired || f.runCalls != 1 || len(f.events) != 2 {
		t.Fatalf("terminal diagnostic failure hid recovery boundary: %+v %+v %+v", out, problem, f.events)
	}
	f = newIntakeDiagnosticFixture(t)
	f.failAppendAt = 2
	out, problem = f.coordinator.Intake(context.Background(), diagnosticIntakeRequest(), "", "", assessmentcontext.Month{Year: 2026, Number: 9})
	if problem == nil || problem.Code != "JOB_INTAKE_DIAGNOSTIC_FAILED" || !out.RecoveryRequired || f.runCalls != 0 || len(f.events) != 1 {
		t.Fatalf("owner ran without confirmed dispatch boundary: %+v %+v %+v", out, problem, f.events)
	}
}
