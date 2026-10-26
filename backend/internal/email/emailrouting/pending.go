package emailrouting

import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

// ErrPendingUnavailable does not expose stored sender addresses or SQL details.
var ErrPendingUnavailable = errors.New("EMAIL_CONNECTION_FAILED")

// PendingSender is the public source-shaped unknown-sender suggestion. It
// contains aggregate receipt facts, never a message subject or body.
type PendingSender struct {
	SenderAddress       string   `json:"sender_address"`
	MessageCount        int      `json:"message_count"`
	FirstSeenAt         string   `json:"first_seen_at"`
	LastSeenAt          string   `json:"last_seen_at"`
	DistinctMessageDays int      `json:"distinct_message_days"`
	ObservedSpanDays    int      `json:"observed_span_days"`
	Recommendation      string   `json:"recommendation"`
	SuggestionReasons   []string `json:"suggestion_reasons"`
}

// LoadPendingSenders reads the Python _pending_senders projection from an
// already-owned current-schema read transaction. The caller admits and pins
// the Home and holds this transaction across the complete connection snapshot.
func LoadPendingSenders(ctx context.Context, tx *sql.Tx) ([]PendingSender, error) {
	if ctx == nil || tx == nil || ctx.Err() != nil {
		return nil, ErrPendingUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT receipts.sender_address, COUNT(*) AS message_count,
		MIN(receipts.first_seen_at) AS first_seen_at,
		MAX(receipts.updated_at) AS last_seen_at,
		COUNT(DISTINCT NULLIF(receipts.message_sent_date, '')) AS distinct_message_days,
		MIN(NULLIF(receipts.message_sent_date, '')) AS first_message_date,
		MAX(NULLIF(receipts.message_sent_date, '')) AS last_message_date,
		SUM(receipts.subject_job_signal) AS subject_job_signal_count,
		SUM(receipts.subject_service_signal) AS subject_service_signal_count,
		SUM(receipts.subject_application_signal) AS subject_application_signal_count
		FROM job_email_message_receipts AS receipts
		LEFT JOIN job_email_sender_rules AS rules
		  ON rules.connection_id = receipts.connection_id
		 AND rules.sender_address = receipts.sender_address
		WHERE receipts.connection_id = ?
		  AND receipts.status = 'unknown_sender'
		  AND receipts.sender_address <> ''
		  AND COALESCE(rules.disposition, '') <> 'disabled'
		GROUP BY receipts.sender_address
		ORDER BY last_seen_at DESC, receipts.sender_address`, "primary")
	if err != nil {
		return nil, ErrPendingUnavailable
	}
	defer rows.Close()
	pending := make([]PendingSender, 0)
	for rows.Next() {
		var sender PendingSender
		var firstDate, lastDate sql.NullString
		var jobSignal, serviceSignal, applicationSignal sql.NullInt64
		if err := rows.Scan(&sender.SenderAddress, &sender.MessageCount,
			&sender.FirstSeenAt, &sender.LastSeenAt, &sender.DistinctMessageDays,
			&firstDate, &lastDate, &jobSignal, &serviceSignal, &applicationSignal); err != nil {
			return nil, ErrPendingUnavailable
		}
		suggestion := SuggestUnknownSender(UnknownSenderSignals{
			SenderAddress:                 sender.SenderAddress,
			MessageCount:                  sender.MessageCount,
			DistinctMessageDays:           sender.DistinctMessageDays,
			FirstMessageDate:              firstDate.String,
			LastMessageDate:               lastDate.String,
			SubjectJobSignalCount:         int(jobSignal.Int64),
			SubjectServiceSignalCount:     int(serviceSignal.Int64),
			SubjectApplicationSignalCount: int(applicationSignal.Int64),
		})
		sender.ObservedSpanDays = suggestion.ObservedSpanDays
		sender.Recommendation = suggestion.Recommendation
		sender.SuggestionReasons = suggestion.SuggestionReasons
		pending = append(pending, sender)
	}
	if rows.Err() != nil || rows.Close() != nil {
		return nil, ErrPendingUnavailable
	}
	// Python's second stable sort ranks recommendations but retains the SQL
	// last_seen_at/address order within each rank.
	sort.SliceStable(pending, func(i, j int) bool {
		return suggestionRank(pending[i].Recommendation) < suggestionRank(pending[j].Recommendation)
	})
	return pending, nil
}

func suggestionRank(recommendation string) int {
	switch recommendation {
	case "likely_application_tracking":
		return 0
	case "likely_job_alert":
		return 1
	case "recurring_review":
		return 2
	default:
		return 3
	}
}
