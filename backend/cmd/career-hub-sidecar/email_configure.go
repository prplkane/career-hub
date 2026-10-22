package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emailconnection"
	"careerhub.local/backend/internal/emaildiagnostics"
	"careerhub.local/backend/internal/secretstore"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/readonly"
)

type configureEffects struct {
	commitAttempted, committed, recoveryRequired, revisionRefreshRequired bool
	revision                                                              int64
}

// No account, sender, credential or backend error text is accepted as a public
// error code. Native owner errors are reduced to a fixed allowlist.
func configurePublicCode(code string) string {
	switch code {
	case "EMAIL_CONNECTION_INVALID", "EMAIL_CONNECTION_NOT_CONFIGURED", "EMAIL_CONNECTION_ACCOUNT_IMMUTABLE",
		"EMAIL_CONNECTION_ALREADY_DISABLED", "EMAIL_CONNECTION_CANCELED", "EMAIL_CONNECTION_REVISION_CONFLICT",
		"EMAIL_CONNECTION_DATABASE_UNAVAILABLE", "EMAIL_CONNECTION_SCHEMA_PREPARATION_REQUIRED",
		"EMAIL_CONNECTION_SCHEMA_TOO_NEW", "EMAIL_CONNECTION_PATH_CHANGED", "EMAIL_CONNECTION_RESPONSE_INVALID", "EMAIL_CONNECTION_COMMIT_UNCONFIRMED",
		"EMAIL_CONNECTION_OPERATION_UNCONFIRMED", "EMAIL_CONNECTION_CLEANUP_FAILED", "EMAIL_CONNECTION_FAILED",
		"EMAIL_CREDENTIAL_MISSING", "EMAIL_CREDENTIAL_LEGACY_OWNERSHIP_REQUIRED", "EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED",
		"EMAIL_CREDENTIAL_RECONNECT_REQUIRED", "EMAIL_CREDENTIAL_RECONNECT_NOT_REQUIRED", "EMAIL_CREDENTIAL_SCOPE_CHANGED",
		"EMAIL_CREDENTIAL_SCOPE_UNAVAILABLE", "EMAIL_CREDENTIAL_INTENT_SCOPE_CHANGED",
		"EMAIL_CREDENTIAL_DATABASE_PATH_CHANGED", "EMAIL_CREDENTIAL_DATABASE_STATE_CHANGED", "EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE",
		"EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED", "EMAIL_CREDENTIAL_HOME_CHANGED", "EMAIL_CREDENTIAL_INTENT_CHANGED",
		"EMAIL_CREDENTIAL_INTENT_HOME_CHANGED", "EMAIL_CREDENTIAL_INTENT_INVALID", "EMAIL_CREDENTIAL_INTENT_REMOVE_UNCONFIRMED",
		"EMAIL_CREDENTIAL_INTENT_UNAVAILABLE", "EMAIL_CREDENTIAL_OPERATION_CANCELED", "EMAIL_CREDENTIAL_OPERATION_UNCONFIRMED",
		"EMAIL_CREDENTIAL_PATH_UNAVAILABLE", "EMAIL_CREDENTIAL_PUBLICATION_INVALID", "EMAIL_CREDENTIAL_PUBLICATION_UNCONFIRMED",
		"EMAIL_CREDENTIAL_RECOVERY_REQUIRED", "EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED", "EMAIL_CREDENTIAL_TARGET_COLLISION",
		"EMAIL_SECRET_STORE_FAILED", "EMAIL_SECRET_TARGET_GENERATION_FAILED", "EMAIL_DIAGNOSTIC_EVENT_INVALID", "DATA_HOME_CHANGED",
		"DATA_HOME_OWNERSHIP_REQUIRED", "DATA_HOME_RECOVERY_REQUIRED", "DATA_HOME_UNAVAILABLE":
		return code
	}
	return "EMAIL_CONNECTION_FAILED"
}

func configureError(code string) *httpapi.ApplicationError {
	return &httpapi.ApplicationError{Code: configurePublicCode(code), Category: "email_connection_mutation"}
}

