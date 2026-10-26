package main

import (
	"context"
	"encoding/json"

	"careerhub.local/backend/internal/applications/applicationhelper"
	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/sidecar/assessment"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func extensionActions(w *readonly.Workspace, start protocol.StartRecord) func(context.Context, httpapi.ExtensionRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	owner, helper := applicationwrite.New(start.DataRoot), applicationhelper.New(start.DataRoot)
	return func(ctx context.Context, r httpapi.ExtensionRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		if ctx == nil || ctx.Err() != nil {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "LOCAL_OPERATION_CANCELED"}
		}
		if w.CheckAdmission() != nil {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "WORKSPACE_UNAVAILABLE"}
		}
		out, problem := runExtension(ctx, owner, helper, start, r)
		if w.CheckAdmission() != nil {
			if out == nil {
				out = &httpapi.ApplicationOutcome{}
			}
			out.Result = nil
			return out, &httpapi.ApplicationError{Code: "WORKSPACE_UNAVAILABLE"}
		}
		return out, problem
	}
}

func runExtension(ctx context.Context, owner *applicationwrite.Service, helper *applicationhelper.Service, start protocol.StartRecord, r httpapi.ExtensionRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	f := r.Fields
	if r.Operation == "projection" || r.Operation == "answers.save" || r.Operation == "answers.delete" {
		value, problem := helper.Run(ctx, applicationhelper.Request{Operation: r.Operation, Fields: f})
		return extensionHelperOutcome(value, problem)
	}
	if r.Operation == "capture" {
		value, problem := owner.CaptureDiscovery(ctx, r.Capture)
		out := feedWriteOutcome(value)
		if problem != nil {
			code := "EXTENSION_CAPTURE_FAILED"
			if problem.SourceClass == "DiscoveryIdempotencyConflict" {
				code = "EXTENSION_CAPTURE_IDEMPOTENCY_CONFLICT"
			}
			if problem.SourceClass == "DiscoveryError" {
				code = "EXTENSION_CAPTURE_INVALID"
			}
			return out, &httpapi.ApplicationError{Code: code}
		}
		var saved struct {
			Outcome   string `json:"outcome"`
			Discovery struct {
				ID string `json:"discovery_id"`
			} `json:"discovery"`
		}
		if out == nil || json.Unmarshal(out.Result, &saved) != nil || saved.Discovery.ID == "" {
			return out, &httpapi.ApplicationError{Code: "EXTENSION_CAPTURE_RESPONSE_INVALID"}
		}
		service, err := assessment.Open(start)
		if err != nil {
			out.Result = nil
			return out, &httpapi.ApplicationError{Code: "DISCOVERY_READ_UNAVAILABLE"}
		}
		preview, err := service.Review(ctx, saved.Discovery.ID)
		out, readErr := mergeFeedMutationReview(out, feedReviewOutcome(preview), feedReadError(err))
		if readErr != nil {
			return out, readErr
		}
		if preview == nil || preview.Preview == nil {
			out.Result = nil
			return out, &httpapi.ApplicationError{Code: "EXTENSION_CAPTURE_RESPONSE_INVALID"}
		}
		out.Result, err = json.Marshal(map[string]any{"contract_version": 1, "outcome": saved.Outcome, "readiness": r.Readiness, "capture_scope": r.Scope, "content_chars": r.ContentChars, "receipt": preview.Preview.Receipt, "review": map[string]any{"discovery_id": saved.Discovery.ID}})
		if err != nil {
			out.Result = nil
			return out, &httpapi.ApplicationError{Code: "EXTENSION_CAPTURE_RESPONSE_INVALID"}
		}
		return out, nil
	}
	var value *applicationwrite.Outcome
	var problem *applicationwrite.Error
	capability := applicationwrite.SessionCapability{SessionID: f["session_id"], Token: f["session_token"], TabID: f["tab_id"], PageOrigin: f["page_origin"]}
	switch r.Operation {
	case "claim":
		value, problem = owner.ClaimOrCreateApplicationSession(ctx, f["request_id"], f["current_url"], f["tab_id"])
	case "signal":
		value, problem = owner.RecordSubmissionSignal(ctx, applicationwrite.SubmissionSignal{Capability: capability, EventKey: f["event_key"], SuccessRule: f["success_rule"], SubmitObservedAt: f["submit_observed_at"], SuccessObservedAt: f["success_observed_at"]})
	case "interrupt":
		value, problem = owner.InterruptApplicationSession(ctx, capability, f["reason"])
	default:
		return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "APPLICATION_SESSION_INVALID"}
	}
	return extensionSessionOutcome(value, problem)
}

func extensionSessionOutcome(value *applicationwrite.Outcome, problem *applicationwrite.Error) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	out := feedWriteOutcome(value)
	if problem == nil {
		return out, nil
	}
	code := "APPLICATION_SESSION_FAILED"
	switch problem.SourceClass {
	case "ApplicationSessionUnauthorized":
		code = "APPLICATION_SESSION_UNAUTHORIZED"
	case "ApplicationSessionNotFound":
		code = "APPLICATION_SESSION_NOT_FOUND"
	case "ApplicationSessionExpired":
		code = "APPLICATION_SESSION_EXPIRED"
	case "ApplicationSessionClaimUnavailable":
		code = "APPLICATION_SESSION_NOT_PENDING"
	case "ApplicationSessionClaimAmbiguous":
		code = "APPLICATION_SESSION_CLAIM_AMBIGUOUS"
	case "ApplicationSessionRevisionConflict":
		code = "APPLICATION_SESSION_REVISION_CONFLICT"
	case "ApplicationSessionEventConflict":
		code = "APPLICATION_SESSION_EVENT_CONFLICT"
	case "ApplicationNotFound":
		code = "APPLICATION_JOB_NOT_FOUND"
	case "ApplicationSessionError":
		code = "APPLICATION_SESSION_INVALID"
	}
	return out, &httpapi.ApplicationError{Code: code}
}

func extensionHelperOutcome(value *applicationhelper.Outcome, problem *applicationhelper.Error) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	out := &httpapi.ApplicationOutcome{}
	if value != nil {
		out.Result = value.Result
		out.Committed = value.Committed
		out.CleanupFailed = value.CleanupFailed
	}
	if problem == nil {
		return out, nil
	}
	out.Result = nil
	// A native publication error without confirmation is not proof of rollback.
	out.CommitUnknown = problem.Stage == "save" && !out.Committed
	code := "APPLICATION_HELPER_OPERATION_FAILED"
	switch problem.Code {
	case "APPLICATION_ANSWER_INVALID", "APPLICATION_ANSWER_REVISION_CONFLICT", "APPLICATION_HELPER_SOURCE_UNAVAILABLE", "APPLICATION_HELPER_CONFIGURATION_INVALID":
		code = problem.Code
	case "APPLICATION_HELPER_CANCELED":
		code = "LOCAL_OPERATION_CANCELED"
	}
	return out, &httpapi.ApplicationError{Code: code}
}
