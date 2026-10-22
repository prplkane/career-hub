package emailreceipts

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const leaseDuration = 5 * time.Minute

var ErrAlreadyRunning = errors.New("EMAIL_SYNC_ALREADY_RUNNING")

func parseUTC(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if parsed, err := time.Parse(layout, raw); err == nil && parsed.Year() >= 1 && parsed.Year() <= 9999 {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func formatUTC(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05Z")
}

func retryAt(completed time.Time, previousFailures int64) string {
	minutes := 60
	if previousFailures < 4 {
		if previousFailures < 0 {
			previousFailures = 0
		}
		minutes = 5 << previousFailures
	}
	return formatUTC(completed.Add(time.Duration(minutes) * time.Minute))
}

// AcquireLease claims the worker after checking the existing lease. The
// returned timestamp is nonempty only when a scheduled run is deferred by
// backoff; manual runs bypass backoff, never an active lease. The caller must
// commit the transaction before doing any mailbox work.
func AcquireLease(ctx context.Context, tx *sql.Tx, startedAt, owner string, respectBackoff bool) (string, error) {
	if err := checkContext(ctx, tx); err != nil {
		return "", err
	}
	started, valid := parseUTC(startedAt)
	if !valid || owner == "" {
		return "", ErrInvalid
	}
	var activeOwner string
	var activeExpiry, nextRetry sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT lease_owner, lease_expires_at, next_retry_at
		FROM job_email_sync_state WHERE connection_id = ? AND folder = ?`,
		connectionID, folder).Scan(&activeOwner, &activeExpiry, &nextRetry)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotConfigured
	}
	if err != nil {
		return "", ErrLocalWrite
	}
	if expiry, ok := parseUTC(activeExpiry.String); activeOwner != "" && ok && expiry.After(started) {
		return "", ErrAlreadyRunning
	}
	if retry, ok := parseUTC(nextRetry.String); respectBackoff && ok && retry.After(started) {
		return formatUTC(retry), nil
	}
	updated, err := tx.ExecContext(ctx, `UPDATE job_email_sync_state
		SET last_started_at = ?, last_result = 'running', last_error_code = '',
			lease_owner = ?, lease_expires_at = ?, revision = revision + 1
		WHERE connection_id = ? AND folder = ?`,
		startedAt, owner, formatUTC(started.Add(leaseDuration)), connectionID, folder)
	if err != nil {
		return "", ErrLocalWrite
	}
	rows, err := updated.RowsAffected()
	if err != nil || rows != 1 {
		return "", ErrLocalWrite
	}
	return "", nil
}

func failuresForOwner(ctx context.Context, tx *sql.Tx, owner string) (int64, bool, error) {
	var failures int64
	err := tx.QueryRowContext(ctx, `SELECT consecutive_failures FROM job_email_sync_state
		WHERE connection_id = ? AND folder = ? AND lease_owner = ?`,
		connectionID, folder, owner).Scan(&failures)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil || failures < 0 {
		return 0, false, ErrLocalWrite
	}
	return failures, true, nil
}

// MarkFailed records a run-level failure only for the current lease owner.
// A stale owner is a no-op, matching Python. The host retains commit outcome.
func MarkFailed(ctx context.Context, tx *sql.Tx, owner, errorCode, completedAt string) (bool, error) {
	if err := checkContext(ctx, tx); err != nil {
		return false, err
	}
	completed, valid := parseUTC(completedAt)
	if owner == "" || !valid {
		return false, ErrInvalid
	}
	failures, matched, err := failuresForOwner(ctx, tx, owner)
	if err != nil || !matched {
		return false, err
	}
	code := safeErrorCode(errorCode)
	updated, err := tx.ExecContext(ctx, `UPDATE job_email_sync_state
		SET last_completed_at = ?, last_result = 'failed',
			consecutive_failures = consecutive_failures + 1,
			next_retry_at = ?, last_error_code = ?,
			lease_owner = '', lease_expires_at = NULL, revision = revision + 1
		WHERE connection_id = ? AND folder = ? AND lease_owner = ?`,
		completedAt, retryAt(completed, failures), code, connectionID, folder, owner)
	if err != nil {
		return false, ErrLocalWrite
	}
	rows, err := updated.RowsAffected()
	if err != nil || rows != 1 {
		return false, ErrLocalWrite
	}
	_, err = tx.ExecContext(ctx, `UPDATE job_email_connections
		SET last_tested_at = ?, last_test_status = 'failed',
			last_error_code = ?, updated_at = ? WHERE connection_id = ?`,
		completedAt, code, completedAt, connectionID)
	if err != nil {
		return false, ErrLocalWrite
	}
	return true, nil
}

// MarkCompleted records success or partial success only for the current lease
// owner. Message retries extend backoff; a completed run without retries clears
// the failure streak. Connection health mirrors the Python source: successful
// even when some messages were skipped or remain pending.
func MarkCompleted(ctx context.Context, tx *sql.Tx, owner, completedAt string, hadMessageIssues, retryPending bool) (bool, error) {
	if err := checkContext(ctx, tx); err != nil {
		return false, err
	}
	completed, valid := parseUTC(completedAt)
	if owner == "" || !valid {
		return false, ErrInvalid
	}
	failures, matched, err := failuresForOwner(ctx, tx, owner)
	if err != nil || !matched {
		return false, err
	}
	result, nextRetry, errorCode, nextFailures := "succeeded", any(nil), "", int64(0)
	if hadMessageIssues {
		result, errorCode = "partial", "EMAIL_SYNC_MESSAGE_SKIPPED"
	}
	if retryPending {
		nextFailures, nextRetry, errorCode = failures+1, retryAt(completed, failures), "EMAIL_SYNC_MESSAGE_RETRY"
	}
	updated, err := tx.ExecContext(ctx, `UPDATE job_email_sync_state
		SET last_completed_at = ?, last_result = ?, consecutive_failures = ?,
			next_retry_at = ?, last_error_code = ?, lease_owner = '',
			lease_expires_at = NULL, revision = revision + 1
		WHERE connection_id = ? AND folder = ? AND lease_owner = ?`,
		completedAt, result, nextFailures, nextRetry, errorCode, connectionID, folder, owner)
	if err != nil {
		return false, ErrLocalWrite
	}
	rows, err := updated.RowsAffected()
	if err != nil || rows != 1 {
		return false, ErrLocalWrite
	}
	_, err = tx.ExecContext(ctx, `UPDATE job_email_connections
		SET last_tested_at = ?, last_test_status = 'succeeded',
			last_error_code = '', updated_at = ? WHERE connection_id = ?`,
		completedAt, completedAt, connectionID)
	if err != nil {
		return false, ErrLocalWrite
	}
	return true, nil
}
