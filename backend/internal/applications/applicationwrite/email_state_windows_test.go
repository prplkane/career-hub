//go:build windows

package applicationwrite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/email/emailreceipts"
	"careerhub.local/backend/internal/email/emailrouting"
	"careerhub.local/backend/internal/email/emailworker"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

const stateStart = "2026-08-14T12:00:00Z"

func seedEmailState(t *testing.T, home string) {
	t.Helper()
	path := filepath.Join(home, "applications", "career_hub.sqlite3")
	db, err := sqlitestore.OpenReadWriteExisting(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, query := range []string{
		`INSERT INTO job_email_connections
			(connection_id, provider, account_address, folder, secret_name,
			enabled, lookback_days, max_messages_per_sync, revision, created_at, updated_at)
			VALUES ('primary','gmail_imap','alerts@example.test','INBOX',
			'synthetic-no-secret',1,14,250,1,'t0','t0')`,
		`INSERT INTO job_email_sync_state (connection_id,folder) VALUES ('primary','INBOX')`,
		`INSERT INTO job_email_sender_rules
			(connection_id,sender_address,disposition,purpose,subject_policy_json,created_at,updated_at)
			VALUES ('primary','alerts@example.test','allowed','both',
			'{"job_alerts":{"include_any":["new jobs"],"exclude_any":[]},
			"application_tracking":{"include_any":["application update"],"exclude_any":[]}}',
			't0','t0')`,
		`INSERT INTO job_email_sender_rules
			(connection_id,sender_address,disposition,purpose,subject_policy_json,created_at,updated_at)
			VALUES ('primary','disabled@example.test','disabled','job_alerts','{}','t0','t0')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEmailStateOwnedHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_EMAIL_STATE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	ctx, home := context.Background(), os.Getenv("CAREERHUB_EMAIL_STATE_HOME")
	mode := os.Getenv("CAREERHUB_EMAIL_STATE_MODE")
	if _, e := datahome.BootstrapForProcess(ctx, home, "dev"); e != nil {
		t.Fatalf("synthetic bootstrap: %s", e.Code)
	}
	if _, err := schema.PrepareOwned(ctx, home); err != nil {
		t.Fatalf("synthetic schema: %v", err)
	}
	if mode != "missing" {
		seedEmailState(t, home)
	}
	service := New(home)
	state := service.EmailSyncState()
	if mode == "missing" {
		if _, _, err := state.Position(ctx); err == nil || err.Error() != "EMAIL_CONNECTION_NOT_CONFIGURED" {
			t.Fatalf("missing configuration: %v", err)
		}
		return
	}
	if mode == "unknown" {
		service.commit = func(tx *sql.Tx) error {
			if err := tx.Commit(); err != nil {
				return err
			}
			return errors.New("private uncertain commit")
		}
		if _, err := state.Acquire(ctx, stateStart, "owner-unknown", false); !errors.Is(err, emailworker.ErrCommitUnknown) {
			t.Fatalf("unconfirmed lease: %v", err)
		}
		read, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(home, "applications", "career_hub.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		var lease string
		if err := read.QueryRow(`SELECT lease_owner FROM job_email_sync_state WHERE connection_id='primary' AND folder='INBOX'`).Scan(&lease); err != nil || lease != "owner-unknown" {
			t.Fatalf("unconfirmed commit was actually applied: %q %v", lease, err)
		}
		return
	}
	if mode == "record-rollback" {
		if reset, err := state.SetUIDValidity(ctx, "41"); err != nil || reset {
			t.Fatalf("epoch setup: %t %v", reset, err)
		}
		db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(home, "applications", "career_hub.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TRIGGER synthetic_cursor_abort BEFORE UPDATE OF last_uid
			ON job_email_sync_state WHEN NEW.last_uid > 0
			BEGIN SELECT RAISE(ABORT,'private test cause'); END`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		_, err = state.Record(ctx, emailreceipts.RecordInput{
			UIDValidity: "41", UID: 1, SenderAddress: "alerts@example.test",
			Status: "imported", AdvanceCursor: true, RecordedAt: stateStart,
		})
		if err == nil || err.Error() != "EMAIL_SYNC_LOCAL_WRITE_FAILED" {
			t.Fatalf("cursor failure: %v", err)
		}
		read, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(home, "applications", "career_hub.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		var count, cursor int
		if err := read.QueryRow(`SELECT COUNT(*) FROM job_email_message_receipts`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if err := read.QueryRow(`SELECT last_uid FROM job_email_sync_state WHERE connection_id='primary' AND folder='INBOX'`).Scan(&cursor); err != nil || count != 0 || cursor != 0 {
			t.Fatalf("partial receipt escaped rollback: rows=%d cursor=%d err=%v", count, cursor, err)
		}
		return
	}
	if mode == "malformed-rules" {
		db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(home, "applications", "career_hub.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE job_email_sender_rules SET subject_policy_json='{"job_alerts":42}' WHERE sender_address='alerts@example.test'`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if rules, err := state.AllowedRules(ctx); err == nil || err.Error() != "EMAIL_CONNECTION_INVALID" || rules != nil {
			t.Fatalf("malformed policy admitted: %v %v", rules, err)
		}
		return
	}
	if epoch, uid, err := state.Position(ctx); err != nil || epoch != "" || uid != 0 {
		t.Fatalf("initial cursor: %q %d %v", epoch, uid, err)
	}
	rules, err := state.AllowedRules(ctx)
	if err != nil || len(rules) != 1 || rules["alerts@example.test"].Purpose != emailrouting.SenderPurposeBoth ||
		emailrouting.RoutedPurpose(rules["alerts@example.test"].Purpose,
			rules["alerts@example.test"].Policy, "New jobs today") != emailrouting.SenderPurposeJobAlerts {
		t.Fatalf("typed sender policy was lost: count=%d err=%v", len(rules), err)
	}
	if deferred, err := state.Acquire(ctx, stateStart, "owner-1", false); err != nil || deferred != "" {
		t.Fatalf("lease: %q %v", deferred, err)
	}
	if _, err := state.Acquire(ctx, stateStart, "owner-2", false); err == nil || err.Error() != "EMAIL_SYNC_ALREADY_RUNNING" {
		t.Fatalf("active lease: %v", err)
	}
	if reset, err := state.SetUIDValidity(ctx, "41"); err != nil || reset {
		t.Fatalf("initial epoch: %t %v", reset, err)
	}
	if epoch, uid, err := state.Position(ctx); err != nil || epoch != "41" || uid != 0 {
		t.Fatalf("initial epoch must commit although reset=false: %q %d %v", epoch, uid, err)
	}
	input := emailreceipts.RecordInput{
		UIDValidity: "41", UID: 1, SenderAddress: "alerts@example.test",
		Status: "failed_retryable", MessageIDHash: "sha256:message",
		ContentSHA256: "sha256:content", RecordedAt: stateStart,
		ErrorCode: "EMAIL_IMPORT_DISCOVERY_FAILED",
	}
	if attempts, err := state.Record(ctx, input); err != nil || attempts != 1 {
		t.Fatalf("retry receipt: %d %v", attempts, err)
	}
	if retry, err := state.PendingRetryUIDs(ctx, "41", map[string]bool{"alerts@example.test": true}); err != nil || !reflect.DeepEqual(retry, []uint32{1}) {
		t.Fatalf("pending retry: %v %v", retry, err)
	}
	if receipt, err := state.Receipt(ctx, "41", 1); err != nil || receipt == nil || receipt.AttemptCount != 1 || receipt.Status != "failed_retryable" {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	input.Status, input.AdvanceCursor, input.RecordedAt, input.ErrorCode = "imported", true, "2026-08-14T12:01:00Z", ""
	input.CandidateCount, input.DiscoveryCount = 2, 1
	if attempts, err := state.Record(ctx, input); err != nil || attempts != 2 {
		t.Fatalf("terminal receipt: %d %v", attempts, err)
	}
	if epoch, uid, err := state.Position(ctx); err != nil || epoch != "41" || uid != 1 {
		t.Fatalf("committed cursor: %q %d %v", epoch, uid, err)
	}
	if yes, err := state.IdentityProcessed(ctx, "content_sha256", "sha256:content", "41", 2); err != nil || !yes {
		t.Fatalf("terminal identity: %t %v", yes, err)
	}
	if err := state.Complete(ctx, "owner-1", "2026-08-14T12:02:00Z", false, false); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if deferred, err := state.Acquire(ctx, "2026-08-14T12:03:00Z", "owner-2", false); err != nil || deferred != "" {
		t.Fatalf("second lease: %q %v", deferred, err)
	}
	if reset, err := state.SetUIDValidity(ctx, "42"); err != nil || !reset {
		t.Fatalf("epoch reset: %t %v", reset, err)
	}
	if epoch, uid, err := state.Position(ctx); err != nil || epoch != "42" || uid != 0 {
		t.Fatalf("reset cursor: %q %d %v", epoch, uid, err)
	}
	if err := state.Fail(ctx, "owner-2", "EMAIL_SYNC_PROTOCOL_FAILED", "2026-08-14T12:04:00Z"); err != nil {
		t.Fatalf("failed run: %v", err)
	}
	if deferred, err := state.Acquire(ctx, "2026-08-14T12:05:00Z", "owner-3", true); err != nil || deferred != "2026-08-14T12:09:00Z" {
		t.Fatalf("scheduled backoff: %q %v", deferred, err)
	}
	if deferred, err := state.Acquire(ctx, "2026-08-14T12:05:00Z", "owner-3", false); err != nil || deferred != "" {
		t.Fatalf("manual backoff bypass: %q %v", deferred, err)
	}
	if err := state.Complete(ctx, "owner-3", "2026-08-14T12:06:00Z", false, false); err != nil {
		t.Fatalf("manual completion: %v", err)
	}
}

func TestEmailStateNativeOwner(t *testing.T) {
	for _, mode := range []string{"sequence", "missing", "unknown", "record-rollback", "malformed-rules"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEmailStateOwnedHelper$")
			command.Env = append(os.Environ(), "CAREERHUB_EMAIL_STATE_HELPER=1",
				"CAREERHUB_EMAIL_STATE_HOME="+filepath.Join(t.TempDir(), "home"),
				"CAREERHUB_EMAIL_STATE_MODE="+mode)
			command.WaitDelay = time.Second
			var output, diagnostic bytes.Buffer
			command.Stdout, command.Stderr = &output, &diagnostic
			if err := command.Run(); err != nil {
				t.Fatalf("native fixture: %v stdout=%.800s stderr=%.400s", err, output.String(), diagnostic.String())
			}
		})
	}
}
