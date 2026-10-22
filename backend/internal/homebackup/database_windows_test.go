//go:build windows

package homebackup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sqlitestore"
	"modernc.org/sqlite"
)

func TestDatabaseSnapshotNative(t *testing.T) {
	for _, scenario := range []string{"wal", "delete", "consume-failure", "cancel", "absent", "orphan", "future-schema", "existing-stage", "corrupt", "unowned"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDatabaseSnapshotHelper$")
			cmd.Env = append(os.Environ(), "CH_BACKUP_DATABASE_SCENARIO="+scenario, "CH_BACKUP_DATABASE_HOME="+dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", scenario, err, out)
			}
			if scenario != "wal" {
				return
			}

		})
	}
}

func TestDatabaseSnapshotHelper(t *testing.T) {
	scenario := os.Getenv("CH_BACKUP_DATABASE_SCENARIO")
	if scenario == "" {
		return
	}
	dir := os.Getenv("CH_BACKUP_DATABASE_HOME")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sourcePath := filepath.Join(dir, filepath.FromSlash(databasePath))
	if err := os.Mkdir(filepath.Dir(sourcePath), 0700); err != nil {
		t.Fatal(err)
	}
	var live *sql.DB
	wantCode := ""
	switch scenario {
	case "absent":
	case "orphan":
		if err := os.WriteFile(sourcePath+"-wal", []byte("orphan"), 0600); err != nil {
			t.Fatal(err)
		}
		wantCode = "BACKUP_DATABASE_ORPHANED_FILES"
	case "corrupt":
		if err := os.WriteFile(sourcePath, []byte("not a SQLite database"), 0600); err != nil {
			t.Fatal(err)
		}
	case "unowned":
		wantCode = "BACKUP_OWNERSHIP_REQUIRED"
	default:
		if err := os.WriteFile(sourcePath, nil, 0600); err != nil {
			t.Fatal(err)
		}
		var err error
		live, err = sqlitestore.OpenReadWriteExisting(ctx, sourcePath)
		if err != nil {
			t.Fatal(err)
		}
		live.SetMaxOpenConns(1)
		defer live.Close()
		mode := "WAL"
		if scenario == "delete" {
			mode = "DELETE"
		}
		if _, err = live.ExecContext(ctx, "PRAGMA journal_mode="+mode+"; PRAGMA wal_autocheckpoint=0; PRAGMA user_version=1; CREATE TABLE fixture(value TEXT); INSERT INTO fixture VALUES ('committed WAL value')"); err != nil {
			t.Fatal(err)
		}
		if mode == "WAL" {
			if info, err := os.Stat(sourcePath + "-wal"); err != nil || info.Size() == 0 {
				t.Fatal("no committed WAL fixture", err)
			}
		}
		switch scenario {
		case "future-schema":
			if _, err = live.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", applications.SchemaVersion+1)); err != nil {
				t.Fatal(err)
			}
			wantCode = "BACKUP_SCHEMA_UNSUPPORTED"
		case "existing-stage":
			target := filepath.Join(dir, filepath.FromSlash(syntheticSnapshot+"/"+databasePath))
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(target, []byte("preserve existing stage"), 0600); err != nil {
				t.Fatal(err)
			}
			wantCode = "BACKUP_DATABASE_STAGE_FAILED"
		case "consume-failure":
			wantCode = "SYNTHETIC_CONSUMER_FAILURE"
		case "cancel":
			wantCode = "BACKUP_CANCELED"
		}
	}
	r, pathProblem := runtimepaths.Open(dir)
	if pathProblem != nil {
		t.Fatal(pathProblem)
	}
	defer r.Close()
	if scenario != "unowned" {
		if pathProblem = r.ClaimForProcess(); pathProblem != nil {
			t.Fatal(pathProblem)
		}
	}
	called := false
	p := withDatabaseSnapshot(ctx, dir, syntheticSnapshot, func(root *runtimepaths.Root, snapshot *databaseSnapshot) *Error {
		called = true
		if scenario == "absent" {
			if snapshot != nil {
				t.Fatal("invented database")
			}
			return nil
		}
		if snapshot == nil || snapshot.SchemaVersion != 1 || snapshot.File.RelativePath != databasePath || snapshot.File.SizeBytes <= 0 || len(snapshot.File.SHA256) != 64 {
			t.Fatal(snapshot)
		}
		copyPath := filepath.Join(dir, filepath.FromSlash(snapshot.RelativePath))
		copy, err := sqlitestore.OpenReadOnly(ctx, copyPath)
		if err != nil {
			t.Fatal(err)
		}
		var value, mode string
		if err = copy.QueryRowContext(ctx, "SELECT value FROM fixture").Scan(&value); err != nil || value != "committed WAL value" {
			t.Fatal("lost committed data", value, err)
		}
		if err = copy.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "delete" {
			t.Fatal("copy depends on WAL", mode, err)
		}
		if err = copy.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err = live.ExecContext(ctx, "PRAGMA busy_timeout=1; INSERT INTO fixture VALUES ('must not commit')"); err == nil {
			t.Fatal("source writer was not reserved through consumer")
		} else {
			var sqliteErr *sqlite.Error
			if !errors.As(err, &sqliteErr) || sqliteErr.Code()&255 != 5 {
				t.Fatal("expected SQLITE_BUSY", err)
			}
		}
		if scenario == "consume-failure" {
			return failure("SYNTHETIC_CONSUMER_FAILURE")
		}
		if scenario == "cancel" {
			cancel()
		}
		return nil
	})
	if scenario == "corrupt" {
		if p == nil || called {
			t.Fatal("corrupt database admitted", p)
		}
	} else if wantCode != "" {
		if p == nil || p.Code != wantCode {
			t.Fatal("unexpected failure", p, "want", wantCode)
		}
	} else if p != nil || !called {
		t.Fatal("snapshot failed", p)
	}
	if wantCode != "" && scenario != "consume-failure" && scenario != "cancel" && called {
		t.Fatal("consumer called after failed admission")
	}
	if live != nil {
		var version int
		wantVersion := 1
		if scenario == "future-schema" {
			wantVersion = applications.SchemaVersion + 1
		}
		if err := live.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != wantVersion {
			t.Fatal("source schema changed", version, err)
		}
		var count int
		if err := live.QueryRow("SELECT count(*) FROM fixture").Scan(&count); err != nil || count != 1 {
			t.Fatal("source contents changed", count, err)
		}
		if _, err := live.Exec("INSERT INTO fixture VALUES ('after return')"); err != nil {
			t.Fatal("writer reservation leaked", err)
		}
		if err := live.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(sourcePath, sourcePath+".closed"); err != nil {
			t.Fatal("source guard leaked", err)
		}
	}
	if called && scenario != "absent" {
		target := filepath.Join(dir, filepath.FromSlash(syntheticSnapshot+"/"+databasePath))
		if err := os.Rename(target, target+".closed"); err != nil {
			t.Fatal("stage guard leaked", err)
		}
		if err := os.Rename(target+".closed", target); err != nil {
			t.Fatal(err)
		}
	}
	if scenario == "existing-stage" {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(syntheticSnapshot+"/"+databasePath)))
		if err != nil || string(data) != "preserve existing stage" {
			t.Fatal("stage overwritten", err)
		}
	}
	if scenario == "absent" || scenario == "orphan" || scenario == "future-schema" || scenario == "unowned" || scenario == "corrupt" {
		if _, err := os.Stat(filepath.Join(dir, ".career_hub_staging")); !os.IsNotExist(err) {
			t.Fatal("stage created before admission", err)
		}
	}
}
