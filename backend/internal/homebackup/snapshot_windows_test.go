//go:build windows

package homebackup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sqlitestore"
)

func TestPreparedSnapshotNative(t *testing.T) {
	for _, scenario := range []string{"empty", "files", "wal", "existing", "consumer-failure", "cancel-after-publish", "source-drift", "selected", "selected-db"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPreparedSnapshotHelper$")
			cmd.Env = append(os.Environ(), "CH_PREPARED_SNAPSHOT_SCENARIO="+scenario, "CH_PREPARED_SNAPSHOT_HOME="+dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", scenario, err, out)
			}
			if scenario != "wal" {
				return
			}

		})
	}
}

func TestPreparedSnapshotHelper(t *testing.T) {
	scenario := os.Getenv("CH_PREPARED_SNAPSHOT_SCENARIO")
	if scenario == "" {
		return
	}
	dir := os.Getenv("CH_PREPARED_SNAPSHOT_HOME")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	write := func(name, value string) {
		t.Helper()
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if scenario != "empty" {
		write("jobs/a.txt", "synthetic job")
		write("resume_versions/резюме.txt", "synthetic resume")
	}
	if scenario == "existing" {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(syntheticSnapshot)), 0700); err != nil {
			t.Fatal(err)
		}
		write(syntheticSnapshot+"/foreign", "preserve")
	}
	if scenario == "wal" || scenario == "selected-db" {
		write(databasePath, "")
		db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(dir, filepath.FromSlash(databasePath)))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; PRAGMA user_version=1; CREATE TABLE fixture(value TEXT); INSERT INTO fixture VALUES ('committed WAL value')"); err != nil {
			t.Fatal(err)
		}
	}
	root, p := runtimepaths.Open(dir)
	if p != nil {
		t.Fatal(p)
	}
	defer root.Close()
	if p = root.ClaimForProcess(); p != nil {
		t.Fatal(p)
	}
	if _, p = root.EnsureDirectory("backups"); p != nil {
		t.Fatal(p)
	}
	var publication runtimepaths.StreamPublication
	called := false
	consume := func(source *runtimepaths.Root, files []File, version *int64) *Error {
		called = true
		if scenario == "consumer-failure" {
			return failure("SYNTHETIC_CONSUMER_FAILURE")
		}
		if (scenario == "selected" || scenario == "selected-db") && (len(files) != 1 || files[0].RelativePath != "jobs/a.txt" || version != nil) {
			t.Fatal("selection ignored", files)
		}
		manifest := testManifest()
		manifest.Files = files
		manifest.SQLiteSchemaVersion = version
		if summary, p := ValidateSnapshot(ctx, dir, syntheticSnapshot, manifest, nil); p != nil {
			return p
		} else if summary == nil {
			return failure("SYNTHETIC_VALIDATION_MISSING")
		}
		var problem *Error
		publication, problem = PublishSnapshot(ctx, source, syntheticSnapshot, "backups/created.zip", manifest)
		if problem != nil {
			return problem
		}
		if scenario == "cancel-after-publish" {
			cancel()
		}
		return nil
	}
	var afterCopy func()
	if scenario == "source-drift" {
		afterCopy = func() { write("jobs/appeared.txt", "new source") }
	}
	var selected []string
	if scenario == "selected" || scenario == "selected-db" {
		selected = []string{"jobs"}
	}
	result, problem := prepareSnapshotWith(ctx, dir, syntheticSnapshot, selected, consume, afterCopy)
	if scenario == "existing" {
		if problem == nil || result.Namespace != runtimepaths.DirectoryNotCreated || called {
			t.Fatal(result, problem)
		}
		if raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(syntheticSnapshot+"/foreign"))); err != nil || string(raw) != "preserve" {
			t.Fatal("existing namespace modified", err)
		}
		return
	}
	if result.Namespace != runtimepaths.DirectoryCreated {
		t.Fatal(result, problem)
	}
	switch scenario {
	case "consumer-failure":
		if problem == nil || problem.Code != "SYNTHETIC_CONSUMER_FAILURE" || !called {
			t.Fatal(result, problem)
		}
	case "source-drift":
		if problem == nil || problem.Code != "BACKUP_SOURCE_CHANGED" || called {
			t.Fatal(result, problem)
		}
	case "cancel-after-publish":
		if problem == nil || problem.Code != "BACKUP_CANCELED" || publication.Outcome != runtimepaths.FilePublished {
			t.Fatal("publication facts lost", result, publication, problem)
		}
	default:
		if problem != nil || !called || publication.Outcome != runtimepaths.FilePublished {
			t.Fatal(result, publication, problem)
		}
	}
	if scenario == "consumer-failure" || scenario == "source-drift" {
		if _, err := os.Stat(filepath.Join(dir, "backups", "created.zip")); !os.IsNotExist(err) {
			t.Fatal("failure published archive", err)
		}
	} else if _, p := VerifyLocal(context.Background(), root, "backups/created.zip"); p != nil {
		t.Fatal("archive not valid after callback/cleanup", p)
	}
	stage := filepath.Join(dir, filepath.FromSlash(syntheticSnapshot))
	if err := os.Rename(stage, stage+".released"); err != nil {
		t.Fatal("staging reservation leaked", err)
	}
	if scenario != "empty" {
		if data, err := os.ReadFile(filepath.Join(dir, "jobs", "a.txt")); err != nil || string(data) != "synthetic job" {
			t.Fatal("source changed", err)
		}
	}
}
