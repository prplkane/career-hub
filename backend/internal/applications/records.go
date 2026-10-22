package applications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

const (
	MaxRecords     = 10000
	MaxHistoryRows = 10000
	// The source writer permits 2 MiB UTF-8 plan_json, including when hidden
	// by a list projection. Do not inherit the smaller CH27 pilot cell ceiling.
	MaxRecordCellBytes = 2 << 20
)

type Filters struct{ Status, Decision *string }

type ResumeEvent struct {
	Action     Value           `json:"action"`
	OccurredAt Value           `json:"occurred_at"`
	RecordedAt Value           `json:"recorded_at"`
	Note       Value           `json:"note"`
	Resume     *ResumeIdentity `json:"resume"`
}
type AppliedResumeHistory struct {
	Recorded bool            `json:"recorded"`
	Resume   *ResumeIdentity `json:"resume"`
	History  []ResumeEvent   `json:"history"`
}

// Record is the full persistence projection, not the narrower Summary shape.
// In particular, unrecorded legacy resume IDs never resolve via another row's
// shared prefetch, and all submitted-resume events remain visible in ID order.
type Record struct {
	JobID              Value                `json:"job_id"`
	Company            Value                `json:"company"`
	Title              Value                `json:"title"`
	SourceURL          Value                `json:"source_url"`
	ContentFingerprint Value                `json:"content_fingerprint"`
	Decision           Value                `json:"decision"`
	CurrentStatus      Value                `json:"current_status"`
	Notes              Value                `json:"notes"`
	CreatedAt          Value                `json:"created_at"`
	UpdatedAt          Value                `json:"updated_at"`
	ResumeAttachment   *Attachment          `json:"resume_attachment"`
	ResumeSelection    Selection            `json:"resume_selection"`
	AppliedResume      AppliedResumeHistory `json:"applied_resume"`
	ResumePlan         *Plan                `json:"resume_plan"`
}
type StatusEvent struct {
	ID         Value `json:"event_id"`
	Status     Value `json:"status"`
	OccurredAt Value `json:"occurred_at"`
	RecordedAt Value `json:"recorded_at"`
	Note       Value `json:"note"`
}
type Detail struct {
	Record
	StatusHistory []StatusEvent `json:"status_history"`
}
type PlanRecord struct {
	JSON      Value `json:"plan_json"`
	Status    Value `json:"status"`
	Revision  Value `json:"revision"`
	CreatedAt Value `json:"created_at"`
	UpdatedAt Value `json:"updated_at"`
}

func ValidateFilters(f Filters) *Error {
	if f.Status != nil && !slices.Contains([]string{"captured", "reviewed", "preparing", "applied", "interview", "offer", "rejected", "withdrawn", "closed"}, *f.Status) {
		return failure("request", "INVALID_STATUS", nil)
	}
	if f.Decision != nil && !slices.Contains([]string{"apply", "consider", "skip"}, *f.Decision) {
		return failure("request", "INVALID_DECISION", nil)
	}
	return nil
}

// readCurrent owns one coherent read transaction and synchronous cleanup, never
// a schema migration. This intentionally improves the source's multi-SELECT
// concurrency behavior. The caller supplies/owns a correctly configured RO DB,
// retained native path guards and process admission. No query here writes data.
func readCurrent(ctx context.Context, db *sql.DB, read func(*queryState) *Error) (problem *Error) {
	if ctx == nil || db == nil {
		return failure("open", "DATABASE_CONNECTION_INVALID", nil)
	}
	if err := ctx.Err(); err != nil {
		return failure("read", "READ_CANCELED", err)
	}
	defer func() {
		if err := ctx.Err(); err != nil && problem == nil {
			problem = failure("read", "READ_CANCELED", err)
		}
	}()
	conn, err := db.Conn(ctx)
	if err != nil {
		return failure("open", "DATABASE_OPEN_FAILED", err)
	}
	defer func() {
		if err := conn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			problem = failure("cleanup", "DATABASE_CLEANUP_FAILED", err)
		}
	}()
	// Avoid database/sql's asynchronous context rollback; every statement uses
	// ctx, but native transaction cleanup must finish before the lease closes.
	tx, err := conn.BeginTx(context.WithoutCancel(ctx), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return failure("open", "DATABASE_TRANSACTION_FAILED", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			problem = failure("cleanup", "DATABASE_CLEANUP_FAILED", err)
		}
	}()
	var version int64
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return failure("schema", "DATABASE_QUERY_FAILED", err)
	}
	if version < SchemaVersion {
		return failure("schema", "SCHEMA_PREPARATION_REQUIRED", nil)
	}
	if version > SchemaVersion {
		return failure("schema", "SCHEMA_TOO_NEW", nil)
	}
	q := queryState{tx, ctx, budget{remaining: MaxScannedBytes, cellLimit: MaxRecordCellBytes}}
	problem = read(&q)
	if err := ctx.Err(); err != nil {
		return failure("read", "READ_CANCELED", err)
	}
	return problem
}

const applicationColumns = "job_id, company, title, source_url, content_fingerprint, decision, current_status, notes, created_at, updated_at, applied_resume_id, applied_resume_recorded"

