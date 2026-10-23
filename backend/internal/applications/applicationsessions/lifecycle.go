package applicationsessions

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"slices"
	"time"

	"careerhub.local/backend/internal/applications"
)

const ttl = 4 * time.Hour
const signalWindow = 15 * time.Minute
const clockSkew = 5 * time.Minute

func (e *engine) create(jobID string) (any, *Error) {
	job, p := e.application(jobID)
	if p != nil {
		return nil, p
	}
	if !startable(job.Status) {
		return nil, invalid("Application sessions can start only for Saved or Preparing applications")
	}
	url, origin, p := currentURL(job.SourceURL)
	if p != nil {
		return nil, p
	}
	expires := e.now.Add(ttl)
	if expires.Year() > 9999 {
		return nil, unsupported("CLOCK_RANGE")
	}
	result, ap := applications.StartInTransaction(e.ctx, e.tx, jobID, e.clock, e.clock)
	if ap != nil {
		return nil, applicationProblem(ap, jobID)
	}
	e.changed = e.changed || result.Changed
	raw, err := e.entropy.ID()
	if err != nil {
		return nil, unsupported("ENTROPY_FAILED")
	}
	id, p := sessionID("application_session_" + raw)
	if p != nil {
		return nil, unsupported("ENTROPY_FAILED")
	}
	if p = e.expire(); p != nil {
		return nil, p
	}
	detected, p := e.query("SELECT session_id FROM application_sessions WHERE job_id=? AND state='submission_detected' LIMIT 1", []any{jobID}, 1)
	if p != nil {
		return nil, p
	}
	if len(detected) > 0 {
		return nil, invalid("Review the existing submission signal before starting another session")
	}
	if p = e.update("state='cancelled',interruption_reason='replaced',session_token_sha256='',resolved_at=?,updated_at=?", "job_id=? AND state IN ('pending_claim','armed')", []any{e.clock, e.clock}, []any{jobID}); p != nil {
		return nil, p
	}
	if p = e.exec("INSERT INTO application_sessions (session_id,job_id,state,expected_url,expected_origin,created_at,expires_at,updated_at,revision) VALUES (?,?,'pending_claim',?,?,?,?,?,1)", id, jobID, url, origin, e.clock, stamp(expires), e.clock); p != nil {
		return nil, p
	}
	return e.project(id)
}

