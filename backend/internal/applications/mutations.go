package applications

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"careerhub.local/backend/internal/textcompat"
)

// These transaction-local primitives neither open a database nor commit it.
// Their owning service must normalize inputs/time, admit the owned current-schema
// database and synchronously roll back on any error, including response errors.
type JobSnapshot struct{ JobID, Company, Title, SourceURL, ContentFingerprint string }
type MutationResult struct {
	Result  json.RawMessage
	Changed bool
}

// ApplicationInTransaction reuses the complete bounded read projection without
// opening a nested transaction. Mutations must build this response before commit.
func ApplicationInTransaction(ctx context.Context, tx *sql.Tx, id string) (*Detail, *Error) {
	q, e := mutationQuery(ctx, tx)
	if e != nil {
		return nil, e
	}
	return applicationDetail(q, id)
}

// SessionJob is a narrow decision input obtained only after validating the same
// complete application projection used by GetApplication. It does not relax
// history/resume representation checks or open a nested transaction.
type SessionJob struct {
	Status, SourceURL  string
	SubmissionRecorded bool
}

func SessionJobInTransaction(ctx context.Context, tx *sql.Tx, id string) (*SessionJob, *Error) {
	q, e := mutationQuery(ctx, tx)
	if e != nil {
		return nil, e
	}
	detail, e := applicationDetail(q, id)
	if e != nil {
		return nil, e
	}
	status, ok := detail.CurrentStatus.value.(string)
	url, urlOK := detail.SourceURL.value.(string)
	if !ok || !urlOK {
		return nil, failure("applications", "DATABASE_VALUE_UNSUPPORTED", nil)
	}
	confirmed := func(value any) bool {
		s, ok := value.(string)
		return ok && slices.Contains([]string{"applied", "interview", "offer"}, s)
	}
	submitted := confirmed(status)
	for _, event := range detail.StatusHistory {
		submitted = submitted || confirmed(event.Status.value)
	}
	return &SessionJob{status, url, submitted}, nil
}

func mutationQuery(ctx context.Context, tx *sql.Tx) (*queryState, *Error) {
	if ctx == nil || tx == nil {
		return nil, failure("write", "DATABASE_CONNECTION_INVALID", nil)
	}
	if ctx.Err() != nil {
		return nil, failure("write", "WRITE_CANCELED", nil)
	}
	return &queryState{tx, ctx, budget{remaining: MaxScannedBytes, cellLimit: MaxRecordCellBytes}}, nil
}
func mutationExec(ctx context.Context, tx *sql.Tx, query string, args ...any) *Error {
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return failure("write", "DATABASE_WRITE_FAILED", err)
	}
	return nil
}
func mutationResult(value any, changed bool) (*MutationResult, *Error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, failure("result", "DATABASE_VALUE_UNSUPPORTED", nil)
	}
	if len(raw) > MaxResultBytes {
		return nil, failure("result", "RESULT_LIMIT", nil)
	}
	return &MutationResult{raw, changed}, nil
}
func mutationExisting(q *queryState, id string) ([]any, *Error) {
	rows, err := selectedApplications(q, " WHERE job_id = ?", []any{id})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, failure("applications", "NOT_FOUND", nil)
	}
	return rows[0], nil
}

// guardRevision is the optional optimistic-concurrency check: a caller that
// loaded the application at `expected` refuses to overwrite a newer row. A nil
// expectation keeps the unguarded behavior. The row's revision is the v28
// INTEGER column; any other storage class is an unsupported database value.
func guardRevision(row []any, expected *int64) *Error {
	current, ok := row[12].(int64)
	if !ok || current < 1 {
		return failure("applications", "DATABASE_VALUE_UNSUPPORTED", nil)
	}
	if expected != nil && *expected != current {
		return failure("applications", "REVISION_CONFLICT", nil)
	}
	return nil
}

