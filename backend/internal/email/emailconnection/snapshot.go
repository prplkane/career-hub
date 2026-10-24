package emailconnection

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"careerhub.local/backend/internal/email/emailquality"
	"careerhub.local/backend/internal/email/emailrouting"
	"careerhub.local/backend/internal/textcompat"
)

// ErrSnapshotUnavailable does not reveal account addresses, senders, SQL,
// credential targets or message content.
var ErrSnapshotUnavailable = errors.New("EMAIL_CONNECTION_FAILED")

type SubjectRoutePolicy struct {
	IncludeAny []string `json:"include_any"`
	ExcludeAny []string `json:"exclude_any"`
}

type SubjectPolicy struct {
	JobAlerts           SubjectRoutePolicy `json:"job_alerts"`
	ApplicationTracking SubjectRoutePolicy `json:"application_tracking"`
}

type ObservedHeaders struct {
	MessageCount           int `json:"message_count"`
	JobSignalCount         int `json:"job_signal_count"`
	ApplicationSignalCount int `json:"application_signal_count"`
	ServiceSignalCount     int `json:"service_signal_count"`
	FilteredCount          int `json:"filtered_count"`
}

type SnapshotSenderRule struct {
	SenderAddress   string          `json:"sender_address"`
	Disposition     string          `json:"disposition"`
	Purpose         string          `json:"purpose"`
	SubjectPolicy   SubjectPolicy   `json:"subject_policy"`
	ObservedHeaders ObservedHeaders `json:"observed_headers"`
}

