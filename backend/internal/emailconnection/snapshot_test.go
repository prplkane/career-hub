package emailconnection

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

	"careerhub.local/backend/internal/sqlitestore"
)

func snapshotDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "connection-snapshot.sqlite3")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.OpenReadWriteExisting(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, schema := range []string{
		`CREATE TABLE job_email_connections (connection_id TEXT, provider TEXT,
			account_address TEXT, folder TEXT, secret_name TEXT, enabled INTEGER,
			schedule_enabled INTEGER, interval_minutes INTEGER, lookback_days INTEGER,
			max_messages_per_sync INTEGER, last_tested_at TEXT, last_test_status TEXT,
			last_error_code TEXT, revision INTEGER)`,
		`CREATE TABLE job_email_sender_rules (connection_id TEXT, sender_address TEXT,
			disposition TEXT, purpose TEXT, subject_policy_json TEXT)`,
		`CREATE TABLE job_email_sync_state (connection_id TEXT, folder TEXT,
			uidvalidity TEXT, last_uid INTEGER, last_started_at TEXT,
			last_completed_at TEXT, last_result TEXT, consecutive_failures INTEGER,
			next_retry_at TEXT, last_error_code TEXT, revision INTEGER)`,
		`CREATE TABLE job_email_message_receipts (receipt_id INTEGER PRIMARY KEY,
			connection_id TEXT, sender_address TEXT, status TEXT, error_code TEXT,
			message_id_hash TEXT, message_sent_date TEXT, first_seen_at TEXT,
			updated_at TEXT, subject_job_signal INTEGER,
			subject_application_signal INTEGER, subject_service_signal INTEGER,
			subject_route TEXT)`,
		`CREATE TABLE job_discoveries (discovery_id TEXT, title TEXT, company TEXT,
			location TEXT, canonical_url TEXT, observed_url TEXT, state TEXT)`,
		`CREATE TABLE job_discovery_observations (observation_id INTEGER PRIMARY KEY,
			discovery_id TEXT, source_kind TEXT, metadata_json TEXT)`,
		`CREATE TABLE job_discovery_field_overrides (discovery_id TEXT,
			title TEXT, company TEXT, location TEXT)`,
	} {
		if _, err := db.Exec(schema); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

type snapshotFixture struct {
	Unconfigured struct {
		CredentialPresent bool               `json:"credential_present"`
		Want              ConnectionSnapshot `json:"want"`
	} `json:"unconfigured"`
	Configured struct {
		CredentialPresent bool `json:"credential_present"`
		Connection        struct {
			Provider           string `json:"provider"`
			AccountAddress     string `json:"account_address"`
			Folder             string `json:"folder"`
			SecretName         string `json:"secret_name"`
			Enabled            int    `json:"enabled"`
			ScheduleEnabled    int    `json:"schedule_enabled"`
			IntervalMinutes    int    `json:"interval_minutes"`
			LookbackDays       int    `json:"lookback_days"`
			MaxMessagesPerSync int    `json:"max_messages_per_sync"`
			LastTestedAt       string `json:"last_tested_at"`
			LastTestStatus     string `json:"last_test_status"`
			LastErrorCode      string `json:"last_error_code"`
			Revision           int    `json:"revision"`
		} `json:"connection"`
		Sync struct {
			UIDValidity         string `json:"uidvalidity"`
			LastUID             int    `json:"last_uid"`
			LastStartedAt       string `json:"last_started_at"`
			LastCompletedAt     string `json:"last_completed_at"`
			LastResult          string `json:"last_result"`
			ConsecutiveFailures int    `json:"consecutive_failures"`
			NextRetryAt         string `json:"next_retry_at"`
			LastErrorCode       string `json:"last_error_code"`
			Revision            int    `json:"revision"`
		} `json:"sync"`
		Rules []struct {
			SenderAddress     string `json:"sender_address"`
			Disposition       string `json:"disposition"`
			Purpose           string `json:"purpose"`
			SubjectPolicyJSON string `json:"subject_policy_json"`
		} `json:"rules"`
		Receipts []struct {
			SenderAddress            string `json:"sender_address"`
			Status                   string `json:"status"`
			MessageIDHash            string `json:"message_id_hash"`
			MessageSentDate          string `json:"message_sent_date"`
			FirstSeenAt              string `json:"first_seen_at"`
			UpdatedAt                string `json:"updated_at"`
			ErrorCode                string `json:"error_code"`
			SubjectRoute             string `json:"subject_route"`
			SubjectJobSignal         int    `json:"subject_job_signal"`
			SubjectApplicationSignal int    `json:"subject_application_signal"`
			SubjectServiceSignal     int    `json:"subject_service_signal"`
		} `json:"receipts"`
		Discoveries []struct {
			DiscoveryID string `json:"discovery_id"`
			Title       string `json:"title"`
			Company     string `json:"company"`
			Location    string `json:"location"`
			SourceURL   string `json:"source_url"`
			State       string `json:"state"`
		} `json:"discoveries"`
		Observations []struct {
			DiscoveryID  string `json:"discovery_id"`
			SourceKind   string `json:"source_kind"`
			MetadataJSON string `json:"metadata_json"`
		} `json:"observations"`
		Want ConnectionSnapshot `json:"want"`
	} `json:"configured"`
}

func TestConnectionSnapshotMatchesPythonSource(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "migration_email_connection_snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture snapshotFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	db := snapshotDB(t)
	read := func(credentialPresent bool) ConnectionSnapshot {
		t.Helper()
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		got, err := ReadSnapshot(context.Background(), tx, credentialPresent)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := read(fixture.Unconfigured.CredentialPresent); !reflect.DeepEqual(got, fixture.Unconfigured.Want) {
		t.Errorf("unconfigured snapshot = %+v, want %+v", got, fixture.Unconfigured.Want)
	}
	c := fixture.Configured.Connection
	if _, err := db.Exec(`INSERT INTO job_email_connections VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"primary", c.Provider, c.AccountAddress, c.Folder, c.SecretName, c.Enabled,
		c.ScheduleEnabled, c.IntervalMinutes, c.LookbackDays, c.MaxMessagesPerSync,
		c.LastTestedAt, c.LastTestStatus, c.LastErrorCode, c.Revision); err != nil {
		t.Fatal(err)
	}
	s := fixture.Configured.Sync
	if _, err := db.Exec(`INSERT INTO job_email_sync_state VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"primary", c.Folder, s.UIDValidity, s.LastUID, s.LastStartedAt, s.LastCompletedAt,
		s.LastResult, s.ConsecutiveFailures, s.NextRetryAt, s.LastErrorCode, s.Revision); err != nil {
		t.Fatal(err)
	}
	for _, r := range fixture.Configured.Rules {
		if _, err := db.Exec(`INSERT INTO job_email_sender_rules VALUES (?, ?, ?, ?, ?)`,
			"primary", r.SenderAddress, r.Disposition, r.Purpose, r.SubjectPolicyJSON); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range fixture.Configured.Receipts {
		if _, err := db.Exec(`INSERT INTO job_email_message_receipts
			(connection_id,sender_address,status,error_code,message_id_hash,
			message_sent_date,first_seen_at,updated_at,subject_job_signal,
			subject_application_signal,subject_service_signal,subject_route)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, "primary", r.SenderAddress, r.Status,
			r.ErrorCode, r.MessageIDHash, r.MessageSentDate, r.FirstSeenAt, r.UpdatedAt,
			r.SubjectJobSignal, r.SubjectApplicationSignal, r.SubjectServiceSignal,
			r.SubjectRoute); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range fixture.Configured.Discoveries {
		if _, err := db.Exec(`INSERT INTO job_discoveries VALUES (?,?,?,?,?,?,?)`,
			d.DiscoveryID, d.Title, d.Company, d.Location, d.SourceURL, d.SourceURL,
			d.State); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range fixture.Configured.Observations {
		if _, err := db.Exec(`INSERT INTO job_discovery_observations
			(discovery_id,source_kind,metadata_json) VALUES (?,?,?)`,
			o.DiscoveryID, o.SourceKind, o.MetadataJSON); err != nil {
			t.Fatal(err)
		}
	}
	got := read(fixture.Configured.CredentialPresent)
	if !reflect.DeepEqual(got, fixture.Configured.Want) {
		t.Errorf("configured snapshot = %+v, want %+v", got, fixture.Configured.Want)
	}
	encoded, err := json.Marshal(got)
	if err != nil || strings.Contains(string(encoded), c.SecretName) {
		t.Fatalf("credential target leaked in snapshot or encoding failed: %v", err)
	}
}

func TestConnectionSnapshotInvalidReadIsContentFree(t *testing.T) {
	t.Parallel()
	for _, input := range []struct {
		ctx context.Context
		tx  *sql.Tx
	}{{nil, nil}, {context.Background(), nil}} {
		_, err := ReadSnapshot(input.ctx, input.tx, false)
		if !errors.Is(err, ErrSnapshotUnavailable) || err.Error() != "EMAIL_CONNECTION_FAILED" {
			t.Fatalf("invalid read returned %v", err)
		}
	}
	db := snapshotDB(t)
	if _, err := db.Exec(`DROP TABLE job_email_connections`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err := ReadSnapshot(context.Background(), tx, false)
	if !reflect.DeepEqual(got, ConnectionSnapshot{}) || !errors.Is(err, ErrSnapshotUnavailable) || err.Error() != "EMAIL_CONNECTION_FAILED" {
		t.Fatalf("failed read returned %+v, %v", got, err)
	}
}

func TestConnectionSnapshotCorruptErrorCodesFailClosed(t *testing.T) {
	t.Parallel()
	db := snapshotDB(t)
	if _, err := db.Exec(`INSERT INTO job_email_connections VALUES (
		'primary', 'gmail_imap', 'jobs@example.test', 'INBOX', 'synthetic-secret-target',
		1, 0, 30, 14, 250, NULL, 'failed', 'private IMAP response', 1)`); err != nil {
		t.Fatal(err)
	}
	read := func() {
		t.Helper()
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		got, err := ReadSnapshot(context.Background(), tx, false)
		if !reflect.DeepEqual(got, ConnectionSnapshot{}) || !errors.Is(err, ErrSnapshotUnavailable) || err.Error() != "EMAIL_CONNECTION_FAILED" {
			t.Fatalf("corrupt code leaked a snapshot: %+v, %v", got, err)
		}
	}
	read()
	if _, err := db.Exec(`UPDATE job_email_connections SET last_error_code = ''`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO job_email_sync_state VALUES (
		'primary', 'INBOX', '', 0, NULL, NULL, 'failed', 1, NULL,
		'private IMAP response', 1)`); err != nil {
		t.Fatal(err)
	}
	read()
}