// RegisterInTransaction keeps last duplicate content, first unique-ID allocation
// order, and input-order duplicate responses. It never resets workflow metadata.
func RegisterInTransaction(ctx context.Context, tx *sql.Tx, snapshots []JobSnapshot, now string) (*MutationResult, *Error) {
	q, err := mutationQuery(ctx, tx)
	if err != nil {
		return nil, err
	}
	allIDs := make([]string, len(snapshots))
	for i, item := range snapshots {
		allIDs[i] = item.JobID
	}
	if err := ValidateSummaryRequest(allIDs); err != nil {
		return nil, err
	}
	latest := []JobSnapshot{}
	positions := map[string]int{}
	ids := []string{}
	for _, item := range snapshots {
		if index, exists := positions[item.JobID]; exists {
			latest[index] = item
		} else {
			positions[item.JobID] = len(latest)
			latest = append(latest, item)
			ids = append(ids, item.JobID)
		}
	}
	existing := map[string][]any{}
	if err := q.batches(ids, "applications", "SELECT "+applicationColumns+" FROM job_applications WHERE job_id IN (", ")", applicationColumnCount, func(raw []any) error {
		id, ok := raw[0].(string)
		if !ok {
			return errBudget
		}
		existing[id] = raw
		return nil
	}); err != nil {
		return nil, err
	}
	newRows, changedRows := []JobSnapshot{}, []JobSnapshot{}
	for _, item := range latest {
		row, found := existing[item.JobID]
		if !found {
			newRows = append(newRows, item)
			continue
		}
		if row[1] != item.Company || row[2] != item.Title || row[3] != item.SourceURL || row[4] != item.ContentFingerprint {
			changedRows = append(changedRows, item)
		}
	}
	for _, item := range newRows {
		if err := mutationExec(ctx, tx, "INSERT INTO job_applications (job_id,company,title,source_url,content_fingerprint,decision,current_status,notes,created_at,updated_at) VALUES (?,?,?,?,?,NULL,'captured','',?,?)",
			item.JobID, item.Company, item.Title, item.SourceURL, item.ContentFingerprint, now, now); err != nil {
			return nil, err
		}
	}
	for _, item := range newRows {
		if err := mutationExec(ctx, tx, "INSERT INTO application_status_events (job_id,status,occurred_at,recorded_at,note) VALUES (?,'captured',?,?,'')", item.JobID, now, now); err != nil {
			return nil, err
		}
	}
	for _, item := range changedRows {
		if err := mutationExec(ctx, tx, "UPDATE job_applications SET company=?,title=?,source_url=?,content_fingerprint=?,updated_at=?,revision=revision+1 WHERE job_id=?",
			item.Company, item.Title, item.SourceURL, item.ContentFingerprint, now, item.JobID); err != nil {
			return nil, err
		}
	}
	for _, item := range changedRows {
		if existing[item.JobID][4] != item.ContentFingerprint {
			if err := mutationExec(ctx, tx, "DELETE FROM job_assessments WHERE job_id=?", item.JobID); err != nil {
				return nil, err
			}
		}
	}
	byID, err := summaryRecords(q, ids)
	if err != nil {
		return nil, err
	}
	result := make([]Summary, 0, len(snapshots))
	for _, item := range snapshots {
		if record, exists := byID[item.JobID]; exists {
			result = append(result, record)
		}
	}
	return mutationResult(result, len(newRows)+len(changedRows) > 0)
}

// DecisionInTransaction sets the decision and, when notes is non-nil, the
// notes. expectedRevision, when non-nil, must equal the row's current revision
// or the write is refused with REVISION_CONFLICT before anything changes.
func DecisionInTransaction(ctx context.Context, tx *sql.Tx, id, decision string, notes *string, expectedRevision *int64, now string) (*MutationResult, *Error) {
	if err := ValidateFilters(Filters{Decision: &decision}); err != nil {
		return nil, err
	}
	q, err := mutationQuery(ctx, tx)
	if err != nil {
		return nil, err
	}
	row, err := mutationExisting(q, id)
	if err != nil {
		return nil, err
	}
	if err := guardRevision(row, expectedRevision); err != nil {
		return nil, err
	}
	newNotes := row[7]
	if notes != nil {
		newNotes = strings.TrimFunc(*notes, textcompat.IsSpace)
	}
	changed := row[5] != decision || !sqlScalarEqual(row[7], newNotes)
	if changed {
		if err := mutationExec(ctx, tx, "UPDATE job_applications SET decision=?,notes=?,updated_at=?,revision=revision+1 WHERE job_id=?", decision, newNotes, now, id); err != nil {
			return nil, err
		}
	}
	row, err = mutationExisting(q, id)
	if err != nil {
		return nil, err
	}
	record, err := applicationRecord(q, row)
	if err != nil {
		return nil, err
	}
	return mutationResult(record, changed)
}

