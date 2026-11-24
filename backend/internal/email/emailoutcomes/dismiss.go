package emailoutcomes

import (
	"context"
	"database/sql"
)

// Unresolved updates are the ones a person may still act on: link them to an
// application or dismiss them. Linked updates record a status change already
// made to an application and are never deleted here.
var dismissibleResolutions = map[string]bool{
	"unmatched": true, "discovery_dismissed": true, "ambiguous": true, "status_conflict": true,
}

// DismissInTransaction deletes one unresolved stored signal at the expected
// revision. The caller owns the transaction.
//
// A deleted signal is not remembered: if the same email is read again (a
// manual rescan or .eml re-import) it is recorded and matched again. That is
// deliberate; a durable dismissed state would need a schema migration.
func DismissInTransaction(ctx context.Context, tx *sql.Tx, outcomeID string, expectedRevision int64) error {
	if ctx == nil || tx == nil || ctx.Err() != nil {
		return storeFailure("EMAIL_OUTCOME_DISMISS_CANCELED")
	}
	id, err := plainText(outcomeID, maxFieldChars, true)
	if err != nil || id != outcomeID || !outcomeLinkIDPattern.MatchString(id) || expectedRevision <= 0 {
		return storeFailure("EMAIL_OUTCOME_DISMISS_INVALID")
	}
	row, err := readStored(ctx, tx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return storeFailure("EMAIL_OUTCOME_DISMISS_NOT_FOUND")
	}
	if row.result.Revision != expectedRevision {
		return storeFailure("EMAIL_OUTCOME_DISMISS_REVISION_CONFLICT")
	}
	if !dismissibleResolutions[row.resolution] {
		return storeFailure("EMAIL_OUTCOME_DISMISS_LINKED")
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM job_email_application_outcomes
		WHERE outcome_id=? AND revision=?`, id, expectedRevision)
	if err != nil {
		return storeFailure("EMAIL_OUTCOME_WRITE_FAILED")
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return storeFailure("EMAIL_OUTCOME_DISMISS_REVISION_CONFLICT")
	}
	return nil
}

// ClearUnmatchedInTransaction deletes every signal that matched nothing:
// unmatched ones and those whose Job Feed item was dismissed or deleted.
// Updates that need review (ambiguous, status conflict) stay for a person to
// decide; linked updates always stay. It returns how many were deleted.
func ClearUnmatchedInTransaction(ctx context.Context, tx *sql.Tx) (int64, error) {
	if ctx == nil || tx == nil || ctx.Err() != nil {
		return 0, storeFailure("EMAIL_OUTCOME_DISMISS_CANCELED")
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM job_email_application_outcomes
		WHERE resolution IN ('unmatched','discovery_dismissed')`)
	if err != nil {
		return 0, storeFailure("EMAIL_OUTCOME_WRITE_FAILED")
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, storeFailure("EMAIL_OUTCOME_WRITE_FAILED")
	}
	return deleted, nil
}

// ClearForDiscoveriesInTransaction deletes the unlinked signals that point at
// Job Feed items about to be deleted, so they do not linger as orphans once
// the foreign key clears matched_discovery_id. Call it before deleting the
// discoveries, inside the same transaction.
func ClearForDiscoveriesInTransaction(ctx context.Context, tx *sql.Tx, discoveryIDs []string) (int64, error) {
	if ctx == nil || tx == nil || ctx.Err() != nil {
		return 0, storeFailure("EMAIL_OUTCOME_DISMISS_CANCELED")
	}
	var deleted int64
	for _, id := range discoveryIDs {
		result, err := tx.ExecContext(ctx, `DELETE FROM job_email_application_outcomes
			WHERE matched_discovery_id=? AND resolution IN ('unmatched','discovery_dismissed')`, id)
		if err != nil {
			return deleted, storeFailure("EMAIL_OUTCOME_WRITE_FAILED")
		}
		count, err := result.RowsAffected()
		if err != nil {
			return deleted, storeFailure("EMAIL_OUTCOME_WRITE_FAILED")
		}
		deleted += count
	}
	return deleted, nil
}
