//go:build windows

package emailconnection

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emailgate"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/sqlitestore"
)

func TestOwnedEmailCursorReset(t *testing.T) {
	if os.Getenv("CAREERHUB_CURSOR_RESET_CHILD") == "1" {
		checkOwnedEmailCursorReset(t, os.Getenv("CAREERHUB_CURSOR_RESET_HOME"))
		return
	}
	homePath := filepath.Join(t.TempDir(), "home")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnedEmailCursorReset$", "-test.v")
	child.Env = append(os.Environ(), "CAREERHUB_CURSOR_RESET_CHILD=1", "CAREERHUB_CURSOR_RESET_HOME="+homePath)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("synthetic cursor-reset child failed: %v %.2500s", err, output)
	}
	home, admission := datahome.Inspect(homePath, nil)
	if admission != nil || home == nil {
		t.Fatalf("synthetic Home missing after child: %v", admission)
	}
	if _, problem := ResetCursor(context.Background(), homePath, *home, 2); problem == nil || problem.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatalf("unclaimed parent cursor reset was not denied: %v", problem)
	}
}

func checkOwnedEmailCursorReset(t *testing.T, homePath string) {
	t.Helper()
	ctx := context.Background()
	home, startup := datahome.BootstrapForProcess(ctx, homePath, "dev")
	if startup != nil {
		t.Fatal(startup.Code)
	}
	if _, err := schema.PrepareOwned(ctx, homePath); err != nil {
		t.Fatal(err)
	}
	if _, problem := ResetCursor(ctx, homePath, *home, 1); problem == nil || problem.Code != "EMAIL_CONNECTION_NOT_CONFIGURED" {
		t.Fatalf("unconfigured reset accepted: %v", problem)
	}
	db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(homePath, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `INSERT INTO job_email_connections (
		connection_id, provider, account_address, folder, secret_name,
		enabled, lookback_days, max_messages_per_sync, revision,
		created_at, updated_at
	) VALUES ('primary', 'gmail_imap', 'jobs@example.test', 'INBOX',
		'job-discovery.gmail-imap.primary.v1', 1, 14, 250, 1, 't0', 't0')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO job_email_sync_state (
		connection_id, folder, uidvalidity, last_uid, last_started_at,
		last_completed_at, last_result, consecutive_failures, next_retry_at,
		last_error_code, revision
	) VALUES ('primary', 'INBOX', '55', 42, 'started', 'completed',
		'failed', 2, 'retry', 'EMAIL_TEST_FAILURE', 3)`); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		folder string
		uid    int
		code   string
	}{
		{"INBOX", 41, ""},
		{"INBOX", 42, "EMAIL_SUBJECT_POLICY_FILTERED"},
		{"OTHER", 43, "EMAIL_SUBJECT_POLICY_FILTERED"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO job_email_message_receipts (
			connection_id, folder, uidvalidity, message_uid, status,
			error_code, first_seen_at, updated_at
		) VALUES ('primary', ?, '55', ?, 'no_candidates', ?, 't0', 't0')`,
			item.folder, item.uid, item.code); err != nil {
			t.Fatal(err)
		}
	}
	if _, problem := ResetCursor(ctx, homePath, *home, 0); problem == nil || problem.Code != "EMAIL_CONNECTION_INVALID" {
		t.Fatalf("zero revision accepted: %v", problem)
	}
	if _, problem := ResetCursor(ctx, homePath, *home, 2); problem == nil || problem.Code != "EMAIL_CONNECTION_REVISION_CONFLICT" {
		t.Fatalf("stale revision accepted: %v", problem)
	}
	wrong := *home
	wrong.DataHomeID = "dh_00000000000000000000000000000000"
	if wrong.DataHomeID == home.DataHomeID {
		wrong.DataHomeID = "dh_11111111111111111111111111111111"
	}
	if _, problem := ResetCursor(ctx, homePath, wrong, 1); problem == nil || problem.Code != "DATA_HOME_CHANGED" {
		t.Fatalf("wrong startup identity accepted: %v", problem)
	}
	wrong = *home
	wrong.Channel = "stable"
	if _, problem := ResetCursor(ctx, homePath, wrong, 1); problem == nil || problem.Code != "DATA_HOME_CHANNEL_MISMATCH" {
		t.Fatalf("wrong startup channel accepted: %v", problem)
	}
	release, err := emailgate.TryAcquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	blockedCtx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	_, blocked := ResetCursor(blockedCtx, homePath, *home, 1)
	cancel()
	release()
	if blocked == nil || blocked.Code != "EMAIL_CONNECTION_CANCELED" {
		t.Fatalf("occupied email gate was bypassed: %v", blocked)
	}
	release, err = operationgate.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	blockedCtx, cancel = context.WithTimeout(ctx, 25*time.Millisecond)
	_, blocked = ResetCursor(blockedCtx, homePath, *home, 1)
	cancel()
	release()
	if blocked == nil || blocked.Code != "EMAIL_CONNECTION_CANCELED" {
		t.Fatalf("occupied general gate was bypassed: %v", blocked)
	}
	if _, problem := resetCursorWithHooks(ctx, homePath, *home, 1, cursorResetHooks{
		beforeCommit: func() *Error { return failure("EMAIL_CONNECTION_TEST_ABORT") },
	}); problem == nil || problem.Code != "EMAIL_CONNECTION_TEST_ABORT" {
		t.Fatalf("known precommit failure not returned: %v", problem)
	}
	assertCursorRow(t, db, 1, "t0", "55", 42, "failed", 2, "retry", "EMAIL_TEST_FAILURE", 3, 3)
	when := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	result, problem := resetCursorWithHooks(ctx, homePath, *home, 1, cursorResetHooks{
		now: func() time.Time { return when },
	})
	if problem != nil || !result.CommitAttempted || !result.Committed || result.RevisionRefreshRequired || result.Revision != 2 {
		t.Fatalf("owned reset did not commit: result=%+v problem=%v", result, problem)
	}
	assertCursorRow(t, db, 2, "2026-08-14T12:00:00Z", "", 0, "never", 0, "", "", 4, 2)
	for uid, want := range map[int]int{41: 1, 42: 0, 43: 1} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_email_message_receipts
			WHERE connection_id='primary' AND message_uid=?`, uid).Scan(&count); err != nil || count != want {
			t.Fatalf("receipt %d count=%d err=%v want=%d", uid, count, err, want)
		}
	}
	if _, problem := ResetCursor(ctx, homePath, *home, 1); problem == nil || problem.Code != "EMAIL_CONNECTION_REVISION_CONFLICT" {
		t.Fatalf("old revision replayed: %v", problem)
	}
	unknown, problem := resetCursorWithHooks(ctx, homePath, *home, 2, cursorResetHooks{
		commit: func(tx *sql.Tx) error {
			if err := tx.Commit(); err != nil {
				return err
			}
			return errors.New("synthetic report after commit")
		},
	})
	if problem == nil || problem.Code != "EMAIL_CONNECTION_COMMIT_UNCONFIRMED" || !unknown.CommitAttempted || unknown.Committed || !unknown.RevisionRefreshRequired {
		t.Fatalf("reported-after-commit fact misclassified: result=%+v problem=%v", unknown, problem)
	}
	assertCursorRow(t, db, 3, "", "", 0, "never", 0, "", "", 5, 2)
	if _, err := db.ExecContext(ctx, `PRAGMA user_version=26`); err != nil {
		t.Fatal(err)
	}
	if _, problem := ResetCursor(ctx, homePath, *home, 3); problem == nil || problem.Code != "EMAIL_CONNECTION_SCHEMA_PREPARATION_REQUIRED" {
		t.Fatalf("old schema admitted: %v", problem)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA user_version=28`); err != nil {
		t.Fatal(err)
	}
	if _, problem := ResetCursor(ctx, homePath, *home, 3); problem == nil || problem.Code != "EMAIL_CONNECTION_SCHEMA_TOO_NEW" {
		t.Fatalf("future schema admitted: %v", problem)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA user_version=27`); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homePath, datahome.EmailCredentialRecoveryPath), []byte("synthetic-pending-intent"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, problem := ResetCursor(ctx, homePath, *home, 3); problem == nil || problem.Code != "DATA_HOME_RECOVERY_REQUIRED" {
		t.Fatalf("recovery intent admitted: %v", problem)
	}
	if err := os.Remove(filepath.Join(homePath, datahome.EmailCredentialRecoveryPath)); err != nil {
		t.Fatal(err)
	}
}

