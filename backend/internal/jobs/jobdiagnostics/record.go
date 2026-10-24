// Package jobdiagnostics defines content-minimized saved-JD reanalysis and
// new-job intake events. This pure codec neither opens a Data Home nor
// publishes a journal or route.
package jobdiagnostics

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sync"
	"time"

	"careerhub.local/backend/internal/jobs/jobcard"
)

const MaxEventBytes = 4096

var (
	runIDPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{6}Z_[0-9a-f]{8}$`)
	ErrState     = errors.New("JOB_DIAGNOSTIC_STATE_INVALID")
	ErrEvent     = errors.New("JOB_DIAGNOSTIC_EVENT_INVALID")
)

type Event struct{ raw []byte }

func (e Event) Bytes() []byte { return bytes.Clone(e.raw) }

type details struct {
	Operation       string `json:"operation"`
	JobKeySHA256    string `json:"job_key_sha256,omitempty"`
	PriorCardSHA256 string `json:"prior_card_sha256,omitempty"`
	NextCardSHA256  string `json:"next_card_sha256,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
	Publication     string `json:"publication_state,omitempty"`
	CommitAttempted *bool  `json:"commit_attempted,omitempty"`
	Committed       *bool  `json:"committed,omitempty"`
	DurationMS      *int64 `json:"total_duration_ms,omitempty"`

	SourceKeySHA256 string `json:"source_key_sha256,omitempty"`
	InitialStatus   string `json:"initial_status,omitempty"`
	DurablePhase    string `json:"durable_phase,omitempty"`
	Duplicate       *bool  `json:"duplicate,omitempty"`
	RecoveryNeeded  *bool  `json:"recovery_required,omitempty"`
}

type record struct {
	SchemaVersion int     `json:"schema_version"`
	Timestamp     string  `json:"timestamp"`
	RunID         string  `json:"run_id"`
	Sequence      int     `json:"sequence"`
	Component     string  `json:"component"`
	Source        string  `json:"source"`
	Stage         string  `json:"stage"`
	Event         string  `json:"event"`
	Status        string  `json:"status"`
	Details       details `json:"details"`
}

// Decoded exposes only fixed metadata and hashes. It contains no JD, job ID,
// URL, profile value or user-authored text.
type Decoded struct {
	RunID                               string
	Sequence                            int
	Operation, Event, Status, ErrorCode string
	PublicationState                    PublicationState
	JobKeySHA256                        [32]byte
	SourceKeySHA256                     [32]byte
	PriorCardSHA256, NextCardSHA256     [32]byte
	HasPlan, CommitAttempted, Committed bool
	InitialStatus, DurablePhase         string
	Duplicate, RecoveryRequired         bool
}

type Run struct {
	mu      sync.Mutex
	id      string
	jobKey  [32]byte
	started time.Time
	now     func() time.Time
	phase   uint8 // 0 new, 1 started, 2 planned, 3 terminal
}

// IntakeRun records only a keyed source fingerprint and fixed phase facts. It
// never stores the source fingerprint itself, JD text, URL, title or job ID.
type IntakeRun struct {
	mu            sync.Mutex
	id            string
	sourceKey     [32]byte
	initialStatus string
	started       time.Time
	now           func() time.Time
	phase         uint8 // 0 new, 1 started, 2 dispatched, 3 terminal
}

func ValidRunID(value string) bool { return runIDPattern.MatchString(value) }

// New always mints a random local run ID and stores only a digest of the
// validated job selector. That digest permits bounded crash lookup, but is
// not a secrecy guarantee for guessable job IDs.
func New(jobID string) (*Run, error) { return newWithClock("", jobID, time.Now, rand.Read) }

// NewIntake requires the normalized source fingerprint produced by the intake
// analyzer and an explicit normalized initial status.
func NewIntake(sourceFingerprint, initialStatus string) (*IntakeRun, error) {
	return newIntakeWithClock("", sourceFingerprint, initialStatus, time.Now, rand.Read)
}

func newWithClock(requested, jobID string, now func() time.Time, read func([]byte) (int, error)) (*Run, error) {
	if now == nil || read == nil || !jobcard.ValidID(jobID) {
		return nil, ErrState
	}
	started := now().UTC()
	id := requested
	if !ValidRunID(id) {
		var suffix [4]byte
		if n, err := read(suffix[:]); err != nil || n != len(suffix) {
			return nil, ErrState
		}
		id = started.Format("20060102T150405.000000Z") + "_" + hex.EncodeToString(suffix[:])
	}
	return &Run{id: id, jobKey: sha256.Sum256([]byte(jobID)), started: started, now: now}, nil
}

