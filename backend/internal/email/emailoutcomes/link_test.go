package emailoutcomes

import (
	"context"
	"database/sql"
	"testing"
)

const linkOutcomeID = "email_outcome_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func insertLinkOutcome(t *testing.T, db *sql.DB, status, resolution string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO job_email_application_outcomes (
		outcome_id,message_fingerprint,source_provider,posting_identity,source_url,
		title,company,location,application_status,evidence_code,observed_at,
		resolution,revision,created_at,updated_at
	) VALUES (?,'sha256:synthetic','handshake','','','Engineer','Synthetic Co','','applied',
		'handshake_application_sent','2026-08-18T18:45:00Z',?,1,'t0','t0')`, linkOutcomeID, resolution)
	if err != nil {
		t.Fatal(err)
	}
	if status != "" {
		insertJob(t, db, "synthetic-job", "https://example.test/job", status)
	}
}

func linkInTest(t *testing.T, db *sql.DB, jobID string, revision int64, apply ApplyStatus) (Result, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	out, err := LinkInTransaction(context.Background(), tx, linkOutcomeID, jobID, revision, "2026-08-20T12:00:00Z", apply)
	if err == nil {
		if commitErr := tx.Commit(); commitErr != nil {
			t.Fatal(commitErr)
		}
	}
	return out, err
}

func linkCode(err error) string {
	if problem, ok := err.(*StoreError); ok {
		return problem.Code
	}
	return ""
}

func TestLinkStoredOutcomeExplicitly(t *testing.T) {
	for _, tc := range []struct {
		name, initial, resolution, final string
		changes                          int
	}{
		{"applied", "captured", "application_updated", "applied", 1},
		{"conflict", "rejected", "status_conflict", "rejected", 0},
		{"already_current", "applied", "application_already_current", "applied", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := outcomeDatabase(t)
			insertLinkOutcome(t, db, tc.initial, "unmatched")
			calls := 0
			out, err := linkInTest(t, db, "synthetic-job", 1, fakeStatus(&calls))
			if err != nil || out.Resolution != tc.resolution || out.MatchedJobID != "synthetic-job" ||
				out.Revision != 2 || out.ApplicationChanged != (tc.changes == 1) || calls != tc.changes {
				t.Fatalf("out=%+v error=%v calls=%d", out, err, calls)
			}
			var current, note string
			if err := db.QueryRow(`SELECT current_status FROM job_applications WHERE job_id='synthetic-job'`).Scan(&current); err != nil || current != tc.final {
				t.Fatalf("status=%q error=%v", current, err)
			}
			if tc.changes == 1 {
				if err := db.QueryRow(`SELECT note FROM application_status_events`).Scan(&note); err != nil || note != "Handshake submission received by email and linked by the user." {
					t.Fatalf("note=%q error=%v", note, err)
				}
			}
		})
	}
}

func TestLinkStoredOutcomeRefusalsAndRollback(t *testing.T) {
	for _, tc := range []struct {
		name, initial, resolution, job string
		revision                       int64
		want                           string
	}{
		{"missing_job", "", "unmatched", "synthetic-job", 1, "EMAIL_OUTCOME_LINK_NOT_FOUND"},
		{"stale", "captured", "unmatched", "synthetic-job", 2, "EMAIL_OUTCOME_LINK_REVISION_CONFLICT"},
		{"resolved", "captured", "application_updated", "synthetic-job", 1, "EMAIL_OUTCOME_LINK_ALREADY_RESOLVED"},
		{"invalid_job", "captured", "unmatched", "../job", 1, "EMAIL_OUTCOME_LINK_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := outcomeDatabase(t)
			insertLinkOutcome(t, db, tc.initial, tc.resolution)
			calls := 0
			out, err := linkInTest(t, db, tc.job, tc.revision, fakeStatus(&calls))
			if linkCode(err) != tc.want || out.OutcomeID != "" || calls != 0 {
				t.Fatalf("out=%+v error=%v calls=%d", out, err, calls)
			}
			var revision int
			if err := db.QueryRow(`SELECT revision FROM job_email_application_outcomes`).Scan(&revision); err != nil || revision != 1 {
				t.Fatalf("revision=%d error=%v", revision, err)
			}
		})
	}
	db := outcomeDatabase(t)
	insertLinkOutcome(t, db, "captured", "unmatched")
	if _, err := db.Exec(`CREATE TRIGGER reject_link BEFORE UPDATE ON job_email_application_outcomes
		BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if _, err := linkInTest(t, db, "synthetic-job", 1, fakeStatus(&calls)); linkCode(err) != "EMAIL_OUTCOME_WRITE_FAILED" || calls != 1 {
		t.Fatalf("rollback refusal=%v calls=%d", err, calls)
	}
	var status string
	var events, revision int
	if err := db.QueryRow(`SELECT current_status FROM job_applications WHERE job_id='synthetic-job'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM application_status_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT revision FROM job_email_application_outcomes`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if status != "captured" || events != 0 || revision != 1 {
		t.Fatalf("failed link changed state: %s/%d/%d", status, events, revision)
	}
	unsafe := outcomeDatabase(t)
	insertLinkOutcome(t, unsafe, "captured", "unmatched")
	if _, err := unsafe.Exec(`UPDATE job_email_application_outcomes SET evidence_code='generic_message'`); err != nil {
		t.Fatal(err)
	}
	calls = 0
	if _, err := linkInTest(t, unsafe, "synthetic-job", 1, fakeStatus(&calls)); linkCode(err) != "EMAIL_OUTCOME_VALUE_INVALID" || calls != 0 {
		t.Fatalf("unsupported stored evidence linked: %v calls=%d", err, calls)
	}
}
