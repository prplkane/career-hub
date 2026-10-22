// Package applications owns application-ledger projections and transaction-local
// mutations. It does not select a Data Home, open database paths, migrate schemas,
// commit caller transactions, or submit applications.
package applications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	SchemaVersion   = 27
	BatchSize       = 900
	MaxRequestedIDs = 10000
	MaxIDBytes      = 4096
	MaxRequestBytes = 1 << 20
	MaxCellBytes    = 1 << 20
	MaxScannedBytes = 8 << 20
	MaxResultBytes  = 8 << 20
)

// Error exposes fixed operation metadata. Its underlying cause is for typed
// driver-code inspection, not logging: SQLite errors may contain private text.
type Error struct {
	Stage, Code string
	cause       error
}

func (e *Error) Error() string                       { return "application summaries could not be read" }
func (e *Error) Unwrap() error                       { return e.cause }
func failure(stage, code string, cause error) *Error { return &Error{stage, code, cause} }

// Value retains JSON-compatible SQLite storage values without database/sql's
// implicit string/bool coercion. Even declared TEXT/INTEGER columns can contain
// other SQLite storage classes. BLOB/nonfinite/invalid-UTF8 values cannot cross
// this JSON read-model boundary and receive an explicit unsupported error.
type Value struct{ value any }

func (v Value) MarshalJSON() ([]byte, error) {
	encoded, err := json.Marshal(v.value)
	// SQLite REAL values remain floats in the Python source. Go normally encodes
	// integral-looking floats below 1e21 without a decimal point, which changes
	// their type when decoded by Python. Keep the type, not Python's spelling.
	if _, real := v.value.(float64); err == nil && real && !strings.ContainsAny(string(encoded), ".eE") {
		encoded = append(encoded, '.', '0')
	}
	return encoded, err
}

type ResumeIdentity struct {
	ID          Value `json:"id"`
	DisplayName Value `json:"display_name"`
}
type Attachment struct {
	ResumeID     Value `json:"resume_id"`
	RelativePath Value `json:"relative_path"`
	FileName     Value `json:"file_name"`
	Label        Value `json:"label"`
	SHA256       Value `json:"sha256"`
	SizeBytes    Value `json:"size_bytes"`
	AttachedAt   Value `json:"attached_at"`
}
type Selection struct {
	State  string          `json:"state"`
	Resume *ResumeIdentity `json:"resume"`
}
type AppliedResume struct {
	Recorded bool            `json:"recorded"`
	Resume   *ResumeIdentity `json:"resume"`
}
type Plan struct {
	Status    Value `json:"status"`
	Revision  Value `json:"revision"`
	CreatedAt Value `json:"created_at"`
	UpdatedAt Value `json:"updated_at"`
}
type Summary struct {
	JobID              Value         `json:"job_id"`
	Company            Value         `json:"company"`
	Title              Value         `json:"title"`
	SourceURL          Value         `json:"source_url"`
	ContentFingerprint Value         `json:"content_fingerprint"`
	Decision           Value         `json:"decision"`
	CurrentStatus      Value         `json:"current_status"`
	Notes              Value         `json:"notes"`
	CreatedAt          Value         `json:"created_at"`
	UpdatedAt          Value         `json:"updated_at"`
	ResumeAttachment   *Attachment   `json:"resume_attachment"`
	ResumeSelection    Selection     `json:"resume_selection"`
	AppliedResume      AppliedResume `json:"applied_resume"`
	ResumePlan         *Plan         `json:"resume_plan"`
}

type budget struct {
	remaining int
	cellLimit int // Zero preserves the original summary-pilot limit.
}

func value(raw any) (Value, error) {
	switch v := raw.(type) {
	case nil, int64:
	case float64:
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return Value{}, errors.New("unsupported value")
		}
	case string:
		if !utf8.ValidString(v) {
			return Value{}, errors.New("unsupported value")
		}
	default:
		return Value{}, errors.New("unsupported value")
	}
	return Value{raw}, nil
}

