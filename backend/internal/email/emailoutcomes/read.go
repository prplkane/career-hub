package emailoutcomes

import (
	"context"
	"database/sql"
	"fmt"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications"
)

// UpdatePage is the minimized Applications inbox projection. It never reads
// message content, credentials, or the complete outcome evidence record.
type UpdatePage struct {
	Total               int64        `json:"total"`
	NextCursor          *string      `json:"next_cursor"`
	ApplicationsUpdated int64        `json:"applications_updated"`
	Unlinked            int64        `json:"unlinked"`
	NeedsReview         int64        `json:"needs_review"`
	Items               []UpdateItem `json:"items"`
}

type UpdateItem struct {
	OutcomeID          string `json:"outcome_id"`
	SourceProvider     string `json:"source_provider"`
	Title              string `json:"title"`
	Company            string `json:"company"`
	Location           string `json:"location"`
	ApplicationStatus  string `json:"application_status"`
	ObservedAt         string `json:"observed_at"`
	Resolution         string `json:"resolution"`
	Revision           int64  `json:"revision"`
	MatchedDiscoveryID string `json:"matched_discovery_id"`
	MatchedJobID       string `json:"matched_job_id"`
}

type UpdateReadError struct{ Code string }

func (e *UpdateReadError) Error() string { return "Email application updates are unavailable." }

func updateReadFailure(code string) *UpdateReadError { return &UpdateReadError{Code: code} }

// ReadUpdatePage uses one read transaction for counts and page rows. The
// caller owns Data Home, path, SQLite-file and post-cleanup admission checks.
func ReadUpdatePage(ctx context.Context, db *sql.DB, offset int64, limit int) (UpdatePage, *UpdateReadError) {
	empty := UpdatePage{}
	if ctx == nil || ctx.Err() != nil {
		return empty, updateReadFailure("CANCELED")
	}
	if db == nil || offset < 0 || limit < 1 || limit > 100 {
		return empty, updateReadFailure("INPUT_INVALID")
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return empty, updateReadFailure("READ_FAILED")
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return empty, updateReadFailure("READ_FAILED")
	}
	if version < applications.SchemaVersion {
		return empty, updateReadFailure("SCHEMA_PREPARATION_REQUIRED")
	}
	if version > applications.SchemaVersion {
		return empty, updateReadFailure("SCHEMA_TOO_NEW")
	}
	page := UpdatePage{Items: []UpdateItem{}}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN resolution='application_updated' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN resolution IN ('unmatched','discovery_dismissed') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN resolution IN ('status_conflict','ambiguous') THEN 1 ELSE 0 END),0)
		FROM job_email_application_outcomes`).Scan(&page.Total, &page.ApplicationsUpdated, &page.Unlinked, &page.NeedsReview); err != nil {
		return empty, updateReadFailure("READ_FAILED")
	}
	if page.Total < 0 || page.ApplicationsUpdated < 0 || page.Unlinked < 0 || page.NeedsReview < 0 {
		return empty, updateReadFailure("VALUE_INVALID")
	}
	rows, err := tx.QueryContext(ctx, `SELECT outcome_id,source_provider,title,company,location,
		application_status,observed_at,resolution,revision,matched_discovery_id,matched_job_id
		FROM job_email_application_outcomes
		ORDER BY CASE WHEN resolution IN ('unmatched','discovery_dismissed','ambiguous','status_conflict') THEN 0 ELSE 1 END,
			observed_at DESC,outcome_id DESC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return empty, updateReadFailure("READ_FAILED")
	}
	for rows.Next() {
		item := UpdateItem{}
		var discovery, job sql.NullString
		if err := rows.Scan(&item.OutcomeID, &item.SourceProvider, &item.Title, &item.Company,
			&item.Location, &item.ApplicationStatus, &item.ObservedAt, &item.Resolution,
			&item.Revision, &discovery, &job); err != nil {
			rows.Close()
			return empty, updateReadFailure("READ_FAILED")
		}
		item.MatchedDiscoveryID, item.MatchedJobID = discovery.String, job.String
		if !validUpdateItem(item) {
			rows.Close()
			return empty, updateReadFailure("VALUE_INVALID")
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return empty, updateReadFailure("READ_FAILED")
	}
	if err := rows.Close(); err != nil {
		return empty, updateReadFailure("READ_FAILED")
	}
	if int64(len(page.Items)) < page.Total-offset && offset < page.Total {
		cursor := fmt.Sprintf("offset:%d", offset+int64(len(page.Items)))
		page.NextCursor = &cursor
	}
	if ctx.Err() != nil {
		return empty, updateReadFailure("CANCELED")
	}
	if err := tx.Commit(); err != nil {
		return empty, updateReadFailure("READ_FAILED")
	}
	return page, nil
}

func validUpdateItem(item UpdateItem) bool {
	for _, field := range []struct {
		value string
		limit int
	}{
		{item.OutcomeID, 128}, {item.SourceProvider, 64}, {item.Title, 500},
		{item.Company, 500}, {item.Location, 500}, {item.ApplicationStatus, 64},
		{item.ObservedAt, 64}, {item.Resolution, 64},
		{item.MatchedDiscoveryID, 500}, {item.MatchedJobID, 500},
	} {
		if !utf8.ValidString(field.value) || utf8.RuneCountInString(field.value) > field.limit {
			return false
		}
	}
	return item.OutcomeID != "" && item.Revision > 0
}