func hashToken(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func (e *engine) claim(fields map[string]any, allowCreate bool) (any, *Error) {
	value, p := e.claimOnce(fields)
	if p == nil || !allowCreate || p.Code != "APPLICATION_SESSION_NOT_PENDING" {
		return value, p
	}
	original := p
	url, _, p := currentURL(fields["current_url"])
	if p != nil {
		return nil, p
	}
	rows, p := e.query("SELECT "+boundedTextSQL("job_id")+","+boundedTextSQL("source_url")+" FROM job_applications WHERE current_status IN ('captured','reviewed','preparing') AND source_url<>'' ORDER BY updated_at DESC,job_id LIMIT 10001", nil, maxRows)
	if p != nil {
		return nil, p
	}
	selected := ""
	found := false
	for _, row := range rows {
		id, ok := row[0].(string)
		if !ok {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		current, _, p := currentURL(row[1])
		if p != nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(current), []byte(url)) == 1 {
			if found {
				return nil, problem("CLAIM_AMBIGUOUS", "More than one saved application has this exact URL")
			}
			if len(id) > applications.MaxIDBytes {
				return nil, unsupported("JOB_ID_UNSUPPORTED")
			}
			selected = id
			found = true
		}
	}
	if !found {
		return nil, original
	}
	if _, p = e.create(selected); p != nil {
		return nil, p
	}
	return e.claimOnce(fields)
}
func (e *engine) claimOnce(fields map[string]any) (any, *Error) {
	request, p := uuid(fields["request_id"], "request_id")
	if p != nil {
		return nil, p
	}
	tab, p := tabID(fields["tab_id"])
	if p != nil {
		return nil, p
	}
	url, origin, p := currentURL(fields["current_url"])
	if p != nil {
		return nil, p
	}
	capability, err := e.entropy.Token()
	// A broken entropy provider must never issue a weaker or malformed token.
	decoded, de := base64.RawURLEncoding.DecodeString(capability)
	if err != nil || de != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != capability {
		return nil, unsupported("ENTROPY_FAILED")
	}
	if p = e.expire(); p != nil {
		return nil, p
	}
	pending, p := e.sessions(" WHERE s.state='pending_claim' AND s.expected_url=? ORDER BY s.created_at DESC,s.session_id LIMIT 2", []any{url}, 2)
	if p != nil {
		return nil, p
	}
	if len(pending) > 1 {
		return nil, problem("CLAIM_AMBIGUOUS", "More than one pending application session has this exact URL")
	}
	var selected *session
	if len(pending) == 1 {
		selected = pending[0]
	} else {
		armed, p := e.sessions(" WHERE s.state='armed' AND s.expected_url=? AND s.bound_tab_id=? AND s.bound_origin=? LIMIT 2", []any{url, tab, origin}, 2)
		if p != nil {
			return nil, p
		}
		if len(armed) > 1 {
			return nil, problem("CLAIM_AMBIGUOUS", "More than one armed application session matched this tab")
		}
		if len(armed) == 1 {
			selected = armed[0]
		}
	}
	if selected == nil {
		return nil, problem("NOT_PENDING", "Start this exact application from Career Hub before arming the tab")
	}
	id := selected.text("session_id")
	if p = e.update("state='expired',interruption_reason='tab_reused',session_token_sha256='',resolved_at=?,updated_at=?", "state='armed' AND bound_tab_id=? AND session_id<>?", []any{e.clock, e.clock}, []any{tab, id}); p != nil {
		return nil, p
	}
	if p = e.update("state='armed',bound_tab_id=?,bound_origin=?,session_token_sha256=?,claim_request_id=?,claimed_at=coalesce(claimed_at,?),updated_at=?", "session_id=? AND state IN ('pending_claim','armed')", []any{tab, origin, hashToken(capability), request, e.clock, e.clock}, []any{id}); p != nil {
		return nil, p
	}
	s, p := e.session(id)
	if p != nil {
		return nil, p
	}
	if s.text("state") != "armed" {
		return nil, problem("NOT_PENDING", "Application session could not be armed")
	}
	return map[string]any{"contract_version": ContractVersion, "outcome": "armed", "session_id": id, "session_token": capability, "bound_origin": origin, "expires_at": s.text("expires_at")}, nil
}

func (e *engine) authorized(fields map[string]any) (*session, *Error) {
	id, p := sessionID(fields["session_id"])
	if p != nil {
		return nil, p
	}
	capability, p := token(fields["session_token"])
	if p != nil {
		return nil, p
	}
	tab, p := tabID(fields["tab_id"])
	if p != nil {
		return nil, p
	}
	origin, p := origin(fields["page_origin"])
	if p != nil {
		return nil, p
	}
	if p = e.expire(); p != nil {
		return nil, p
	}
	s, p := e.session(id)
	if p != nil {
		return nil, p
	}
	if s.text("state") == "expired" {
		return nil, problem("EXPIRED", "Application session has expired")
	}
	stored := s.text("session_token_sha256")
	if stored == "" || subtle.ConstantTimeCompare([]byte(stored), []byte(hashToken(capability))) != 1 {
		return nil, problem("UNAUTHORIZED", "Application session capability is invalid")
	}
	if s.tab == nil || *s.tab != tab {
		return nil, problem("UNAUTHORIZED", "Application session is bound to another tab")
	}
	// Byte comparison is constant-time for equal lengths, including UTF-8.
	// Unlike Python compare_digest(str,str), it cannot panic on an IDN origin.
	if subtle.ConstantTimeCompare([]byte(s.text("bound_origin")), []byte(origin)) != 1 {
		return nil, problem("UNAUTHORIZED", "Application session origin changed")
	}
	return s, nil
}
func smallOutcome(outcome string, review bool) map[string]any {
	return map[string]any{"contract_version": ContractVersion, "outcome": outcome, "review_required": review}
}
func (e *engine) signal(fields map[string]any) (any, *Error) {
	event, p := uuid(fields["event_key"], "event_key")
	if p != nil {
		return nil, p
	}
	rule, ok := fields["success_rule"].(string)
	if !ok || !slices.Contains([]string{"application_submitted", "application_received", "successfully_applied", "thank_you_for_applying"}, rule) {
		return nil, invalid("success_rule is not supported")
	}
	submit, p := timestamp(fields["submit_observed_at"], "submit_observed_at")
	if p != nil {
		return nil, p
	}
	success, p := timestamp(fields["success_observed_at"], "success_observed_at")
	if p != nil {
		return nil, p
	}
	if success.Before(submit) {
		return nil, invalid("success must be observed after the form submit")
	}
	if success.Sub(submit) > signalWindow {
		return nil, invalid("submission signal window exceeded")
	}
	if success.After(e.now.Add(clockSkew)) {
		return nil, invalid("success_observed_at is too far in the future")
	}
	if success.Before(e.now.Add(-signalWindow - clockSkew)) {
		return nil, invalid("submission signal is too old")
	}
	s, p := e.authorized(fields)
	if p != nil {
		return nil, p
	}
	claimed, p := timestamp(s.values["claimed_at"], "claimed_at")
	if p != nil {
		return nil, p
	}
	if submit.Before(claimed.Add(-clockSkew)) {
		return nil, invalid("submit_observed_at predates the armed application session")
	}
	id := s.text("session_id")
	prior, p := e.query("SELECT "+boundedTextSQL("session_id")+","+boundedTextSQL("rule_id")+","+boundedTextSQL("submit_observed_at")+","+boundedTextSQL("success_observed_at")+" FROM application_session_events WHERE event_key=?", []any{event}, 1)
	if p != nil {
		return nil, p
	}
	submitText, successText := stamp(submit), stamp(success)
	if len(prior) > 0 {
		row := prior[0]
		if row[0] != id || row[1] != rule || row[2] != submitText || row[3] != successText {
			return nil, problem("EVENT_CONFLICT", "Submission event key was reused with different evidence")
		}
		return smallOutcome("duplicate", true), nil
	}
	if s.text("state") == "submission_detected" {
		return smallOutcome("already_detected", true), nil
	}
	if s.text("state") != "armed" {
		return nil, invalid("Application session is not armed")
	}
	if p = e.exec("INSERT INTO application_session_events (session_id,event_key,event_type,rule_id,submit_observed_at,success_observed_at,recorded_at) VALUES (?,?,'submission_detected',?,?,?,?)", id, event, rule, submitText, successText, e.clock); p != nil {
		return nil, p
	}
	if p = e.update("state='submission_detected',detected_event_key=?,detection_rule=?,detected_at=?,updated_at=?", "session_id=?", []any{event, rule, successText, e.clock}, []any{id}); p != nil {
		return nil, p
	}
	return smallOutcome("submission_detected", true), nil
}
func (e *engine) interrupt(fields map[string]any) (any, *Error) {
	reason, ok := fields["reason"].(string)
	if !ok || !slices.Contains([]string{"origin_changed", "tab_closed", "tab_reused", "instrumentation_lost", "manual_stop", "timeout", "replaced"}, reason) {
		return nil, invalid("interruption reason is not supported")
	}
	s, p := e.authorized(fields)
	if p != nil {
		return nil, p
	}
	switch s.text("state") {
	case "submission_detected":
		return smallOutcome("review_preserved", true), nil
	case "interrupted":
		return smallOutcome("duplicate", false), nil
	case "armed":
	default:
		return nil, invalid("Application session is not armed")
	}
	// Retain the capability for an idempotent interruption replay. Only an
	// explicit terminal resolution, replacement or expiry revokes it.
	if p = e.update("state='interrupted',interruption_reason=?,resolved_at=?,updated_at=?", "session_id=?", []any{reason, e.clock, e.clock}, []any{s.text("session_id")}); p != nil {
		return nil, p
	}
	return smallOutcome("interrupted", false), nil
}
func (e *engine) resolve(action string, fields map[string]any) (any, *Error) {
	id, p := sessionID(fields["session_id"])
	if p != nil {
		return nil, p
	}
	revision, ok := integer(fields["expected_revision"])
	if !ok {
		return nil, invalid("expected_revision must be an integer")
	}
	if action != "dismiss" {
		if p = e.expire(); p != nil {
			return nil, p
		}
	}
	s, p := e.session(id)
	if p != nil {
		return nil, p
	}
	terminal := map[string]string{"confirm": "confirmed_applied", "dismiss": "dismissed", "cancel": "cancelled"}[action]
	if s.text("state") == terminal {
		return s.projection(), nil
	}
	if s.revision != revision {
		noun := map[string]string{"confirm": "confirmation", "dismiss": "review", "cancel": "action"}[action]
		return nil, problem("REVISION_CONFLICT", "Application session changed after the "+noun+" loaded")
	}
	if action == "cancel" {
		if !slices.Contains([]string{"pending_claim", "armed", "interrupted"}, s.text("state")) {
			return nil, invalid("This application session cannot be cancelled")
		}
	} else if s.text("state") != "submission_detected" {
		return nil, invalid("Application session has no submission to " + action)
	}
	if action == "confirm" {
		jobID := s.text("job_id")
		job, p := e.application(jobID)
		if p != nil {
			return nil, p
		}
		if !job.SubmissionRecorded && startable(job.Status) {
			detected, p := timestamp(s.values["detected_at"], "occurred_at")
			if p != nil {
				return nil, p
			}
			result, ap := applications.StatusInTransaction(e.ctx, e.tx, jobID, "applied", stamp(detected), e.clock, "Submission detected by the attended browser extension and confirmed by the user.", nil)
			if ap != nil {
				return nil, applicationProblem(ap, jobID)
			}
			e.changed = e.changed || result.Changed
			// Triggers or a corrupt store must not allow the status write to
			// silently replace the session being confirmed within this tx.
			latest, p := e.session(id)
			if p != nil {
				return nil, p
			}
			if latest.text("state") != "submission_detected" || latest.revision != revision {
				return nil, problem("REVISION_CONFLICT", "Application session changed during confirmation")
			}
		}
	}
	set := "state=?,session_token_sha256='',resolved_at=?,updated_at=?"
	if action == "cancel" {
		set += ",interruption_reason='manual_stop'"
	}
	if p = e.update(set, "session_id=?", []any{terminal, e.clock, e.clock}, []any{id}); p != nil {
		return nil, p
	}
	return e.project(id)
}
