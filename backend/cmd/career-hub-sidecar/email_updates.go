package main

import (
	"context"
	"encoding/json"
	"errors"

	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func readEmailUpdates(ctx context.Context, workspace *readonly.Workspace, offset int64, limit int) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	page, err := workspace.EmailApplicationUpdates(ctx, offset, limit)
	if err != nil {
		return &httpapi.ApplicationOutcome{}, emailUpdatesFailure(err)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "EMAIL_APPLICATION_UPDATE_READ_FAILED", Category: "email_application_updates_read"}
	}
	return &httpapi.ApplicationOutcome{Result: raw}, nil
}

func emailUpdatesFailure(err error) *httpapi.ApplicationError {
	code := "EMAIL_APPLICATION_UPDATE_READ_FAILED"
	var safe *readonly.Error
	if errors.As(err, &safe) {
		switch safe.Code {
		case "EMAIL_APPLICATION_UPDATE_INVALID":
			code = "EMAIL_APPLICATION_UPDATE_INVALID"
		case "APPLICATION_READ_CANCELED":
			code = "EMAIL_APPLICATION_UPDATE_READ_CANCELED"
		case "APPLICATION_SCHEMA_TOO_NEW", "APPLICATION_SCHEMA_PREPARATION_REQUIRED",
			"APPLICATION_DATABASE_UNAVAILABLE", "APPLICATION_STORAGE_UNSUPPORTED",
			"APPLICATION_PATH_REJECTED", "DATA_HOME_UNAVAILABLE",
			"DATA_HOME_OWNERSHIP_REQUIRED", "DATA_HOME_CHANGED", "DATA_HOME_IDENTITY_CHANGED",
			"DATA_HOME_RECOVERY_REQUIRED":
			code = "EMAIL_APPLICATION_UPDATE_READ_UNAVAILABLE"
		}
	}
	return &httpapi.ApplicationError{Code: code, Category: "email_application_updates_read"}
}