func executeConfigure(ctx context.Context, dataRoot string, home datahome.Info, backend secretstore.Backend,
	request httpapi.EmailConfigureRequest) (configureEffects, *emailconnection.Error) {
	interval := request.IntervalMinutes
	if request.Reconnect {
		if !request.PasswordSupplied || request.ExpectedRevision < 1 {
			return configureEffects{}, &emailconnection.Error{Code: "EMAIL_CONNECTION_INVALID"}
		}
		result, problem := emailconnection.Reconnect(ctx, dataRoot, backend, emailconnection.PromoteLegacyInput{
			ExpectedHome: &home, AccountAddress: request.AccountAddress, Password: request.Password,
			JobSenders: request.JobSenders, ApplicationSenders: request.ApplicationSenders,
			ScheduleEnabled: request.ScheduleEnabled, IntervalMinutes: &interval, ExpectedRevision: request.ExpectedRevision,
		})
		return configureEffects{result.CommitAttempted, result.Committed, result.RecoveryRequired,
			result.CommitAttempted || result.Committed, result.Revision}, problem
	}
	if request.ExpectedRevision == 0 {
		result, problem := emailconnection.ConfigureNew(ctx, dataRoot, backend, emailconnection.ConfigureNewInput{
			ExpectedHome: &home, AccountAddress: request.AccountAddress, Password: request.Password,
			JobSenders: request.JobSenders, ApplicationSenders: request.ApplicationSenders,
			ScheduleEnabled: request.ScheduleEnabled, IntervalMinutes: &interval, ExpectedRevision: 0,
		})
		return configureEffects{result.CommitAttempted, result.Committed, result.RecoveryRequired,
			result.CommitAttempted || result.Committed, result.Revision}, problem
	}
	if request.PasswordSupplied {
		result, problem := emailconnection.Rotate(ctx, dataRoot, backend, emailconnection.RotateInput{
			ExpectedHome: &home, AccountAddress: request.AccountAddress, Password: request.Password,
			JobSenders: request.JobSenders, ApplicationSenders: request.ApplicationSenders,
			ScheduleEnabled: request.ScheduleEnabled, IntervalMinutes: &interval,
			ExpectedRevision: request.ExpectedRevision,
		})
		return configureEffects{result.CommitAttempted, result.Committed, result.RecoveryRequired,
			result.CommitAttempted || result.Committed, result.Revision}, problem
	}
	result, problem := emailconnection.EditSettings(ctx, dataRoot, backend, emailconnection.EditSettingsInput{
		ExpectedHome: &home, AccountAddress: request.AccountAddress,
		JobSenders: request.JobSenders, ApplicationSenders: request.ApplicationSenders,
		ScheduleEnabled: request.ScheduleEnabled, IntervalMinutes: &interval,
		ExpectedRevision: request.ExpectedRevision,
	})
	return configureEffects{result.CommitAttempted, result.Committed, false,
		result.RevisionRefreshRequired || result.CommitAttempted || result.Committed, result.Revision}, problem
}

func configureTerminalFailure(ctx context.Context, dataRoot string, home datahome.Info, run *emaildiagnostics.Run,
	out *httpapi.ApplicationOutcome, code, stage string, effects configureEffects,
) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	public := configurePublicCode(code)
	event, err := run.ConnectionFailed(public, stage, effects.commitAttempted, effects.committed, effects.recoveryRequired)
	if err != nil {
		return out, configureError("EMAIL_DIAGNOSTIC_EVENT_INVALID")
	}
	logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, problem := emaildiagnostics.Append(logCtx, dataRoot, home, event); problem != nil {
		return out, &httpapi.ApplicationError{Code: problem.Code, Category: "email_connection_mutation"}
	}
	return out, configureError(public)
}

