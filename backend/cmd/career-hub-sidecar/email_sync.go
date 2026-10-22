package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emaildiagnostics"
	"careerhub.local/backend/internal/emailworker"
	"careerhub.local/backend/internal/secretstore"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func emailSyncFailure(code string) *httpapi.ApplicationError {
	switch code {
	case "EMAIL_CONNECTION_INVALID", "EMAIL_CONNECTION_NOT_CONFIGURED", "EMAIL_CREDENTIAL_MISSING",
		"EMAIL_SECRET_STORE_FAILED", "EMAIL_SYNC_DISABLED", "EMAIL_SYNC_ALREADY_RUNNING",
		"EMAIL_SYNC_CANCELED", "EMAIL_SYNC_AUTH_FAILED", "EMAIL_SYNC_NETWORK_FAILED",
		"EMAIL_SYNC_PROTOCOL_FAILED", "EMAIL_SYNC_UIDVALIDITY_INVALID", "EMAIL_SYNC_RESPONSE_TOO_LARGE",
		"EMAIL_SYNC_LOCAL_WRITE_FAILED", "EMAIL_SYNC_COMMIT_UNKNOWN", "EMAIL_SYNC_FAILED",
		"EMAIL_CONNECTION_DATABASE_UNAVAILABLE", "EMAIL_CONNECTION_SCHEMA_PREPARATION_REQUIRED",
		"EMAIL_CONNECTION_SCHEMA_TOO_NEW", "EMAIL_CONNECTION_PATH_CHANGED",
		"EMAIL_CONNECTION_CLEANUP_FAILED", "EMAIL_CONNECTION_RESPONSE_INVALID",
		"EMAIL_CONNECTION_RESPONSE_LIMIT", "EMAIL_CONNECTION_CANCELED",
		"DATA_HOME_OWNERSHIP_REQUIRED", "DATA_HOME_UNAVAILABLE",
		"DATA_HOME_CHANGED", "DATA_HOME_CHANNEL_MISMATCH", "DATA_HOME_RECOVERY_REQUIRED",
		"DATA_HOME_NEWER_THAN_APP", "DATA_HOME_MIGRATION_REQUIRED", "DATA_HOME_INPUT_UNSUPPORTED",
		"INVALID_DATA_HOME", "EMAIL_DIAGNOSTIC_CANCELED", "EMAIL_DIAGNOSTIC_EVENT_INVALID",
		"EMAIL_DIAGNOSTIC_HOME_CHANGED", "EMAIL_DIAGNOSTIC_DIRECTORY_INVALID",
		"EMAIL_DIAGNOSTIC_RECOVERY_REQUIRED", "EMAIL_DIAGNOSTIC_OWNERSHIP_REQUIRED",
		"EMAIL_DIAGNOSTIC_STORE_UNAVAILABLE", "EMAIL_DIAGNOSTIC_COMMIT_UNKNOWN",
		"EMAIL_DIAGNOSTIC_LIMIT", "EMAIL_DIAGNOSTIC_CLOSE_FAILED",
		"EMAIL_DIAGNOSTIC_POSTCOMMIT_FAILED":
	default:
		// Never echo an arbitrary transport, SQLite or credential-provider error.
		code = "EMAIL_SYNC_FAILED"
	}
	return &httpapi.ApplicationError{Code: code, Category: "email_sync"}
}

// A failed page may already have committed individual receipts/discoveries.
// Only these pre-worker refusals are known to have made no sync mutation.
func emailSyncNoEffects(code string) bool {
	switch code {
	case "EMAIL_CREDENTIAL_MISSING", "EMAIL_SECRET_STORE_FAILED", "EMAIL_SYNC_DISABLED", "EMAIL_SYNC_ALREADY_RUNNING":
		return true
	}
	return false
}

func emailSyncTerminalFailure(ctx context.Context, dataRoot string, home datahome.Info,
	run *emaildiagnostics.Run, out *httpapi.ApplicationOutcome, code, stage string,
) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	event, err := run.Failed(code, stage)
	if err != nil {
		out.RevisionRefreshRequired = out.Committed || out.CommitUnknown
		return out, emailSyncFailure("EMAIL_DIAGNOSTIC_EVENT_INVALID")
	}
	// The mailbox owner has returned. Persist its terminal fact even if the
	// browser disconnected or its request deadline elapsed after some imports.
	logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, problem := emaildiagnostics.Append(logCtx, dataRoot, home, event); problem != nil {
		out.RevisionRefreshRequired = out.Committed || out.CommitUnknown
		return out, emailSyncFailure(problem.Code)
	}
	return out, emailSyncFailure(code)
}

