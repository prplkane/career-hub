// Package emaildiagnostics builds content-minimized, source-shaped email run
// events. It neither opens a Data Home nor writes a log; persistence and HTTP
// exposure remain separate owners.
package emaildiagnostics

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/email/emailworker"
)

const MaxEventBytes = 16 * 1024

var (
	runIDPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{6}Z_[0-9a-f]{8}$`)
	codePattern  = regexp.MustCompile(`^EMAIL_[A-Z0-9_]{1,80}$`)
	ErrState     = errors.New("EMAIL_DIAGNOSTIC_STATE_INVALID")
	ErrResult    = errors.New("EMAIL_DIAGNOSTIC_RESULT_INVALID")
	ErrEncode    = errors.New("EMAIL_DIAGNOSTIC_ENCODE_FAILED")
)

type Run struct {
	mu        sync.Mutex
	id        string
	source    string
	stage     string
	operation string
	started   time.Time
	now       func() time.Time
	seq       int
}

// Event is minted only by Run's fixed, content-minimized constructors. Its
// bytes cannot be replaced by an HTTP caller or a future mailbox adapter.
type Event struct{ raw []byte }

func (e Event) Bytes() []byte { return bytes.Clone(e.raw) }

func ValidRunID(value string) bool { return runIDPattern.MatchString(value) }

// New accepts the source client's correlation ID only if it has the exact
// safe format. Other values get a fresh, cryptographically random suffix.
func New(requested string) (*Run, error) { return newWithClock(requested, time.Now, rand.Read) }

// NewConnection creates only the fixed connection-save run type. It cannot
// receive account, password, sender or arbitrary operation text.
func NewConnection(requested string) (*Run, error) {
	return newConnectionWithClock(requested, time.Now, rand.Read)
}

func newWithClock(requested string, now func() time.Time, read func([]byte) (int, error)) (*Run, error) {
	return newRunWithClock(requested, now, read, "gmail_sync", "email_sync_run", "sync")
}

func newConnectionWithClock(requested string, now func() time.Time, read func([]byte) (int, error)) (*Run, error) {
	return newRunWithClock(requested, now, read, "gmail_connection", "email_connection_run", "save")
}

func newRunWithClock(requested string, now func() time.Time, read func([]byte) (int, error), source, stage, operation string) (*Run, error) {
	if now == nil || read == nil {
		return nil, ErrState
	}
	started := now().UTC()
	id := strings.TrimSpace(requested)
	if !ValidRunID(id) {
		var suffix [4]byte
		if n, err := read(suffix[:]); err != nil || n != len(suffix) {
			return nil, ErrState
		}
		id = started.Format("20060102T150405.000000Z") + "_" + hex.EncodeToString(suffix[:])
	}
	return &Run{id: id, source: source, stage: stage, operation: operation, started: started, now: now}, nil
}

func (r *Run) ID() string {
	if r == nil {
		return ""
	}
	return r.id
}

type record struct {
	SchemaVersion int            `json:"schema_version"`
	Timestamp     string         `json:"timestamp"`
	RunID         string         `json:"run_id"`
	Sequence      int            `json:"sequence"`
	Component     string         `json:"component"`
	Source        string         `json:"source"`
	Stage         string         `json:"stage"`
	Event         string         `json:"event"`
	Status        string         `json:"status"`
	Details       map[string]any `json:"details"`
}

type guidance struct {
	summary, suggestion string
	severity            string
	retryable           bool
}

var failureGuidance = map[string]guidance{
	"EMAIL_SYNC_AUTH_FAILED": {"Gmail rejected the saved App Password.",
		"Create a fresh Google App Password, save it in Connection settings, then run Sync now again.", "warning", true},
	"EMAIL_SYNC_NETWORK_FAILED": {"Career Hub could not reach Gmail within the network limit.",
		"Check the internet connection and retry Sync now. The saved local data is unchanged.", "error", true},
	"EMAIL_SYNC_PROTOCOL_FAILED": {"Gmail did not return the expected read-only mailbox response.",
		"Retry once. If it repeats, report the Diagnostics run ID without changing Gmail security settings.", "error", true},
	"EMAIL_SYNC_ALREADY_RUNNING": {"Another mailbox operation is still running.",
		"Wait for it to finish. If it remains in progress past the safety limit, restart Career Hub before retrying.", "warning", true},
	"EMAIL_SYNC_LOCAL_WRITE_FAILED": {"An email result could not be stored in the local Data Home.",
		"Check disk space and folder access, then retry; Gmail messages were not changed.", "error", true},
	"EMAIL_CREDENTIAL_MISSING": {"A Gmail App Password is required.",
		"Create a fresh Google App Password, enter it in Connection settings, and save again.", "warning", true},
	"EMAIL_SECRET_STORE_FAILED": {"The App Password could not be stored in the operating system's protected credential store.",
		"Restart Career Hub and save the App Password again. If it repeats, report the Diagnostics run ID.", "error", true},
	"EMAIL_SYNC_DISABLED": {"The job-alert mailbox connection is disabled.",
		"Open Connection settings and save an enabled connection before syncing.", "warning", true},
	"EMAIL_SYNC_FAILED": {"The Gmail sync could not be completed safely.",
		"Open Diagnostics, inspect the failed stage, and retry after resolving the reported local error.", "error", true},
}

func (r *Run) encode(sequence int, event, status string, details map[string]any) (Event, error) {
	current := r.now().UTC()
	stamp := current.Format("2006-01-02T15:04:05.000000+00:00")
	if current.Nanosecond()/1000 == 0 {
		stamp = current.Format("2006-01-02T15:04:05+00:00")
	}
	raw, err := json.Marshal(record{1, stamp, r.id, sequence, "career_vault_intake", r.source, r.stage, event, status, details})
	if err != nil || len(raw)+1 > MaxEventBytes {
		return Event{}, ErrEncode
	}
	return Event{raw: append(raw, '\n')}, nil
}

func (r *Run) Started() (Event, error) {
	if r == nil {
		return Event{}, ErrState
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seq != 0 {
		return Event{}, ErrState
	}
	raw, err := r.encode(1, "started", "running", map[string]any{"operation": r.operation})
	if err == nil {
		r.seq = 1
	}
	return raw, err
}

func (r *Run) durationMS() float64 {
	millis := r.now().UTC().Sub(r.started).Seconds() * 1000
	if millis < 0 {
		millis = 0
	}
	return math.Round(millis*1000) / 1000
}

func diagnosticCounts(c emailworker.Counts) map[string]int {
	return map[string]int{
		"uids_found": c.UIDsFound, "headers_fetched": c.HeadersFetched,
		"bodies_fetched": c.BodiesFetched, "messages_imported": c.MessagesImported,
		"messages_without_candidates": c.MessagesWithoutCandidates,
		"messages_filtered_subject":   c.MessagesFilteredSubject,
		"messages_duplicate":          c.MessagesDuplicate,
		"messages_unknown_sender":     c.MessagesUnknownSender,
		"messages_too_large":          c.MessagesTooLarge, "messages_failed": c.MessagesFailed,
		"messages_retry_pending": c.MessagesRetryPending,
		"messages_failed_final":  c.MessagesFailedFinal,
		"candidates_found":       c.CandidatesFound, "discoveries_written": c.DiscoveriesWritten,
		"application_outcomes_found": c.ApplicationOutcomesFound,
		"applications_updated":       c.ApplicationsUpdated,
	}
}

func partialGuidance(code string) (string, string) {
	if code == "EMAIL_SYNC_MESSAGE_RETRY" {
		return "Gmail was reached, but one or more messages could not yet be imported.",
			"Newer messages were still checked. Retry later; if the same code remains, report the Diagnostics run ID and failure-code counts."
	}
	return "Gmail was reached, but one or more messages were skipped after a bounded import failure.",
		"Other messages were still processed. Inspect the Diagnostics failure-code counts before rescanning or changing source rules."
}

// Completed accepts only native worker summaries, never mailbox text or user
// supplied counts. A partial run is terminal with warnings, not a failed page.
func (r *Run) Completed(trigger string, result emailworker.Result) (Event, error) {
	if r == nil {
		return Event{}, ErrState
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seq != 1 || r.operation != "sync" {
		return Event{}, ErrState
	}
	if (trigger != "manual" && trigger != "scheduled") || result.ContractVersion != emailworker.ContractVersion ||
		(result.Status != "succeeded" && result.Status != "partial" && result.Status != "disabled" && result.Status != "deferred") ||
		(result.Status == "partial" && result.ErrorCode != "EMAIL_SYNC_MESSAGE_RETRY" && result.ErrorCode != "EMAIL_SYNC_MESSAGE_SKIPPED") {
		return Event{}, ErrResult
	}
	failures := map[string]int{}
	for code, count := range result.FailureCodeCounts {
		if !codePattern.MatchString(code) || count < 1 || count > emailworker.MaxMessagesPerSync || len(failures) >= 64 {
			return Event{}, ErrResult
		}
		failures[code] = count
	}
	for _, count := range diagnosticCounts(result.Counts) {
		if count < 0 || count > emailworker.MaxMessagesPerSync {
			return Event{}, ErrResult
		}
	}
	details := map[string]any{
		"operation": "sync", "trigger": trigger, "result_status": result.Status,
		"counts": diagnosticCounts(result.Counts), "failure_code_counts": failures,
		"has_more": result.HasMore, "total_duration_ms": r.durationMS(),
	}
	status := "success"
	if result.Status == "partial" {
		status = "success_with_warnings"
		details["error_code"] = result.ErrorCode
		details["summary"], details["suggestion"] = partialGuidance(result.ErrorCode)
	}
	raw, err := r.encode(2, "completed", status, details)
	if err == nil {
		r.seq = 2
	}
	return raw, err
}

// Failed records only a fixed code and stage. Raw exception text and message
// content have no path into this record.
func (r *Run) Failed(code, stage string) (Event, error) {
	if r == nil {
		return Event{}, ErrState
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seq != 1 || r.operation != "sync" || !codePattern.MatchString(code) {
		return Event{}, ErrState
	}
	switch stage {
	case "request", "mailbox", "local_persistence", "readback", "connection":
	default:
		return Event{}, ErrState
	}
	public, known := failureGuidance[code]
	if !known {
		// The adapter supplies a fixed, public code. Keep it for correlation
		// with the HTTP response, but never derive prose from private failures.
		public = failureGuidance["EMAIL_SYNC_FAILED"]
	}
	raw, err := r.encode(2, "failed", "error", map[string]any{
		"operation": "sync", "error_code": code,
		"failed_stage": stage, "total_duration_ms": r.durationMS(),
		"summary": public.summary, "suggestion": public.suggestion,
		"severity": public.severity, "retryable": public.retryable,
	})
	if err == nil {
		r.seq = 2
	}
	return raw, err
}

// ConnectionCompleted records only the confirmed revision and whether the
// request supplied a credential. The secret itself has no logging path.
func (r *Run) ConnectionCompleted(revision int64, credentialSupplied bool) (Event, error) {
	if r == nil {
		return Event{}, ErrState
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.operation != "save" || r.seq != 1 || revision < 1 || revision > emailcredentials.MaxRevision {
		return Event{}, ErrState
	}
	raw, err := r.encode(2, "completed", "success", map[string]any{
		"operation": "save", "revision": revision,
		"credential_supplied": credentialSupplied, "total_duration_ms": r.durationMS(),
	})
	if err == nil {
		r.seq = 2
	}
	return raw, err
}

// ConnectionFailed stores fixed failure facts, including whether the native
// owner attempted or confirmed a commit. It never claims rollback from a
// timed-out request or post-commit readback error.
func (r *Run) ConnectionFailed(code, stage string, commitAttempted, committed, recoveryRequired bool) (Event, error) {
	if r == nil {
		return Event{}, ErrState
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.operation != "save" || r.seq != 1 || !connectionCodeAllowed(code) || committed && !commitAttempted {
		return Event{}, ErrState
	}
	switch stage {
	case "request", "credential", "local_persistence", "readback", "connection":
	default:
		return Event{}, ErrState
	}
	raw, err := r.encode(2, "failed", "error", map[string]any{
		"operation": "save", "error_code": code, "failed_stage": stage,
		"commit_attempted": commitAttempted, "committed": committed,
		"recovery_required": recoveryRequired, "total_duration_ms": r.durationMS(),
		"summary":    "The email connection change could not be verified safely.",
		"suggestion": "Reload the connection and inspect its run ID before retrying.",
		"severity":   "error", "retryable": false,
	})
	if err == nil {
		r.seq = 2
	}
	return raw, err
}
