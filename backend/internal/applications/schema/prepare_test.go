package schema

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"careerhub.local/backend/internal/storage/sqlitestore"
)

func nativeSchemaFixture(t *testing.T, path string, version int, wal bool) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.OpenReadWriteExisting(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	stop := min(version, Version)
	for _, step := range migrations[:stop] {
		for _, op := range step {
			if _, err := applyAction(context.Background(), tx, op); err != nil {
				_ = tx.Rollback()
				_ = db.Close()
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if version > Version {
		if _, err := db.Exec("PRAGMA user_version=" + strconv.Itoa(version)); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if wal {
		if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0"); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if version >= 13 {
		_, err := db.Exec(`INSERT INTO job_discoveries(discovery_id,state,title,observed_url,content_scope,first_seen_at,last_seen_at,updated_at,revision) VALUES ('synthetic','inbox','must survive backup','https://linkedin.com/jobs/view/123','link_only','now','now','now',1)`)
		if err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	return db
}

func TestPreparationKeepsReservationUntilExplicitRollback(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "fixture.sqlite3")
	db := nativeSchemaFixture(t, path, 17, false)
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	tx, err := beginPreparationTransaction(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	cancel()
	// BeginTx's internal context must not be auto-canceled; transaction work
	// remains owned until our synchronous rollback, even if the caller cancels.
	var version int
	if err := tx.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil || version != 17 {
		t.Fatalf("cancellation asynchronously ended the owned transaction: %v", err)
	}
	competitor, err := sqlitestore.OpenReadWriteExisting(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer competitor.Close()
	if _, err := competitor.Exec("PRAGMA busy_timeout=1; UPDATE job_discoveries SET title='competitor'"); err == nil {
		t.Fatal("reservation released before explicit rollback")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := competitor.Exec("UPDATE job_discoveries SET title='competitor'"); err != nil {
		t.Fatal("explicit rollback did not release writer reservation")
	}
}
