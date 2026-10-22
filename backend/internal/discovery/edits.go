package discovery

import (
	"careerhub.local/backend/internal/textcompat"
	"context"
	"database/sql"
	"encoding/json"
	"slices"
)

type EditResult struct {
	Result  json.RawMessage
	Changed bool
}

func revisionConflict() *WriteError {
	return sourceFailure("DiscoveryRevisionConflict", "Discovery changed after it was loaded")
}
func writeRecord(r *reader, id string) (Record, *WriteError) {
	rows, e := selected(r, "SELECT "+columns+" FROM job_discoveries WHERE discovery_id=?", []any{id}, 1)
	if e != nil {
		return Record{}, fromRead(e)
	}
	if len(rows) == 0 {
		return Record{}, sourceFailure("DiscoveryNotFound", "Discovery does not exist")
	}
	return rows[0], nil
}
func editResponse(r *reader, id string, changed bool) (*EditResult, *WriteError) {
	record, e := writeRecord(r, id)
	if e != nil {
		return nil, e
	}
	records := []Record{record}
	// Writes return source facts, not a legacy engine's disposable assessment.
	// The native feed owner supplies a current review after the transaction.
	// All provenance/override validation still runs before committing a change.
	if e := enrichCapture(r, records, false); e != nil {
		return nil, fromRead(e)
	}
	return serializedEdit(records[0], changed)
}
func serializedEdit(value any, changed bool) (*EditResult, *WriteError) {
	raw, e := json.Marshal(value)
	if e != nil {
		return nil, writeFailure("result", "DATABASE_VALUE_UNSUPPORTED")
	}
	if len(raw) > MaxReadBytes {
		return nil, writeFailure("result", "RESULT_LIMIT")
	}
	return &EditResult{raw, changed}, nil
}
func editSQL(ctx context.Context, tx *sql.Tx, query string, args ...any) *WriteError {
	result, e := tx.ExecContext(ctx, query, args...)
	if e != nil {
		return writeFailure("write", "DATABASE_WRITE_FAILED")
	}
	count, e := result.RowsAffected()
	if e != nil {
		return writeFailure("write", "DATABASE_WRITE_FAILED")
	}
	if count != 1 {
		return revisionConflict()
	}
	return nil
}

// EditInTransaction never opens/commits SQL or writes files. The future owning
// service must admit the home/schema, retain path guards, observe every source
// read, recheck before commit and roll back ANY error (including response errors).
// These primitives alone must not be activated as installed routes.
func EditInTransaction(ctx context.Context, tx *sql.Tx, files CaptureFiles, input EditRequest, now string) (*EditResult, *WriteError) {
	plan, e := normalizeEdit(input)
	if e != nil {
		return nil, e
	}
	if ctx == nil || tx == nil {
		return nil, writeFailure("transaction", "DATABASE_CONNECTION_INVALID")
	}
	if ctx.Err() != nil {
		return nil, writeFailure("context", "WRITE_CANCELED")
	}
	r := &reader{query: tx, ctx: ctx, remaining: MaxReadBytes}
	current, e := writeRecord(r, plan.id)
	if e != nil {
		return nil, e
	}
	switch plan.op {
	case "transition":
		return transitionEdit(ctx, tx, r, plan, current, now)
	case "overrides":
		return overridesEdit(ctx, tx, r, plan, current, now)
	case "confirm":
		return confirmEdit(ctx, tx, r, files, plan, current, now)
	case "prepare":
		return prepareEdit(r, files, plan, current)
	case "promote":
		return promoteEdit(ctx, tx, r, files, plan, current, now)
	}
	return nil, writeFailure("request", "OPERATION_UNSUPPORTED")
}

