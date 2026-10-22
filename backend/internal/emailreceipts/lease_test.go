package emailreceipts

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

type leaseSnapshot struct {
	result, retry, errorCode, owner, expiry      string
	health, healthError, testedAt, healthUpdated string
	failures                                     int
}

func snapshotLease(t *testing.T, db *sql.DB) leaseSnapshot {
	t.Helper()
	var state leaseSnapshot
	var retry, expiry sql.NullString
	err := db.QueryRow(`SELECT last_result, consecutive_failures, next_retry_at,
		last_error_code, lease_owner, lease_expires_at FROM job_email_sync_state
		WHERE connection_id = 'primary'`).Scan(
		&state.result, &state.failures, &retry, &state.errorCode, &state.owner, &expiry,
	)
	if err != nil {
		t.Fatal(err)
	}
	state.retry, state.expiry = retry.String, expiry.String
	var tested sql.NullString
	if err := db.QueryRow(`SELECT last_test_status, last_error_code, last_tested_at, updated_at
		FROM job_email_connections WHERE connection_id = 'primary'`).Scan(
		&state.health, &state.healthError, &tested, &state.healthUpdated,
	); err != nil {
		t.Fatal(err)
	}
	state.testedAt = tested.String
	return state
}

func TestPythonSourceLeaseTransitions(t *testing.T) {
	t.Parallel()
	ctx, db := context.Background(), receiptDatabase(t)
	if _, err := db.Exec(`UPDATE job_email_sync_state SET next_retry_at = ?
		WHERE connection_id = 'primary'`, "2026-08-14T12:30:00Z"); err != nil {
		t.Fatal(err)
	}
	acquire := func(when, owner string, scheduled bool) (string, error) {
		var deferred string
		var problem error
		inTransaction(t, db, func(tx *sql.Tx) {
			deferred, problem = AcquireLease(ctx, tx, when, owner, scheduled)
		})
		return deferred, problem
	}
	complete := func(owner, when string, issues, retry bool) bool {
		var matched bool
		inTransaction(t, db, func(tx *sql.Tx) {
			var err error
			matched, err = MarkCompleted(ctx, tx, owner, when, issues, retry)
			if err != nil {
				t.Fatal(err)
			}
		})
		return matched
	}
	fail := func(owner, when string) bool {
		var matched bool
		inTransaction(t, db, func(tx *sql.Tx) {
			var err error
			matched, err = MarkFailed(ctx, tx, owner, "EMAIL_SYNC_NETWORK_FAILED", when)
			if err != nil {
				t.Fatal(err)
			}
		})
		return matched
	}
	if deferred, err := acquire("2026-08-14T12:00:00Z", "scheduled", true); err != nil || deferred != "2026-08-14T12:30:00Z" {
		t.Fatalf("scheduled deferral: %q %v", deferred, err)
	}
	if state := snapshotLease(t, db); state.result != "never" || state.owner != "" || state.health != "not_tested" {
		t.Fatalf("deferral mutated state: %+v", state)
	}
	if deferred, err := acquire("2026-08-14T12:00:00Z", "a", false); err != nil || deferred != "" {
		t.Fatalf("manual acquire: %q %v", deferred, err)
	}
	if _, err := acquire("2026-08-14T12:01:00Z", "other", false); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("active lease: %v", err)
	}
	if _, err := acquire("2026-08-14T12:01:00Z", "scheduled", true); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("active lease must take precedence over backoff: %v", err)
	}
	if complete("stale", "2026-08-14T12:02:00Z", false, false) {
		t.Fatal("stale owner completed active lease")
	}
	if state := snapshotLease(t, db); state.owner != "a" || state.expiry != "2026-08-14T12:05:00Z" {
		t.Fatalf("lease after stale owner: %+v", state)
	}
	if !complete("a", "2026-08-14T12:02:00Z", true, true) {
		t.Fatal("owner could not complete")
	}
	if state := snapshotLease(t, db); state.result != "partial" || state.failures != 1 ||
		state.retry != "2026-08-14T12:07:00Z" || state.errorCode != "EMAIL_SYNC_MESSAGE_RETRY" ||
		state.owner != "" || state.expiry != "" || state.health != "succeeded" || state.healthError != "" ||
		state.testedAt != "2026-08-14T12:02:00Z" || state.healthUpdated != "2026-08-14T12:02:00Z" {
		t.Fatalf("partial retry state: %+v", state)
	}
	if deferred, err := acquire("2026-08-14T12:03:00Z", "scheduled", true); err != nil || deferred != "2026-08-14T12:07:00Z" {
		t.Fatalf("partial backoff: %q %v", deferred, err)
	}
	if deferred, err := acquire("2026-08-14T12:03:00Z", "b", false); err != nil || deferred != "" {
		t.Fatalf("manual bypass: %q %v", deferred, err)
	}
	if !complete("b", "2026-08-14T12:04:00Z", false, false) {
		t.Fatal("success completion missing")
	}
	if state := snapshotLease(t, db); state.result != "succeeded" || state.failures != 0 || state.retry != "" || state.errorCode != "" {
		t.Fatalf("success did not clear backoff: %+v", state)
	}
	if _, err := acquire("2026-08-14T12:05:00Z", "c", false); err != nil {
		t.Fatal(err)
	}
	if !fail("c", "2026-08-14T12:06:00Z") {
		t.Fatal("failure completion missing")
	}
	if state := snapshotLease(t, db); state.result != "failed" || state.failures != 1 ||
		state.retry != "2026-08-14T12:11:00Z" || state.errorCode != "EMAIL_SYNC_NETWORK_FAILED" ||
		state.health != "failed" || state.healthError != "EMAIL_SYNC_NETWORK_FAILED" ||
		state.testedAt != "2026-08-14T12:06:00Z" || state.healthUpdated != "2026-08-14T12:06:00Z" {
		t.Fatalf("first failure: %+v", state)
	}
	if deferred, err := acquire("2026-08-14T12:07:00Z", "scheduled", true); err != nil || deferred != "2026-08-14T12:11:00Z" {
		t.Fatalf("failure backoff: %q %v", deferred, err)
	}
	if _, err := acquire("2026-08-14T12:07:00Z", "d", false); err != nil {
		t.Fatal(err)
	}
	if !fail("d", "2026-08-14T12:08:00Z") {
		t.Fatal("second failure completion missing")
	}
	if state := snapshotLease(t, db); state.failures != 2 || state.retry != "2026-08-14T12:18:00Z" {
		t.Fatalf("exponential backoff: %+v", state)
	}
	if _, err := acquire("2026-08-14T12:19:00Z", "e", true); err != nil {
		t.Fatal(err)
	}
	if !complete("e", "2026-08-14T12:20:00Z", true, false) {
		t.Fatal("skipped-message completion missing")
	}
	if state := snapshotLease(t, db); state.result != "partial" || state.failures != 0 ||
		state.retry != "" || state.errorCode != "EMAIL_SYNC_MESSAGE_SKIPPED" || state.health != "succeeded" ||
		state.testedAt != "2026-08-14T12:20:00Z" || state.healthUpdated != "2026-08-14T12:20:00Z" {
		t.Fatalf("partial without retry: %+v", state)
	}
	if _, err := acquire("2026-08-14T12:21:00Z", "f", false); err != nil {
		t.Fatal(err)
	}
	if _, err := acquire("2026-08-14T12:27:00Z", "g", false); err != nil {
		t.Fatal(err)
	}
	if fail("f", "2026-08-14T12:28:00Z") {
		t.Fatal("expired owner marked current lease failed")
	}
	if state := snapshotLease(t, db); state.owner != "g" || state.expiry != "2026-08-14T12:32:00Z" {
		t.Fatalf("expired-owner takeover: %+v", state)
	}
	if !complete("g", "2026-08-14T12:29:00Z", false, false) {
		t.Fatal("takeover owner could not complete")
	}
	if state := snapshotLease(t, db); state.result != "succeeded" || state.failures != 0 || state.retry != "" || state.owner != "" {
		t.Fatalf("final state: %+v", state)
	}
}

func TestRetryBackoffCapsAtOneHour(t *testing.T) {
	t.Parallel()
	completed := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	for failures, want := range map[int64]string{
		0: "2026-08-14T12:05:00Z", 1: "2026-08-14T12:10:00Z",
		2: "2026-08-14T12:20:00Z", 3: "2026-08-14T12:40:00Z",
		4: "2026-08-14T13:00:00Z", 100: "2026-08-14T13:00:00Z",
	} {
		if got := retryAt(completed, failures); got != want {
			t.Errorf("failures=%d: %s, want %s", failures, got, want)
		}
	}
}
