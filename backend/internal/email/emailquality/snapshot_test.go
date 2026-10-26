package emailquality

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/storage/sqlitestore"
)

func qualityDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "quality.sqlite3")
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
			connection_id TEXT, sender_address TEXT, disposition TEXT,
			purpose TEXT, subject_policy_json TEXT)`,
		`CREATE TABLE job_email_message_receipts (
			receipt_id INTEGER PRIMARY KEY, connection_id TEXT,
			message_id_hash TEXT, sender_address TEXT)`,
		`CREATE TABLE job_discoveries (
			discovery_id TEXT, title TEXT, company TEXT, location TEXT,
			canonical_url TEXT, observed_url TEXT, state TEXT)`,
		`CREATE TABLE job_discovery_observations (
			observation_id INTEGER PRIMARY KEY, discovery_id TEXT,
			source_kind TEXT, metadata_json TEXT)`,
		`CREATE TABLE job_discovery_field_overrides (
			discovery_id TEXT, title TEXT, company TEXT, location TEXT)`,
	} {
		if _, err := db.Exec(schema); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestQualitySnapshotMatchesPythonSource(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "migration_email_quality_snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SenderRules []struct {
			ConnectionID  string `json:"connection_id"`
			SenderAddress string `json:"sender_address"`
			Disposition   string `json:"disposition"`
			Purpose       string `json:"purpose"`
		} `json:"sender_rules"`
		Receipts []struct {
			MessageIDHash string `json:"message_id_hash"`
			SenderAddress string `json:"sender_address"`
			ConnectionID  string `json:"connection_id"`
		} `json:"receipts"`
		Discoveries []struct {
			DiscoveryID string `json:"discovery_id"`
			Title       string `json:"title"`
			Company     string `json:"company"`
			Location    string `json:"location"`
			SourceURL   string `json:"source_url"`
			State       string `json:"state"`
		} `json:"discoveries"`
		Overrides []struct {
			DiscoveryID string `json:"discovery_id"`
			Title       string `json:"title"`
			Company     string `json:"company"`
			Location    string `json:"location"`
		} `json:"overrides"`
		Observations []struct {
			DiscoveryID  string `json:"discovery_id"`
			SourceKind   string `json:"source_kind"`
			MetadataJSON string `json:"metadata_json"`
		} `json:"observations"`
		Want Snapshot `json:"want"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Observations) < 10 {
		t.Fatalf("invalid shared fixture: %v", err)
	}
	db := qualityDB(t)
	for _, rule := range fixture.SenderRules {
		if _, err := db.Exec(`INSERT INTO job_email_sender_rules VALUES (?, ?, ?, ?, '{}')`,
			rule.ConnectionID, rule.SenderAddress, rule.Disposition, rule.Purpose); err != nil {
			t.Fatal(err)
		}
	}
	for _, receipt := range fixture.Receipts {
		if _, err := db.Exec(`INSERT INTO job_email_message_receipts (connection_id, message_id_hash, sender_address) VALUES (?, ?, ?)`,
			receipt.ConnectionID, receipt.MessageIDHash, receipt.SenderAddress); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range fixture.Discoveries {
		if _, err := db.Exec(`INSERT INTO job_discoveries VALUES (?, ?, ?, ?, ?, ?, ?)`,
			item.DiscoveryID, item.Title, item.Company, item.Location,
			item.SourceURL, item.SourceURL, item.State); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range fixture.Overrides {
		if _, err := db.Exec(`INSERT INTO job_discovery_field_overrides VALUES (?, ?, ?, ?)`,
			item.DiscoveryID, item.Title, item.Company, item.Location); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range fixture.Observations {
		if _, err := db.Exec(`INSERT INTO job_discovery_observations (discovery_id, source_kind, metadata_json) VALUES (?, ?, ?)`,
			item.DiscoveryID, item.SourceKind, item.MetadataJSON); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err := LoadSnapshot(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, fixture.Want) {
		t.Errorf("quality snapshot = %+v, want %+v", got, fixture.Want)
	}
}

func TestQualitySnapshotEmptyAndUnavailable(t *testing.T) {
	t.Parallel()
	db := qualityDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err := LoadSnapshot(context.Background(), tx)
	if err != nil || got.Version != 2 || got.ActiveIssueCount != 0 || got.SenderCount != 0 || got.Alerts == nil {
		t.Fatalf("empty snapshot = %+v, %v", got, err)
	}
	for _, bad := range []struct {
		ctx context.Context
		tx  *sql.Tx
	}{{nil, tx}, {context.Background(), nil}} {
		_, err := LoadSnapshot(bad.ctx, bad.tx)
		if !errors.Is(err, ErrSnapshotUnavailable) || err.Error() != "EMAIL_CONNECTION_FAILED" {
			t.Fatalf("invalid snapshot read returned %v", err)
		}
	}
}

func TestQualitySnapshotStorageFailureIsContentFree(t *testing.T) {
	t.Parallel()
	db := qualityDB(t)
	if _, err := db.Exec(`DROP TABLE job_discovery_observations`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err := LoadSnapshot(context.Background(), tx)
	if !reflect.DeepEqual(got, Snapshot{}) || !errors.Is(err, ErrSnapshotUnavailable) || err.Error() != "EMAIL_CONNECTION_FAILED" {
		t.Fatalf("storage failure returned %+v, %v", got, err)
	}
}

func TestQualityMetadataLimitAndShape(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{strings.Repeat("x", maxMetadataBytes+1), "{bad", `[]`, `{"message_id_hash": 7}`} {
		if got := messageHash(raw); got != "" {
			t.Fatalf("invalid metadata created a hash: %q", got)
		}
	}
}
