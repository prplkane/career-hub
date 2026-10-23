package emailoutcomes

import (
	"context"
	"database/sql"
	"errors"
	"regexp"

	"careerhub.local/backend/internal/email/emailcandidates"
	"careerhub.local/backend/internal/jobs/joburls"
)

const maxApplicationScan = 10_000

var appliedLocationSuffix = regexp.MustCompile(`(?i)\s+applied\s+on\s+[a-z]{3,9}\s+[0-9]{1,2}(?:,\s+[0-9]{4})?$`)

// StoreError contains a fixed code only. SQL and private outcome text are
// never exposed to the caller as error strings.
type StoreError struct{ Code string }

func (e *StoreError) Error() string  { return e.Code }
func storeFailure(code string) error { return &StoreError{Code: code} }

// ApplyStatus is supplied by the single application owner. It must mutate
// within tx, must not commit, and must report whether it changed the status.
// The native adapter calls applications.StatusInTransaction, which also
// records the applied-resume snapshot when appropriate.
type ApplyStatus func(context.Context, *sql.Tx, string, string, string, string, string) (bool, error)

type Result struct {
	OutcomeID          string `json:"outcome_id"`
	SourceProvider     string `json:"source_provider"`
	PostingIdentity    string `json:"posting_identity"`
	SourceURL          string `json:"source_url"`
	Title              string `json:"title"`
	Company            string `json:"company"`
	Location           string `json:"location"`
	ApplicationStatus  string `json:"application_status"`
	ObservedAt         string `json:"observed_at"`
	Resolution         string `json:"resolution"`
	Revision           int64  `json:"revision"`
	MatchedDiscoveryID string `json:"matched_discovery_id"`
	MatchedJobID       string `json:"matched_job_id"`
	ApplicationChanged bool   `json:"application_changed"`
	DiscoveryChanged   bool   `json:"discovery_changed"`
	Duplicate          bool   `json:"duplicate"`
}

type stored struct {
	result     Result
	resolution string
	evidence   string
}

func readStored(ctx context.Context, tx *sql.Tx, id string) (*stored, error) {
	var value stored
	var discoveryID, jobID sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT outcome_id, source_provider,
		posting_identity, source_url, title, company, location, application_status, evidence_code,
		observed_at, resolution, revision, matched_discovery_id, matched_job_id
		FROM job_email_application_outcomes WHERE outcome_id=?`, id).Scan(
		&value.result.OutcomeID, &value.result.SourceProvider,
		&value.result.PostingIdentity, &value.result.SourceURL,
		&value.result.Title, &value.result.Company, &value.result.Location,
		&value.result.ApplicationStatus, &value.evidence, &value.result.ObservedAt,
		&value.result.Resolution, &value.result.Revision, &discoveryID, &jobID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, storeFailure("EMAIL_OUTCOME_QUERY_FAILED")
	}
	if value.result.Revision < 1 {
		return nil, storeFailure("EMAIL_OUTCOME_VALUE_INVALID")
	}
	value.result.MatchedDiscoveryID = discoveryID.String
	value.result.MatchedJobID = jobID.String
	value.resolution = value.result.Resolution
	return &value, nil
}

type discoveryMatch struct {
	id, state, location string
	promotedJobID       sql.NullString
}

func exactDiscovery(ctx context.Context, tx *sql.Tx, identity string) (*discoveryMatch, error) {
	if identity == "" {
		return nil, nil
	}
	var value discoveryMatch
	err := tx.QueryRowContext(ctx, `SELECT discovery_id,state,location,promoted_job_id
		FROM job_discoveries WHERE posting_identity=?`, identity).Scan(
		&value.id, &value.state, &value.location, &value.promotedJobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, storeFailure("EMAIL_OUTCOME_QUERY_FAILED")
	}
	return &value, nil
}

func applicationMatches(ctx context.Context, tx *sql.Tx, identity string, discovery *discoveryMatch) (map[string]bool, error) {
	matches := make(map[string]bool)
	if identity == "" {
		return matches, nil
	}
	if discovery != nil && discovery.promotedJobID.Valid {
		matches[discovery.promotedJobID.String] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT job_id,source_url FROM job_applications
		ORDER BY job_id LIMIT ?`, maxApplicationScan+1)
	if err != nil {
		return nil, storeFailure("EMAIL_OUTCOME_QUERY_FAILED")
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if count > maxApplicationScan {
			return nil, storeFailure("EMAIL_OUTCOME_SCAN_LIMIT")
		}
		var jobID, sourceURL string
		if err := rows.Scan(&jobID, &sourceURL); err != nil {
			return nil, storeFailure("EMAIL_OUTCOME_QUERY_FAILED")
		}
		posting, err := joburls.Resolve(sourceURL)
		if err != nil {
			return nil, storeFailure("EMAIL_OUTCOME_VALUE_INVALID")
		}
		if posting.Identity == identity {
			matches[jobID] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, storeFailure("EMAIL_OUTCOME_QUERY_FAILED")
	}
	return matches, nil
}

