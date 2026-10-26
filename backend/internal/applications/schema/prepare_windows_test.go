//go:build windows

package schema

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

type nativePreparationRequest struct {
	Root       string
	Claim      bool
	Fault      string
	KeepSource bool
}

type nativePreparationReply struct {
	Report  *Preparation
	Code    string
	Cleaned bool
}

func TestPreparationNativeWorker(t *testing.T) {
	if os.Getenv("CAREERHUB_SCHEMA_TEST_WORKER") != "1" {
		return
	}
	var request nativePreparationRequest
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		t.Fatal(err)
	}
	if request.Claim {
		root, e := runtimepaths.Open(request.Root)
		if e != nil {
			t.Fatal(e)
		}
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e)
		}
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if request.Fault == "hot-journal" {
		// Force a real uncommitted page-cache spill, not just a journal-shaped
		// sentinel. Process exit bypasses Go/sqlite cleanup and releases locks.
		db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(request.Root, filepath.FromSlash(databaseRelative)))
		if err != nil {
			t.Fatal(err)
		}
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, statement := range []string{"PRAGMA cache_size=1", "BEGIN IMMEDIATE", "UPDATE crash_fixture SET value=replace(value,'stable','broken')"} {
			if _, err := conn.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		os.Exit(77)
	}
	fault := func(stage string) error {
		if request.Fault == "exit-"+stage {
			os.Exit(77)
		}
		if request.Fault == stage {
			return errors.New("synthetic private failure must not escape")
		}
		if request.Fault == "cancel-"+stage {
			cancel()
		}
		return nil
	}
	report, err := prepareOwned(ctx, request.Root, fault)
	reply := nativePreparationReply{Report: report, Cleaned: true}
	if err != nil {
		var known *PreparationError
		if !errors.As(err, &known) || errors.Unwrap(err) != nil || strings.Contains(err.Error(), request.Root) {
			t.Fatal("preparation leaked an unexpected error")
		}
		reply.Code = known.Code
	}
	// Prove returned calls released their own main-file pins before process
	// exit. WAL fixtures intentionally retain another connection in the parent.
	if !request.KeepSource {
		path := filepath.Join(request.Root, filepath.FromSlash(databaseRelative))
		if _, err := os.Stat(path); err == nil {
			if err := os.Rename(path, path+".closed"); err != nil {
				reply.Cleaned = false
			} else if err := os.Rename(path+".closed", path); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(reply); err != nil {
		t.Fatal(err)
	}
	// Process exit, not a public unlock, releases the intentionally retained
	// Data Home claim. Preparation has already performed synchronous cleanup.
	os.Exit(0)
}

func invokePreparationCrash(t *testing.T, request nativePreparationRequest) {
	t.Helper()
	input, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestPreparationNativeWorker$")
	command.Env = append(os.Environ(), "CAREERHUB_SCHEMA_TEST_WORKER=1")
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() != 77 || len(output) != 0 {
		t.Fatalf("native worker did not reach the exact abrupt-exit milestone: %v", err)
	}
}

func TestRestartAfterAbruptPreparationExit(t *testing.T) {
	for _, stage := range []string{"reserved", "backed-up", "upgraded", "committed"} {
		t.Run(stage, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, filepath.FromSlash(databaseRelative))
			db := nativeSchemaFixture(t, path, 17, false)
			before := snapshot(t, db)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			invokePreparationCrash(t, nativePreparationRequest{Root: root, Claim: true, Fault: "exit-" + stage})
			reply := invokePreparation(t, nativePreparationRequest{Root: root, Claim: true})
			if reply.Code != "" || !reply.Cleaned || reply.Report == nil {
				t.Fatalf("crash restart failed: %+v", reply)
			}
			if stage == "committed" {
				if reply.Report.PreviousVersion != Version || reply.Report.CommitAttempted || reply.Report.Recovery != nil {
					t.Fatal("post-commit restart replayed an already committed upgrade")
				}
			} else {
				if reply.Report.PreviousVersion != 17 || !reply.Report.Committed || reply.Report.Recovery == nil {
					t.Fatal("pre-commit crash silently committed a partial upgrade")
				}
			}
			metadata, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(recoveryDirectory), "*.json"))
			if err != nil {
				t.Fatal(err)
			}
			count := 2
			if stage == "reserved" || stage == "committed" {
				count = 1
			}
			if len(metadata) != count {
				t.Fatal("restart lost or duplicated a completed recovery record")
			}
			for _, path := range metadata {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var record Recovery
				if err := json.Unmarshal(raw, &record); err != nil {
					t.Fatal(err)
				}
				verifyNativeRecovery(t, root, &record, before)
			}
		})
	}
}