// configureEmailConnection invokes only typed native owners. Existing legacy
// v1 credential retirement remains refused by those owners until single-owner
// preflight is proven; a failed response never implies credential rollback.
func configureEmailConnection(ctx context.Context, workspace *readonly.Workspace, dataRoot string,
	backendFactory func() (secretstore.Backend, error), request httpapi.EmailConfigureRequest, requestedRunID string,
) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	if ctx == nil || ctx.Err() != nil {
		return &httpapi.ApplicationOutcome{}, configureError("EMAIL_CONNECTION_CANCELED")
	}
	if workspace == nil || workspace.CheckAdmission() != nil {
		return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "WORKSPACE_UNAVAILABLE", Category: "email_connection_mutation"}
	}
	home, err := workspace.StartupHome()
	if err != nil {
		return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "WORKSPACE_UNAVAILABLE", Category: "email_connection_mutation"}
	}
	run, err := emaildiagnostics.NewConnection(requestedRunID)
	if err != nil {
		return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "EMAIL_DIAGNOSTIC_STORE_UNAVAILABLE", Category: "email_connection_mutation"}
	}
	out := &httpapi.ApplicationOutcome{RunID: run.ID()}
	started, err := run.Started()
	if err != nil {
		return out, &httpapi.ApplicationError{Code: "EMAIL_DIAGNOSTIC_EVENT_INVALID", Category: "email_connection_mutation"}
	}
	if _, problem := emaildiagnostics.Append(ctx, dataRoot, home, started); problem != nil {
		return out, &httpapi.ApplicationError{Code: problem.Code, Category: "email_connection_mutation"}
	}
	if backendFactory == nil {
		return configureTerminalFailure(ctx, dataRoot, home, run, out, "EMAIL_SECRET_STORE_FAILED", "credential", configureEffects{})
	}
	backend, err := backendFactory()
	if err != nil || secretstore.IsNilBackend(backend) {
		return configureTerminalFailure(ctx, dataRoot, home, run, out, "EMAIL_SECRET_STORE_FAILED", "credential", configureEffects{})
	}
	effects, problem := executeConfigure(ctx, dataRoot, home, backend, request)
	out.Committed, out.CommitUnknown, out.RecoveryRequired = effects.committed,
		effects.commitAttempted && !effects.committed, effects.recoveryRequired
	out.RevisionRefreshRequired = effects.revisionRefreshRequired
	if problem != nil {
		stage := "connection"
		if strings.HasPrefix(problem.Code, "EMAIL_CREDENTIAL_") || problem.Code == "EMAIL_SECRET_STORE_FAILED" {
			stage = "credential"
		}
		if effects.commitAttempted || effects.recoveryRequired {
			stage = "local_persistence"
		}
		return configureTerminalFailure(ctx, dataRoot, home, run, out, problem.Code, stage, effects)
	}
	read, readProblem := readEmailConnection(ctx, workspace, dataRoot, func() (secretstore.Backend, error) { return backend, nil })
	if readProblem != nil || read == nil || len(read.Result) == 0 {
		return configureTerminalFailure(ctx, dataRoot, home, run, out, "EMAIL_CONNECTION_RESPONSE_INVALID", "readback", effects)
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(read.Result, &result) != nil || result == nil {
		return configureTerminalFailure(ctx, dataRoot, home, run, out, "EMAIL_CONNECTION_RESPONSE_INVALID", "readback", effects)
	}
	var observedRevision int64
	if json.Unmarshal(result["revision"], &observedRevision) != nil || observedRevision != effects.revision || result["configured"] == nil {
		return configureTerminalFailure(ctx, dataRoot, home, run, out, "EMAIL_CONNECTION_RESPONSE_INVALID", "readback", effects)
	}
	result["run_id"], _ = json.Marshal(run.ID())
	out.Result, err = json.Marshal(result)
	if err != nil {
		return configureTerminalFailure(ctx, dataRoot, home, run, out, "EMAIL_CONNECTION_RESPONSE_INVALID", "readback", effects)
	}
	terminal, err := run.ConnectionCompleted(effects.revision, request.PasswordSupplied)
	if err != nil {
		out.Result = nil
		return out, &httpapi.ApplicationError{Code: "EMAIL_DIAGNOSTIC_EVENT_INVALID", Category: "email_connection_mutation"}
	}
	logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, problem := emaildiagnostics.Append(logCtx, dataRoot, home, terminal); problem != nil {
		out.Result = nil
		return out, &httpapi.ApplicationError{Code: problem.Code, Category: "email_connection_mutation"}
	}
	return out, nil
}
