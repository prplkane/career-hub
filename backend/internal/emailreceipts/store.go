// Package emailreceipts ports the durable receipt/cursor primitives used by
// Python email_sync. The caller owns Data Home admission and the SQL transaction;
// this package neither opens a database nor commits one.
package emailreceipts

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"regexp"
	"strings"
)

const (
	connectionID = "primary"
	folder       = "INBOX"
)

var (
	ErrInvalid       = errors.New("EMAIL_CONNECTION_INVALID")
	ErrCanceled      = errors.New("EMAIL_SYNC_CANCELED")
	ErrNotConfigured = errors.New("EMAIL_CONNECTION_NOT_CONFIGURED")
	ErrLocalWrite    = errors.New("EMAIL_SYNC_LOCAL_WRITE_FAILED")
	validErrorCode   = regexp.MustCompile(`^EMAIL_[A-Z0-9_]{1,80}$`)
	validEpoch       = regexp.MustCompile(`^[1-9][0-9]*$`)
)

type Receipt struct {
	UIDValidity              string
	UID                      uint32
	MessageIDHash            string
	ContentSHA256            string
	SenderAddress            string
	Status                   string
	AttemptCount             int
	CandidateCount           int
	DiscoveryCount           int
	ErrorCode                string
	FirstSeenAt              string
	UpdatedAt                string
	MessageSentDate          string
	SubjectJobSignal         bool
	SubjectServiceSignal     bool
	SubjectApplicationSignal bool
	SubjectRoute             string
}

type RecordInput struct {
	UIDValidity              string
	UID                      uint32
	MessageIDHash            string
	ContentSHA256            string
	SenderAddress            string
	Status                   string
	CandidateCount           int
	DiscoveryCount           int
	ErrorCode                string
	AdvanceCursor            bool
	RecordedAt               string
	MessageSentDate          *string
	SubjectJobSignal         *bool
	SubjectServiceSignal     *bool
	SubjectApplicationSignal *bool
	SubjectRoute             *string
}

func checkContext(ctx context.Context, tx *sql.Tx) error {
	if ctx == nil || tx == nil {
		return ErrInvalid
	}
	if ctx.Err() != nil {
		return ErrCanceled
	}
	return nil
}

func validStatus(status string) bool {
	switch status {
	case "imported", "no_candidates", "duplicate", "unknown_sender", "too_large", "failed_retryable", "failed_final":
		return true
	default:
		return false
	}
}

func validRoute(route string) bool {
	switch route {
	case "", "job_alerts", "application_tracking", "both", "filtered":
		return true
	default:
		return false
	}
}

func safeErrorCode(raw string) string {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if code == "" || validErrorCode.MatchString(code) {
		return code
	}
	return "EMAIL_IMPORT_FAILED"
}

// Position returns the provider epoch and last completed UID from the
// existing sync-state row. It never creates missing configuration.
func Position(ctx context.Context, tx *sql.Tx) (string, uint32, error) {
	if err := checkContext(ctx, tx); err != nil {
		return "", 0, err
	}
	var epoch string
	var uid int64
	err := tx.QueryRowContext(ctx, `SELECT uidvalidity, last_uid FROM job_email_sync_state
		WHERE connection_id = ? AND folder = ?`, connectionID, folder).Scan(&epoch, &uid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrNotConfigured
	}
	if err != nil || uid < 0 || uid > int64(^uint32(0)) {
		return "", 0, ErrLocalWrite
	}
	return epoch, uint32(uid), nil
}