func newIntakeWithClock(requested, sourceFingerprint, initialStatus string, now func() time.Time, read func([]byte) (int, error)) (*IntakeRun, error) {
	fingerprint, valid := decodeHash(sourceFingerprint)
	if now == nil || read == nil || !valid || fingerprint == ([32]byte{}) ||
		(initialStatus != "captured" && initialStatus != "applied") {
		return nil, ErrState
	}
	started := now().UTC()
	id := requested
	if !ValidRunID(id) {
		var suffix [4]byte
		if n, err := read(suffix[:]); err != nil || n != len(suffix) {
			return nil, ErrState
		}
		id = started.Format("20060102T150405.000000Z") + "_" + hex.EncodeToString(suffix[:])
	}
	return &IntakeRun{
		id: id, sourceKey: sha256.Sum256([]byte(sourceFingerprint)), initialStatus: initialStatus,
		started: started, now: now,
	}, nil
}

func (r *Run) ID() string {
	if r == nil {
		return ""
	}
	return r.id
}

func (r *IntakeRun) ID() string {
	if r == nil {
		return ""
	}
	return r.id
}

func (r *Run) encode(sequence int, event, status string, value details) (Event, error) {
	value.JobKeySHA256 = hex.EncodeToString(r.jobKey[:])
	stamp := r.now().UTC().Format("2006-01-02T15:04:05.000000+00:00")
	raw, err := json.Marshal(record{1, stamp, r.id, sequence, "career_vault_intake", "saved_job", "job_reanalysis_run", event, status, value})
	if err != nil || len(raw)+1 > MaxEventBytes {
		return Event{}, ErrEvent
	}
	return Event{raw: append(raw, '\n')}, nil
}

func (r *Run) durationMS() int64 {
	value := r.now().UTC().Sub(r.started).Milliseconds()
	if value < 0 {
		return 0
	}
	return value
}

func (r *IntakeRun) encode(sequence int, event, status string, value details) (Event, error) {
	value.SourceKeySHA256 = hex.EncodeToString(r.sourceKey[:])
	stamp := r.now().UTC().Format("2006-01-02T15:04:05.000000+00:00")
	raw, err := json.Marshal(record{1, stamp, r.id, sequence, "career_vault_intake", "job_intake", "new_job_intake_run", event, status, value})
	if err != nil || len(raw)+1 > MaxEventBytes {
		return Event{}, ErrEvent
	}
	return Event{raw: append(raw, '\n')}, nil
}

func (r *IntakeRun) durationMS() int64 {
	value := r.now().UTC().Sub(r.started).Milliseconds()
	if value < 0 {
		return 0
	}
	return value
}