// Charge every selected cell, including ignored plan JSON and hidden resume
// IDs. Python's text_factory also rejects invalid UTF-8 before field selection.
// This bounds consumed values, not allocations inside the SQLite driver.
func (b *budget) row(raw []any) error {
	cellLimit := b.cellLimit
	if cellLimit == 0 {
		cellLimit = MaxCellBytes
	}
	for _, cell := range raw {
		size := 8
		switch v := cell.(type) {
		case string:
			if !utf8.ValidString(v) {
				return errors.New("unsupported text")
			}
			size = len(v)
		case []byte:
			size = len(v)
		case nil, int64, float64:
		default:
			return errors.New("unsupported storage class")
		}
		b.remaining -= size
		if size > cellLimit || b.remaining < 0 {
			return errBudget
		}
	}
	return nil
}

var errBudget = errors.New("result budget exceeded")

func truth(raw any) (bool, error) {
	switch v := raw.(type) {
	case nil:
		return false, nil
	case int64:
		return v != 0, nil
	case float64:
		return v != 0, nil
	case string:
		return v != "", nil
	case []byte:
		return len(v) != 0, nil
	default:
		return false, errors.New("unsupported flag")
	}
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type queryState struct {
	conn   queryer
	ctx    context.Context
	budget budget
}

func (q *queryState) rows(stage, query string, args []any, columns int, consume func([]any) error) *Error {
	rows, err := q.conn.QueryContext(q.ctx, query, args...)
	if err != nil {
		return failure(stage, "DATABASE_QUERY_FAILED", err)
	}
	defer rows.Close()
	for rows.Next() {
		values := make([]any, columns)
		dest := make([]any, columns)
		for i := range values {
			dest[i] = &values[i]
		}
		if err = rows.Scan(dest...); err != nil {
			return failure(stage, "DATABASE_SCAN_FAILED", err)
		}
		if err = q.budget.row(values); err != nil {
			if errors.Is(err, errBudget) {
				return failure(stage, "RESULT_LIMIT", nil)
			}
			return failure(stage, "DATABASE_VALUE_UNSUPPORTED", nil)
		}
		if err = consume(values); err != nil {
			if errors.Is(err, errBudget) {
				return failure(stage, "RESULT_LIMIT", nil)
			}
			return failure(stage, "DATABASE_VALUE_UNSUPPORTED", nil)
		}
	}
	if err = rows.Err(); err != nil {
		return failure(stage, "DATABASE_QUERY_FAILED", err)
	}
	if err = rows.Close(); err != nil {
		return failure(stage, "DATABASE_QUERY_FAILED", err)
	}
	return nil
}
func (q *queryState) cells(raw []any) ([]Value, error) {
	values := make([]Value, len(raw))
	for i, v := range raw {
		cell, err := value(v)
		if err != nil {
			return nil, err
		}
		values[i] = cell
	}
	return values, nil
}
func (q *queryState) batches(ids []string, stage, prefix, suffix string, columns int, consume func([]any) error) *Error {
	for start := 0; start < len(ids); start += BatchSize {
		end := min(start+BatchSize, len(ids))
		args := make([]any, end-start)
		for i, id := range ids[start:end] {
			args[i] = id
		}
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(args)), ", ")
		if err := q.rows(stage, prefix+marks+suffix, args, columns, consume); err != nil {
			return err
		}
	}
	return nil
}

// ValidateSummaryRequest lets the owning service reject requests before any
// filesystem admission. Duplicate IDs consume the same input budget as other
// IDs. Validation never trims, normalizes, deduplicates or changes the input.
func ValidateSummaryRequest(jobIDs []string) *Error {
	if len(jobIDs) > MaxRequestedIDs {
		return failure("request", "INPUT_LIMIT", nil)
	}
	inputBytes := 0
	for _, id := range jobIDs {
		inputBytes += len(id)
		if len(id) > MaxIDBytes || inputBytes > MaxRequestBytes {
			return failure("request", "INPUT_LIMIT", nil)
		}
		if !utf8.ValidString(id) {
			return failure("request", "INPUT_UNSUPPORTED", nil)
		}
	}
	return nil
}

