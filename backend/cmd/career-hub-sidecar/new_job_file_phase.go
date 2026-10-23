package main

import (
	"context"
	"errors"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

// beginNewJobFiles is the private file phase behind the preview intake
// coordinator. The writer takes the gate before this adapter calls the real
// owned inspector; the returned verification callback stays bound to that
// exact preparation throughout journal/raw/card publication. Later SQLite,
// assessment, Applied and terminal phases remain separately observable.
func beginNewJobFiles(ctx context.Context, workspace *readonly.Workspace, start protocol.StartRecord,
	request readonly.NewJobRequest, company, title string) (*applicationwrite.NewJobFilePhaseOutcome, *applicationwrite.Error) {
	if !workspace.MatchesStart(start) {
		return &applicationwrite.NewJobFilePhaseOutcome{}, &applicationwrite.Error{Stage: "configuration", Code: "JOB_INTAKE_REQUEST_INVALID"}
	}
	home, err := workspace.StartupHome()
	if err != nil {
		return &applicationwrite.NewJobFilePhaseOutcome{}, &applicationwrite.Error{Stage: "admission", Code: "JOB_INTAKE_HOME_UNAVAILABLE"}
	}
	owner := applicationwrite.New(start.DataRoot).PinnedToHome(home).PinnedToResources(start.ResourceRoot)
	inspectionCode := ""
	out, problem := owner.BeginNewJobFilePhase(ctx, func(ctx context.Context) (*applicationwrite.NewJobFileChoice, error) {
		inspected, err := workspace.InspectNewJob(ctx, request, company, title)
		if err != nil || inspected == nil {
			var classified *readonly.Error
			if errors.As(err, &classified) && (classified.Code == "JOB_INTAKE_RETRY_CONFLICT" || classified.Code == "JOB_INTAKE_DUPLICATE_AMBIGUOUS") {
				inspectionCode = classified.Code
			}
			if errors.As(err, &classified) && (classified.Code == "JOB_INTAKE_NAMESPACE_UNAVAILABLE" || classified.Code == "JOB_INTAKE_NAMESPACE_CHANGED") {
				inspectionCode = "JOB_INTAKE_STORAGE_UNAVAILABLE"
			}
			return nil, err
		}
		if duplicate := inspected.Duplicate; duplicate != nil {
			return &applicationwrite.NewJobFileChoice{Duplicate: &applicationwrite.NewJobFileDuplicate{
				JobID: duplicate.JobID, Reason: duplicate.Reason, CardPath: duplicate.CardPath,
				RawPath: duplicate.RawPath, InitialStatus: request.InitialStatus,
				CardFileSHA256: duplicate.CardFileSHA256, RawFileSHA256: duplicate.RawFileSHA256,
			}}, nil
		}
		prepared := inspected.Prepared
		if prepared == nil || prepared.Digest == nil {
			return nil, nil
		}
		return &applicationwrite.NewJobFileChoice{Prepared: &applicationwrite.NewJobFileDraft{
			Destination: prepared.Destination, RawText: request.RawText, CardBytes: prepared.CardBytes,
			RawFileSHA256: prepared.RawFileSHA256, CardFileSHA256: prepared.PlannedCardSHA256,
			ContentFingerprint: prepared.Digest.ContentFingerprint,
			InitialStatus:      request.InitialStatus,
			VerifyReadSet:      func(ctx context.Context) error { return workspace.VerifyNewJobReadSet(ctx, prepared) },
		}}, nil
	})
	if problem != nil && problem.Code == "JOB_INTAKE_INSPECTION_FAILED" && inspectionCode != "" {
		problem = &applicationwrite.Error{Stage: "inspection", Code: inspectionCode}
	}
	return out, problem
}
