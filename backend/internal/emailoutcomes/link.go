package emailoutcomes

import (
	"context"
	"database/sql"
	"regexp"
	"time"

	"careerhub.local/backend/internal/jobcard"
)

var outcomeLinkIDPattern = regexp.MustCompile(`^email_outcome_[0-9a-f]{64}$`)

// LinkInTransaction explicitly applies one unresolved stored signal to an
// existing application. The caller owns the transaction and status writer;
// failures must roll back both the status and outcome revision.
func LinkInTransaction(ctx context.Context, tx *sql.Tx, outcomeID, jobID string, expectedRevision int64, recordedAt string, apply ApplyStatus) (Result, error) {
	if ctx == nil || tx == nil || ctx.Err() != nil {
		return Result{}, storeFailure("EMAIL_OUTCOME_LINK_CANCELED")
	}
	id, err := plainText(outcomeID, maxFieldChars, true)
	if err != nil || id != outcomeID || !outcomeLinkIDPattern.MatchString(id) {
		return Result{}, storeFailure("EMAIL_OUTCOME_LINK_INVALID")
	}
	job, err := plainText(jobID, maxFieldChars, true)
	if err != nil || job != jobID || !jobcard.ValidID(job) || expectedRevision <= 0 {
		return Result{}, storeFailure("EMAIL_OUTCOME_LINK_INVALID")
	}
	if _, err := time.Parse(time.RFC3339Nano, recordedAt); err != nil {
		return Result{}, storeFailure("EMAIL_OUTCOME_LINK_INVALID")
	}
	row, err := readStored(ctx, tx, id)
	if err != nil {
		return Result{}, err
	}
	if row == nil {
		return Result{}, storeFailure("EMAIL_OUTCOME_LINK_NOT_FOUND")
	}
	if row.result.Revision != expectedRevision {
		return Result{}, storeFailure("EMAIL_OUTCOME_LINK_REVISION_CONFLICT")
	}
	switch row.resolution {
	case "unmatched", "discovery_dismissed", "ambiguous", "status_conflict":
	default:
		return Result{}, storeFailure("EMAIL_OUTCOME_LINK_ALREADY_RESOLVED")
	}
	event := Normalized{Provider: row.result.SourceProvider, ApplicationStatus: row.result.ApplicationStatus, ObservedAt: row.result.ObservedAt}
	if (event.Provider == "linkedin" && (event.ApplicationStatus != "rejected" || row.evidence != "linkedin_application_not_moving_forward")) ||
		(event.Provider == "handshake" && (event.ApplicationStatus != "applied" || row.evidence != "handshake_application_sent")) ||
		(event.Provider != "linkedin" && event.Provider != "handshake") {
		return Result{}, storeFailure("EMAIL_OUTCOME_VALUE_INVALID")
	}
	if _, err := time.Parse(time.RFC3339Nano, event.ObservedAt); err != nil {
		return Result{}, storeFailure("EMAIL_OUTCOME_VALUE_INVALID")
	}
	resolution, changed, err := statusResolution(ctx, tx, event, job, recordedAt, true, apply)
	if err != nil {
		return Result{}, err
	}
	if resolution == "unmatched" {
		return Result{}, storeFailure("EMAIL_OUTCOME_LINK_NOT_FOUND")
	}
	result, err := tx.ExecContext(ctx, `UPDATE job_email_application_outcomes
		SET resolution=?,matched_job_id=?,updated_at=?,revision=revision+1
		WHERE outcome_id=? AND revision=?`, resolution, job, recordedAt, id, expectedRevision)
	if err != nil {
		return Result{}, storeFailure("EMAIL_OUTCOME_WRITE_FAILED")
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return Result{}, storeFailure("EMAIL_OUTCOME_LINK_REVISION_CONFLICT")
	}
	updated, err := readStored(ctx, tx, id)
	if err != nil || updated == nil {
		return Result{}, storeFailure("EMAIL_OUTCOME_QUERY_FAILED")
	}
	out := updated.result
	out.ApplicationChanged = changed
	return out, nil
}