type SyncSnapshot struct {
	UIDValidityKnown    bool    `json:"uidvalidity_known"`
	LastUID             int64   `json:"last_uid"`
	LastStartedAt       *string `json:"last_started_at"`
	LastCompletedAt     *string `json:"last_completed_at"`
	LastResult          string  `json:"last_result"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	NextRetryAt         *string `json:"next_retry_at"`
	LastErrorCode       string  `json:"last_error_code"`
	Revision            int64   `json:"revision"`
}

type ReceiptSummary struct {
	Total             int            `json:"total"`
	Imported          int            `json:"imported"`
	NoCandidates      int            `json:"no_candidates"`
	Duplicate         int            `json:"duplicate"`
	UnknownSender     int            `json:"unknown_sender"`
	FilteredSubject   int            `json:"filtered_subject"`
	Failed            int            `json:"failed"`
	FailureCodeCounts map[string]int `json:"failure_code_counts"`
}

type ConnectionSnapshot struct {
	ContractVersion    int                          `json:"contract_version"`
	Configured         bool                         `json:"configured"`
	Provider           string                       `json:"provider"`
	AccountAddress     string                       `json:"account_address"`
	Folder             string                       `json:"folder"`
	Enabled            bool                         `json:"enabled"`
	ScheduleEnabled    bool                         `json:"schedule_enabled"`
	IntervalMinutes    int                          `json:"interval_minutes"`
	CredentialPresent  bool                         `json:"credential_present"`
	LookbackDays       int                          `json:"lookback_days"`
	MaxMessagesPerSync int                          `json:"max_messages_per_sync"`
	LastTestedAt       *string                      `json:"last_tested_at"`
	LastTestStatus     string                       `json:"last_test_status"`
	LastErrorCode      string                       `json:"last_error_code"`
	Revision           int64                        `json:"revision"`
	SenderRules        []SnapshotSenderRule         `json:"sender_rules"`
	PendingSenders     []emailrouting.PendingSender `json:"pending_senders"`
	CaptureQuality     emailquality.Snapshot        `json:"capture_quality"`
	Sync               SyncSnapshot                 `json:"sync"`
	Receipts           ReceiptSummary               `json:"receipts"`
}

// ReadSnapshot composes Python _connection_snapshot inside one caller-owned
// current-schema read transaction. The caller must independently admit and
// pin the Home, validate its credential target and supply only its presence.
// This function never reads or returns a credential target or secret.
func ReadSnapshot(ctx context.Context, tx *sql.Tx, credentialPresent bool) (ConnectionSnapshot, error) {
	if ctx == nil || tx == nil || ctx.Err() != nil {
		return ConnectionSnapshot{}, ErrSnapshotUnavailable
	}
	result := ConnectionSnapshot{
		ContractVersion: 2, Provider: "gmail_imap", Folder: "INBOX",
		IntervalMinutes: 30, CredentialPresent: credentialPresent,
		LookbackDays: 14, MaxMessagesPerSync: 250, LastTestStatus: "not_tested",
		SenderRules: []SnapshotSenderRule{}, PendingSenders: []emailrouting.PendingSender{},
		CaptureQuality: emailquality.Snapshot{Version: 2, Alerts: []emailquality.Alert{}},
	}
	var testedAt sql.NullString
	var enabled, scheduleEnabled int
	err := tx.QueryRowContext(ctx, `SELECT provider, account_address, folder, enabled,
		schedule_enabled, interval_minutes, lookback_days, max_messages_per_sync,
		last_tested_at, last_test_status, last_error_code, revision
		FROM job_email_connections WHERE connection_id = ?`, "primary").Scan(
		&result.Provider, &result.AccountAddress, &result.Folder, &enabled,
		&scheduleEnabled, &result.IntervalMinutes, &result.LookbackDays,
		&result.MaxMessagesPerSync, &testedAt, &result.LastTestStatus,
		&result.LastErrorCode, &result.Revision,
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ConnectionSnapshot{}, ErrSnapshotUnavailable
	}
	if err == nil {
		if !validSnapshotCode(result.LastErrorCode) {
			return ConnectionSnapshot{}, ErrSnapshotUnavailable
		}
		result.Configured = true
		result.Enabled = enabled != 0
		result.ScheduleEnabled = scheduleEnabled != 0
		result.LastTestedAt = nullableText(testedAt)
		result.SenderRules, err = readSnapshotSenderRules(ctx, tx)
		if err != nil {
			return ConnectionSnapshot{}, ErrSnapshotUnavailable
		}
		result.PendingSenders, err = emailrouting.LoadPendingSenders(ctx, tx)
		if err != nil {
			return ConnectionSnapshot{}, ErrSnapshotUnavailable
		}
		result.CaptureQuality, err = emailquality.LoadSnapshot(ctx, tx)
		if err != nil {
			return ConnectionSnapshot{}, ErrSnapshotUnavailable
		}
	}
	result.Sync, err = readSyncSnapshot(ctx, tx, result.Folder)
	if err != nil {
		return ConnectionSnapshot{}, ErrSnapshotUnavailable
	}
	result.Receipts, err = readReceiptSummary(ctx, tx)
	if err != nil {
		return ConnectionSnapshot{}, ErrSnapshotUnavailable
	}
	return result, nil
}

func readSnapshotSenderRules(ctx context.Context, tx *sql.Tx) ([]SnapshotSenderRule, error) {
	rows, err := tx.QueryContext(ctx, `SELECT rules.sender_address, rules.disposition,
		rules.purpose, rules.subject_policy_json,
		COUNT(receipts.receipt_id) AS message_count,
		COALESCE(SUM(receipts.subject_job_signal), 0) AS subject_job_signal_count,
		COALESCE(SUM(receipts.subject_application_signal), 0) AS subject_application_signal_count,
		COALESCE(SUM(receipts.subject_service_signal), 0) AS subject_service_signal_count,
		COALESCE(SUM(CASE WHEN receipts.subject_route = 'filtered' THEN 1 ELSE 0 END), 0) AS subject_filtered_count
		FROM job_email_sender_rules AS rules
		LEFT JOIN job_email_message_receipts AS receipts
		  ON receipts.connection_id = rules.connection_id
		 AND receipts.sender_address = rules.sender_address
		WHERE rules.connection_id = ?
		GROUP BY rules.sender_address, rules.disposition, rules.purpose, rules.subject_policy_json
		ORDER BY rules.disposition, rules.purpose, rules.sender_address`, "primary")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := make([]SnapshotSenderRule, 0)
	for rows.Next() {
		var rule SnapshotSenderRule
		var rawPolicy string
		if err := rows.Scan(&rule.SenderAddress, &rule.Disposition, &rule.Purpose,
			&rawPolicy, &rule.ObservedHeaders.MessageCount,
			&rule.ObservedHeaders.JobSignalCount,
			&rule.ObservedHeaders.ApplicationSignalCount,
			&rule.ObservedHeaders.ServiceSignalCount,
			&rule.ObservedHeaders.FilteredCount); err != nil {
			return nil, err
		}
		policy, err := emailrouting.ParsePolicyJSON(rawPolicy)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(policy.CanonicalJSON()), &rule.SubjectPolicy); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return rules, nil
}

func readSyncSnapshot(ctx context.Context, tx *sql.Tx, folder string) (SyncSnapshot, error) {
	result := SyncSnapshot{LastResult: "never"}
	var uidValidity string
	var started, completed, retry sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT uidvalidity, last_uid, last_started_at,
		last_completed_at, last_result, consecutive_failures, next_retry_at,
		last_error_code, revision FROM job_email_sync_state
		WHERE connection_id = ? AND folder = ?`, "primary", folder).Scan(
		&uidValidity, &result.LastUID, &started, &completed,
		&result.LastResult, &result.ConsecutiveFailures, &retry,
		&result.LastErrorCode, &result.Revision,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return SyncSnapshot{}, err
	}
	if !validSnapshotCode(result.LastErrorCode) {
		return SyncSnapshot{}, ErrSnapshotUnavailable
	}
	result.UIDValidityKnown = uidValidity != ""
	result.LastStartedAt = nullableText(started)
	result.LastCompletedAt = nullableText(completed)
	result.NextRetryAt = nullableText(retry)
	return result, nil
}

