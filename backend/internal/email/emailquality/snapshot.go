// Package emailquality reads the active email-card warning summary. The
// caller owns Data Home admission, path pins and the SQLite transaction.
package emailquality

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/email/emailrouting"
)

const (
	captureQualityVersion = 2
	maxMetadataBytes      = 4096 // The source writer's metadata JSON limit.
)

// ErrSnapshotUnavailable never includes metadata, URLs, sender addresses or SQL.
var ErrSnapshotUnavailable = errors.New("EMAIL_CONNECTION_FAILED")

type StatusCounts struct {
	SuspectedNoise int `json:"suspected_noise"`
	NeedsReview    int `json:"needs_review"`
	Limited        int `json:"limited"`
}

type Alert struct {
	SenderAddress     string       `json:"sender_address"`
	Purpose           string       `json:"purpose"`
	IssueCount        int          `json:"issue_count"`
	StatusCounts      StatusCounts `json:"status_counts"`
	SampleDiscoveryID string       `json:"sample_discovery_id"`
}

type Snapshot struct {
	Version          int     `json:"version"`
	ActiveIssueCount int     `json:"active_issue_count"`
	SenderCount      int     `json:"sender_count"`
	Alerts           []Alert `json:"alerts"`
}

// LoadSnapshot ports Python _capture_quality_snapshot without creating a
// transaction or reading email content. Only active email observations whose
// receipt hash maps to an approved exact sender contribute.
func LoadSnapshot(ctx context.Context, tx *sql.Tx) (Snapshot, error) {
	if ctx == nil || tx == nil || ctx.Err() != nil {
		return Snapshot{}, ErrSnapshotUnavailable
	}
	allowed, err := emailrouting.LoadAllowed(ctx, tx)
	if err != nil {
		return Snapshot{}, ErrSnapshotUnavailable
	}
	senders, err := receiptSenders(ctx, tx)
	if err != nil {
		return Snapshot{}, ErrSnapshotUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT discoveries.discovery_id,
		COALESCE(overrides.title, discoveries.title) AS title,
		COALESCE(overrides.company, discoveries.company) AS company,
		COALESCE(overrides.location, discoveries.location) AS location,
		COALESCE(NULLIF(discoveries.canonical_url, ''), discoveries.observed_url) AS source_url,
		observations.metadata_json
		FROM job_discoveries AS discoveries
		JOIN job_discovery_observations AS observations
		  ON observations.discovery_id = discoveries.discovery_id
		 AND observations.source_kind = 'email_file'
		LEFT JOIN job_discovery_field_overrides AS overrides
		  ON overrides.discovery_id = discoveries.discovery_id
		WHERE discoveries.state IN ('inbox', 'shortlisted')
		ORDER BY observations.observation_id`)
	if err != nil {
		return Snapshot{}, ErrSnapshotUnavailable
	}
	defer rows.Close()
	alerts := map[string]*Alert{}
	sampleRanks := map[string]int{}
	seen := map[[2]string]bool{}
	issueIDs := map[string]bool{}
	for rows.Next() {
		var id, metadata string
		var title, company, location, sourceURL sql.NullString
		if err := rows.Scan(&id, &title, &company, &location, &sourceURL, &metadata); err != nil {
			return Snapshot{}, ErrSnapshotUnavailable
		}
		hash := messageHash(metadata)
		sender := senders[hash]
		rule, approved := allowed[sender]
		if hash == "" || sender == "" || !approved {
			continue
		}
		pair := [2]string{sender, id}
		if seen[pair] {
			continue
		}
		seen[pair] = true
		quality, err := discovery.ClassifyEmailQuality(discovery.QualityInput{
			Title: title.String, Company: company.String,
			Location: location.String, SourceURL: sourceURL.String,
		})
		if err != nil {
			return Snapshot{}, ErrSnapshotUnavailable
		}
		if quality.Status == "ok" {
			continue
		}
		rank, known := severityRank(quality.Status)
		if !known {
			return Snapshot{}, ErrSnapshotUnavailable
		}
		issueIDs[id] = true
		alert := alerts[sender]
		if alert == nil {
			alert = &Alert{SenderAddress: sender, Purpose: rule.Purpose, SampleDiscoveryID: id}
			alerts[sender] = alert
			sampleRanks[sender] = rank
		}
		alert.IssueCount++
		switch quality.Status {
		case "suspected_noise":
			alert.StatusCounts.SuspectedNoise++
		case "needs_review":
			alert.StatusCounts.NeedsReview++
		case "limited":
			alert.StatusCounts.Limited++
		}
		if rank < sampleRanks[sender] {
			alert.SampleDiscoveryID = id
			sampleRanks[sender] = rank
		}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return Snapshot{}, ErrSnapshotUnavailable
	}
	result := Snapshot{Version: captureQualityVersion, ActiveIssueCount: len(issueIDs), Alerts: make([]Alert, 0, len(alerts))}
	for _, alert := range alerts {
		result.Alerts = append(result.Alerts, *alert)
	}
	sort.Slice(result.Alerts, func(i, j int) bool {
		a, b := result.Alerts[i], result.Alerts[j]
		if a.StatusCounts.SuspectedNoise != b.StatusCounts.SuspectedNoise {
			return a.StatusCounts.SuspectedNoise > b.StatusCounts.SuspectedNoise
		}
		if a.StatusCounts.NeedsReview != b.StatusCounts.NeedsReview {
			return a.StatusCounts.NeedsReview > b.StatusCounts.NeedsReview
		}
		if a.IssueCount != b.IssueCount {
			return a.IssueCount > b.IssueCount
		}
		return a.SenderAddress < b.SenderAddress
	})
	result.SenderCount = len(result.Alerts)
	return result, nil
}

func receiptSenders(ctx context.Context, tx *sql.Tx) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT message_id_hash, sender_address
		FROM job_email_message_receipts
		WHERE connection_id = ? AND message_id_hash <> '' AND sender_address <> ''
		ORDER BY receipt_id`, "primary")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	senders := make(map[string]string)
	for rows.Next() {
		var hash, sender string
		if err := rows.Scan(&hash, &sender); err != nil {
			return nil, err
		}
		senders[hash] = sender // The last receipt wins, as in Python's dict.
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return senders, nil
}

func messageHash(metadata string) string {
	if len(metadata) > maxMetadataBytes {
		return ""
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(metadata), &object); err != nil || object == nil {
		return ""
	}
	hash, _ := object["message_id_hash"].(string)
	return hash
}

func severityRank(status string) (int, bool) {
	switch status {
	case "suspected_noise":
		return 0, true
	case "needs_review":
		return 1, true
	case "limited":
		return 2, true
	default:
		return 0, false
	}
}
