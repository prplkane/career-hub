package emailoutcomes

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/email/emailalerts"
	_ "modernc.org/sqlite"
)

func outcomeFixture(t *testing.T, name string) emailalerts.Result {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "email_alerts", name))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := emailalerts.Parse(raw)
	if err != nil || len(parsed.ApplicationOutcomes) != 1 {
		t.Fatalf("parsed=%+v error=%v", parsed, err)
	}
	return parsed
}

func outcomeDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, query := range []string{
		`CREATE TABLE job_applications (job_id TEXT PRIMARY KEY,source_url TEXT NOT NULL,
			current_status TEXT NOT NULL,updated_at TEXT NOT NULL)`,
		`CREATE TABLE application_status_events (event_id INTEGER PRIMARY KEY,
			job_id TEXT,status TEXT,occurred_at TEXT,recorded_at TEXT,note TEXT)`,
		`CREATE TABLE job_discoveries (discovery_id TEXT PRIMARY KEY,
			posting_identity TEXT NOT NULL UNIQUE,state TEXT NOT NULL,location TEXT NOT NULL,
			promoted_job_id TEXT,updated_at TEXT NOT NULL,revision INTEGER NOT NULL)`,
		`CREATE TABLE job_email_application_outcomes (outcome_id TEXT PRIMARY KEY,
			message_fingerprint TEXT NOT NULL,source_provider TEXT NOT NULL,
			posting_identity TEXT NOT NULL,source_url TEXT NOT NULL,title TEXT NOT NULL,
			company TEXT NOT NULL,location TEXT NOT NULL,application_status TEXT NOT NULL,
			evidence_code TEXT NOT NULL,observed_at TEXT NOT NULL,resolution TEXT NOT NULL,
			matched_discovery_id TEXT,matched_job_id TEXT,revision INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,updated_at TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func insertJob(t *testing.T, db *sql.DB, id, url, status string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO job_applications(job_id,source_url,current_status,updated_at)
		VALUES(?,?,?,'t0')`, id, url, status); err != nil {
		t.Fatal(err)
	}
}

func fakeStatus(calls *int) ApplyStatus {
	return func(ctx context.Context, tx *sql.Tx, id, status, occurredAt, recordedAt, note string) (bool, error) {
		*calls++
		if _, err := tx.ExecContext(ctx, `INSERT INTO application_status_events
			(job_id,status,occurred_at,recorded_at,note) VALUES(?,?,?,?,?)`, id, status, occurredAt, recordedAt, note); err != nil {
			return false, err
		}
		_, err := tx.ExecContext(ctx, `UPDATE job_applications SET current_status=?,updated_at=? WHERE job_id=?`, status, recordedAt, id)
		return err == nil, err
	}
}

func runOutcome(t *testing.T, db *sql.DB, parsed emailalerts.Result, at string, apply ApplyStatus) (Result, bool) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, changed, err := RecordInTransaction(ctx, tx, parsed.MessageFingerprint,
		parsed.ApplicationOutcomes[0], parsed.ReceivedAt, at, apply)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	return result, changed
}

func TestExactMatchAndDuplicate(t *testing.T) {
	t.Parallel()
	db := outcomeDatabase(t)
	parsed := outcomeFixture(t, "linkedin_rejection_with_suggestions.eml")
	insertJob(t, db, "synthetic-job", "https://www.linkedin.com/jobs/view/4451000001/", "applied")
	calls := 0
	first, changed := runOutcome(t, db, parsed, "2026-08-20T10:00:00Z", fakeStatus(&calls))
	second, changedAgain := runOutcome(t, db, parsed, "2026-08-20T10:01:00Z", fakeStatus(&calls))
	if !changed || changedAgain || first.Resolution != "application_updated" || !first.ApplicationChanged ||
		first.MatchedJobID != "synthetic-job" || first.Duplicate || !second.Duplicate || calls != 1 {
		t.Fatalf("first=%+v second=%+v changed=%t/%t calls=%d", first, second, changed, changedAgain, calls)
	}
	var status, note string
	if err := db.QueryRow(`SELECT current_status FROM job_applications WHERE job_id='synthetic-job'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT note FROM application_status_events`).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if status != "rejected" || note != "LinkedIn rejection received by email." {
		t.Fatalf("status=%q note=%q", status, note)
	}
}

