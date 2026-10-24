package main

import (
	"context"
	"encoding/json"

	"careerhub.local/backend/internal/email/emailconnection"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/storage/secretstore"
)

// readEmailConnection is an explicit read-only preview seam. It does not
// enable any email controls, open a mailbox, or write a credential.
func readEmailConnection(ctx context.Context, workspace *readonly.Workspace, dataRoot string, backendFactory func() (secretstore.Backend, error)) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	if ctx == nil || ctx.Err() != nil {
		return nil, emailReadFailure("EMAIL_CONNECTION_CANCELED")
	}
	home, err := workspace.StartupHome()
	if err != nil {
		return nil, emailReadFailure("DATA_HOME_UNAVAILABLE")
	}
	if backendFactory == nil {
		return nil, emailReadFailure("EMAIL_SECRET_STORE_FAILED")
	}
	backend, err := backendFactory()
	if err != nil || secretstore.IsNilBackend(backend) {
		return nil, emailReadFailure("EMAIL_SECRET_STORE_FAILED")
	}
	snapshot, problem := emailconnection.ReadOwnedSnapshot(ctx, dataRoot, home, backend)
	if problem != nil {
		return nil, emailReadFailure(problem.Code)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, emailReadFailure("EMAIL_CONNECTION_RESPONSE_INVALID")
	}
	if len(encoded) > httpapi.MaxResponseBytes {
		return nil, emailReadFailure("EMAIL_CONNECTION_RESPONSE_LIMIT")
	}
	return &httpapi.ApplicationOutcome{Result: encoded}, nil
}

func emailReadFailure(code string) *httpapi.ApplicationError {
	switch code {
	case "EMAIL_CONNECTION_CANCELED", "EMAIL_CONNECTION_INVALID", "EMAIL_CONNECTION_FAILED",
		"EMAIL_CONNECTION_DATABASE_UNAVAILABLE", "EMAIL_CONNECTION_SCHEMA_PREPARATION_REQUIRED",
		"EMAIL_CONNECTION_SCHEMA_TOO_NEW", "EMAIL_CONNECTION_PATH_CHANGED",
		"EMAIL_CONNECTION_CLEANUP_FAILED", "EMAIL_CONNECTION_RESPONSE_INVALID",
		"EMAIL_CONNECTION_RESPONSE_LIMIT", "EMAIL_SECRET_STORE_FAILED",
		"DATA_HOME_OWNERSHIP_REQUIRED", "DATA_HOME_UNAVAILABLE", "DATA_HOME_CHANGED",
		"DATA_HOME_CHANNEL_MISMATCH", "DATA_HOME_RECOVERY_REQUIRED",
		"DATA_HOME_NEWER_THAN_APP", "DATA_HOME_MIGRATION_REQUIRED",
		"DATA_HOME_INPUT_UNSUPPORTED", "INVALID_DATA_HOME":
	default:
		code = "EMAIL_CONNECTION_FAILED"
	}
	return &httpapi.ApplicationError{Code: code, Category: "email_connection_read"}
}
