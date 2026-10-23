package main

import (
	"context"
	"encoding/json"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/agent/agentcontext"
	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

type agentPrepareRequest = httpapi.AgentPrepareRequest

func agentPrepareAction(w *readonly.Workspace, start protocol.StartRecord) func(context.Context, agentPrepareRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	intake := newIntakeDiagnosticCoordinator(w, start)
	return func(ctx context.Context, req agentPrepareRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		invalid := func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "AGENT_PREPARE_REQUEST_INVALID", Category: "agent_prepare"}
		}
		if req.Source == nil {
			if req.SourceName != "" || req.PostingID != "" {
				return invalid()
			}
			return agentPrepareSavedAction(w, start)(ctx, req.AgentContextRequest)
		}
		check := agentcontext.SavedPacketRequest{JobID: "input", Purpose: req.Purpose, RegistryMode: req.RegistryMode, Task: req.Task, Operation: "context", Profile: "explicit", Channel: start.Channel}
		if req.JobID != "" || agentcontext.ValidateSourceRequest(*req.Source) != nil || agentcontext.ValidateSavedPacketRequest(check) != nil || !jobcard.ValidIntakeSourceName(req.SourceName) || len(req.PostingID) > 4096 || !utf8.ValidString(req.PostingID) {
			return invalid()
		}
		if ctx == nil || ctx.Err() != nil {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "LOCAL_OPERATION_CANCELED", Category: "agent_prepare"}
		}
		if !w.MatchesStart(start) || w.CheckAdmission() != nil {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "AGENT_PREPARE_UNAVAILABLE", Category: "agent_prepare"}
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		now := time.Now().UTC()
		progress, problem := intake.Intake(ctx, readonly.NewJobRequest{SourceName: req.SourceName, RawText: req.Source.Text, SourceURL: req.Source.SourceURL, PostingID: req.PostingID, InitialStatus: "captured"}, req.Source.Company, req.Source.Title, assessmentcontext.Month{Year: now.Year(), Number: int(now.Month())})
		out := jobIntakeProgressOutcome(progress, problem != nil)
		phase, _ := intakeProgressState(progress)
		stages := &httpapi.PreparationStages{IntakePhase: phase}
		out.PreparationStages = stages
		if problem != nil {
			return out, jobIntakeError(out, problem)
		}
		job, binding, origin, valid := preparationFromIntake(progress)
		if !valid || out.RecoveryRequired || out.CommitUnknown || out.CleanupFailed {
			out.Partial = true
			return out, &httpapi.ApplicationError{Code: "AGENT_PREPARE_INTAKE_UNCONFIRMED", Category: "agent_prepare"}
		}
		stages.IntakeComplete = true
		home, err := w.StartupHome()
		if err != nil || w.CheckAdmission() != nil {
			out.Partial = true
			return out, &httpapi.ApplicationError{Code: "AGENT_PREPARE_UNAVAILABLE", Category: "agent_prepare"}
		}
		owner := applicationwrite.New(start.DataRoot).PinnedToHome(home)
		saved := req.AgentContextRequest
		saved.JobID, saved.Source = job, nil
		prepared, prepareErr := prepareSavedAgentWithOrigin(ctx, saved, start.Channel,
			func(ctx context.Context, id string) (*applicationwrite.Outcome, *applicationwrite.Error) {
				value, problem := owner.PrepareSavedJobAfterIntake(ctx, id, binding)
				stages.PreparationComplete = problem == nil && value != nil && !value.CleanupFailed && !value.RecoveryRequired && (!value.CommitAttempted || value.Committed)
				return value, problem
			}, w.AgentPreparedContext, &origin)
		stages.ContextComplete = prepareErr == nil
		out, prepareErr = mergeJobResult(out, prepared, prepareErr)
		if prepareErr != nil {
			out.Partial = out.Partial || out.Committed || out.CommitUnknown
		}
		if w.CheckAdmission() != nil {
			out.Result = nil
			out.Partial = true
			stages.ContextComplete = false
			return out, &httpapi.ApplicationError{Code: "AGENT_PREPARE_UNAVAILABLE", Category: "agent_prepare"}
		}
		return out, prepareErr
	}
}

// Use only completed owner facts. These inputs are never populated from HTTP.
// New intake proves the ledger was absent before its Captured insert. Duplicate
// intake returns the actual before-status read inside its own SQL transaction.
func preparationFromIntake(p *freshJobProgress) (string, applicationwrite.PreparationSourceBinding, agentPreparationOrigin, bool) {
	empty := applicationwrite.PreparationSourceBinding{}
	if p == nil || p.Files == nil || p.RecoveryRequired || p.Applied != nil {
		return "", empty, agentPreparationOrigin{}, false
	}
	if _, ok := intakeCompletedPhase(p); !ok {
		return "", empty, agentPreparationOrigin{}, false
	}
	f := p.Files
	if !jobcard.ValidID(f.JobID) || f.CardFileSHA256 == ([32]byte{}) || f.RawFileSHA256 == ([32]byte{}) || f.Unconfirmed || f.CleanupFailed {
		return "", empty, agentPreparationOrigin{}, false
	}
	origin := agentPreparationOrigin{created: f.Duplicate == nil}
	status := "captured"
	if f.Duplicate != nil {
		if f.Duplicate.JobID != f.JobID || f.Duplicate.InitialStatus != "captured" {
			return "", empty, origin, false
		}
		var body map[string]json.RawMessage
		if p.Duplicate == nil || json.Unmarshal(p.Duplicate.Result, &body) != nil {
			return "", empty, origin, false
		}
		var id string
		var duplicate bool
		if json.Unmarshal(body["job_id"], &id) != nil || id != f.JobID || json.Unmarshal(body["duplicate"], &duplicate) != nil || !duplicate || json.Unmarshal(body["status_before"], &origin.before) != nil {
			return "", empty, origin, false
		}
		if origin.before != nil {
			status = *origin.before
		}
	} else if !f.Complete || p.Ledger == nil || !p.Ledger.Committed || p.Ledger.CleanupFailed || p.Assessment == nil || !p.Assessment.Committed || p.Assessment.CleanupFailed || p.Finalization == nil || p.Finalization.JobID != f.JobID || p.Finalization.OperationID != f.OperationID {
		return "", empty, origin, false
	}
	return f.JobID, applicationwrite.PreparationSourceBinding{CardSHA256: f.CardFileSHA256, RawSHA256: f.RawFileSHA256, Status: status}, origin, true
}
