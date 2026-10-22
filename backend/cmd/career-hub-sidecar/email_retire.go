package main

import (
	"context"
	"encoding/json"

	"careerhub.local/backend/internal/emailconnection"
	"careerhub.local/backend/internal/secretstore"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/readonly"
)

// retireEmailConnection is a preview-only adapter. The owner commits a v2
// disconnect/forget before deleting its Home-bound credential; a failed
// cleanup or readback keeps that commit visible in the HTTP operation facts.
func retireEmailConnection(ctx context.Context, workspace *readonly.Workspace, dataRoot string,
	backendFactory func() (secretstore.Backend, error), operation string, revision int64,
) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	out := &httpapi.ApplicationOutcome{}
	if ctx == nil || ctx.Err() != nil {
		return out, configureError("EMAIL_CONNECTION_CANCELED")
	}
	if operation != "disconnect" && operation != "forget" || revision < 1 {
		return out, configureError("EMAIL_CONNECTION_INVALID")
	}
	if workspace == nil || workspace.CheckAdmission() != nil {
		return out, &httpapi.ApplicationError{Code: "WORKSPACE_UNAVAILABLE", Category: "email_connection_mutation"}
	}
	home, err := workspace.StartupHome()
	if err != nil {
		return out, &httpapi.ApplicationError{Code: "WORKSPACE_UNAVAILABLE", Category: "email_connection_mutation"}
	}
	if backendFactory == nil {
		return out, configureError("EMAIL_SECRET_STORE_FAILED")
	}
	backend, err := backendFactory()
	if err != nil || secretstore.IsNilBackend(backend) {
		return out, configureError("EMAIL_SECRET_STORE_FAILED")
	}
	var result emailconnection.RetireResult
	var problem *emailconnection.Error
	if operation == "disconnect" {
		result, problem = emailconnection.DisconnectForHome(ctx, dataRoot, backend, home, revision)
	} else {
		result, problem = emailconnection.ForgetForHome(ctx, dataRoot, backend, home, revision)
	}
	out.Committed = result.Committed
	out.CommitUnknown = result.CommitAttempted && !result.Committed ||
		problem != nil && problem.Code == "EMAIL_CREDENTIAL_OPERATION_UNCONFIRMED" && !result.Committed
	out.RecoveryRequired = result.RecoveryRequired
	out.RevisionRefreshRequired = result.CommitAttempted || result.Committed
	if problem != nil {
		return out, configureError(problem.Code)
	}
	if !result.Committed || !result.IntentRemoved || !result.OldDeleteConfirmed {
		return out, configureError("EMAIL_CREDENTIAL_OPERATION_UNCONFIRMED")
	}
	read, readProblem := readEmailConnection(ctx, workspace, dataRoot, func() (secretstore.Backend, error) { return backend, nil })
	if readProblem != nil || read == nil || len(read.Result) == 0 {
		return out, configureError("EMAIL_CONNECTION_RESPONSE_INVALID")
	}
	var observed struct {
		ContractVersion   *int   `json:"contract_version"`
		Configured        *bool  `json:"configured"`
		Enabled           *bool  `json:"enabled"`
		ScheduleEnabled   *bool  `json:"schedule_enabled"`
		CredentialPresent *bool  `json:"credential_present"`
		Revision          *int64 `json:"revision"`
	}
	if json.Unmarshal(read.Result, &observed) != nil || observed.ContractVersion == nil || *observed.ContractVersion != 2 ||
		observed.Configured == nil || observed.Revision == nil ||
		(operation == "disconnect" && (*observed.Configured != true || observed.Enabled == nil || *observed.Enabled ||
			observed.ScheduleEnabled == nil || *observed.ScheduleEnabled || observed.CredentialPresent == nil || *observed.CredentialPresent ||
			*observed.Revision != result.Revision)) ||
		(operation == "forget" && (*observed.Configured || *observed.Revision != 0)) {
		return out, configureError("EMAIL_CONNECTION_RESPONSE_INVALID")
	}
	out.Result = read.Result
	return out, nil
}