func shouldDismiss(discovery *discoveryMatch, event Normalized) (bool, bool) {
	if event.ApplicationStatus != "rejected" || discovery == nil || discovery.promotedJobID.Valid {
		return false, false
	}
	dismiss := discovery.state == "inbox" || discovery.state == "shortlisted"
	cleanLocation := event.Location != "" && event.Location != discovery.location &&
		appliedLocationSuffix.MatchString(discovery.location) && !appliedLocationSuffix.MatchString(event.Location)
	return dismiss, cleanLocation
}

func statusResolution(ctx context.Context, tx *sql.Tx, event Normalized, jobID, recordedAt string, linkedByUser bool, apply ApplyStatus) (string, bool, error) {
	var current string
	err := tx.QueryRowContext(ctx, `SELECT current_status FROM job_applications WHERE job_id=?`, jobID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return "unmatched", false, nil
	}
	if err != nil {
		return "", false, storeFailure("EMAIL_OUTCOME_QUERY_FAILED")
	}
	if event.ApplicationStatus == "applied" {
		if current == "applied" || current == "interview" || current == "offer" {
			return "application_already_current", false, nil
		}
		if current != "captured" && current != "reviewed" && current != "preparing" {
			return "status_conflict", false, nil
		}
	} else {
		if current == "rejected" {
			return "application_already_current", false, nil
		}
		if current != "captured" && current != "reviewed" && current != "preparing" && current != "applied" && current != "interview" {
			return "status_conflict", false, nil
		}
	}
	if apply == nil {
		return "", false, storeFailure("EMAIL_OUTCOME_STATUS_OWNER_MISSING")
	}
	source := "LinkedIn"
	if event.Provider == "handshake" {
		source = "Handshake"
	}
	kind := "rejection"
	if event.ApplicationStatus == "applied" {
		kind = "submission"
	}
	note := source + " " + kind + " received by email."
	if linkedByUser {
		note = source + " " + kind + " received by email and linked by the user."
	}
	changed, err := apply(ctx, tx, jobID, event.ApplicationStatus, event.ObservedAt, recordedAt, note)
	if err != nil || !changed {
		return "", false, storeFailure("EMAIL_OUTCOME_STATUS_WRITE_FAILED")
	}
	return "application_updated", true, nil
}

func execOne(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return storeFailure("EMAIL_OUTCOME_WRITE_FAILED")
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return storeFailure("EMAIL_OUTCOME_WRITE_FAILED")
	}
	return nil
}

