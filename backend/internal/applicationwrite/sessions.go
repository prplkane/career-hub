package applicationwrite

import (
	"context"
	"database/sql"
	"fmt"
	"io"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/applicationsessions"
)

func (s *Service) session(ctx context.Context, op string, fields map[string]any) (*Outcome, *Error) {
	if s == nil || s.sessionClock == nil || s.sessionEntropy == nil {
		return emptyFailure(failed("configuration", "APPLICATION_SESSION_UNAVAILABLE"))
	}
	// All session operations use the same gate/ownership/transaction owner.
	// Latest/reviews can expire records; they must not use a read-only shortcut.
	return s.run(ctx, "", func(_ *sourceReads) (mutation, *Error) {
		now := s.sessionClock()
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			result, p := applicationsessions.ApplyInTransaction(ctx, tx, applicationsessions.Request{Operation: op, Fields: fields}, now, s.sessionEntropy)
			if p != nil {
				return nil, &Error{Stage: p.Stage, Code: p.Code, SourceClass: p.SourceClass, SourceMessage: p.SourceMessage}
			}
			return result, nil
		}, nil
	})
}

// Trusted first-party operations. A future HTTP/agent adapter must authorize
// these separately; an extension capability never permits explicit resolution.
func (s *Service) CreateApplicationSession(ctx context.Context, jobID string) (*Outcome, *Error) {
	return s.session(ctx, "create", map[string]any{"job_id": jobID})
}
func (s *Service) LatestApplicationSession(ctx context.Context, jobID string) (*Outcome, *Error) {
	return s.session(ctx, "latest", map[string]any{"job_id": jobID})
}
func (s *Service) SubmissionReviews(ctx context.Context) (*Outcome, *Error) {
	return s.session(ctx, "reviews", map[string]any{})
}
func (s *Service) ConfirmApplicationSession(ctx context.Context, id, revision any) (*Outcome, *Error) {
	return s.session(ctx, "confirm", map[string]any{"session_id": id, "expected_revision": revision})
}
func (s *Service) DismissApplicationSession(ctx context.Context, id, revision any) (*Outcome, *Error) {
	return s.session(ctx, "dismiss", map[string]any{"session_id": id, "expected_revision": revision})
}
func (s *Service) CancelApplicationSession(ctx context.Context, id, revision any) (*Outcome, *Error) {
	return s.session(ctx, "cancel", map[string]any{"session_id": id, "expected_revision": revision})
}

// Extension operations have no caller-supplied job selector and cannot choose
// Applied status. Claim/create finds a single exact saved-job URL, not a title.
func (s *Service) ClaimApplicationSession(ctx context.Context, requestID, url, tabID any) (*Outcome, *Error) {
	return s.session(ctx, "claim", map[string]any{"request_id": requestID, "current_url": url, "tab_id": tabID})
}
func (s *Service) ClaimOrCreateApplicationSession(ctx context.Context, requestID, url, tabID any) (*Outcome, *Error) {
	return s.session(ctx, "claim_create", map[string]any{"request_id": requestID, "current_url": url, "tab_id": tabID})
}

type SessionCapability struct{ SessionID, Token, TabID, PageOrigin any }

// Diagnostic formatting must not expose a capability through its parent signal.
// The claim result is intentionally returned to its paired caller, not logged.
func (SessionCapability) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted session capability]")
}

func (c SessionCapability) fields() map[string]any {
	return map[string]any{"session_id": c.SessionID, "session_token": c.Token, "tab_id": c.TabID, "page_origin": c.PageOrigin}
}

type SubmissionSignal struct {
	Capability                                                 SessionCapability
	EventKey, SuccessRule, SubmitObservedAt, SuccessObservedAt any
}

func (SubmissionSignal) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted submission signal]")
}

func (s *Service) RecordSubmissionSignal(ctx context.Context, signal SubmissionSignal) (*Outcome, *Error) {
	fields := signal.Capability.fields()
	fields["event_key"], fields["success_rule"] = signal.EventKey, signal.SuccessRule
	fields["submit_observed_at"], fields["success_observed_at"] = signal.SubmitObservedAt, signal.SuccessObservedAt
	return s.session(ctx, "signal", fields)
}
func (s *Service) InterruptApplicationSession(ctx context.Context, capability SessionCapability, reason any) (*Outcome, *Error) {
	fields := capability.fields()
	fields["reason"] = reason
	return s.session(ctx, "interrupt", fields)
}
