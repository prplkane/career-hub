package schema

import (
	"context"
	"testing"

	"careerhub.local/backend/internal/emailreceipts"
)

func TestEmailReceiptsCurrentSchema(t *testing.T) {
	db := database(t)
	if err := upgrade(t, db, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO job_email_connections (
		connection_id, provider, account_address, folder, secret_name,
		enabled, lookback_days, max_messages_per_sync, revision,
		created_at, updated_at
	) VALUES ('primary', 'gmail_imap', 'alerts@example.test', 'INBOX',
		'synthetic', 1, 14, 250, 1, 't0', 't0')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO job_email_sync_state (connection_id, folder)
		VALUES ('primary', 'INBOX')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if changed, err := emailreceipts.SetUIDValidity(ctx, tx, "100"); err != nil || changed {
		t.Fatalf("initial epoch: changed=%t error=%v", changed, err)
	}
	input := emailreceipts.RecordInput{
		UIDValidity: "100", UID: 4, SenderAddress: "alerts@example.test",
		Status: "no_candidates", AdvanceCursor: true,
		RecordedAt: "2026-08-14T12:00:00Z",
	}
	if attempts, err := emailreceipts.Record(ctx, tx, input); err != nil || attempts != 1 {
		t.Fatalf("receipt: attempts=%d error=%v", attempts, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	read, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Rollback()
	epoch, uid, err := emailreceipts.Position(ctx, read)
	if err != nil || epoch != "100" || uid != 4 {
		t.Fatalf("current-schema cursor: epoch=%q uid=%d error=%v", epoch, uid, err)
	}
	receipt, err := emailreceipts.ByTransport(ctx, read, "100", 4)
	if err != nil || receipt == nil || receipt.Status != "no_candidates" {
		t.Fatalf("current-schema receipt: %+v error=%v", receipt, err)
	}
	if err := read.Rollback(); err != nil {
		t.Fatal(err)
	}
	lease, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Rollback()
	deferred, err := emailreceipts.AcquireLease(ctx, lease, "2026-08-14T12:00:00Z", "synthetic-owner", false)
	if err != nil || deferred != "" {
		t.Fatalf("current-schema lease: deferred=%q error=%v", deferred, err)
	}
	completed, err := emailreceipts.MarkCompleted(ctx, lease, "synthetic-owner", "2026-08-14T12:01:00Z", false, false)
	if err != nil || !completed {
		t.Fatalf("current-schema completion: completed=%t error=%v", completed, err)
	}
	if err := lease.Commit(); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.QueryRow(`SELECT last_test_status FROM job_email_connections
		WHERE connection_id = 'primary'`).Scan(&status); err != nil || status != "succeeded" {
		t.Fatalf("current-schema connection health: status=%q error=%v", status, err)
	}
}
