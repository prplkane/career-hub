package emailoutcomes

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"testing"
)

func outcomeID(n int) string { return fmt.Sprintf("email_outcome_%064x", n) }

// insertOutcome stores one synthetic signal; discoveryID may be empty.
func insertOutcome(t *testing.T, db *sql.DB, n int, resolution, discoveryID string) {
	t.Helper()
	var discovery any
	if discoveryID != "" {
		discovery = discoveryID
	}
	_, err := db.Exec(`INSERT INTO job_email_application_outcomes (
		outcome_id,message_fingerprint,source_provider,posting_identity,source_url,
		title,company,location,application_status,evidence_code,observed_at,
		resolution,matched_discovery_id,revision,created_at,updated_at
	) VALUES (?,?,'linkedin','','','Engineer','Synthetic Co','','rejected',
		'linkedin_application_not_moving_forward','2026-08-18T18:45:00Z',?,?,1,'t0','t0')`,
		outcomeID(n), fmt.Sprintf("sha256:%d", n), resolution, discovery)
	if err != nil {
		t.Fatal(err)
	}
}

func insertDiscoveryRow(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO job_discoveries
		(discovery_id,posting_identity,state,location,updated_at,revision)
		VALUES(?,?,'dismissed','','t0',1)`, id, "linkedin:"+id); err != nil {
		t.Fatal(err)
	}
}

func remainingOutcomes(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT resolution FROM job_email_application_outcomes`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var resolution string
		if err := rows.Scan(&resolution); err != nil {
			t.Fatal(err)
		}
		out = append(out, resolution)
	}
	sort.Strings(out)
	return out
}

func inTx(t *testing.T, db *sql.DB, work func(*sql.Tx) error) error {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := work(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return nil
}

func TestDismissDeletesOneUnresolvedUpdate(t *testing.T) {
	for _, resolution := range []string{"unmatched", "discovery_dismissed", "ambiguous", "status_conflict"} {
		t.Run(resolution, func(t *testing.T) {
			db := outcomeDatabase(t)
			insertOutcome(t, db, 1, resolution, "")
			insertOutcome(t, db, 2, "unmatched", "")
			err := inTx(t, db, func(tx *sql.Tx) error {
				return DismissInTransaction(context.Background(), tx, outcomeID(1), 1)
			})
			if err != nil {
				t.Fatalf("dismiss %s: %v", resolution, err)
			}
			if got := remainingOutcomes(t, db); len(got) != 1 || got[0] != "unmatched" {
				t.Fatalf("remaining = %v, want only the other update", got)
			}
		})
	}
}

func TestDismissRefusesLinkedStaleMissingAndMalformed(t *testing.T) {
	db := outcomeDatabase(t)
	insertOutcome(t, db, 1, "application_updated", "")
	insertOutcome(t, db, 2, "unmatched", "")
	for _, tc := range []struct {
		name, id string
		revision int64
		code     string
	}{
		{"linked", outcomeID(1), 1, "EMAIL_OUTCOME_DISMISS_LINKED"},
		{"stale", outcomeID(2), 2, "EMAIL_OUTCOME_DISMISS_REVISION_CONFLICT"},
		{"missing", outcomeID(9), 1, "EMAIL_OUTCOME_DISMISS_NOT_FOUND"},
		{"malformed", "email_outcome_x", 1, "EMAIL_OUTCOME_DISMISS_INVALID"},
		{"no revision", outcomeID(2), 0, "EMAIL_OUTCOME_DISMISS_INVALID"},
	} {
		err := inTx(t, db, func(tx *sql.Tx) error {
			return DismissInTransaction(context.Background(), tx, tc.id, tc.revision)
		})
		if linkCode(err) != tc.code {
			t.Fatalf("%s: code %q, want %q", tc.name, linkCode(err), tc.code)
		}
	}
	if got := remainingOutcomes(t, db); len(got) != 2 {
		t.Fatalf("a refused dismiss deleted something: %v", got)
	}
}

func TestClearUnmatchedKeepsReviewAndLinkedUpdates(t *testing.T) {
	db := outcomeDatabase(t)
	for n, resolution := range []string{"unmatched", "discovery_dismissed", "ambiguous", "status_conflict", "application_updated", "application_already_current"} {
		insertOutcome(t, db, n+1, resolution, "")
	}
	var cleared int64
	err := inTx(t, db, func(tx *sql.Tx) error {
		var err error
		cleared, err = ClearUnmatchedInTransaction(context.Background(), tx)
		return err
	})
	if err != nil || cleared != 2 {
		t.Fatalf("cleared %d (%v), want 2", cleared, err)
	}
	want := []string{"ambiguous", "application_already_current", "application_updated", "status_conflict"}
	if got := remainingOutcomes(t, db); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("remaining = %v, want %v", got, want)
	}
}

func TestClearForDiscoveriesRemovesOnlyTheirUnlinkedUpdates(t *testing.T) {
	db := outcomeDatabase(t)
	insertDiscoveryRow(t, db, "d1")
	insertDiscoveryRow(t, db, "d2")
	insertOutcome(t, db, 1, "unmatched", "d1")
	insertOutcome(t, db, 2, "discovery_dismissed", "d1")
	insertOutcome(t, db, 3, "application_updated", "d1")
	insertOutcome(t, db, 4, "unmatched", "d2")
	var cleared int64
	err := inTx(t, db, func(tx *sql.Tx) error {
		var err error
		cleared, err = ClearForDiscoveriesInTransaction(context.Background(), tx, []string{"d1"})
		return err
	})
	if err != nil || cleared != 2 {
		t.Fatalf("cleared %d (%v), want 2", cleared, err)
	}
	want := []string{"application_updated", "unmatched"}
	if got := remainingOutcomes(t, db); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("remaining = %v, want the linked d1 update and d2's update %v", got, want)
	}
}