func TestPreparationFailureAfterCommitRetainsCommitFacts(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db := nativeSchemaFixture(t, filepath.Join(root, filepath.FromSlash(databaseRelative)), 17, false)
	before := snapshot(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reply := invokePreparation(t, nativePreparationRequest{Root: root, Claim: true, Fault: "committed"})
	if reply.Code != "APPLICATION_PREPARATION_CHECKPOINT_FAILED" || !reply.Cleaned || reply.Report == nil || !reply.Report.CommitAttempted || !reply.Report.Committed || reply.Report.Recovery == nil {
		t.Fatalf("post-commit failure lost its commit facts: %+v", reply)
	}
	verifyNativeRecovery(t, root, reply.Report.Recovery, before)
	restarted := invokePreparation(t, nativePreparationRequest{Root: root, Claim: true})
	if restarted.Code != "" || restarted.Report.PreviousVersion != Version || restarted.Report.CommitAttempted || restarted.Report.Recovery != nil {
		t.Fatal("retry after post-commit failure applied migration twice")
	}
}

func TestPreparationRecoversSpilledHotJournalBeforeBackup(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(databaseRelative))
	db := nativeSchemaFixture(t, path, 17, false)
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE crash_fixture(id INTEGER PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for id := 0; id < 40; id++ {
		if _, err := tx.Exec("INSERT INTO crash_fixture VALUES(?,?)", id, strings.Repeat("stable", 4096)); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	invokePreparationCrash(t, nativePreparationRequest{Root: root, Claim: true, Fault: "hot-journal"})
	journal, err := os.ReadFile(path + "-journal")
	if err != nil || len(journal) <= 512 || bytes.Equal(journal[:8], make([]byte, 8)) {
		t.Fatal("fixture did not leave a nonzero hot rollback journal")
	}
	uncommitted, err := os.ReadFile(path)
	if err != nil || bytes.Equal(original, uncommitted) {
		t.Fatal("fixture did not spill uncommitted pages to the main file")
	}
	// Do not open a different SQLite reader first: preparation itself must
	// recover the interrupted writer before creating its logical recovery copy.
	reply := invokePreparation(t, nativePreparationRequest{Root: root, Claim: true})
	if reply.Code != "" || !reply.Cleaned || reply.Report == nil || reply.Report.PreviousVersion != 17 || !reply.Report.Committed || reply.Report.Recovery == nil {
		t.Fatalf("hot-journal preparation failed: %+v", reply)
	}
	verifyNativeRecovery(t, root, reply.Report.Recovery, before)
	current, err := sqlitestore.OpenReadOnly(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	var changed int
	if err := current.QueryRow("SELECT count(*) FROM crash_fixture WHERE value <> ?", strings.Repeat("stable", 4096)).Scan(&changed); err != nil || changed != 0 {
		t.Fatal("an uncommitted update survived crash recovery")
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + "-journal"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("finished preparation retained a hot journal")
	}
}

func TestPrepareOwnedNative(t *testing.T) {
	cases := []struct {
		name, fault, code string
		version           int
		claim, wal        bool
	}{
		{name: "new", version: -1, claim: true},
		{name: "empty-existing", version: 0, claim: true},
		{name: "current", version: Version, claim: true},
		{name: "previous", version: Version - 1, claim: true},
		{name: "newer", version: Version + 1, claim: true, code: "APPLICATION_SCHEMA_TOO_NEW"},
		{name: "upgrade-delete", version: 17, claim: true},
		{name: "upgrade-wal", version: 17, claim: true, wal: true},
		{name: "fault-before-backup", version: 17, claim: true, fault: "reserved", code: "APPLICATION_PREPARATION_CHECKPOINT_FAILED"},
		{name: "fault-after-backup", version: 17, claim: true, fault: "backed-up", code: "APPLICATION_PREPARATION_CHECKPOINT_FAILED"},
		{name: "fault-after-upgrade", version: 17, claim: true, fault: "upgraded", code: "APPLICATION_PREPARATION_CHECKPOINT_FAILED"},
		{name: "cancel-after-upgrade", version: 17, claim: true, fault: "cancel-upgraded", code: "APPLICATION_PREPARATION_CANCELED"},
		{name: "unowned", version: 17, code: "DATA_HOME_OWNERSHIP_REQUIRED"},
		{name: "orphan-wal", version: -1, claim: true, code: "APPLICATION_ORPHANED_SQLITE_FILES"},
		{name: "backup-directory-collision", version: 17, claim: true, code: "APPLICATION_BACKUP_PATH_FAILED"},
		{name: "dangling-reference", version: 9, claim: true, code: "APPLICATION_SCHEMA_UPGRADE_FAILED"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, filepath.FromSlash(databaseRelative))
			var before map[string]any
			var fixture *sql.DB
			if c.version >= 0 {
				fixture = nativeSchemaFixture(t, path, c.version, c.wal)
				if c.name == "dangling-reference" {
					if _, err := fixture.Exec(`INSERT INTO job_applications(job_id,company,title,source_url,content_fingerprint,current_status,created_at,updated_at,applied_resume_id) VALUES ('synthetic','c','t','u','h','captured','now','now','missing')`); err != nil {
						_ = fixture.Close()
						t.Fatal(err)
					}
				}
				before = snapshot(t, fixture)
				if c.wal {
					defer fixture.Close()
				} else if err := fixture.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if c.name == "orphan-wal" {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path+"-wal", []byte("orphan synthetic WAL"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if c.name == "backup-directory-collision" {
				if err := os.WriteFile(filepath.Join(root, "backups"), []byte("must remain"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			reply := invokePreparation(t, nativePreparationRequest{Root: root, Claim: c.claim, Fault: c.fault, KeepSource: c.wal})
			if reply.Code != c.code || !reply.Cleaned || reply.Report == nil {
				t.Fatalf("unexpected preparation result: %+v", reply)
			}
			if reply.Report.CommitAttempted != reply.Report.Committed {
				t.Fatal("unexpected unconfirmed commit")
			}
			if c.code == "" && c.version < Version {
				if !reply.Report.Committed {
					t.Fatal("successful upgrade not committed")
				}
			} else if reply.Report.Committed {
				t.Fatal("failed/current/newer preparation committed")
			}
			if c.name == "orphan-wal" {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("orphaned auxiliary file caused a new main to be created")
				}
				return
			}
			db, err := sqlitestore.OpenReadOnly(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			after := snapshot(t, db)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if c.code != "" || c.version == Version {
				if !reflect.DeepEqual(before, after) {
					t.Fatal("failed/no-op preparation changed schema/data")
				}
			} else if after["version"] != float64(Version) {
				t.Fatal("upgrade did not reach current schema")
			}
			if reply.Report.Recovery != nil {
				verifyNativeRecovery(t, root, reply.Report.Recovery, before)
			} else if c.version > 0 && c.version < Version && c.code == "" {
				t.Fatal("upgrade committed without a verified recovery record")
			}
			if c.version <= 0 || c.version >= Version || !c.claim || c.fault == "reserved" {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(recoveryDirectory))); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("unnecessary recovery directory or backup created")
				}
			}
		})
	}
}

func TestPrepareOwnedPreservesCorruptDatabase(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(databaseRelative))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	before := []byte("corrupt synthetic database must not be reset")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	reply := invokePreparation(t, nativePreparationRequest{Root: root, Claim: true})
	if reply.Code == "" || reply.Report == nil || reply.Report.CommitAttempted || reply.Report.DatabaseCreated || !reply.Cleaned {
		t.Fatalf("corrupt database accepted/reset: %+v", reply)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed preparation changed corrupt original")
	}
}

func invokePreparation(t *testing.T, request nativePreparationRequest) nativePreparationReply {
	t.Helper()
	input, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestPreparationNativeWorker$")
	command.Env = append(os.Environ(), "CAREERHUB_SCHEMA_TEST_WORKER=1")
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("native worker failed: %v; %s", err, output)
	}
	var reply nativePreparationReply
	if err := json.Unmarshal(output, &reply); err != nil {
		t.Fatalf("worker protocol: %v; %s", err, output)
	}
	return reply
}

func verifyNativeRecovery(t *testing.T, root string, record *Recovery, expected map[string]any) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(recoveryDirectory), record.BackupFile)
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bytes)
	if record.SHA256 != hex.EncodeToString(digest[:]) || record.SizeBytes != int64(len(bytes)) {
		t.Fatal("recovery fingerprint does not describe its file")
	}
	metadata, err := os.ReadFile(strings.TrimSuffix(path, ".sqlite3") + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var persisted Recovery
	if err := json.Unmarshal(metadata, &persisted); err != nil || !reflect.DeepEqual(record, &persisted) || metadata[len(metadata)-1] != '\n' {
		t.Fatal("recovery completion metadata differs")
	}
	db, err := sqlitestore.OpenReadOnly(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	actual := snapshot(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("recovery is not the exact pre-upgrade logical image: %s", firstDifference(actual, expected))
	}

	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("closed recovery still depends on an auxiliary file")
		}
	}
}
