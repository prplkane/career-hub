package main

import (
	"context"
	"sync"

	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/jobs/jobdiagnostics"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/storage/datahome"
)

type intakeDiagnosticPorts struct {
	home    func() (datahome.Info, error)
	run     func(context.Context, readonly.NewJobRequest, string, string, assessmentcontext.Month) (*freshJobProgress, *applicationwrite.Error)
	last    func(context.Context, string, datahome.Info) (*jobdiagnostics.Decoded, *jobdiagnostics.StoreError)
	append  func(context.Context, string, datahome.Info, jobdiagnostics.Event) (jobdiagnostics.AppendOutcome, *jobdiagnostics.StoreError)
	observe func(context.Context, datahome.Info) (*applicationwrite.NewJobRecoveryObservation, *applicationwrite.Error)
}

// intakeDiagnosticCoordinator serializes one diagnostic lifecycle. The
// journal's own sequence validation remains the cross-coordinator boundary.
// No owner effect begins until the started event is durably appended.
type intakeDiagnosticCoordinator struct {
	mu       sync.Mutex
	dataRoot string
	ports    intakeDiagnosticPorts
}

func newIntakeDiagnosticCoordinator(w *readonly.Workspace, start protocol.StartRecord) *intakeDiagnosticCoordinator {
	owner := applicationwrite.New(start.DataRoot)
	return &intakeDiagnosticCoordinator{dataRoot: start.DataRoot, ports: intakeDiagnosticPorts{
		home: w.StartupHome,
		run: func(ctx context.Context, request readonly.NewJobRequest, company, title string, month assessmentcontext.Month) (*freshJobProgress, *applicationwrite.Error) {
			return intakeFreshNewJob(ctx, w, start, request, company, title, month)
		},
		last: jobdiagnostics.ReadLastEvent, append: jobdiagnostics.Append,
		observe: func(ctx context.Context, home datahome.Info) (*applicationwrite.NewJobRecoveryObservation, *applicationwrite.Error) {
			return owner.PinnedToHome(home).ObserveNewJobRecovery(ctx)
		},
	}}
}

func intakeDiagnosticError(stage, code string) *applicationwrite.Error {
	return &applicationwrite.Error{Stage: stage, Code: code}
}

func (c *intakeDiagnosticCoordinator) appendExact(ctx context.Context, home datahome.Info, event jobdiagnostics.Event) bool {
	result, problem := c.ports.append(ctx, c.dataRoot, home, event)
	return problem == nil && result.Committed
}

func intakeTailAdmitsNewRun(last *jobdiagnostics.Decoded) bool {
	if last == nil {
		return true
	}
	switch last.Operation {
	case "reanalyze":
		return (last.Event == "completed" || last.Event == "failed") &&
			last.PublicationState != jobdiagnostics.PublicationUnknown
	case "intake":
		return last.Event == "completed" || last.Event == "failed" && !last.RecoveryRequired
	default:
		return false
	}
}

func (c *intakeDiagnosticCoordinator) reconcileTail(ctx context.Context, home datahome.Info, last *jobdiagnostics.Decoded) bool {
	if last != nil && last.Operation == "intake" && last.Event == "started" && last.Sequence == 1 {
		terminal, err := jobdiagnostics.IntakeInterrupted(*last, "none", false)
		return err == nil && c.appendExact(ctx, home, terminal)
	}
	if last != nil && last.Operation == "intake" && last.Event == "dispatched" && last.Sequence == 2 {
		phase := "none"
		if observed, _ := c.ports.observe(ctx, home); observed != nil && observed.SourceKeySHA256 == last.SourceKeySHA256 {
			phase = intakeObservedDurablePhase(observed)
		}
		terminal, err := jobdiagnostics.IntakeInterrupted(*last, phase, true)
		if err == nil {
			_ = c.appendExact(ctx, home, terminal)
		}
		return false
	}
	return intakeTailAdmitsNewRun(last)
}

func intakeObservedDurablePhase(observed *applicationwrite.NewJobRecoveryObservation) string {
	if observed == nil {
		return "none"
	}
	if observed.FileState != "complete" {
		if observed.FileState == "raw_only" || observed.FileState == "conflict" {
			return "files"
		}
		return "none"
	}
	if observed.ReceiptState == "exact" {
		return "terminal"
	}
	switch observed.LedgerState {
	case "captured":
		return "captured"
	case "assessed":
		return "assessed"
	case "applied":
		return "applied"
	}
	return "files"
}

func intakeProgressState(progress *freshJobProgress) (phase string, recovery bool) {
	phase = "none"
	if progress == nil {
		return phase, false
	}
	if value := progress.Files; value != nil {
		if value.NamespaceMayHaveChanged {
			phase = "namespace"
		}
		recovery = recovery || value.RecoveryRequired || value.Unconfirmed || value.CleanupFailed
		if value.Duplicate != nil {
			phase = "duplicate"
		}
		if value.Complete {
			phase = "files"
		}
	}
	if value := progress.Ledger; value != nil {
		recovery = recovery || value.RecoveryRequired || value.CleanupFailed || value.CommitAttempted && !value.Committed
		if value.Committed {
			phase = "captured"
		}
	}
	if value := progress.Assessment; value != nil {
		recovery = recovery || value.RecoveryRequired || value.CleanupFailed || value.CommitAttempted && !value.Committed
		if value.Committed {
			phase = "assessed"
		}
	}
	if value := progress.Applied; value != nil {
		recovery = recovery || value.RecoveryRequired || value.CleanupFailed || value.CommitAttempted && !value.Committed
		if value.Committed {
			phase = "applied"
		}
	}
	if value := progress.Duplicate; value != nil {
		recovery = recovery || value.RecoveryRequired || value.CleanupFailed || value.CommitAttempted && !value.Committed
		phase = "duplicate"
	}
	if value := progress.Finalization; value != nil {
		recovery = recovery || value.RecoveryRequired || value.Unconfirmed || value.CleanupFailed
		if value.ReceiptObserved {
			phase = "terminal"
		}
	}
	return phase, recovery
}