func (r *IntakeRun) Started() (Event, error) {
	if r == nil {
		return Event{}, ErrState
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != 0 {
		return Event{}, ErrState
	}
	event, err := r.encode(1, "started", "running", details{Operation: "intake", InitialStatus: r.initialStatus})
	if err == nil {
		r.phase = 1
	}
	return event, err
}

// Dispatched is appended immediately before the first owner call. A crash
// after started but before this event proves that no owner effect was invoked;
// a dispatched tail is intentionally ambiguous until recovery observes state.
func (r *IntakeRun) Dispatched() (Event, error) {
	if r == nil {
		return Event{}, ErrState
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != 1 {
		return Event{}, ErrState
	}
	event, err := r.encode(2, "dispatched", "running", details{Operation: "intake"})
	if err == nil {
		r.phase = 2
	}
	return event, err
}

// Completed closes either a fully settled fresh intake or a verified duplicate
// branch. Those are the only success phases that require no recovery.
func (r *IntakeRun) Completed(durablePhase string, duplicate bool) (Event, error) {
	if r == nil || (durablePhase != "terminal" && durablePhase != "duplicate") || duplicate != (durablePhase == "duplicate") {
		return Event{}, ErrState
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != 2 {
		return Event{}, ErrState
	}
	falseValue, duration := false, r.durationMS()
	event, err := r.encode(3, "completed", "success", details{
		Operation: "intake", DurablePhase: durablePhase, Duplicate: &duplicate,
		RecoveryNeeded: &falseValue, DurationMS: &duration,
	})
	if err == nil {
		r.phase = 3
	}
	return event, err
}

// Failed accepts only fixed public categories and a fixed durable phase. The
// caller decides whether the observed durable state requires explicit recovery;
// arbitrary owner errors never enter the journal.
func (r *IntakeRun) Failed(code, durablePhase string, recoveryRequired bool) (Event, error) {
	if r == nil || !validIntakeFailureCode(code) || code == "JOB_INTAKE_INTERRUPTED" || !validIntakeDurablePhase(durablePhase) {
		return Event{}, ErrState
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != 2 {
		return Event{}, ErrState
	}
	duration := r.durationMS()
	event, err := r.encode(3, "failed", "error", details{
		Operation: "intake", ErrorCode: code, DurablePhase: durablePhase,
		RecoveryNeeded: &recoveryRequired, DurationMS: &duration,
	})
	if err == nil {
		r.phase = 3
	}
	return event, err
}

func (r *Run) Started() (Event, error) {
	if r == nil {
		return Event{}, ErrState
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != 0 {
		return Event{}, ErrState
	}
	event, err := r.encode(1, "started", "running", details{Operation: "reanalyze"})
	if err == nil {
		r.phase = 1
	}
	return event, err
}

// Planned must be appended durably before the guarded card replacement. Equal
// hashes cannot prove a publication after a crash and are refused as a plan.
func (r *Run) Planned(prior, next [32]byte) (Event, error) {
	if r == nil || prior == next {
		return Event{}, ErrState
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != 1 {
		return Event{}, ErrState
	}
	event, err := r.encode(2, "planned", "running", details{Operation: "reanalyze", PriorCardSHA256: hex.EncodeToString(prior[:]), NextCardSHA256: hex.EncodeToString(next[:])})
	if err == nil {
		r.phase = 2
	}
	return event, err
}

func (r *Run) Completed() (Event, error) {
	if r == nil {
		return Event{}, ErrState
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != 2 {
		return Event{}, ErrState
	}
	trueValue, duration := true, r.durationMS()
	event, err := r.encode(3, "completed", "success", details{Operation: "reanalyze", CommitAttempted: &trueValue, Committed: &trueValue, DurationMS: &duration})
	if err == nil {
		r.phase = 3
	}
	return event, err
}

// Unchanged terminates a fully checked no-op without a planned publication.
// The writer must have verified the displayed revision and all digest inputs
// under its own gate before the caller appends this event.
func (r *Run) Unchanged() (Event, error) {
	if r == nil {
		return Event{}, ErrState
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != 1 {
		return Event{}, ErrState
	}
	falseValue, duration := false, r.durationMS()
	event, err := r.encode(2, "completed", "success", details{Operation: "reanalyze", CommitAttempted: &falseValue, Committed: &falseValue, DurationMS: &duration})
	if err == nil {
		r.phase = 3
	}
	return event, err
}

// Failed accepts only fixed public categories; arbitrary exception text cannot
// enter the journal. Before a plan, no card write can have been attempted.
func (r *Run) Failed(code string, attempted, committed bool) (Event, error) {
	if r == nil || !validFailureCode(code) || code == "JOB_REANALYSIS_INTERRUPTED" || committed && !attempted {
		return Event{}, ErrState
	}
	if attempted && !committed && code != "JOB_REANALYSIS_WRITE_UNCERTAIN" || code == "JOB_REANALYSIS_WRITE_UNCERTAIN" && (!attempted || committed) {
		return Event{}, ErrState
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != 1 && r.phase != 2 || r.phase == 1 && (attempted || committed) {
		return Event{}, ErrState
	}
	sequence, duration := 2, r.durationMS()
	if r.phase == 2 {
		sequence = 3
	}
	value := details{Operation: "reanalyze", ErrorCode: code, CommitAttempted: &attempted, Committed: &committed, DurationMS: &duration}
	if attempted && !committed {
		value.Publication = string(PublicationUnknown)
	}
	event, err := r.encode(sequence, "failed", "error", value)
	if err == nil {
		r.phase = 3
	}
	return event, err
}

func validFailureCode(code string) bool {
	switch code {
	case "JOB_REANALYSIS_REQUEST_INVALID", "JOB_REANALYSIS_PREPARATION_FAILED",
		"JOB_REANALYSIS_REVISION_CONFLICT", "JOB_REANALYSIS_INPUT_CHANGED",
		"JOB_REANALYSIS_NOT_DRAFT", "JOB_REANALYSIS_SOURCE_INVALID",
		"JOB_REANALYSIS_HOME_UNAVAILABLE", "JOB_REANALYSIS_WRITE_UNCERTAIN",
		"JOB_REANALYSIS_COMMITTED_UNVERIFIED", "JOB_REANALYSIS_DIAGNOSTIC_FAILED",
		"JOB_REANALYSIS_RESULT_INVALID", "JOB_REANALYSIS_CANCELED",
		"JOB_REANALYSIS_INTERRUPTED":
		return true
	}
	return false
}

func validIntakeFailureCode(code string) bool {
	switch code {
	case "JOB_INTAKE_REQUEST_FAILED", "JOB_INTAKE_INSPECTION_FAILED",
		"JOB_INTAKE_FILES_FAILED", "JOB_INTAKE_LEDGER_FAILED",
		"JOB_INTAKE_ASSESSMENT_FAILED", "JOB_INTAKE_APPLIED_FAILED",
		"JOB_INTAKE_TERMINAL_FAILED", "JOB_INTAKE_RECOVERY_REQUIRED",
		"JOB_INTAKE_DIAGNOSTIC_FAILED", "JOB_INTAKE_INTERRUPTED":
		return true
	}
	return false
}

func validIntakeDurablePhase(value string) bool {
	switch value {
	case "none", "namespace", "files", "captured", "assessed", "applied", "terminal", "duplicate":
		return true
	}
	return false
}

func decodeHash(value string) ([32]byte, bool) {
	var result [32]byte
	if len(value) != 64 {
		return result, false
	}
	raw, err := hex.DecodeString(value)
	if err != nil || hex.EncodeToString(raw) != value {
		return result, false
	}
	copy(result[:], raw)
	return result, true
}

// Decode refuses noncanonical, oversized and private-field-bearing records.
// A future store may admit only events that pass this boundary.
func Decode(raw []byte) (Decoded, error) {
	if len(raw) == 0 || len(raw) > MaxEventBytes || raw[len(raw)-1] != '\n' || bytes.Count(raw, []byte{'\n'}) != 1 {
		return Decoded{}, ErrEvent
	}
	var value record
	if json.Unmarshal(raw[:len(raw)-1], &value) != nil {
		return Decoded{}, ErrEvent
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, raw[:len(raw)-1]) || value.SchemaVersion != 1 ||
		!ValidRunID(value.RunID) || value.Component != "career_vault_intake" {
		return Decoded{}, ErrEvent
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000000+00:00", value.Timestamp); err != nil {
		return Decoded{}, ErrEvent
	}
	d := value.Details
	decoded := Decoded{RunID: value.RunID, Sequence: value.Sequence, Operation: d.Operation, Event: value.Event, Status: value.Status, ErrorCode: d.ErrorCode}
	if d.Operation == "intake" {
		return decodeIntake(value, decoded)
	}
	if d.Operation != "reanalyze" || value.Source != "saved_job" || value.Stage != "job_reanalysis_run" ||
		d.SourceKeySHA256 != "" || d.InitialStatus != "" || d.DurablePhase != "" || d.Duplicate != nil || d.RecoveryNeeded != nil {
		return Decoded{}, ErrEvent
	}
	jobKey, keyValid := decodeHash(d.JobKeySHA256)
	if !keyValid || jobKey == ([32]byte{}) {
		return Decoded{}, ErrEvent
	}
	decoded.JobKeySHA256 = jobKey
	switch {
	case value.Sequence == 1 && value.Event == "started" && value.Status == "running":
		if d.PriorCardSHA256 != "" || d.NextCardSHA256 != "" || d.ErrorCode != "" || d.Publication != "" || d.CommitAttempted != nil || d.Committed != nil || d.DurationMS != nil {
			return Decoded{}, ErrEvent
		}
	case value.Sequence == 2 && value.Event == "planned" && value.Status == "running":
		prior, okPrior := decodeHash(d.PriorCardSHA256)
		next, okNext := decodeHash(d.NextCardSHA256)
		if !okPrior || !okNext || prior == next || d.ErrorCode != "" || d.Publication != "" || d.CommitAttempted != nil || d.Committed != nil || d.DurationMS != nil {
			return Decoded{}, ErrEvent
		}
		decoded.PriorCardSHA256, decoded.NextCardSHA256, decoded.HasPlan = prior, next, true
	case (value.Sequence == 2 || value.Sequence == 3) && value.Event == "failed" && value.Status == "error" && d.ErrorCode == "JOB_REANALYSIS_INTERRUPTED":
		state := PublicationState(d.Publication)
		if d.PriorCardSHA256 != "" || d.NextCardSHA256 != "" || d.CommitAttempted != nil || d.Committed != nil || d.DurationMS != nil ||
			(state != PublicationPublished && state != PublicationNotPublished && state != PublicationUnknown) ||
			value.Sequence == 2 && state != PublicationNotPublished {
			return Decoded{}, ErrEvent
		}
		decoded.PublicationState = state
	case (value.Sequence == 2 || value.Sequence == 3) && value.Event == "failed" && value.Status == "error":
		if d.PriorCardSHA256 != "" || d.NextCardSHA256 != "" || !validFailureCode(d.ErrorCode) || d.CommitAttempted == nil || d.Committed == nil || d.DurationMS == nil || *d.DurationMS < 0 || *d.Committed && !*d.CommitAttempted || value.Sequence == 2 && (*d.CommitAttempted || *d.Committed) {
			return Decoded{}, ErrEvent
		}
		if *d.CommitAttempted && !*d.Committed {
			if d.ErrorCode != "JOB_REANALYSIS_WRITE_UNCERTAIN" || d.Publication != string(PublicationUnknown) {
				return Decoded{}, ErrEvent
			}
			decoded.PublicationState = PublicationUnknown
		} else if d.Publication != "" || d.ErrorCode == "JOB_REANALYSIS_WRITE_UNCERTAIN" {
			return Decoded{}, ErrEvent
		}
		decoded.CommitAttempted, decoded.Committed = *d.CommitAttempted, *d.Committed
	case value.Sequence == 3 && value.Event == "completed" && value.Status == "success":
		if d.PriorCardSHA256 != "" || d.NextCardSHA256 != "" || d.ErrorCode != "" || d.Publication != "" || d.CommitAttempted == nil || d.Committed == nil || !*d.CommitAttempted || !*d.Committed || d.DurationMS == nil || *d.DurationMS < 0 {
			return Decoded{}, ErrEvent
		}
		decoded.CommitAttempted, decoded.Committed = true, true
	case value.Sequence == 2 && value.Event == "completed" && value.Status == "success":
		if d.PriorCardSHA256 != "" || d.NextCardSHA256 != "" || d.ErrorCode != "" || d.Publication != "" || d.CommitAttempted == nil || d.Committed == nil || *d.CommitAttempted || *d.Committed || d.DurationMS == nil || *d.DurationMS < 0 {
			return Decoded{}, ErrEvent
		}
	default:
		return Decoded{}, ErrEvent
	}
	return decoded, nil
}

func decodeIntake(value record, decoded Decoded) (Decoded, error) {
	d := value.Details
	if value.Source != "job_intake" || value.Stage != "new_job_intake_run" || d.JobKeySHA256 != "" ||
		d.PriorCardSHA256 != "" || d.NextCardSHA256 != "" || d.Publication != "" ||
		d.CommitAttempted != nil || d.Committed != nil {
		return Decoded{}, ErrEvent
	}
	sourceKey, valid := decodeHash(d.SourceKeySHA256)
	if !valid || sourceKey == ([32]byte{}) {
		return Decoded{}, ErrEvent
	}
	decoded.SourceKeySHA256 = sourceKey
	switch {
	case value.Sequence == 1 && value.Event == "started" && value.Status == "running":
		if (d.InitialStatus != "captured" && d.InitialStatus != "applied") || d.ErrorCode != "" ||
			d.DurablePhase != "" || d.Duplicate != nil || d.RecoveryNeeded != nil || d.DurationMS != nil {
			return Decoded{}, ErrEvent
		}
		decoded.InitialStatus = d.InitialStatus
	case value.Sequence == 2 && value.Event == "dispatched" && value.Status == "running":
		if d.InitialStatus != "" || d.ErrorCode != "" || d.DurablePhase != "" ||
			d.Duplicate != nil || d.RecoveryNeeded != nil || d.DurationMS != nil {
			return Decoded{}, ErrEvent
		}
	case value.Sequence == 3 && value.Event == "completed" && value.Status == "success":
		if d.InitialStatus != "" || d.ErrorCode != "" || d.Duplicate == nil || d.RecoveryNeeded == nil ||
			*d.RecoveryNeeded || d.DurationMS == nil || *d.DurationMS < 0 ||
			(d.DurablePhase != "terminal" && d.DurablePhase != "duplicate") || *d.Duplicate != (d.DurablePhase == "duplicate") {
			return Decoded{}, ErrEvent
		}
		decoded.DurablePhase, decoded.Duplicate = d.DurablePhase, *d.Duplicate
	case (value.Sequence == 2 || value.Sequence == 3) && value.Event == "failed" && value.Status == "error" && d.ErrorCode == "JOB_INTAKE_INTERRUPTED":
		if d.InitialStatus != "" || !validIntakeDurablePhase(d.DurablePhase) || d.Duplicate != nil ||
			d.RecoveryNeeded == nil || d.DurationMS != nil ||
			(value.Sequence == 2 && (d.DurablePhase != "none" || *d.RecoveryNeeded)) ||
			(value.Sequence == 3 && !*d.RecoveryNeeded) {
			return Decoded{}, ErrEvent
		}
		decoded.DurablePhase, decoded.RecoveryRequired = d.DurablePhase, *d.RecoveryNeeded
	case value.Sequence == 3 && value.Event == "failed" && value.Status == "error":
		if d.InitialStatus != "" || !validIntakeFailureCode(d.ErrorCode) || !validIntakeDurablePhase(d.DurablePhase) ||
			d.Duplicate != nil || d.RecoveryNeeded == nil || d.DurationMS == nil || *d.DurationMS < 0 {
			return Decoded{}, ErrEvent
		}
		decoded.DurablePhase, decoded.RecoveryRequired = d.DurablePhase, *d.RecoveryNeeded
	default:
		return Decoded{}, ErrEvent
	}
	return decoded, nil
}

type PublicationState string

const (
	PublicationUnknown      PublicationState = "unknown"
	PublicationNotPublished PublicationState = "not_published"
	PublicationPublished    PublicationState = "published"
)

// IntakeInterrupted closes an unfinished intake without replaying it. A
// started-only tail proves the owner was never dispatched. Once dispatched,
// the diagnostic must remain recovery-required regardless of later absence.
func IntakeInterrupted(last Decoded, durablePhase string, recoveryRequired bool) (Event, error) {
	started := last.Event == "started" && last.Sequence == 1
	dispatched := last.Event == "dispatched" && last.Sequence == 2
	if last.Operation != "intake" || !ValidRunID(last.RunID) || last.SourceKeySHA256 == ([32]byte{}) ||
		(!started && !dispatched) || !validIntakeDurablePhase(durablePhase) ||
		(started && (durablePhase != "none" || recoveryRequired)) || (dispatched && !recoveryRequired) {
		return Event{}, ErrState
	}
	sequence := 2
	if dispatched {
		sequence = 3
	}
	run := &IntakeRun{id: last.RunID, sourceKey: last.SourceKeySHA256, now: time.Now}
	return run.encode(sequence, "failed", "error", details{
		Operation: "intake", ErrorCode: "JOB_INTAKE_INTERRUPTED", DurablePhase: durablePhase,
		RecoveryNeeded: &recoveryRequired,
	})
}

// Interrupted closes one unfinished run after an explicit guarded read. No
// attempt fact is invented: a crash can occur on either side of the write call.
// Without a durable plan, the writer could not have started publication.
func Interrupted(last Decoded, state PublicationState) (Event, error) {
	planned := last.Event == "planned" && last.Sequence == 2
	started := last.Event == "started" && last.Sequence == 1
	if last.Operation != "reanalyze" || !ValidRunID(last.RunID) || last.JobKeySHA256 == ([32]byte{}) || (!started && !planned) ||
		(state != PublicationPublished && state != PublicationNotPublished && state != PublicationUnknown) ||
		!planned && state != PublicationNotPublished {
		return Event{}, ErrState
	}
	sequence := 2
	if planned {
		sequence = 3
	}
	run := &Run{id: last.RunID, jobKey: last.JobKeySHA256, now: time.Now}
	return run.encode(sequence, "failed", "error", details{Operation: "reanalyze", ErrorCode: "JOB_REANALYSIS_INTERRUPTED", Publication: string(state)})
}

// ClassifyPublication is an observation, not a replay, rollback or proof of
// physical power-loss durability. The caller must guard its current card read.
func ClassifyPublication(plan Decoded, current []byte, present bool) PublicationState {
	if !plan.HasPlan || !present || plan.PriorCardSHA256 == plan.NextCardSHA256 {
		return PublicationUnknown
	}
	observed := sha256.Sum256(current)
	switch observed {
	case plan.PriorCardSHA256:
		return PublicationNotPublished
	case plan.NextCardSHA256:
		return PublicationPublished
	default:
		return PublicationUnknown
	}
}