// syncEmailConnection is intentionally not a scheduler or a credential writer.
// The request is admitted against the process startup Home before a backend is
// read; the worker owns its mailbox/lease and all writes through that Home.
func syncEmailConnection(ctx context.Context, workspace *readonly.Workspace, dataRoot string,
	backendFactory func() (secretstore.Backend, error), runner emailSyncRunner, trigger, requestedRunID string,
) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	if ctx == nil || ctx.Err() != nil {
		return &httpapi.ApplicationOutcome{}, emailSyncFailure("EMAIL_SYNC_CANCELED")
	}
	if runner == nil || (trigger != "manual" && trigger != "scheduled") {
		return &httpapi.ApplicationOutcome{}, emailSyncFailure("EMAIL_CONNECTION_INVALID")
	}
	if workspace == nil || workspace.CheckAdmission() != nil {
		return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "WORKSPACE_UNAVAILABLE", Category: "email_sync"}
	}
	home, err := workspace.StartupHome()
	if err != nil {
		return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "WORKSPACE_UNAVAILABLE", Category: "email_sync"}
	}
	run, err := emaildiagnostics.New(requestedRunID)
	if err != nil {
		return &httpapi.ApplicationOutcome{}, emailSyncFailure("EMAIL_DIAGNOSTIC_STORE_UNAVAILABLE")
	}
	out := &httpapi.ApplicationOutcome{RunID: run.ID()}
	started, err := run.Started()
	if err != nil {
		return out, emailSyncFailure("EMAIL_DIAGNOSTIC_EVENT_INVALID")
	}
	if _, problem := emaildiagnostics.Append(ctx, dataRoot, home, started); problem != nil {
		return out, emailSyncFailure(problem.Code)
	}
	if backendFactory == nil {
		return emailSyncTerminalFailure(ctx, dataRoot, home, run, out, "EMAIL_SECRET_STORE_FAILED", "connection")
	}
	backend, err := backendFactory()
	if err != nil || secretstore.IsNilBackend(backend) {
		return emailSyncTerminalFailure(ctx, dataRoot, home, run, out, "EMAIL_SECRET_STORE_FAILED", "connection")
	}
	result, err := runner(ctx, applicationwrite.New(dataRoot).PinnedToHome(home), backend, trigger)
	if err != nil {
		code := err.Error()
		if errors.Is(err, emailworker.ErrCommitUnknown) {
			code = "EMAIL_SYNC_COMMIT_UNKNOWN"
		}
		problem := emailSyncFailure(code)
		unknown := !emailSyncNoEffects(problem.Code)
		out.CommitUnknown, out.RevisionRefreshRequired = unknown, unknown
		stage := "mailbox"
		if problem.Code == "EMAIL_SYNC_LOCAL_WRITE_FAILED" || problem.Code == "EMAIL_SYNC_COMMIT_UNKNOWN" {
			stage = "local_persistence"
		}
		return emailSyncTerminalFailure(ctx, dataRoot, home, run, out, problem.Code, stage)
	}
	out.Committed, out.Partial = result.Status == "succeeded" || result.Status == "partial", result.Status == "partial"
	if result.ContractVersion != emailworker.ContractVersion ||
		(result.Status != "succeeded" && result.Status != "partial" && result.Status != "disabled" && result.Status != "deferred") {
		out.CommitUnknown, out.RevisionRefreshRequired = true, true
		return emailSyncTerminalFailure(ctx, dataRoot, home, run, out, "EMAIL_SYNC_FAILED", "mailbox")
	}
	// The source UI expects the refreshed connection alongside the worker result.
	// Reuse the owned read instead of fabricating a cursor or credential state.
	connection, problem := readEmailConnection(ctx, workspace, dataRoot, func() (secretstore.Backend, error) { return backend, nil })
	if problem != nil {
		out.RevisionRefreshRequired = true
		return emailSyncTerminalFailure(ctx, dataRoot, home, run, out, emailSyncFailure(problem.Code).Code, "readback")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		out.RevisionRefreshRequired = true
		return emailSyncTerminalFailure(ctx, dataRoot, home, run, out, "EMAIL_SYNC_FAILED", "local_persistence")
	}
	var response map[string]json.RawMessage
	if json.Unmarshal(encoded, &response) != nil || connection == nil || len(connection.Result) == 0 {
		out.RevisionRefreshRequired = true
		return emailSyncTerminalFailure(ctx, dataRoot, home, run, out, "EMAIL_SYNC_FAILED", "readback")
	}
	response["connection"] = connection.Result
	response["run_id"], _ = json.Marshal(run.ID())
	if result.CompletedAt == "" {
		response["completed_at"] = json.RawMessage("null")
	}
	if result.DeferredUntil == "" {
		response["deferred_until"] = json.RawMessage("null")
	}
	out.Result, err = json.Marshal(response)
	if err != nil {
		out.RevisionRefreshRequired = true
		return emailSyncTerminalFailure(ctx, dataRoot, home, run, out, "EMAIL_SYNC_FAILED", "local_persistence")
	}
	terminal, err := run.Completed(trigger, result)
	if err != nil {
		out.Result = nil
		out.RevisionRefreshRequired = true
		return out, emailSyncFailure("EMAIL_DIAGNOSTIC_EVENT_INVALID")
	}
	logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, problem := emaildiagnostics.Append(logCtx, dataRoot, home, terminal); problem != nil {
		out.Result = nil
		out.RevisionRefreshRequired = true
		return out, emailSyncFailure(problem.Code)
	}
	return out, nil
}
