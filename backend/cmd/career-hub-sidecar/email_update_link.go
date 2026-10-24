package main

import (
	"context"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/storage/datahome"
)

func linkEmailUpdate(ctx context.Context, dataRoot string, home datahome.Info, request httpapi.EmailUpdateLinkRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	owner := applicationwrite.New(dataRoot).PinnedToHome(home)
	value, problem := owner.LinkEmailApplicationOutcome(ctx, request.OutcomeID, request.JobID, request.ExpectedRevision)
	out := feedWriteOutcome(value)
	if problem == nil {
		return out, nil
	}
	return out, emailLinkFailure(problem)
}

func emailLinkFailure(problem *applicationwrite.Error) *httpapi.ApplicationError {
	code := "EMAIL_APPLICATION_UPDATE_LINK_FAILED"
	switch problem.Code {
	case "EMAIL_OUTCOME_LINK_INVALID", "EMAIL_OUTCOME_LINK_ALREADY_RESOLVED":
		code = "EMAIL_APPLICATION_UPDATE_INVALID"
	case "EMAIL_OUTCOME_LINK_NOT_FOUND":
		code = "EMAIL_APPLICATION_UPDATE_NOT_FOUND"
	case "EMAIL_OUTCOME_LINK_REVISION_CONFLICT":
		code = "EMAIL_APPLICATION_UPDATE_REVISION_CONFLICT"
	case "EMAIL_OUTCOME_LINK_CANCELED", "APPLICATION_LEDGER_CANCELED":
		code = "EMAIL_APPLICATION_UPDATE_LINK_CANCELED"
	case "APPLICATION_LEDGER_COMMIT_UNCONFIRMED":
		code = "EMAIL_APPLICATION_UPDATE_LINK_COMMIT_UNCONFIRMED"
	case "APPLICATION_LEDGER_HOME_CHANGED", "DATA_HOME_OWNERSHIP_REQUIRED",
		"DATA_HOME_UNAVAILABLE", "SCHEMA_PREPARATION_REQUIRED", "SCHEMA_TOO_NEW":
		code = "EMAIL_APPLICATION_UPDATE_LINK_UNAVAILABLE"
	}
	return &httpapi.ApplicationError{Code: code, Category: "email_application_update_link"}
}