// RecordInTransaction applies one already parsed signal under the caller's
// existing owner and transaction. It never opens a DB, commits or publishes a
// file. On any error the caller must roll back the whole transaction.
func RecordInTransaction(
	ctx context.Context, tx *sql.Tx, fingerprint string, raw emailcandidates.ApplicationOutcome,
	observedAt, recordedAt string, apply ApplyStatus,
) (Result, bool, error) {
	if ctx == nil || tx == nil || ctx.Err() != nil {
		return Result{}, false, storeFailure("EMAIL_OUTCOME_CONNECTION_INVALID")
	}
	event, err := Normalize(fingerprint, raw, observedAt, recordedAt)
	if err != nil {
		return Result{}, false, ErrInvalid
	}
	storedAt, err := eventTime(recordedAt, recordedAt)
	if err != nil {
		return Result{}, false, ErrInvalid
	}
	existing, err := readStored(ctx, tx, event.ID)
	if err != nil {
		return Result{}, false, err
	}
	if existing != nil && existing.resolution != "unmatched" && existing.resolution != "discovery_dismissed" {
		result := existing.result
		result.Duplicate = true
		return result, false, nil
	}
	discovery, err := exactDiscovery(ctx, tx, event.PostingIdentity)
	if err != nil {
		return Result{}, false, err
	}
	discoveryID := ""
	discoveryChanged := false
	if discovery != nil {
		discoveryID = discovery.id
		dismiss, cleanLocation := shouldDismiss(discovery, event)
		if dismiss || cleanLocation {
			state, location := discovery.state, discovery.location
			if dismiss {
				state = "dismissed"
			}
			if cleanLocation {
				location = event.Location
			}
			if err := execOne(ctx, tx, `UPDATE job_discoveries SET state=?,location=?,updated_at=?,revision=revision+1
				WHERE discovery_id=?`, state, location, storedAt, discovery.id); err != nil {
				return Result{}, false, err
			}
			discoveryChanged = true
		}
	}
	matches, err := applicationMatches(ctx, tx, event.PostingIdentity, discovery)
	if err != nil {
		return Result{}, false, err
	}
	matchedJobID, resolution, applicationChanged := "", "", false
	switch len(matches) {
	case 0:
		if discoveryID != "" && event.ApplicationStatus == "rejected" {
			resolution = "discovery_dismissed"
		} else {
			resolution = "unmatched"
		}
	case 1:
		for id := range matches {
			matchedJobID = id
		}
		resolution, applicationChanged, err = statusResolution(ctx, tx, event, matchedJobID, storedAt, false, apply)
		if err != nil {
			return Result{}, false, err
		}
		if resolution == "unmatched" {
			matchedJobID = ""
		}
	default:
		resolution = "ambiguous"
	}
	if existing != nil && !applicationChanged && !discoveryChanged &&
		existing.result.Resolution == resolution && existing.result.MatchedDiscoveryID == discoveryID &&
		existing.result.MatchedJobID == matchedJobID {
		result := existing.result
		result.Duplicate = true
		return result, false, nil
	}
	var matchedDiscovery, matchedJob any
	if discoveryID != "" {
		matchedDiscovery = discoveryID
	}
	if matchedJobID != "" {
		matchedJob = matchedJobID
	}
	if existing == nil {
		err = execOne(ctx, tx, `INSERT INTO job_email_application_outcomes (
			outcome_id,message_fingerprint,source_provider,posting_identity,source_url,title,
			company,location,application_status,evidence_code,observed_at,resolution,
			matched_discovery_id,matched_job_id,created_at,updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			event.ID, event.MessageFingerprint, event.Provider, event.PostingIdentity,
			event.SourceURL, event.Title, event.Company, event.Location, event.ApplicationStatus,
			event.EvidenceCode, event.ObservedAt, resolution, matchedDiscovery, matchedJob, storedAt, storedAt)
	} else {
		err = execOne(ctx, tx, `UPDATE job_email_application_outcomes SET resolution=?,
			matched_discovery_id=?,matched_job_id=?,updated_at=?,revision=revision+1
			WHERE outcome_id=?`, resolution, matchedDiscovery, matchedJob, storedAt, event.ID)
	}
	if err != nil {
		return Result{}, false, err
	}
	updated, err := readStored(ctx, tx, event.ID)
	if err != nil || updated == nil {
		return Result{}, false, storeFailure("EMAIL_OUTCOME_QUERY_FAILED")
	}
	result := updated.result
	result.ApplicationChanged, result.DiscoveryChanged = applicationChanged, discoveryChanged
	result.Duplicate = existing != nil && !applicationChanged && !discoveryChanged
	return result, true, nil
}
