//go:build windows

package emailconnection

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

func TestConnectionSnapshotCurrentSchema(t *testing.T) {
	if os.Getenv("CAREERHUB_SNAPSHOT_SCHEMA_CHILD") == "1" {
		checkConnectionSnapshotCurrentSchema(t, os.Getenv("CAREERHUB_SNAPSHOT_SCHEMA_HOME"))
		return
	}
	homePath := filepath.Join(t.TempDir(), "home")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConnectionSnapshotCurrentSchema$", "-test.v")
	child.Env = append(os.Environ(),
		"CAREERHUB_SNAPSHOT_SCHEMA_CHILD=1", "CAREERHUB_SNAPSHOT_SCHEMA_HOME="+homePath)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("disposable schema-28 snapshot child failed: %v %.1800s", err, output)
	}
}

func checkConnectionSnapshotCurrentSchema(t *testing.T, homePath string) {
	t.Helper()
	ctx := context.Background()
	if _, problem := datahome.BootstrapForProcess(ctx, homePath, "dev"); problem != nil {
		t.Fatal(problem.Code)
	}
	if _, err := schema.PrepareOwned(ctx, homePath); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(homePath, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != schema.Version {
		t.Fatalf("wrong synthetic schema version: %d, %v", version, err)
	}
	read := func() ConnectionSnapshot {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		snapshot, err := ReadSnapshot(ctx, tx, false)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	if got := read(); got.Configured || got.ContractVersion != 2 || got.CaptureQuality.Version != 2 {
		t.Fatal("fresh schema produced an invalid unconfigured snapshot")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO job_email_connections (
		connection_id, provider, account_address, folder, secret_name,
		enabled, lookback_days, max_messages_per_sync, revision,
		created_at, updated_at
	) VALUES ('primary', 'gmail_imap', 'synthetic@example.test', 'INBOX',
		'synthetic-not-real', 1, 14, 250, 1, 't0', 't0')`); err != nil {
		t.Fatal(err)
	}
	if got := read(); !got.Configured || got.AccountAddress != "synthetic@example.test" ||
		got.Revision != 1 || got.Sync.LastResult != "never" || got.Receipts.Total != 0 {
		t.Fatal("current-schema row produced an invalid configured snapshot")
	}
}
