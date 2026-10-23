package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/jobs/jobdiagnostics"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func jobIntakeAction(w *readonly.Workspace, start protocol.StartRecord) func(context.Context, httpapi.JobIntakeRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	coordinator := newIntakeDiagnosticCoordinator(w, start)
	return func(ctx context.Context, request httpapi.JobIntakeRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		now := time.Now().UTC()
		progress, problem := coordinator.Intake(ctx, readonly.NewJobRequest{
			SourceName: request.SourceName, RawText: request.SourceText,
			SourceURL: request.SourceURL, PostingID: request.PostingID,
			IdempotencyKey: request.IdempotencyKey, InitialStatus: request.InitialStatus,
		}, request.Company, request.Title, assessmentcontext.Month{Year: now.Year(), Number: int(now.Month())})
		out := jobIntakeProgressOutcome(progress, problem != nil)
		if problem != nil {
			return out, jobIntakeError(out, problem)
		}
		payload, duplicate, err := composeJobIntakeResponse(ctx, w, progress)
		if err != nil {
			out.Partial = out.Partial || out.Committed
			return out, &httpapi.ApplicationError{Code: "JOB_INTAKE_RESULT_INVALID", Category: "job_intake"}
		}
		out.Result = payload
		out.Created = !duplicate
		return out, nil
	}
}

func jobIntakeProgressOutcome(progress *freshJobProgress, failed bool) *httpapi.ApplicationOutcome {
	out := &httpapi.ApplicationOutcome{}
	if progress == nil {
		return out
	}
	out.RunID, out.RecoveryRequired = progress.RunID, progress.RecoveryRequired
	if files := progress.Files; files != nil {
		out.Partial = failed && files.NamespaceMayHaveChanged
		fileEffect := files.IntentObserved || files.RawObserved || files.CardObserved
		out.Committed = out.Committed || fileEffect
		out.CommitUnknown = out.CommitUnknown || files.Unconfirmed
		out.CleanupFailed = out.CleanupFailed || files.CleanupFailed
	}
	for _, phase := range []*applicationwrite.Outcome{progress.Ledger, progress.Assessment, progress.Applied, progress.Duplicate} {
		if phase == nil {
			continue
		}
		out.Committed = out.Committed || phase.Committed
		out.CommitUnknown = out.CommitUnknown || phase.CommitAttempted && !phase.Committed
		out.CleanupFailed = out.CleanupFailed || phase.CleanupFailed
	}
	if terminal := progress.Finalization; terminal != nil {
		out.Committed = out.Committed || terminal.ReceiptObserved || terminal.RemovalObserved
		out.CommitUnknown = out.CommitUnknown || terminal.Unconfirmed
		out.CleanupFailed = out.CleanupFailed || terminal.CleanupFailed
	}
	out.Partial = out.Partial || failed && (out.Committed || out.CommitUnknown || out.RecoveryRequired)
	return out
}