func transitionEdit(ctx context.Context, tx *sql.Tx, r *reader, plan edit, current Record, now string) (*EditResult, *WriteError) {
	if current.Revision != plan.revision {
		return nil, revisionConflict()
	}
	if current.State == plan.state {
		return editResponse(r, plan.id, false)
	}
	if current.State == "promoted" {
		return nil, invalidWrite("Discovery cannot transition from promoted to " + plan.state)
	}
	if !slices.Contains([]string{"inbox", "shortlisted", "dismissed"}, current.State) {
		return nil, writeFailure("state", "DATABASE_VALUE_UNSUPPORTED")
	}
	if e := nextRevision(current.Revision); e != nil {
		return nil, e
	}
	if e := editSQL(ctx, tx, "UPDATE job_discoveries SET state=?,updated_at=?,revision=revision+1 WHERE discovery_id=? AND revision=?",
		plan.state, now, plan.id, plan.revision); e != nil {
		return nil, e
	}
	return editResponse(r, plan.id, true)
}
func optionalEqual(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func overridesEdit(ctx context.Context, tx *sql.Tx, r *reader, plan edit, current Record, now string) (*EditResult, *WriteError) {
	if current.State == "promoted" {
		return nil, invalidWrite("Promoted discovery fields must be edited in Job Workspace")
	}
	var fields [3]*string
	var revision int64
	err := r.rows("overrides", "SELECT title,company,location,revision FROM job_discovery_field_overrides WHERE discovery_id=?", []any{plan.id}, 4, 1, func(raw []any) error {
		for i := range fields {
			if raw[i] != nil {
				text, e := textCell(raw[i], false)
				if e != nil {
					return e
				}
				fields[i] = &text
			}
		}
		var e error
		revision, e = integerCell(raw[3])
		return e
	})
	if err != nil {
		return nil, fromRead(err)
	}
	equal := true
	for i := range fields {
		equal = equal && optionalEqual(fields[i], plan.fields[i])
	}
	if equal {
		return editResponse(r, plan.id, false)
	}
	if revision != plan.revision {
		return nil, sourceFailure("DiscoveryFieldOverrideRevisionConflict", "Discovery field corrections changed after they were loaded")
	}
	if e := nextRevision(revision); e != nil {
		return nil, e
	}
	if e := nextRevision(current.Revision); e != nil {
		return nil, e
	}
	if e := editSQL(ctx, tx, `INSERT INTO job_discovery_field_overrides(discovery_id,title,company,location,revision,created_at,updated_at)
VALUES (?,?,?,?,?,?,?) ON CONFLICT(discovery_id) DO UPDATE SET title=excluded.title,company=excluded.company,
location=excluded.location,revision=excluded.revision,updated_at=excluded.updated_at`,
		plan.id, plan.fields[0], plan.fields[1], plan.fields[2], revision+1, now, now); e != nil {
		return nil, e
	}
	if e := editSQL(ctx, tx, "UPDATE job_discoveries SET updated_at=?,revision=revision+1 WHERE discovery_id=?", now, plan.id); e != nil {
		return nil, e
	}
	return editResponse(r, plan.id, true)
}
func confirmEdit(ctx context.Context, tx *sql.Tx, r *reader, files CaptureFiles, plan edit, current Record, now string) (*EditResult, *WriteError) {
	if current.Revision != plan.revision {
		return nil, revisionConflict()
	}
	if current.State == "promoted" {
		return editResponse(r, plan.id, false)
	}
	if current.State == "dismissed" {
		return nil, sourceFailure("DiscoveryVerificationNotReady", "Restore the dismissed discovery before confirming its content")
	}
	if current.ContentScope == "confirmed_full" {
		return editResponse(r, plan.id, false)
	}
	if !slices.Contains([]string{"selection", "page_visible"}, current.ContentScope) {
		return nil, sourceFailure("DiscoveryVerificationNotReady", "Captured page or selected text is required before confirmation")
	}
	if current.SourceRelativePath == "" || current.ContentSHA256 == "" || current.ContentChars <= 0 {
		return nil, sourceFailure("DiscoveryVerificationNotReady", "Retained source text is required before confirmation")
	}
	// Declared correction: the source only checks nonempty metadata here, not
	// the retained bytes. Refuse a missing/tampered file before changing review.
	if _, e := ReadCapturedSource(ctx, files, current); e != nil {
		return nil, fromRead(e)
	}
	if e := nextRevision(current.Revision); e != nil {
		return nil, e
	}
	if e := editSQL(ctx, tx, `UPDATE job_discoveries SET content_scope='confirmed_full',content_verified_at=?,
content_verified_from_scope=?,updated_at=?,revision=revision+1
WHERE discovery_id=? AND revision=? AND state IN ('inbox','shortlisted') AND content_scope IN ('selection','page_visible')`,
		now, current.ContentScope, now, plan.id, plan.revision); e != nil {
		return nil, e
	}
	return editResponse(r, plan.id, true)
}
func prepareEdit(r *reader, files CaptureFiles, plan edit, current Record) (*EditResult, *WriteError) {
	records := []Record{current}
	if e := enrichCapture(r, records, false); e != nil {
		return nil, fromRead(e)
	}
	detail := &Detail{Record: records[0]}
	var err *Error
	if detail.SourceText, err = capturedText(files, current); err != nil {
		return nil, fromRead(err)
	}
	if detail.Observations, err = detailObservations(r, plan.id); err != nil {
		return nil, fromRead(err)
	}
	if detail.EmailSource, err = emailSource(r, detail.Observations); err != nil {
		return nil, fromRead(err)
	}
	if current.State == "promoted" {
		return serializedEdit(detail, false)
	}
	if current.Revision != plan.revision {
		return nil, revisionConflict()
	}
	if e := promotionReady(current); e != nil {
		return nil, e
	}
	if textcompat.Strip(detail.SourceText) == "" {
		return nil, sourceFailure("DiscoveryPromotionNotReady", "A confirmed complete job description is required before promotion")
	}
	return serializedEdit(detail, false)
}
func promotionReady(current Record) *WriteError {
	if current.State == "dismissed" {
		return sourceFailure("DiscoveryPromotionNotReady", "Restore the dismissed discovery before promoting it")
	}
	if current.ContentScope != "confirmed_full" {
		return sourceFailure("DiscoveryPromotionNotReady", "A confirmed complete job description is required before promotion")
	}
	if current.ContentVerification.VerifiedAt == "" {
		return sourceFailure("DiscoveryPromotionNotReady", "The complete job description must be verified before promotion")
	}
	return nil
}
func promoteEdit(ctx context.Context, tx *sql.Tx, r *reader, files CaptureFiles, plan edit, current Record, now string) (*EditResult, *WriteError) {
	if current.State == "promoted" {
		if current.PromotedJobID != plan.job {
			return nil, invalidWrite("Discovery is already linked to a different saved job")
		}
		return editResponse(r, plan.id, false)
	}
	if current.Revision != plan.revision {
		return nil, revisionConflict()
	}
	if e := promotionReady(current); e != nil {
		return nil, e
	}
	if _, e := ReadCapturedSource(ctx, files, current); e != nil {
		return nil, fromRead(e)
	}
	found := false
	if e := r.rows("application", "SELECT job_id FROM job_applications WHERE job_id=?", []any{plan.job}, 1, 1, func([]any) error { found = true; return nil }); e != nil {
		return nil, fromRead(e)
	}
	if !found {
		return nil, invalidWrite("Promoted job must be registered first")
	}
	if e := nextRevision(current.Revision); e != nil {
		return nil, e
	}
	if e := editSQL(ctx, tx, `UPDATE job_discoveries SET state='promoted',promoted_job_id=?,promoted_at=?,updated_at=?,revision=revision+1
WHERE discovery_id=? AND revision=? AND state<>'promoted'`, plan.job, now, now, plan.id, plan.revision); e != nil {
		return nil, e
	}
	return editResponse(r, plan.id, true)
}