// SetUIDValidity resets only the cursor when Gmail's provider epoch changes.
// A first nonempty epoch is not reported as a reset, matching Python.
func SetUIDValidity(ctx context.Context, tx *sql.Tx, epoch string) (bool, error) {
	if err := checkContext(ctx, tx); err != nil {
		return false, err
	}
	if !validEpoch.MatchString(epoch) {
		return false, ErrInvalid
	}
	previous, _, err := Position(ctx, tx)
	if err != nil {
		return false, err
	}
	if previous == epoch {
		return false, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE job_email_sync_state
		SET uidvalidity = ?, last_uid = 0, revision = revision + 1
		WHERE connection_id = ? AND folder = ?`, epoch, connectionID, folder)
	if err != nil {
		return false, ErrLocalWrite
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return false, ErrLocalWrite
	}
	return previous != "", nil
}

// ByTransport reads the one receipt keyed by folder, provider epoch and UID.
func ByTransport(ctx context.Context, tx *sql.Tx, epoch string, uid uint32) (*Receipt, error) {
	if err := checkContext(ctx, tx); err != nil {
		return nil, err
	}
	if !validEpoch.MatchString(epoch) || uid == 0 {
		return nil, ErrInvalid
	}
	var result Receipt
	var storedUID int64
	var job, service, application int
	err := tx.QueryRowContext(ctx, `SELECT uidvalidity, message_uid, message_id_hash,
		content_sha256, sender_address, status, attempt_count, candidate_count,
		discovery_count, error_code, first_seen_at, updated_at, message_sent_date,
		subject_job_signal, subject_service_signal, subject_application_signal,
		subject_route FROM job_email_message_receipts
		WHERE connection_id = ? AND folder = ? AND uidvalidity = ? AND message_uid = ?`,
		connectionID, folder, epoch, uid).Scan(
		&result.UIDValidity, &storedUID, &result.MessageIDHash,
		&result.ContentSHA256, &result.SenderAddress, &result.Status,
		&result.AttemptCount, &result.CandidateCount, &result.DiscoveryCount,
		&result.ErrorCode, &result.FirstSeenAt, &result.UpdatedAt,
		&result.MessageSentDate, &job, &service, &application, &result.SubjectRoute,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil || storedUID != int64(uid) || (job != 0 && job != 1) ||
		(service != 0 && service != 1) || (application != 0 && application != 1) {
		return nil, ErrLocalWrite
	}
	result.UID = uid
	result.SubjectJobSignal = job == 1
	result.SubjectServiceSignal = service == 1
	result.SubjectApplicationSignal = application == 1
	return &result, nil
}

// IdentityWasProcessed matches only a terminal receipt at a different
// transport identity. Dynamic SQL is selected from two fixed statements.
func IdentityWasProcessed(ctx context.Context, tx *sql.Tx, field, value, epoch string, uid uint32) (bool, error) {
	if err := checkContext(ctx, tx); err != nil {
		return false, err
	}
	if !validEpoch.MatchString(epoch) || uid == 0 {
		return false, ErrInvalid
	}
	if field != "message_id_hash" && field != "content_sha256" {
		return false, ErrInvalid
	}
	if value == "" {
		return false, nil
	}
	const filter = ` AND NOT (folder = ? AND uidvalidity = ? AND message_uid = ?)
		AND status IN ('imported', 'no_candidates', 'duplicate', 'too_large', 'failed_final') LIMIT 1`
	query := `SELECT 1 FROM job_email_message_receipts WHERE connection_id = ? AND message_id_hash = ?` + filter
	if field == "content_sha256" {
		query = `SELECT 1 FROM job_email_message_receipts WHERE connection_id = ? AND content_sha256 = ?` + filter
	}
	var found int
	err := tx.QueryRowContext(ctx, query, connectionID, value, folder, epoch, uid).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, ErrLocalWrite
	}
	return true, nil
}

// PendingRetryUIDs includes failed_retryable receipts and newly approved
// unknown-sender receipts in ascending UID order.
func PendingRetryUIDs(ctx context.Context, tx *sql.Tx, epoch string, allowed map[string]bool) ([]uint32, error) {
	if err := checkContext(ctx, tx); err != nil {
		return nil, err
	}
	if !validEpoch.MatchString(epoch) {
		return nil, ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, `SELECT message_uid, status, sender_address
		FROM job_email_message_receipts
		WHERE connection_id = ? AND folder = ? AND uidvalidity = ?
		AND status IN ('failed_retryable', 'unknown_sender') ORDER BY message_uid`,
		connectionID, folder, epoch)
	if err != nil {
		return nil, ErrLocalWrite
	}
	defer rows.Close()
	result := []uint32{}
	for rows.Next() {
		var uid int64
		var status, sender string
		if rows.Scan(&uid, &status, &sender) != nil || uid <= 0 || uid > int64(^uint32(0)) {
			return nil, ErrLocalWrite
		}
		if status == "failed_retryable" || allowed[sender] {
			result = append(result, uint32(uid))
		}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return nil, ErrLocalWrite
	}
	return result, nil
}

func keepString(incoming *string, previous string) string {
	if incoming == nil {
		return previous
	}
	return *incoming
}

func keepFlag(incoming *bool, previous bool) int {
	if incoming == nil {
		if previous {
			return 1
		}
		return 0
	}
	if *incoming {
		return 1
	}
	return 0
}

// Record writes a receipt and optionally advances the cursor atomically in
// the caller's transaction. The caller must commit or roll back the whole
// transaction; a successful return is not a durable commit claim.
func Record(ctx context.Context, tx *sql.Tx, input RecordInput) (int, error) {
	if err := checkContext(ctx, tx); err != nil {
		return 0, err
	}
	if !validEpoch.MatchString(input.UIDValidity) || input.UID == 0 ||
		!validStatus(input.Status) || input.CandidateCount < 0 || input.DiscoveryCount < 0 || input.RecordedAt == "" {
		return 0, ErrInvalid
	}
	previous, err := ByTransport(ctx, tx, input.UIDValidity, input.UID)
	if err != nil {
		return 0, err
	}
	attempts, firstSeen := 1, input.RecordedAt
	var previousDate, previousRoute string
	var previousJob, previousService, previousApplication bool
	if previous != nil {
		if previous.AttemptCount >= math.MaxInt {
			return 0, ErrLocalWrite
		}
		attempts, firstSeen = previous.AttemptCount+1, previous.FirstSeenAt
		previousDate, previousRoute = previous.MessageSentDate, previous.SubjectRoute
		previousJob, previousService = previous.SubjectJobSignal, previous.SubjectServiceSignal
		previousApplication = previous.SubjectApplicationSignal
	}
	route := keepString(input.SubjectRoute, previousRoute)
	if !validRoute(route) {
		return 0, ErrInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO job_email_message_receipts (
		connection_id, folder, uidvalidity, message_uid, message_id_hash,
		content_sha256, sender_address, status, attempt_count, candidate_count,
		discovery_count, error_code, first_seen_at, updated_at, message_sent_date,
		subject_job_signal, subject_service_signal, subject_application_signal,
		subject_route
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(connection_id, folder, uidvalidity, message_uid) DO UPDATE SET
		message_id_hash = excluded.message_id_hash,
		content_sha256 = excluded.content_sha256,
		sender_address = excluded.sender_address, status = excluded.status,
		attempt_count = excluded.attempt_count,
		candidate_count = excluded.candidate_count,
		discovery_count = excluded.discovery_count,
		error_code = excluded.error_code,
		message_sent_date = excluded.message_sent_date,
		subject_job_signal = excluded.subject_job_signal,
		subject_service_signal = excluded.subject_service_signal,
		subject_application_signal = excluded.subject_application_signal,
		subject_route = excluded.subject_route, updated_at = excluded.updated_at`,
		connectionID, folder, input.UIDValidity, input.UID, input.MessageIDHash,
		input.ContentSHA256, input.SenderAddress, input.Status, attempts,
		input.CandidateCount, input.DiscoveryCount, safeErrorCode(input.ErrorCode),
		firstSeen, input.RecordedAt, keepString(input.MessageSentDate, previousDate),
		keepFlag(input.SubjectJobSignal, previousJob),
		keepFlag(input.SubjectServiceSignal, previousService),
		keepFlag(input.SubjectApplicationSignal, previousApplication), route,
	)
	if err != nil {
		return 0, ErrLocalWrite
	}
	if input.AdvanceCursor {
		updated, err := tx.ExecContext(ctx, `UPDATE job_email_sync_state
			SET last_uid = MAX(last_uid, ?), revision = revision + 1
			WHERE connection_id = ? AND folder = ? AND uidvalidity = ?`,
			input.UID, connectionID, folder, input.UIDValidity)
		if err != nil {
			return 0, ErrLocalWrite
		}
		rows, err := updated.RowsAffected()
		if err != nil || rows != 1 {
			return 0, ErrNotConfigured
		}
	}
	return attempts, nil
}