func jobIntakeError(out *httpapi.ApplicationOutcome, problem *applicationwrite.Error) *httpapi.ApplicationError {
	if problem == nil {
		return nil
	}
	code := "JOB_INTAKE_UNAVAILABLE"
	switch problem.Code {
	case "APPLICATION_LEDGER_CANCELED", "SAVED_ASSESSMENT_CANCELED":
		code = "LOCAL_OPERATION_CANCELED"
	case "JOB_INTAKE_SOURCE_INVALID":
		code = "JOB_INTAKE_SOURCE_INVALID"
	case "JOB_INTAKE_RETRY_CONFLICT":
		code = "JOB_INTAKE_RETRY_CONFLICT"
	case "JOB_INTAKE_DUPLICATE_AMBIGUOUS":
		code = "JOB_INTAKE_DUPLICATE_AMBIGUOUS"
	case "JOB_INTAKE_RECOVERY_REQUIRED":
		code = "JOB_INTAKE_RECOVERY_REQUIRED"
	case "JOB_INTAKE_DIAGNOSTIC_FAILED":
		code = "JOB_INTAKE_DIAGNOSTIC_FAILED"
	case "JOB_INTAKE_RESULT_INVALID", "JOB_INTAKE_DUPLICATE_RESULT_INVALID":
		code = "JOB_INTAKE_RESULT_INVALID"
	case "JOB_INTAKE_REQUEST_INVALID", "JOB_INTAKE_INITIAL_STATUS_INVALID", "JOB_INTAKE_NAME_INVALID":
		code = "JOB_INTAKE_REQUEST_INVALID"
	case "JOB_INTAKE_STORAGE_UNAVAILABLE", "JOB_INTAKE_PATH_UNAVAILABLE", "JOB_INTAKE_PATH_CONFLICT":
		code = "JOB_INTAKE_STORAGE_UNAVAILABLE"
	default:
		if problem.Stage == "context" {
			code = "LOCAL_OPERATION_CANCELED"
		} else if problem.Stage == "input" {
			code = "JOB_INTAKE_REQUEST_INVALID"
		} else if out != nil && (out.RecoveryRequired || out.CommitUnknown) {
			code = "JOB_INTAKE_RECOVERY_REQUIRED"
		}
	}
	return &httpapi.ApplicationError{Code: code, Category: "job_intake"}
}

func composeJobIntakeResponse(ctx context.Context, w *readonly.Workspace, progress *freshJobProgress) (json.RawMessage, bool, error) {
	if progress == nil || progress.Files == nil || !jobdiagnostics.ValidRunID(progress.RunID) {
		return nil, false, errors.New("invalid intake progress")
	}
	jobID, duplicate := progress.Files.JobID, false
	if progress.Files.Duplicate != nil {
		jobID, duplicate = progress.Files.Duplicate.JobID, true
	}
	if !jobcard.ValidID(jobID) {
		return nil, false, errors.New("invalid intake job")
	}
	saved, err := w.SavedJob(ctx, jobID)
	if err != nil || saved == nil || saved.Card["id"] != jobID || saved.Card["valid"] != true {
		return nil, false, errors.New("saved intake job unavailable")
	}
	application, err := w.Application(ctx, jobID)
	if err != nil || application == nil {
		return nil, false, errors.New("intake application unavailable")
	}
	assessment, err := jobIntakeAssessment(progress, duplicate)
	if err != nil {
		return nil, false, err
	}
	job := make(map[string]any, len(saved.Card)+2)
	for key, value := range saved.Card {
		job[key] = value
	}
	job["assessment"], job["application"] = assessment, application
	raw, err := json.Marshal(map[string]any{"job": job, "duplicate": duplicate, "run_id": progress.RunID})
	if err != nil {
		return nil, false, err
	}
	return raw, duplicate, nil
}

func jobIntakeAssessment(progress *freshJobProgress, duplicate bool) (json.RawMessage, error) {
	var raw json.RawMessage
	if duplicate {
		if progress == nil || progress.Duplicate == nil {
			return nil, errors.New("duplicate assessment unavailable")
		}
		var duplicateResult map[string]json.RawMessage
		if json.Unmarshal(progress.Duplicate.Result, &duplicateResult) != nil {
			return nil, errors.New("duplicate assessment invalid")
		}
		raw = duplicateResult["assessment"]
	} else {
		if progress == nil || progress.Assessment == nil {
			return nil, errors.New("fresh assessment unavailable")
		}
		raw = progress.Assessment.Result
	}
	var assessed map[string]json.RawMessage
	if json.Unmarshal(raw, &assessed) != nil || len(assessed) == 0 || len(assessed["assessment"]) == 0 || !json.Valid(assessed["assessment"]) {
		return nil, errors.New("assessment result invalid")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(assessed["assessment"], &object) != nil || object == nil {
		return nil, errors.New("assessment projection invalid")
	}
	return assessed["assessment"], nil
}