// ReadSummaries uses one caller-owned database connection but does not start a
// transaction: the Python reference likewise has multiple independent SELECTs.
// The host must supply a read-only, correctly configured DB and own path/ACL and
// lifecycle policy. This function cannot secure a driver's pathname opens.
// Empty IDs return before touching db, including when db is nil.
func ReadSummaries(ctx context.Context, db *sql.DB, jobIDs []string) (map[string]Summary, *Error) {
	result := map[string]Summary{}
	if err := ValidateSummaryRequest(jobIDs); err != nil {
		return nil, err
	}
	ids := uniqueSummaryIDs(jobIDs)
	if len(ids) == 0 {
		return result, nil
	}
	if db == nil || ctx == nil {
		return nil, failure("open", "DATABASE_CONNECTION_INVALID", nil)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, failure("open", "DATABASE_OPEN_FAILED", err)
	}
	defer conn.Close()
	q := queryState{conn, ctx, budget{remaining: MaxScannedBytes}}
	var version int64
	if err = conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return nil, failure("schema", "DATABASE_QUERY_FAILED", err)
	}
	if version < SchemaVersion {
		return result, nil
	}
	if version > SchemaVersion {
		return nil, failure("schema", "SCHEMA_TOO_NEW", nil)
	}
	return summaryRecords(&q, ids)
}

// SummariesInTransaction uses the same bounded projection as ReadSummaries,
// inside a caller-owned, admitted current-schema transaction. It neither opens
// another connection nor commits, rolls back, registers a job or checks schema.
// The owner must keep the transaction alive through the other packet reads and
// synchronously clean it up before publishing. An empty request touches no SQL.
func SummariesInTransaction(ctx context.Context, tx *sql.Tx, jobIDs []string) (map[string]Summary, *Error) {
	if err := ValidateSummaryRequest(jobIDs); err != nil {
		return nil, err
	}
	if len(jobIDs) == 0 {
		return map[string]Summary{}, nil
	}
	if ctx == nil || tx == nil {
		return nil, failure("open", "DATABASE_CONNECTION_INVALID", nil)
	}
	if ctx.Err() != nil {
		return nil, failure("read", "READ_CANCELED", nil)
	}
	q := queryState{tx, ctx, budget{remaining: MaxScannedBytes}}
	result, err := summaryRecords(&q, uniqueSummaryIDs(jobIDs))
	if ctx.Err() != nil {
		return nil, failure("read", "READ_CANCELED", nil)
	}
	return result, err
}

