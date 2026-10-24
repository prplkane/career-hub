package main

import (
	"context"
	"encoding/json"

	"careerhub.local/backend/internal/email/emailconnection"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/storage/secretstore"
)

// A mutation and its source-shaped readback are separate owned operations.
// Keep the commit facts if readback, backend access or admission subsequently
// fails; a failed response never implies that the edit was rolled back.
func runEmailMutation(ctx context.Context, workspace *readonly.Workspace, dataRoot string, backendFactory func() (secretstore.Backend, error), mutate func() (emailconnection.ConnectionMutationResult, *emailconnection.Error)) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	if ctx == nil || ctx.Err() != nil {
		return &httpapi.ApplicationOutcome{}, emailMutationFailure("EMAIL_CONNECTION_CANCELED")
	}
	// Unsupported platforms and unavailable credential backends must not
	// commit an edit that this HTTP route cannot read back at all.
	if backendFactory == nil {
		return &httpapi.ApplicationOutcome{}, emailMutationFailure("EMAIL_SECRET_STORE_FAILED")
	}
	backend, err := backendFactory()
	if err != nil || secretstore.IsNilBackend(backend) {
		return &httpapi.ApplicationOutcome{}, emailMutationFailure("EMAIL_SECRET_STORE_FAILED")
	}
	result, problem := mutate()
	out := &httpapi.ApplicationOutcome{
		Committed:               result.Committed,
		CommitUnknown:           result.CommitAttempted && !result.Committed || problem != nil && problem.Code == "EMAIL_CONNECTION_OPERATION_UNCONFIRMED",
		RevisionRefreshRequired: result.RevisionRefreshRequired,
	}
	if problem != nil {
		return out, emailMutationFailure(problem.Code)
	}
	if !result.Committed {
		return out, emailMutationFailure("EMAIL_CONNECTION_OPERATION_UNCONFIRMED")
	}
	read, failure := readEmailConnection(ctx, workspace, dataRoot, func() (secretstore.Backend, error) { return backend, nil })
	if failure != nil {
		out.RevisionRefreshRequired = true
		return out, emailMutationFailure(failure.Code)
	}
	var revision struct {
		Revision int64 `json:"revision"`
	}
	if read == nil || json.Unmarshal(read.Result, &revision) != nil {
		out.RevisionRefreshRequired = true
		return out, emailMutationFailure("EMAIL_CONNECTION_RESPONSE_INVALID")
	}
	if revision.Revision != result.Revision {
		out.RevisionRefreshRequired = true
		return out, emailMutationFailure("EMAIL_CONNECTION_REVISION_CONFLICT")
	}
	out.Result = read.Result
	return out, nil
}

func emailMutationFailure(code string) *httpapi.ApplicationError {
	problem := emailReadFailure(code)
	switch code {
	case "EMAIL_CONNECTION_REVISION_CONFLICT", "EMAIL_CONNECTION_NOT_CONFIGURED",
		"EMAIL_CONNECTION_COMMIT_UNCONFIRMED", "EMAIL_CONNECTION_OPERATION_UNCONFIRMED":
		problem.Code = code
	}
	problem.Category = "email_connection_mutation"
	return problem
}