func readReceiptSummary(ctx context.Context, tx *sql.Tx) (ReceiptSummary, error) {
	result := ReceiptSummary{FailureCodeCounts: map[string]int{}}
	rows, err := tx.QueryContext(ctx, `SELECT status, error_code, COUNT(*) AS count
		FROM job_email_message_receipts WHERE connection_id = ?
		GROUP BY status, error_code`, "primary")
	if err != nil {
		return ReceiptSummary{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var status, code string
		var count int
		if err := rows.Scan(&status, &code, &count); err != nil {
			return ReceiptSummary{}, err
		}
		result.Total += count
		if code == "EMAIL_SUBJECT_POLICY_FILTERED" {
			result.FilteredSubject += count
		}
		switch status {
		case "imported":
			result.Imported += count
		case "no_candidates":
			result.NoCandidates += count
		case "duplicate":
			result.Duplicate += count
		case "unknown_sender":
			result.UnknownSender += count
		case "failed_retryable", "failed_final", "too_large":
			result.Failed += count
			fallback := "EMAIL_IMPORT_FAILED"
			if status == "too_large" {
				fallback = "EMAIL_TOO_LARGE"
			}
			result.FailureCodeCounts[safeSnapshotErrorCode(code, fallback)] += count
		}
	}
	if rows.Err() != nil {
		return ReceiptSummary{}, rows.Err()
	}
	if err := rows.Close(); err != nil {
		return ReceiptSummary{}, err
	}
	return result, nil
}

func safeSnapshotErrorCode(value, fallback string) string {
	code := strings.ToUpper(strings.TrimFunc(value, textcompat.IsSpace))
	if code == "" {
		return fallback
	}
	if !strings.HasPrefix(code, "EMAIL_") || len(code) < 7 || len(code) > 86 {
		return fallback
	}
	for _, char := range code[6:] {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' {
			return fallback
		}
	}
	return code
}

func validSnapshotCode(value string) bool {
	return value == "" || safeSnapshotErrorCode(value, "") == value
}

func nullableText(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	copy := value.String
	return &copy
}
