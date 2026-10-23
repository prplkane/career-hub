package main

import (
	"context"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/sidecar/httpapi"
)

func contactAction(ctx context.Context, owner *applicationwrite.Service, r httpapi.JobRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	var value *applicationwrite.Outcome
	var err *applicationwrite.Error
	switch r.Operation {
	case "contact.list":
		value, err = owner.ListContacts(ctx, r.Fields["include_archived"])
	case "contact.get":
		value, err = owner.GetContact(ctx, r.ID)
	case "contact.update":
		value, err = owner.UpdateContact(ctx, r.ID, r.Fields["expected_revision"], r.ContactUpdates, nil)
	case "contact.archive":
		value, err = owner.ArchiveContact(ctx, r.ID, r.Fields["expected_revision"])
	case "contact.restore":
		value, err = owner.RestoreContact(ctx, r.ID, r.Fields["expected_revision"])
	case "contact.unlink":
		value, err = owner.UnlinkContact(ctx, applicationwrite.ContactFields{
			"contact_id": r.ID, "expected_revision": r.Fields["expected_revision"],
			"job_ids": r.Fields["job_ids"], "discovery_ids": r.Fields["discovery_ids"],
		})
	default:
		return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "CONTACT_REQUEST_INVALID"}
	}
	return jobEditorResult(value, err, false)
}

// Reuse the owning transaction's publication facts. Never expose source errors
// containing contact facts, evidence, private paths, or database details.
func jobEditorResult(value *applicationwrite.Outcome, err *applicationwrite.Error, plan bool) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	out := feedWriteOutcome(value)
	if err == nil {
		return out, nil
	}
	code := "CONTACT_OPERATION_FAILED"
	if plan {
		code = "RESUME_PLAN_SAVE_FAILED"
		if err.SourceClass == "ResumePlanError" || err.SourceClass == "ApplicationHistoryError" || err.SourceClass == "ApplicationRevisionConflict" {
			code = "RESUME_PLAN_INVALID"
		}
		if err.SourceClass == "ApplicationNotFound" || err.Stage == "applications" && err.Code == "NOT_FOUND" {
			code = "APPLICATION_JOB_NOT_FOUND"
		}
	} else {
		switch err.SourceClass {
		case "ApplicationContactError":
			code = "CONTACT_REQUEST_INVALID"
		case "ApplicationContactNotFound":
			code = "CONTACT_NOT_FOUND"
		case "ApplicationContactRevisionConflict", "ApplicationContactArchived", "ApplicationContactExactMatchAmbiguous", "ApplicationContactProvenanceConflict":
			code = "CONTACT_CONFLICT"
		}
	}
	if err.Code == "APPLICATION_LEDGER_CANCELED" || err.Code == "APPLICATION_CONTACT_CANCELED" || err.Code == "RESUME_PLAN_CANCELED" {
		code = "LOCAL_OPERATION_CANCELED"
	}
	return out, &httpapi.ApplicationError{Code: code}
}