func TestUnmatchedReconcilesOnlyAfterExactJobAppears(t *testing.T) {
	t.Parallel()
	db := outcomeDatabase(t)
	parsed := outcomeFixture(t, "linkedin_rejection_with_suggestions.eml")
	calls := 0
	first, _ := runOutcome(t, db, parsed, "2026-08-20T10:00:00Z", fakeStatus(&calls))
	insertJob(t, db, "exact-job", "https://www.linkedin.com/jobs/view/4451000001/", "captured")
	second, changed := runOutcome(t, db, parsed, "2026-08-20T10:01:00Z", fakeStatus(&calls))
	if first.Resolution != "unmatched" || !changed || second.Resolution != "application_updated" ||
		second.Revision != 2 || second.MatchedJobID != "exact-job" || calls != 1 {
		t.Fatalf("first=%+v second=%+v changed=%t calls=%d", first, second, changed, calls)
	}
}

func TestAmbiguousConflictAndOpaqueHandshakeRemainUnlinked(t *testing.T) {
	t.Parallel()
	t.Run("ambiguous", func(t *testing.T) {
		db := outcomeDatabase(t)
		parsed := outcomeFixture(t, "linkedin_rejection_with_suggestions.eml")
		url := "https://www.linkedin.com/jobs/view/4451000001/"
		insertJob(t, db, "one", url, "applied")
		insertJob(t, db, "two", url, "applied")
		calls := 0
		result, _ := runOutcome(t, db, parsed, "2026-08-20T10:00:00Z", fakeStatus(&calls))
		if result.Resolution != "ambiguous" || result.MatchedJobID != "" || calls != 0 {
			t.Fatalf("result=%+v calls=%d", result, calls)
		}
	})
	t.Run("conflict", func(t *testing.T) {
		db := outcomeDatabase(t)
		parsed := outcomeFixture(t, "linkedin_rejection_with_suggestions.eml")
		insertJob(t, db, "offered", "https://www.linkedin.com/jobs/view/4451000001/", "offer")
		calls := 0
		result, _ := runOutcome(t, db, parsed, "2026-08-20T10:00:00Z", fakeStatus(&calls))
		if result.Resolution != "status_conflict" || result.MatchedJobID != "offered" || calls != 0 {
			t.Fatalf("result=%+v calls=%d", result, calls)
		}
	})
	t.Run("opaque-handshake", func(t *testing.T) {
		db := outcomeDatabase(t)
		parsed := outcomeFixture(t, "handshake_application_sent_with_suggestions.eml")
		insertJob(t, db, "same-company", "https://jobs.example.test/synthetic-ai", "captured")
		calls := 0
		result, _ := runOutcome(t, db, parsed, "2026-08-20T10:00:00Z", fakeStatus(&calls))
		if result.Resolution != "unmatched" || result.MatchedJobID != "" || calls != 0 {
			t.Fatalf("result=%+v calls=%d", result, calls)
		}
	})
}

func TestRejectionDismissesUnpromotedDiscoveryAndCleansLocation(t *testing.T) {
	t.Parallel()
	db := outcomeDatabase(t)
	parsed := outcomeFixture(t, "linkedin_rejection_with_suggestions.eml")
	if _, err := db.Exec(`INSERT INTO job_discoveries
		(discovery_id,posting_identity,state,location,updated_at,revision)
		VALUES('d1','linkedin:4451000001','inbox','United States applied on Aug 15','t0',1)`); err != nil {
		t.Fatal(err)
	}
	result, _ := runOutcome(t, db, parsed, "2026-08-20T10:00:00Z", nil)
	var state, location string
	var revision int
	if err := db.QueryRow(`SELECT state,location,revision FROM job_discoveries WHERE discovery_id='d1'`).Scan(&state, &location, &revision); err != nil {
		t.Fatal(err)
	}
	if result.Resolution != "discovery_dismissed" || !result.DiscoveryChanged ||
		state != "dismissed" || location != "United States" || revision != 2 {
		t.Fatalf("result=%+v state=%q location=%q revision=%d", result, state, location, revision)
	}
}

