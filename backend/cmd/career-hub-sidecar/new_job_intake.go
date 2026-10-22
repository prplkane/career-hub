package main

import (
	"context"
	"fmt"
	"io"

	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

// freshJobProgress never conflates files, Captured, assessment, optional
// Applied and terminal journal settlement.
type freshJobProgress struct {
	RunID            string
	RecoveryRequired bool
	Files            *applicationwrite.NewJobFilePhaseOutcome
	Ledger           *applicationwrite.Outcome
	Assessment       *applicationwrite.Outcome
	Applied          *applicationwrite.Outcome
	Duplicate        *applicationwrite.Outcome
	Finalization     *applicationwrite.NewJobTerminalOutcome
}

// intakeFreshNewJob is the host-only owner behind the diagnostic coordinator.
// It validates the requested initial status before any files are written, then
// composes the distinct phases in order. Applied runs only for an explicit
// sealed Applied intent; opening a job or assessment never changes status.
func intakeFreshNewJob(ctx context.Context, workspace *readonly.Workspace, start protocol.StartRecord,
	request readonly.NewJobRequest, company, title string, month assessmentcontext.Month) (out *freshJobProgress, problem *applicationwrite.Error) {
	if request.InitialStatus != "" && request.InitialStatus != "captured" && request.InitialStatus != "applied" {
		return &freshJobProgress{}, &applicationwrite.Error{Stage: "input", Code: "JOB_INTAKE_INITIAL_STATUS_INVALID"}
	}
	out, problem = captureAndAssessFreshNewJob(ctx, workspace, start, request, company, title, month)
	if problem != nil {
		return out, problem
	}
	if out != nil && out.Duplicate != nil {
		return out, nil
	}
	if out == nil || out.Assessment == nil || out.Assessment.CleanupFailed || !out.Assessment.Committed {
		return out, &applicationwrite.Error{Stage: "assessment", Code: "JOB_INTAKE_ASSESSMENT_UNCONFIRMED"}
	}
	terminalPhase := out.Assessment
	home, homeErr := workspace.StartupHome()
	if homeErr != nil {
		return out, &applicationwrite.Error{Stage: "admission", Code: "JOB_INTAKE_HOME_UNAVAILABLE"}
	}
	owner := applicationwrite.New(start.DataRoot).PinnedToHome(home).PinnedToResources(start.ResourceRoot)
	if request.InitialStatus == "applied" {
		out.Applied, problem = owner.ApplyNewJobAfterAssessment(ctx, out.Assessment)
		if problem != nil {
			return out, problem
		}
		if out.Applied == nil || !out.Applied.Committed || out.Applied.CleanupFailed {
			return out, &applicationwrite.Error{Stage: "applied", Code: "JOB_INTAKE_APPLIED_UNCONFIRMED"}
		}
		terminalPhase = out.Applied
	}
	out.Finalization, problem = owner.FinalizeNewJobAfterSuccess(ctx, terminalPhase)
	return out, problem
}

// captureAndAssessFreshNewJob is a private host continuation. It
// preserves the earlier phase facts when assessment preparation, SQLite or
// cleanup fails. Each owner call has a separate gate and a one-use in-memory
// capability; no phase may be inferred from the preceding phase's success.
func captureAndAssessFreshNewJob(ctx context.Context, workspace *readonly.Workspace, start protocol.StartRecord,
	request readonly.NewJobRequest, company, title string, month assessmentcontext.Month) (out *freshJobProgress, problem *applicationwrite.Error) {
	out, problem = registerFreshNewJob(ctx, workspace, start, request, company, title)
	if problem != nil {
		return out, problem
	}
	if out != nil && out.Files != nil && out.Files.Duplicate != nil {
		home, homeErr := workspace.StartupHome()
		if homeErr != nil {
			return out, &applicationwrite.Error{Stage: "admission", Code: "JOB_INTAKE_HOME_UNAVAILABLE"}
		}
		owner := applicationwrite.New(start.DataRoot).PinnedToHome(home).PinnedToResources(start.ResourceRoot)
		out.Duplicate, problem = owner.ProcessExistingJobDuplicate(ctx, start.ResourceRoot, month, out.Files)
		return out, problem
	}
	if out == nil || out.Ledger == nil || !out.Ledger.Committed {
		return out, &applicationwrite.Error{Stage: "ledger", Code: "JOB_INTAKE_LEDGER_UNCONFIRMED"}
	}
	home, homeErr := workspace.StartupHome()
	if homeErr != nil {
		return out, &applicationwrite.Error{Stage: "admission", Code: "JOB_INTAKE_HOME_UNAVAILABLE"}
	}
	owner := applicationwrite.New(start.DataRoot).PinnedToHome(home).PinnedToResources(start.ResourceRoot)
	out.Assessment, problem = owner.AssessNewJobAfterLedger(ctx, start.ResourceRoot, month, out.Ledger)
	return out, problem
}

func (freshJobProgress) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted fresh job progress]")
}

// registerFreshNewJob is a private host continuation. It runs the real owned
// inspection/file phase first, then passes only its in-memory single-use
// receipt to the pending-intent SQLite owner. The two gates are separate;
// the sealed intent blocks ordinary operations between them and preserves a
// crash/recovery boundary. Duplicate, assessment, Applied, terminal journal,
// later phases remain separate and preserve their own operation facts.
func registerFreshNewJob(ctx context.Context, workspace *readonly.Workspace, start protocol.StartRecord,
	request readonly.NewJobRequest, company, title string) (out *freshJobProgress, problem *applicationwrite.Error) {
	out = &freshJobProgress{}
	files, fileErr := beginNewJobFiles(ctx, workspace, start, request, company, title)
	out.Files = files
	if fileErr != nil {
		return out, fileErr
	}
	if files == nil {
		return out, &applicationwrite.Error{Stage: "files", Code: "JOB_INTAKE_FILES_UNAVAILABLE"}
	}
	if files.Duplicate != nil {
		return out, nil
	}
	if !files.Complete || !files.RecoveryRequired {
		return out, &applicationwrite.Error{Stage: "files", Code: "JOB_INTAKE_FILES_UNCONFIRMED"}
	}
	home, homeErr := workspace.StartupHome()
	if homeErr != nil {
		return out, &applicationwrite.Error{Stage: "admission", Code: "JOB_INTAKE_HOME_UNAVAILABLE"}
	}
	owner := applicationwrite.New(start.DataRoot).PinnedToHome(home).PinnedToResources(start.ResourceRoot)
	out.Ledger, problem = owner.RegisterNewJobAfterFiles(ctx, files)
	return out, problem
}
