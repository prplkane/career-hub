package schema_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/applications/schema"
	_ "modernc.org/sqlite"
)

// The applications package reads and mutates through the real v28 schema here
// because its own tests script a fake driver; the guard contract must hold
// against SQLite's actual storage classes and the v28 CHECK constraint.
func currentDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.ApplyStepsForTest(context.Background(), tx, 0); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestApplicationRevisionGuard(t *testing.T) {
	db := currentDatabase(t)
	ctx := context.Background()
	const now = "2026-10-23T00:00:00Z"
	// mutate runs one ledger mutation in its own transaction, committing on
	// success and rolling back on the expected failure code.
	mutate := func(wantCode string, run func(*sql.Tx) (*applications.MutationResult, *applications.Error)) *applications.MutationResult {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		result, problem := run(tx)
		code := ""
		if problem != nil {
			code = problem.Code
		}
		if code != wantCode {
			_ = tx.Rollback()
			t.Fatalf("got %q, want %q", code, wantCode)
		}
		if problem != nil {
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			return nil
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	row := func(id string) (revision int64, decision, status, notes string) {
		t.Helper()
		var d sql.NullString
		if err := db.QueryRow("SELECT revision, decision, current_status, notes FROM job_applications WHERE job_id = ?", id).Scan(&revision, &d, &status, &notes); err != nil {
			t.Fatal(err)
		}
		return revision, d.String, status, notes
	}
	snapshot := applications.JobSnapshot{JobID: "job", Company: "Synthetic", Title: "Engineer", SourceURL: "https://example.test/job", ContentFingerprint: "fp"}
	mutate("", func(tx *sql.Tx) (*applications.MutationResult, *applications.Error) {
		return applications.RegisterInTransaction(ctx, tx, []applications.JobSnapshot{snapshot, {JobID: "other", Company: "c", Title: "t", SourceURL: "https://example.test/other", ContentFingerprint: "fp2"}}, now)
	})
	if revision, _, _, _ := row("job"); revision != 1 {
		t.Fatalf("new application revision %d", revision)
	}
	one, two, three, five := int64(1), int64(2), int64(3), int64(5)
	notes := "first notes"
	// Unguarded writes keep working and bump the revision.
	mutate("", func(tx *sql.Tx) (*applications.MutationResult, *applications.Error) {
		return applications.DecisionInTransaction(ctx, tx, "job", "consider", &notes, nil, now)
	})
	if revision, decision, _, got := row("job"); revision != 2 || decision != "consider" || got != notes {
		t.Fatalf("unguarded decision: %d %s %q", revision, decision, got)
	}
	// A stale expectation is refused before anything changes.
	stale := "stale notes"
	mutate("REVISION_CONFLICT", func(tx *sql.Tx) (*applications.MutationResult, *applications.Error) {
		return applications.DecisionInTransaction(ctx, tx, "job", "skip", &stale, &one, now)
	})
	mutate("REVISION_CONFLICT", func(tx *sql.Tx) (*applications.MutationResult, *applications.Error) {
		return applications.StatusInTransaction(ctx, tx, "job", "applied", now, now, "", &one)
	})
	if revision, decision, status, got := row("job"); revision != 2 || decision != "consider" || status != "captured" || got != notes {
		t.Fatalf("stale write changed the row: %d %s %s %q", revision, decision, status, got)
	}
	// A matching expectation writes and advances the revision once.
	mutate("", func(tx *sql.Tx) (*applications.MutationResult, *applications.Error) {
		return applications.DecisionInTransaction(ctx, tx, "job", "skip", nil, &two, now)
	})
	if revision, decision, _, got := row("job"); revision != 3 || decision != "skip" || got != notes {
		t.Fatalf("guarded decision: %d %s %q", revision, decision, got)
	}
	// A no-op write with a matching expectation does not advance it.
	mutate("", func(tx *sql.Tx) (*applications.MutationResult, *applications.Error) {
		return applications.DecisionInTransaction(ctx, tx, "job", "skip", nil, &three, now)
	})
	if revision, _, _, _ := row("job"); revision != 3 {
		t.Fatalf("no-op decision advanced revision to %d", revision)
	}
	// Both status update paths (resume snapshot and plain) bump exactly once.
	mutate("", func(tx *sql.Tx) (*applications.MutationResult, *applications.Error) {
		return applications.StatusInTransaction(ctx, tx, "job", "applied", now, now, "sent", &three)
	})
	mutate("", func(tx *sql.Tx) (*applications.MutationResult, *applications.Error) {
		return applications.StatusInTransaction(ctx, tx, "job", "interview", now, now, "", nil)
	})
	if revision, _, status, _ := row("job"); revision != 5 || status != "interview" {
		t.Fatalf("status path: %d %s", revision, status)
	}
	// Registration refreshes and the start action also move the token.
	changed := snapshot
	changed.Title = "Senior Engineer"
	mutate("", func(tx *sql.Tx) (*applications.MutationResult, *applications.Error) {
		return applications.RegisterInTransaction(ctx, tx, []applications.JobSnapshot{changed}, now)
	})
	mutate("", func(tx *sql.Tx) (*applications.MutationResult, *applications.Error) {
		return applications.StartInTransaction(ctx, tx, "other", now, now)
	})
	if revision, _, _, _ := row("job"); revision != 6 {
		t.Fatalf("registration refresh revision %d", revision)
	}
	if revision, _, status, _ := row("other"); revision != 2 || status != "preparing" {
		t.Fatalf("start revision %d %s", revision, status)
	}
	// Every read model exposes the current token under the same key.
	detail, problem := applications.ReadApplication(ctx, db, "job")
	if problem != nil {
		t.Fatal(problem)
	}
	encoded, err := json.Marshal(detail)
	if err != nil || !strings.Contains(string(encoded), `"revision":6,`) {
		t.Fatalf("detail revision missing: %s %v", encoded, err)
	}
	summaries, problem := applications.ReadSummaries(ctx, db, []string{"job", "other"})
	if problem != nil {
		t.Fatal(problem)
	}
	for id, want := range map[string]string{"job": `"revision":6,`, "other": `"revision":2,`} {
		encoded, err := json.Marshal(summaries[id])
		if err != nil || !strings.Contains(string(encoded), want) {
			t.Fatalf("summary %s revision missing: %s %v", id, encoded, err)
		}
	}
	records, problem := applications.ReadApplications(ctx, db, applications.Filters{})
	if problem != nil || len(records) != 2 {
		t.Fatal(len(records), problem)
	}
	if _, err := db.Exec("UPDATE job_applications SET revision = 0 WHERE job_id = 'other'"); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("revision CHECK constraint missing: %v", err)
	}
	mutate("", func(tx *sql.Tx) (*applications.MutationResult, *applications.Error) {
		_, pending := applications.DecisionInTransaction(ctx, tx, "job", "apply", nil, &five, now)
		if pending == nil || pending.Code != "REVISION_CONFLICT" {
			t.Fatalf("expected conflict inside transaction, got %v", pending)
		}
		return &applications.MutationResult{}, nil
	})
}