func assertCursorRow(t *testing.T, db *sql.DB, wantRevision int64, wantUpdated, wantEpoch string, wantUID int64, wantResult string, wantFailures int, wantRetry, wantError string, wantSyncRevision, wantReceipts int) {
	t.Helper()
	var revision int64
	var updated string
	if err := db.QueryRow(`SELECT revision, updated_at FROM job_email_connections WHERE connection_id='primary'`).Scan(&revision, &updated); err != nil || revision != wantRevision || wantUpdated != "" && updated != wantUpdated {
		t.Fatalf("connection revision=%d updated=%s err=%v", revision, updated, err)
	}
	var epoch, result, retry, lastError, started, completed string
	var uid int64
	var failures, syncRevision int
	if err := db.QueryRow(`SELECT uidvalidity, last_uid, last_started_at, last_completed_at,
		last_result, consecutive_failures, COALESCE(next_retry_at, ''), last_error_code, revision
		FROM job_email_sync_state WHERE connection_id='primary' AND folder='INBOX'`).Scan(
		&epoch, &uid, &started, &completed, &result, &failures, &retry, &lastError, &syncRevision,
	); err != nil || epoch != wantEpoch || uid != wantUID || result != wantResult || failures != wantFailures || retry != wantRetry || lastError != wantError || syncRevision != wantSyncRevision || started != "started" || completed != "completed" {
		t.Fatalf("sync state changed incorrectly: epoch=%s uid=%d result=%s failures=%d retry=%s error=%s revision=%d err=%v", epoch, uid, result, failures, retry, lastError, syncRevision, err)
	}
	var receipts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM job_email_message_receipts WHERE connection_id='primary'`).Scan(&receipts); err != nil || receipts != wantReceipts {
		t.Fatalf("receipt history count=%d err=%v want=%d", receipts, err, wantReceipts)
	}
}