func uniqueSummaryIDs(jobIDs []string) []string {
	ids := make([]string, 0, len(jobIDs))
	seen := map[string]bool{}
	for _, id := range jobIDs {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// Registration returns this bounded summary shape, not a detail/history record.
// Keep its actual source projection inside the writer's existing transaction.
func summaryRecords(q *queryState, ids []string) (map[string]Summary, *Error) {
	result := map[string]Summary{}
	type state struct {
		recorded bool
		resumeID any
	}
	states := map[string]state{}
	rowIDs := []string{}
	errRead := q.batches(ids, "applications", "SELECT job_id, company, title, source_url, content_fingerprint, decision, current_status, notes, created_at, updated_at, applied_resume_id, applied_resume_recorded FROM job_applications WHERE job_id IN (", ")", 12, func(raw []any) error {
		id, ok := raw[0].(string)
		if !ok {
			return errors.New("unsupported identifier")
		}
		values, err := q.cells(raw[:10])
		if err != nil {
			return err
		}
		recorded, err := truth(raw[11])
		if err != nil {
			return err
		}
		if _, exists := result[id]; !exists {
			rowIDs = append(rowIDs, id)
		}
		result[id] = Summary{JobID: values[0], Company: values[1], Title: values[2], SourceURL: values[3], ContentFingerprint: values[4], Decision: values[5], CurrentStatus: values[6], Notes: values[7], CreatedAt: values[8], UpdatedAt: values[9], ResumeSelection: Selection{State: "not_recorded"}}
		states[id] = state{recorded, raw[10]}
		return nil
	})
	if errRead != nil {
		return nil, errRead
	}
	// The source reads attachments then plans for EACH batch, before events.
	for start := 0; start < len(rowIDs); start += BatchSize {
		batch := rowIDs[start:min(start+BatchSize, len(rowIDs))]
		if errRead = q.batches(batch, "enrichment", "SELECT job_id, resume_id, relative_path, file_name, label, sha256, size_bytes, attached_at FROM application_resume_attachments WHERE job_id IN (", ")", 8, func(raw []any) error {
			id, ok := raw[0].(string)
			if !ok {
				return errors.New("unsupported identifier")
			}
			values, err := q.cells(raw[1:])
			if err != nil {
				return err
			}
			record := result[id]
			record.ResumeAttachment = &Attachment{values[0], values[1], values[2], values[3], values[4], values[5], values[6]}
			record.ResumeSelection = Selection{"selected", &ResumeIdentity{values[0], values[3]}}
			result[id] = record
			return nil
		}); errRead != nil {
			return nil, errRead
		}
		if errRead = q.batches(batch, "enrichment", "SELECT job_id, plan_json, status, revision, created_at, updated_at FROM application_resume_plans WHERE job_id IN (", ")", 6, func(raw []any) error {
			id, ok := raw[0].(string)
			if !ok {
				return errors.New("unsupported identifier")
			}
			// plan_json is intentionally not parsed, validated, or returned.
			values, err := q.cells(raw[2:])
			if err != nil {
				return err
			}
			record := result[id]
			record.ResumePlan = &Plan{values[0], values[1], values[2], values[3]}
			result[id] = record
			return nil
		}); errRead != nil {
			return nil, errRead
		}
	}
	if errRead = q.batches(rowIDs, "enrichment", "SELECT events.job_id, events.resume_id FROM application_applied_resume_events AS events INNER JOIN (SELECT job_id, MAX(event_id) AS event_id FROM application_applied_resume_events WHERE job_id IN (", ") GROUP BY job_id) AS latest ON latest.job_id = events.job_id AND latest.event_id = events.event_id", 2, func(raw []any) error {
		id, ok := raw[0].(string)
		if !ok {
			return errors.New("unsupported identifier")
		}
		states[id] = state{true, raw[1]}
		return nil
	}); errRead != nil {
		return nil, errRead
	}
	resumeSet := map[string]bool{}
	for _, value := range states {
		if id, ok := value.resumeID.(string); value.recorded && ok && id != "" {
			resumeSet[id] = true
		}
	}
	resumeIDs := make([]string, 0, len(resumeSet))
	for id := range resumeSet {
		resumeIDs = append(resumeIDs, id)
	}
	sort.Strings(resumeIDs)
	resumes := map[string]*ResumeIdentity{}
	if errRead = q.batches(resumeIDs, "enrichment", "SELECT resume_id, display_name FROM resume_library WHERE resume_id IN (", ")", 2, func(raw []any) error {
		id, ok := raw[0].(string)
		if !ok {
			return errors.New("unsupported identifier")
		}
		values, err := q.cells(raw)
		if err != nil {
			return err
		}
		resumes[id] = &ResumeIdentity{values[0], values[1]}
		return nil
	}); errRead != nil {
		return nil, errRead
	}
	for id, record := range result {
		state := states[id]
		record.AppliedResume.Recorded = state.recorded
		// Preserve the source's shared-prefetch behavior even for a legacy false
		// recorded flag whose stale ID is loaded for another requested record.
		if resumeID, ok := state.resumeID.(string); ok && resumeID != "" {
			if resume := resumes[resumeID]; resume != nil {
				owned := *resume
				record.AppliedResume.Resume = &owned
			}
		}
		result[id] = record
	}
	if !fitsSummaries(result, MaxResultBytes) {
		return nil, failure("enrichment", "RESULT_LIMIT", nil)
	}
	return result, nil
}

// Count exact JSON sizes one bounded record at a time. Repeated identities and
// escaping are charged for every output occurrence, before aggregate encoding.
// A record/scalar is encoded temporarily; this is not a peak-heap guarantee.
func fitsSummaries(records map[string]Summary, limit int) bool {
	size := 2
	for id, record := range records {
		key, err := json.Marshal(id)
		if err != nil {
			return false
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return false
		}
		size += len(key) + 1 + len(encoded) + 1
		if size-1 > limit {
			return false
		}
	}
	if len(records) > 0 {
		size--
	}
	return size <= limit
}
