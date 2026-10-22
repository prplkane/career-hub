package main

import (
	"context"
	"errors"

	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/sidecar/assessment"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

func feedActions(start protocol.StartRecord) func(context.Context, httpapi.FeedRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	owner := applicationwrite.New(start.DataRoot)
	review := func(ctx context.Context, id string) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		service, err := assessment.Open(start)
		if err != nil {
			return feedReviewFailure(nil, err)
		}
		result, err := service.Review(ctx, id)
		return feedReviewFailure(result, err)
	}
	return func(ctx context.Context, r httpapi.FeedRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		if r.Operation == "list" {
			service, err := assessment.Open(start)
			if err != nil {
				return feedReviewFailure(nil, err)
			}
			result, err := service.Page(ctx, r.Page)
			return feedPageOutcome(result, err), feedReadError(err)
		}
		if r.Operation == "detail" {
			return review(ctx, r.ID)
		}
		var value *applicationwrite.Outcome
		var e *applicationwrite.Error
		if r.Operation == "delete" || r.Operation == "flush" {
			request := discovery.DeleteRequest{Operation: r.Operation, ExpectedRevision: r.Revision}
			if r.Operation == "delete" {
				request.DiscoveryID = r.ID
			}
			value, e = owner.DeleteDiscoveries(ctx, request)
		} else {
			value, e = owner.EditDiscovery(ctx, discovery.EditRequest{Operation: r.Operation, DiscoveryID: r.ID, State: r.State, Overrides: r.Overrides, ExpectedRevision: r.Revision})
		}
		out := feedWriteOutcome(value)
		if e != nil {
			return out, feedWriteError(e)
		}
		if r.Operation == "confirm" || r.Operation == "transition" || r.Operation == "overrides" {
			reviewed, err := review(ctx, r.ID)
			return mergeFeedMutationReview(out, reviewed, err)
		}
		return out, nil
	}
}

func feedPageOutcome(value *assessment.PageResult, err error) *httpapi.ApplicationOutcome {
	if value == nil {
		return nil
	}
	return &httpapi.ApplicationOutcome{Result: value.Body, Committed: value.Write.Committed,
		CommitUnknown: value.CommitUnknown, CleanupFailed: value.CleanupFailed,
		Partial: value.Write.DirectoriesCreated && !value.Write.Committed || err != nil && value.Write.Committed}
}

func feedReviewFailure(value *assessment.Result, err error) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	out := feedReviewOutcome(value)
	if out == nil {
		out = &httpapi.ApplicationOutcome{}
	}
	var problem *assessment.Error
	if errors.As(err, &problem) && problem.Code == "ASSESSMENT_CLEANUP_FAILED" {
		out.CleanupFailed = true
	}
	return out, feedReadError(err)
}

func feedReviewOutcome(value *assessment.Result) *httpapi.ApplicationOutcome {
	if value == nil {
		return nil
	}
	return &httpapi.ApplicationOutcome{Result: value.Review, Committed: value.Write.Committed, CommitUnknown: value.Write.Attempted && !value.Write.Committed, Partial: value.Write.DirectoriesCreated && !value.Write.Committed, CleanupFailed: value.CleanupFailed}
}

func mergeFeedMutationReview(out, reviewed *httpapi.ApplicationOutcome, err *httpapi.ApplicationError) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	if out == nil {
		return reviewed, err
	}
	out.Result = nil
	if reviewed != nil {
		out.Result = reviewed.Result
		out.Committed = out.Committed || reviewed.Committed
		out.CommitUnknown = out.CommitUnknown || reviewed.CommitUnknown
		out.CleanupFailed = out.CleanupFailed || reviewed.CleanupFailed
		out.Partial = out.Partial || reviewed.Partial
	}
	if err != nil {
		out.Result = nil
		out.Partial = out.Partial || out.Committed
	}
	return out, err
}

func feedWriteOutcome(value *applicationwrite.Outcome) *httpapi.ApplicationOutcome {
	if value == nil {
		return nil
	}
	return &httpapi.ApplicationOutcome{Result: value.Result, Committed: value.Committed, CommitUnknown: value.CommitAttempted && !value.Committed, CleanupFailed: value.CleanupFailed, RecoveryRequired: value.RecoveryRequired, Partial: value.RecoveryRequired && value.Committed}
}
func feedReadError(e error) *httpapi.ApplicationError {
	if e == nil {
		return nil
	}
	code := "DISCOVERY_READ_UNAVAILABLE"
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		code = "LOCAL_OPERATION_CANCELED"
	}
	var problem *readonly.Error
	if errors.As(e, &problem) && problem.Code == "DISCOVERY_NOT_FOUND" {
		code = "DISCOVERY_NOT_FOUND"
	}
	var assessmentProblem *assessment.Error
	if errors.As(e, &assessmentProblem) && assessmentProblem.Code == "ASSESSMENT_INPUT_CANCELED" {
		code = "LOCAL_OPERATION_CANCELED"
	}
	if errors.As(e, &assessmentProblem) && assessmentProblem.Code == "ASSESSMENT_FEED_INPUT_LIMIT" {
		code = "DISCOVERY_QUERY_TOO_BROAD"
	}
	return &httpapi.ApplicationError{Code: code}
}
func feedWriteError(e *applicationwrite.Error) *httpapi.ApplicationError {
	if e == nil {
		return nil
	}
	code := "DISCOVERY_WRITE_FAILED"
	switch e.SourceClass {
	case "DiscoveryNotFound":
		code = "DISCOVERY_NOT_FOUND"
	case "DiscoveryRevisionConflict":
		code = "DISCOVERY_REVISION_CONFLICT"
	case "DiscoveryFieldOverrideRevisionConflict":
		code = "DISCOVERY_FIELD_OVERRIDE_REVISION_CONFLICT"
	case "DiscoveryVerificationNotReady":
		code = "DISCOVERY_VERIFICATION_NOT_READY"
	case "DiscoveryPromotionNotReady":
		code = "DISCOVERY_VERIFICATION_NOT_READY"
	case "DiscoveryError":
		code = "DISCOVERY_UPDATE_INVALID"
	}
	if e.Stage == "request" && e.SourceClass == "" {
		code = "DISCOVERY_UPDATE_INVALID"
	}
	if e.Stage == "context" {
		code = "LOCAL_OPERATION_CANCELED"
	}
	return &httpapi.ApplicationError{Code: code}
}
