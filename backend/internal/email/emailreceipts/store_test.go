package emailreceipts

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/storage/sqlitestore"
)

func receiptDatabase(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "receipts.sqlite3")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.OpenReadWriteExisting(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE job_email_connections (
			connection_id TEXT PRIMARY KEY, last_tested_at TEXT,
			last_test_status TEXT NOT NULL DEFAULT 'not_tested',
			last_error_code TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT 't0')`,
		`CREATE TABLE job_email_sync_state (
			connection_id TEXT NOT NULL, folder TEXT NOT NULL,
			uidvalidity TEXT NOT NULL DEFAULT '', last_uid INTEGER NOT NULL DEFAULT 0,
			revision INTEGER NOT NULL DEFAULT 1,
			last_started_at TEXT, last_completed_at TEXT,
			last_result TEXT NOT NULL DEFAULT 'never',
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			next_retry_at TEXT, last_error_code TEXT NOT NULL DEFAULT '',
			lease_owner TEXT NOT NULL DEFAULT '', lease_expires_at TEXT,
			PRIMARY KEY(connection_id, folder))`,
		`CREATE TABLE job_email_message_receipts (
			receipt_id INTEGER PRIMARY KEY AUTOINCREMENT,
			connection_id TEXT NOT NULL, folder TEXT NOT NULL, uidvalidity TEXT NOT NULL,
			message_uid INTEGER NOT NULL CHECK(message_uid > 0),
			message_id_hash TEXT NOT NULL DEFAULT '', content_sha256 TEXT NOT NULL DEFAULT '',
			sender_address TEXT NOT NULL DEFAULT '', status TEXT NOT NULL,
			attempt_count INTEGER NOT NULL DEFAULT 1 CHECK(attempt_count > 0),
			candidate_count INTEGER NOT NULL DEFAULT 0, discovery_count INTEGER NOT NULL DEFAULT 0,
			error_code TEXT NOT NULL DEFAULT '', first_seen_at TEXT NOT NULL,
			updated_at TEXT NOT NULL, message_sent_date TEXT NOT NULL DEFAULT '',
			subject_job_signal INTEGER NOT NULL DEFAULT 0,
			subject_service_signal INTEGER NOT NULL DEFAULT 0,
			subject_application_signal INTEGER NOT NULL DEFAULT 0,
			subject_route TEXT NOT NULL DEFAULT '',
			UNIQUE(connection_id, folder, uidvalidity, message_uid))`,
		`INSERT INTO job_email_connections(connection_id) VALUES('primary')`,
		`INSERT INTO job_email_sync_state(connection_id, folder) VALUES('primary', 'INBOX')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func inTransaction(t *testing.T, db *sql.DB, fn func(*sql.Tx)) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fn(tx)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func baseInput(uid uint32, status string) RecordInput {
	return RecordInput{
		UIDValidity: "100", UID: uid, SenderAddress: "alerts@example.test",
		Status: status, RecordedAt: "2026-08-14T12:00:00Z",
	}
}

func stringPointer(value string) *string { return &value }
func boolPointer(value bool) *bool       { return &value }

func TestPythonSourceReceiptSequence(t *testing.T) {
	t.Parallel()
	ctx, db := context.Background(), receiptDatabase(t)
	inTransaction(t, db, func(tx *sql.Tx) {
		changed, err := SetUIDValidity(ctx, tx, "100")
		if err != nil || changed {
			t.Fatalf("initial epoch: changed=%t error=%v", changed, err)
		}
	})
	unknown := baseInput(5, "unknown_sender")
	unknown.SenderAddress = "new@example.test"
	unknown.MessageIDHash = "sha256:unknown"
	unknown.ErrorCode = "EMAIL_SYNC_SENDER_NOT_ALLOWED"
	unknown.AdvanceCursor = true
	unknown.MessageSentDate = stringPointer("2026-08-14")
	unknown.SubjectJobSignal = boolPointer(true)
	unknown.SubjectRoute = stringPointer("filtered")
	failed := baseInput(7, "failed_retryable")
	failed.MessageIDHash = "sha256:known"
	failed.ContentSHA256 = "sha256:content"
	failed.ErrorCode = "private text"
	failed.MessageSentDate = stringPointer("2026-08-13")
	failed.SubjectApplicationSignal = boolPointer(true)
	failed.SubjectRoute = stringPointer("both")
	inTransaction(t, db, func(tx *sql.Tx) {
		if attempts, err := Record(ctx, tx, unknown); err != nil || attempts != 1 {
			t.Fatalf("unknown sender: attempts=%d error=%v", attempts, err)
		}
	})
	inTransaction(t, db, func(tx *sql.Tx) {
		if attempts, err := Record(ctx, tx, failed); err != nil || attempts != 1 {
			t.Fatalf("retryable: attempts=%d error=%v", attempts, err)
		}
	})
	inTransaction(t, db, func(tx *sql.Tx) {
		epoch, uid, err := Position(ctx, tx)
		if err != nil || epoch != "100" || uid != 5 {
			t.Fatalf("position: epoch=%q uid=%d error=%v", epoch, uid, err)
		}
		receipt, err := ByTransport(ctx, tx, "100", 7)
		if err != nil || receipt == nil || receipt.ErrorCode != "EMAIL_IMPORT_FAILED" {
			t.Fatalf("safe error code: receipt=%+v error=%v", receipt, err)
		}
		for _, hash := range []string{"sha256:unknown", "sha256:known"} {
			processed, err := IdentityWasProcessed(ctx, tx, "message_id_hash", hash, "100", 9)
			if err != nil || processed {
				t.Fatalf("nonterminal identity %q: processed=%t error=%v", hash, processed, err)
			}
		}
		none, err := PendingRetryUIDs(ctx, tx, "100", nil)
		if err != nil || !reflect.DeepEqual(none, []uint32{7}) {
			t.Fatalf("unapproved retry: %v %v", none, err)
		}
		approved, err := PendingRetryUIDs(ctx, tx, "100", map[string]bool{"new@example.test": true})
		if err != nil || !reflect.DeepEqual(approved, []uint32{5, 7}) {
			t.Fatalf("approved retry: %v %v", approved, err)
		}
	})
	imported := baseInput(7, "imported")
	imported.MessageIDHash = "sha256:known"
	imported.ContentSHA256 = "sha256:content"
	imported.CandidateCount, imported.DiscoveryCount = 2, 1
	imported.AdvanceCursor = true
	imported.RecordedAt = "2026-08-15T12:00:00Z"
	inTransaction(t, db, func(tx *sql.Tx) {
		if attempts, err := Record(ctx, tx, imported); err != nil || attempts != 2 {
			t.Fatalf("import: attempts=%d error=%v", attempts, err)
		}
	})
	noCandidates := baseInput(3, "no_candidates")
	noCandidates.AdvanceCursor = true
	inTransaction(t, db, func(tx *sql.Tx) {
		if attempts, err := Record(ctx, tx, noCandidates); err != nil || attempts != 1 {
			t.Fatalf("no candidates: attempts=%d error=%v", attempts, err)
		}
	})
	inTransaction(t, db, func(tx *sql.Tx) {
		epoch, uid, err := Position(ctx, tx)
		if err != nil || epoch != "100" || uid != 7 {
			t.Fatalf("monotonic cursor: %q %d %v", epoch, uid, err)
		}
		receipt, err := ByTransport(ctx, tx, "100", 7)
		if err != nil || receipt == nil {
			t.Fatalf("updated receipt: %v %v", receipt, err)
		}
		if receipt.AttemptCount != 2 || receipt.FirstSeenAt != "2026-08-14T12:00:00Z" ||
			receipt.UpdatedAt != "2026-08-15T12:00:00Z" || receipt.MessageSentDate != "2026-08-13" ||
			!receipt.SubjectApplicationSignal || receipt.SubjectRoute != "both" || receipt.ErrorCode != "" {
			t.Fatalf("source preservation mismatch: %+v", receipt)
		}
		for _, test := range []struct {
			field, value string
			uid          uint32
			want         bool
		}{
			{"message_id_hash", "sha256:known", 7, false},
			{"message_id_hash", "sha256:known", 9, true},
			{"content_sha256", "sha256:content", 9, true},
			{"message_id_hash", "", 9, false},
		} {
			got, err := IdentityWasProcessed(ctx, tx, test.field, test.value, "100", test.uid)
			if err != nil || got != test.want {
				t.Fatalf("identity %+v: got=%t error=%v", test, got, err)
			}
		}
	})
	inTransaction(t, db, func(tx *sql.Tx) {
		changed, err := SetUIDValidity(ctx, tx, "101")
		if err != nil || !changed {
			t.Fatalf("epoch reset: changed=%t error=%v", changed, err)
		}
		epoch, uid, err := Position(ctx, tx)
		if err != nil || epoch != "101" || uid != 0 {
			t.Fatalf("reset position: %q %d %v", epoch, uid, err)
		}
		retries, err := PendingRetryUIDs(ctx, tx, "101", map[string]bool{"new@example.test": true})
		if err != nil || len(retries) != 0 {
			t.Fatalf("new-epoch retries: %v %v", retries, err)
		}
		duplicate, err := IdentityWasProcessed(ctx, tx, "message_id_hash", "sha256:known", "101", 1)
		if err != nil || !duplicate {
			t.Fatalf("cross-epoch identity: %t %v", duplicate, err)
		}
	})
	duplicate := baseInput(1, "duplicate")
	duplicate.UIDValidity = "101"
	duplicate.MessageIDHash = "sha256:known"
	duplicate.AdvanceCursor = true
	inTransaction(t, db, func(tx *sql.Tx) {
		if attempts, err := Record(ctx, tx, duplicate); err != nil || attempts != 1 {
			t.Fatalf("duplicate: attempts=%d error=%v", attempts, err)
		}
		epoch, uid, err := Position(ctx, tx)
		if err != nil || epoch != "101" || uid != 1 {
			t.Fatalf("post-reset position: %q %d %v", epoch, uid, err)
		}
	})
}

func TestRecordRequiresCallerRollbackOnFailure(t *testing.T) {
	t.Parallel()
	ctx, db := context.Background(), receiptDatabase(t)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	input := baseInput(2, "imported")
	input.UIDValidity = "999"
	input.AdvanceCursor = true
	if _, err := Record(ctx, tx, input); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing matching epoch: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM job_email_message_receipts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("uncommitted receipt: count=%d error=%v", count, err)
	}
}

func TestCanceledContextHasSafeDistinctCode(t *testing.T) {
	t.Parallel()
	db := receiptDatabase(t)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = Position(ctx, tx)
	if !errors.Is(err, ErrCanceled) || err.Error() != "EMAIL_SYNC_CANCELED" {
		t.Fatalf("cancellation code: %v", err)
	}
}