// StatusInTransaction records a status event and the new current status.
// expectedRevision follows the DecisionInTransaction contract.
func StatusInTransaction(ctx context.Context, tx *sql.Tx, id, status, occurredAt, recordedAt, note string, expectedRevision *int64) (*MutationResult, *Error) {
	if status == "reviewed" {
		return nil, failure("request", "INVALID_STATUS", nil)
	}
	if err := ValidateFilters(Filters{Status: &status}); err != nil {
		return nil, err
	}
	q, err := mutationQuery(ctx, tx)
	if err != nil {
		return nil, err
	}
	row, err := mutationExisting(q, id)
	if err != nil {
		return nil, err
	}
	if err := guardRevision(row, expectedRevision); err != nil {
		return nil, err
	}
	recorded, truthErr := truth(row[11])
	if truthErr != nil {
		return nil, failure("applications", "DATABASE_VALUE_UNSUPPORTED", nil)
	}
	needsSnapshot := slices.Contains([]string{"applied", "interview", "offer"}, status) && !recorded
	note = strings.TrimFunc(note, textcompat.IsSpace)
	changed := row[6] != status || note != "" || needsSnapshot
	if changed {
		if row[6] != status || note != "" {
			if err := mutationExec(ctx, tx, "INSERT INTO application_status_events (job_id,status,occurred_at,recorded_at,note) VALUES (?,?,?,?,?)", id, status, occurredAt, recordedAt, note); err != nil {
				return nil, err
			}
		}
		if needsSnapshot {
			var resumeID any
			if err := q.rows("attachment", "SELECT resume_id FROM application_resume_attachments WHERE job_id=?", []any{id}, 1, func(raw []any) error {
				resumeID = raw[0]
				return nil
			}); err != nil {
				return nil, err
			}
			if err := mutationExec(ctx, tx, "UPDATE job_applications SET current_status=?,updated_at=?,applied_resume_id=?,applied_resume_recorded=1,revision=revision+1 WHERE job_id=?", status, recordedAt, resumeID, id); err != nil {
				return nil, err
			}
			if err := mutationExec(ctx, tx, "INSERT INTO application_applied_resume_events (job_id,resume_id,action,occurred_at,recorded_at,note) VALUES (?,?,'initial_snapshot',?,?,'')", id, resumeID, occurredAt, recordedAt); err != nil {
				return nil, err
			}
		} else if err := mutationExec(ctx, tx, "UPDATE job_applications SET current_status=?,updated_at=?,revision=revision+1 WHERE job_id=?", status, recordedAt, id); err != nil {
			return nil, err
		}
	}
	detail, err := applicationDetail(q, id)
	if err != nil {
		return nil, err
	}
	return mutationResult(detail, changed)
}

func StartInTransaction(ctx context.Context, tx *sql.Tx, id, occurredAt, recordedAt string) (*MutationResult, *Error) {
	q, err := mutationQuery(ctx, tx)
	if err != nil {
		return nil, err
	}
	row, err := mutationExisting(q, id)
	if err != nil {
		return nil, err
	}
	status, ok := row[6].(string)
	if !ok {
		return nil, failure("applications", "DATABASE_VALUE_UNSUPPORTED", nil)
	}
	changed := !slices.Contains([]string{"preparing", "applied", "interview", "offer"}, status)
	if changed {
		if err := mutationExec(ctx, tx, "INSERT INTO application_status_events (job_id,status,occurred_at,recorded_at,note) VALUES (?,'preparing',?,?,'')", id, occurredAt, recordedAt); err != nil {
			return nil, err
		}
		if err := mutationExec(ctx, tx, "UPDATE job_applications SET decision='apply',current_status='preparing',updated_at=?,revision=revision+1 WHERE job_id=?", recordedAt, id); err != nil {
			return nil, err
		}
	}
	detail, err := applicationDetail(q, id)
	if err != nil {
		return nil, err
	}
	return mutationResult(detail, changed)
}

func sqlScalarEqual(a, b any) bool {
	// SQLite BLOB cells are slices, unlike Python bytes. Never compare a
	// caller-independent pair of interface slices with ==.
	x, xblob := a.([]byte)
	y, yblob := b.([]byte)
	if xblob || yblob {
		return xblob && yblob && slices.Equal(x, y)
	}
	return a == b
}
