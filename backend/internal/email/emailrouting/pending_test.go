package emailrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/storage/sqlitestore"
)

func pendingDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pending.sqlite3")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.OpenReadWriteExisting(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, schema := range []string{
		`CREATE TABLE job_email_sender_rules (
			connection_id TEXT, sender_address TEXT, disposition TEXT)`,
		`CREATE TABLE job_email_message_receipts (
			connection_id TEXT, sender_address TEXT, status TEXT,
			message_sent_date TEXT, first_seen_at TEXT, updated_at TEXT,
			subject_job_signal INTEGER, subject_service_signal INTEGER,
			subject_application_signal INTEGER)`,
	} {
		if _, err := db.Exec(schema); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestPendingSenderProjectionMatchesPythonSource(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "migration_email_pending_sender_projection.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Rules []struct {
			ConnectionID  string `json:"connection_id"`
			SenderAddress string `json:"sender_address"`
			Disposition   string `json:"disposition"`
		} `json:"rules"`
		Receipts []struct {
			ConnectionID      string `json:"connection_id"`
			SenderAddress     string `json:"sender_address"`
			Status            string `json:"status"`
			MessageSentDate   string `json:"message_sent_date"`
			FirstSeenAt       string `json:"first_seen_at"`
			UpdatedAt         string `json:"updated_at"`
			JobSignal         int    `json:"job_signal"`
			ServiceSignal     int    `json:"service_signal"`
			ApplicationSignal int    `json:"application_signal"`
		} `json:"receipts"`
		Want []PendingSender `json:"want"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Want) < 5 {
		t.Fatalf("invalid shared fixture: %v", err)
	}
	db := pendingDB(t)
	for _, rule := range fixture.Rules {
		if _, err := db.Exec(`INSERT INTO job_email_sender_rules VALUES (?, ?, ?)`,
			rule.ConnectionID, rule.SenderAddress, rule.Disposition); err != nil {
			t.Fatal(err)
		}
	}
	for _, receipt := range fixture.Receipts {
		if _, err := db.Exec(`INSERT INTO job_email_message_receipts VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			receipt.ConnectionID, receipt.SenderAddress, receipt.Status, receipt.MessageSentDate,
			receipt.FirstSeenAt, receipt.UpdatedAt, receipt.JobSignal, receipt.ServiceSignal,
			receipt.ApplicationSignal); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err := LoadPendingSenders(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, fixture.Want) {
		t.Errorf("pending senders = %+v, want %+v", got, fixture.Want)
	}
}

func TestPendingSenderProjectionEmptyAndUnavailable(t *testing.T) {
	t.Parallel()
	db := pendingDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err := LoadPendingSenders(context.Background(), tx)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty projection = %+v, %v", got, err)
	}
	for _, request := range []struct {
		ctx context.Context
		tx  *sql.Tx
	}{
		{nil, tx}, {context.Background(), nil},
	} {
		_, err := LoadPendingSenders(request.ctx, request.tx)
		if !errors.Is(err, ErrPendingUnavailable) || err.Error() != "EMAIL_CONNECTION_FAILED" {
			t.Fatalf("unexpected unavailability error: %v", err)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LoadPendingSenders(canceled, tx); !errors.Is(err, ErrPendingUnavailable) {
		t.Fatalf("canceled read: %v", err)
	}
}

func TestPendingSenderProjectionStorageErrorIsContentFree(t *testing.T) {
	t.Parallel()
	db := pendingDB(t)
	if _, err := db.Exec(`DROP TABLE job_email_message_receipts`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err := LoadPendingSenders(context.Background(), tx)
	if got != nil || !errors.Is(err, ErrPendingUnavailable) || err.Error() != "EMAIL_CONNECTION_FAILED" {
		t.Fatalf("unexpected unavailable projection: %+v, %v", got, err)
	}
}