func selectedApplications(q *queryState, suffix string, args []any) ([][]any, *Error) {
	rows := [][]any{}
	err := q.rows("applications", "SELECT "+applicationColumns+" FROM job_applications"+suffix, args, 12, func(raw []any) error {
		if len(rows) >= MaxRecords {
			return errBudget
		}
		if _, ok := raw[0].(string); !ok {
			return errors.New("unsupported identifier")
		}
		rows = append(rows, raw)
		return nil
	})
	return rows, err
}

func applicationRecords(q *queryState, rows [][]any) ([]Record, *Error) {
	records := make([]Record, len(rows))
	ids, positions := make([]string, len(rows)), map[string]int{}
	for i, raw := range rows {
		fields, err := q.cells(raw[:10])
		if err != nil {
			return nil, failure("applications", "DATABASE_VALUE_UNSUPPORTED", nil)
		}
		ids[i] = raw[0].(string) // selectedApplications validated every identity.
		if _, duplicate := positions[ids[i]]; duplicate {
			return nil, failure("applications", "DATABASE_VALUE_UNSUPPORTED", nil)
		}
		positions[ids[i]] = i
		records[i] = Record{JobID: fields[0], Company: fields[1], Title: fields[2], SourceURL: fields[3], ContentFingerprint: fields[4], Decision: fields[5], CurrentStatus: fields[6], Notes: fields[7], CreatedAt: fields[8], UpdatedAt: fields[9], ResumeSelection: Selection{State: "not_recorded"}, AppliedResume: AppliedResumeHistory{History: []ResumeEvent{}}}
	}
	recordFor := func(raw any) (*Record, error) {
		id, ok := raw.(string)
		index, exists := positions[id]
		if !ok || !exists {
			return nil, errors.New("unexpected application identity")
		}
		return &records[index], nil
	}
	// Bound the number of indexed queries by batch count, not application count.
	// Histories are complete and independently ordered within each exact job.
	if err := q.batches(ids, "attachment", "SELECT job_id, resume_id, relative_path, file_name, label, sha256, size_bytes, attached_at FROM application_resume_attachments WHERE job_id IN (", ")", 8, func(raw []any) error {
		record, err := recordFor(raw[0])
		if err != nil {
			return err
		}
		v, err := q.cells(raw[1:])
		if err != nil {
			return err
		}
		record.ResumeAttachment = &Attachment{v[0], v[1], v[2], v[3], v[4], v[5], v[6]}
		record.ResumeSelection = Selection{"selected", &ResumeIdentity{v[0], v[3]}}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := q.batches(ids, "resume_history", `SELECT events.job_id, events.action, events.occurred_at, events.recorded_at, events.note, library.resume_id, library.display_name
FROM application_applied_resume_events AS events LEFT JOIN resume_library AS library ON library.resume_id = events.resume_id
WHERE events.job_id IN (`, ") ORDER BY events.job_id, events.event_id", 7, func(raw []any) error {
		record, err := recordFor(raw[0])
		if err != nil {
			return err
		}
		if len(record.AppliedResume.History) >= MaxHistoryRows {
			return errBudget
		}
		v, err := q.cells(raw[1:])
		if err != nil {
			return err
		}
		event := ResumeEvent{Action: v[0], OccurredAt: v[1], RecordedAt: v[2], Note: v[3]}
		if raw[5] != nil {
			event.Resume = &ResumeIdentity{v[4], v[5]}
		}
		record.AppliedResume.History = append(record.AppliedResume.History, event)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := q.batches(ids, "plan", "SELECT job_id, plan_json, status, revision, created_at, updated_at FROM application_resume_plans WHERE job_id IN (", ")", 6, func(raw []any) error {
		record, err := recordFor(raw[0])
		if err != nil {
			return err
		}
		v, err := q.cells(raw[2:])
		if err != nil {
			return err
		}
		record.ResumePlan = &Plan{v[0], v[1], v[2], v[3]}
		return nil
	}); err != nil {
		return nil, err
	}
	legacyIDs, legacySeen := []string{}, map[string]bool{}
	for i := range records {
		record := &records[i]
		if history := record.AppliedResume.History; len(history) > 0 {
			record.AppliedResume.Recorded = true
			if latest := history[len(history)-1].Resume; latest != nil {
				owned := *latest
				record.AppliedResume.Resume = &owned
			}
			continue
		}
		recorded, err := truth(rows[i][11])
		if err != nil {
			return nil, failure("applications", "DATABASE_VALUE_UNSUPPORTED", nil)
		}
		record.AppliedResume.Recorded = recorded
		if id, ok := rows[i][10].(string); recorded && ok && id != "" && !legacySeen[id] {
			legacyIDs, legacySeen[id] = append(legacyIDs, id), true
		}
	}
	legacy := map[string]ResumeIdentity{}
	if err := q.batches(legacyIDs, "applied_resume", "SELECT resume_id, display_name, relative_path, file_name, sha256, size_bytes, added_at, archived_at FROM resume_library WHERE resume_id IN (", ")", 8, func(raw []any) error {
		id, ok := raw[0].(string)
		if !ok {
			return errors.New("unsupported resume identity")
		}
		v, err := q.cells(raw[:2])
		if err != nil {
			return err
		}
		legacy[id] = ResumeIdentity{v[0], v[1]}
		return nil
	}); err != nil {
		return nil, err
	}
	for i := range records {
		record := &records[i]
		// Do not copy CH27's false-flag/shared-prefetch behavior into full reads.
		if record.AppliedResume.Recorded && len(record.AppliedResume.History) == 0 {
			if id, ok := rows[i][10].(string); ok {
				if identity, exists := legacy[id]; exists {
					owned := identity
					record.AppliedResume.Resume = &owned
				}
			}
		}
	}
	return records, nil
}

func applicationRecord(q *queryState, raw []any) (Record, *Error) {
	records, err := applicationRecords(q, [][]any{raw})
	if err != nil {
		return Record{}, err
	}
	return records[0], nil
}

func ReadApplications(ctx context.Context, db *sql.DB, filters Filters) ([]Record, *Error) {
	if err := ValidateFilters(filters); err != nil {
		return nil, err
	}
	clauses, args := []string{}, []any{}
	if filters.Status != nil {
		clauses = append(clauses, "current_status = ?")
		args = append(args, *filters.Status)
	}
	if filters.Decision != nil {
		clauses = append(clauses, "decision = ?")
		args = append(args, *filters.Decision)
	}
	suffix := ""
	if len(clauses) > 0 {
		suffix = " WHERE " + strings.Join(clauses, " AND ")
	}
	suffix += " ORDER BY updated_at DESC, job_id"
	result := []Record{}
	err := readCurrent(ctx, db, func(q *queryState) *Error {
		rows, err := selectedApplications(q, suffix, args)
		if err != nil {
			return err
		}
		records, err := applicationRecords(q, rows)
		if err != nil {
			return err
		}
		encodedSize := 2
		for _, record := range records {
			encoded, encodeErr := json.Marshal(record)
			if encodeErr != nil {
				return failure("result", "DATABASE_VALUE_UNSUPPORTED", nil)
			}
			if len(result) > 0 {
				encodedSize++
			}
			encodedSize += len(encoded)
			if encodedSize > MaxResultBytes {
				return failure("result", "RESULT_LIMIT", nil)
			}
			result = append(result, record)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func ReadApplication(ctx context.Context, db *sql.DB, jobID string) (*Detail, *Error) {
	if err := ValidateSummaryRequest([]string{jobID}); err != nil {
		return nil, err
	}
	var result *Detail
	err := readCurrent(ctx, db, func(q *queryState) *Error {
		var err *Error
		result, err = applicationDetail(q, jobID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Reused inside a pending write transaction so an unsupported response cannot
// be discovered only after committing. No independent connection/read is opened.
func applicationDetail(q *queryState, jobID string) (*Detail, *Error) {
	rows, err := selectedApplications(q, " WHERE job_id = ?", []any{jobID})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, failure("applications", "NOT_FOUND", nil)
	}
	record, err := applicationRecord(q, rows[0])
	if err != nil {
		return nil, err
	}
	result := &Detail{Record: record, StatusHistory: []StatusEvent{}}
	if err := q.rows("status_history", "SELECT event_id, status, occurred_at, recorded_at, note FROM application_status_events WHERE job_id = ? ORDER BY occurred_at, event_id", []any{jobID}, 5, func(raw []any) error {
		if len(result.StatusHistory) >= MaxHistoryRows {
			return errBudget
		}
		v, err := q.cells(raw)
		if err != nil {
			return err
		}
		result.StatusHistory = append(result.StatusHistory, StatusEvent{v[0], v[1], v[2], v[3], v[4]})
		return nil
	}); err != nil {
		return nil, err
	}
	if err := checkResult(result); err != nil {
		return nil, err
	}
	return result, nil
}

func ReadResumePlan(ctx context.Context, db *sql.DB, jobID string) (*PlanRecord, *Error) {
	if err := ValidateSummaryRequest([]string{jobID}); err != nil {
		return nil, err
	}
	var result *PlanRecord
	err := readCurrent(ctx, db, func(q *queryState) *Error {
		rows, err := selectedApplications(q, " WHERE job_id = ?", []any{jobID})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return failure("applications", "NOT_FOUND", nil)
		}
		if err := q.rows("plan", "SELECT plan_json, status, revision, created_at, updated_at FROM application_resume_plans WHERE job_id = ?", []any{jobID}, 5, func(raw []any) error {
			v, err := q.cells(raw)
			if err != nil {
				return err
			}
			result = &PlanRecord{v[0], v[1], v[2], v[3], v[4]}
			return nil
		}); err != nil {
			return err
		}
		return checkResult(result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func checkResult(result any) *Error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return failure("result", "DATABASE_VALUE_UNSUPPORTED", nil)
	}
	if len(encoded) > MaxResultBytes {
		return failure("result", "RESULT_LIMIT", nil)
	}
	return nil
}