func intakeFailureCategory(progress *freshJobProgress) string {
	if progress == nil {
		return "JOB_INTAKE_INSPECTION_FAILED"
	}
	switch {
	case progress.Finalization != nil:
		return "JOB_INTAKE_TERMINAL_FAILED"
	case progress.Applied != nil:
		return "JOB_INTAKE_APPLIED_FAILED"
	case progress.Assessment != nil:
		return "JOB_INTAKE_ASSESSMENT_FAILED"
	case progress.Duplicate != nil || progress.Ledger != nil:
		return "JOB_INTAKE_LEDGER_FAILED"
	case progress.Files != nil:
		return "JOB_INTAKE_FILES_FAILED"
	default:
		return "JOB_INTAKE_INSPECTION_FAILED"
	}
}

func intakeCompletedPhase(progress *freshJobProgress) (string, bool) {
	if progress == nil {
		return "", false
	}
	if duplicate := progress.Duplicate; duplicate != nil && !duplicate.RecoveryRequired && !duplicate.CleanupFailed &&
		(!duplicate.CommitAttempted || duplicate.Committed) {
		return "duplicate", true
	}
	if terminal := progress.Finalization; terminal != nil && terminal.RemovalObserved && !terminal.RecoveryRequired &&
		!terminal.Unconfirmed && !terminal.CleanupFailed {
		return "terminal", true
	}
	return "", false
}

func (c *intakeDiagnosticCoordinator) Intake(ctx context.Context, request readonly.NewJobRequest,
	company, title string, month assessmentcontext.Month) (out *freshJobProgress, problem *applicationwrite.Error) {
	out = &freshJobProgress{}
	if c == nil || ctx == nil || c.dataRoot == "" || c.ports.home == nil || c.ports.run == nil ||
		c.ports.last == nil || c.ports.append == nil || c.ports.observe == nil {
		return out, intakeDiagnosticError("configuration", "JOB_INTAKE_REQUEST_INVALID")
	}
	if request.InitialStatus == "" {
		request.InitialStatus = "captured"
	}
	if request.InitialStatus != "captured" && request.InitialStatus != "applied" {
		return out, intakeDiagnosticError("input", "JOB_INTAKE_INITIAL_STATUS_INVALID")
	}
	source, sourceErr := jobcard.IntakeDigestSource(request.RawText)
	if sourceErr != nil {
		return out, intakeDiagnosticError("input", sourceErr.Code)
	}
	fingerprint, fingerprintErr := applicationanalysis.SourceFingerprint(source)
	if fingerprintErr != nil {
		return out, intakeDiagnosticError("input", "JOB_INTAKE_SOURCE_INVALID")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		return out, intakeDiagnosticError("context", "APPLICATION_LEDGER_CANCELED")
	}
	home, homeErr := c.ports.home()
	if homeErr != nil {
		return out, intakeDiagnosticError("admission", "JOB_INTAKE_HOME_UNAVAILABLE")
	}
	last, lastErr := c.ports.last(ctx, c.dataRoot, home)
	if lastErr != nil {
		out.RecoveryRequired = true
		return out, intakeDiagnosticError("diagnostics", "JOB_INTAKE_DIAGNOSTIC_FAILED")
	}
	if !c.reconcileTail(ctx, home, last) {
		out.RecoveryRequired = true
		return out, intakeDiagnosticError("recovery", "JOB_INTAKE_RECOVERY_REQUIRED")
	}
	run, runErr := jobdiagnostics.NewIntake(fingerprint, request.InitialStatus)
	if runErr != nil {
		return out, intakeDiagnosticError("diagnostics", "JOB_INTAKE_DIAGNOSTIC_FAILED")
	}
	out.RunID = run.ID()
	started, eventErr := run.Started()
	if eventErr != nil || !c.appendExact(ctx, home, started) {
		out.RecoveryRequired = true
		return out, intakeDiagnosticError("diagnostics", "JOB_INTAKE_DIAGNOSTIC_FAILED")
	}
	dispatched, eventErr := run.Dispatched()
	if eventErr != nil || !c.appendExact(ctx, home, dispatched) {
		out.RecoveryRequired = true
		return out, intakeDiagnosticError("diagnostics", "JOB_INTAKE_DIAGNOSTIC_FAILED")
	}

	out, problem = c.ports.run(ctx, request, company, title, month)
	if out == nil {
		out = &freshJobProgress{}
	}
	out.RunID = run.ID()
	phase, recovery := intakeProgressState(out)
	var terminal jobdiagnostics.Event
	if problem == nil {
		completedPhase, valid := intakeCompletedPhase(out)
		if !valid {
			problem = intakeDiagnosticError("result", "JOB_INTAKE_RESULT_INVALID")
		} else {
			recovery = false
			terminal, eventErr = run.Completed(completedPhase, completedPhase == "duplicate")
		}
	}
	if problem != nil {
		terminal, eventErr = run.Failed(intakeFailureCategory(out), phase, recovery)
	}
	if eventErr != nil || !c.appendExact(ctx, home, terminal) {
		out.RecoveryRequired = true
		return out, intakeDiagnosticError("diagnostics", "JOB_INTAKE_DIAGNOSTIC_FAILED")
	}
	out.RecoveryRequired = recovery
	return out, problem
}
