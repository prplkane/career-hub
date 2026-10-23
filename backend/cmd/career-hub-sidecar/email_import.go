package main

import (
	"context"
	"encoding/json"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/email/emailimport"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func importEmailFiles(ctx context.Context, workspace *readonly.Workspace, dataRoot string, batch *emailimport.Batch) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	out := &httpapi.ApplicationOutcome{}
	home, err := workspace.StartupHome()
	if err != nil {
		return out, &httpapi.ApplicationError{Code: "WORKSPACE_UNAVAILABLE", Category: "email_import"}
	}
	value, problem := batch.Run(ctx, applicationwrite.New(dataRoot).PinnedToHome(home))
	if value != nil {
		out.Committed, out.CommitUnknown = value.Committed, value.CommitUnknown
		out.CleanupFailed, out.RecoveryRequired = value.CleanupFailed, value.RecoveryRequired
		out.Partial = out.Committed && (problem != nil || value.Result.Summary["files_partial"] > 0 || value.Result.Summary["files_failed"] > 0)
		out.RevisionRefreshRequired = out.Committed || out.CommitUnknown
	}
	if problem != nil {
		return out, &httpapi.ApplicationError{Code: problem.Code, Category: "email_import"}
	}
	if value == nil {
		return out, &httpapi.ApplicationError{Code: "LOCAL_RESPONSE_INVALID", Category: "email_import"}
	}
	out.Result, err = json.Marshal(value.Result)
	if err != nil {
		return out, &httpapi.ApplicationError{Code: "LOCAL_RESPONSE_INVALID", Category: "email_import"}
	}
	return out, nil
}