func TestCallerRollbackIsRequiredAfterStatusFailure(t *testing.T) {
	t.Parallel()
	db := outcomeDatabase(t)
	parsed := outcomeFixture(t, "linkedin_rejection_with_suggestions.eml")
	insertJob(t, db, "synthetic-job", "https://www.linkedin.com/jobs/view/4451000001/", "applied")
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = RecordInTransaction(context.Background(), tx, parsed.MessageFingerprint,
		parsed.ApplicationOutcomes[0], parsed.ReceivedAt, "2026-08-20T10:00:00Z",
		func(ctx context.Context, tx *sql.Tx, id, _, _, _, _ string) (bool, error) {
			_, _ = tx.ExecContext(ctx, `UPDATE job_applications SET current_status='rejected' WHERE job_id=?`, id)
			return false, errors.New("private failure")
		})
	var store *StoreError
	if !errors.As(err, &store) || store.Code != "EMAIL_OUTCOME_STATUS_WRITE_FAILED" {
		t.Fatalf("failure=%v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.QueryRow(`SELECT current_status FROM job_applications WHERE job_id='synthetic-job'`).Scan(&status); err != nil || status != "applied" {
		t.Fatalf("rollback: status=%q err=%v", status, err)
	}
}

func TestPromotedExactDiscoveryMatchesWithoutApplicationURL(t *testing.T) {
	t.Parallel()
	db := outcomeDatabase(t)
	parsed := outcomeFixture(t, "linkedin_rejection_with_suggestions.eml")
	insertJob(t, db, "promoted", "https://jobs.example.test/other", "rejected")
	if _, err := db.Exec(`INSERT INTO job_discoveries
		(discovery_id,posting_identity,state,location,promoted_job_id,updated_at,revision)
		VALUES('promoted-discovery','linkedin:4451000001','promoted','United States',
		'promoted','t0',1)`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	result, _ := runOutcome(t, db, parsed, "2026-08-20T10:00:00Z", fakeStatus(&calls))
	if result.Resolution != "application_already_current" || result.MatchedJobID != "promoted" ||
		result.MatchedDiscoveryID != "promoted-discovery" || result.DiscoveryChanged || calls != 0 {
		t.Fatalf("result=%+v calls=%d", result, calls)
	}
}

func TestUnmatchedOutcomeReconcilesToDiscoveryWithoutJob(t *testing.T) {
	t.Parallel()
	db := outcomeDatabase(t)
	parsed := outcomeFixture(t, "linkedin_rejection_with_suggestions.eml")
	first, _ := runOutcome(t, db, parsed, "2026-08-20T10:00:00Z", nil)
	if _, err := db.Exec(`INSERT INTO job_discoveries
		(discovery_id,posting_identity,state,location,updated_at,revision)
		VALUES('later','linkedin:4451000001','shortlisted','United States','t0',1)`); err != nil {
		t.Fatal(err)
	}
	second, changed := runOutcome(t, db, parsed, "2026-08-20T10:01:00Z", nil)
	third, changedAgain := runOutcome(t, db, parsed, "2026-08-20T10:02:00Z", nil)
	if first.Resolution != "unmatched" || !changed || second.Resolution != "discovery_dismissed" ||
		second.MatchedDiscoveryID != "later" || !second.DiscoveryChanged || second.Revision != 2 ||
		changedAgain || !third.Duplicate {
		t.Fatalf("first=%+v second=%+v third=%+v changed=%t/%t", first, second, third, changed, changedAgain)
	}
}
