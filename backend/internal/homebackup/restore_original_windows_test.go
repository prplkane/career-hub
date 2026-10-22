//go:build windows

package homebackup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

func TestRestoreOriginalNativeProcess(t *testing.T) {
	for _, mode := range []string{"ordinary", "physical", "no-owner", "missing-marker", "wrong-home", "journal", "canceled", "schema-without-database", "database-without-schema", "orphan", "alias", "hardlink"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRestoreOriginalHelper$")
			cmd.Env = append(os.Environ(), "CH_RESTORE_ORIGINAL_HOME="+t.TempDir(), "CH_RESTORE_ORIGINAL_MODE="+mode)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", mode, err, out)
			}
		})
	}
}

func TestRestoreOriginalHelper(t *testing.T) {
	dir, mode := os.Getenv("CH_RESTORE_ORIGINAL_HOME"), os.Getenv("CH_RESTORE_ORIGINAL_MODE")
	if dir == "" || mode == "" {
		return
	}
	root, q := runtimepaths.Open(dir)
	if q != nil {
		t.Fatal(q)
	}
	defer root.Close()
	if mode != "no-owner" {
		if q := root.ClaimForProcess(); q != nil {
			t.Fatal(q)
		}
	}
	write := func(name, value string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	marker, p := datahome.MarkerBytes("stable", testHomeID)
	if p != nil {
		t.Fatal(p)
	}
	if mode != "missing-marker" {
		write(datahome.MarkerPath, string(marker))
	}
	home, p := datahome.ParseMarkerForDocumentMigration(marker)
	if p != nil {
		t.Fatal(p)
	}
	write("jobs/note.txt", "synthetic source")
	// Empty roots remain distinguishable from absent roots for rollback.
	if err := os.MkdirAll(filepath.Join(dir, "templates/empty"), 0700); err != nil {
		t.Fatal(err)
	}
	write("backups/excluded.zip", "not payload")
	write(".career_hub_staging/excluded.txt", "not payload")
	var version *int64
	if mode == "physical" || mode == "schema-without-database" || mode == "alias" {
		n := int64(1)
		version = &n
	}
	if mode == "physical" || mode == "database-without-schema" {
		write(databasePath, "opaque database bytes, not a SQLite validation fixture")
	}
	if mode == "physical" {
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			write(databasePath+suffix, "opaque physical "+suffix)
		}
	}
	if mode == "orphan" {
		write(databasePath+"-wal", "orphan")
	}
	if mode == "alias" {
		write("applications/Career_Hub.sqlite3", "alias")
	}
	if mode == "journal" {
		write(datahome.RestoreRecoveryPath, "pending recovery")
	}
	if mode == "hardlink" {
		if err := os.Link(filepath.Join(dir, "jobs/note.txt"), filepath.Join(dir, "jobs/alias.txt")); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "wrong-home" {
		home.DataHomeID = "dh_" + strings.Repeat("e", 32)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if mode == "canceled" {
		cancel()
	}
	// Snapshot the entire synthetic fixture, including excluded areas. Capture
	// must neither modify bytes nor create a staging/journal/receipt artifact.
	readTree := func() map[string]string {
		t.Helper()
		out := map[string]string{}
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			if entry.IsDir() {
				out[filepath.ToSlash(rel)+"/"] = ""
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(rel)] = string(raw)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := readTree()
	manifest, present, problem := captureRestoreOriginal(ctx, root, *home, version)
	if !reflect.DeepEqual(before, readTree()) {
		t.Fatal("capture modified source")
	}
	if mode != "ordinary" && mode != "physical" {
		if problem == nil || !reflect.DeepEqual(manifest, Manifest{}) || present != nil {
			t.Fatal("invalid source admitted", mode, manifest, present, problem)
		}
		return
	}
	if problem != nil {
		t.Fatal(problem)
	}
	wantRoots := []string{"jobs", "templates"}
	if mode == "physical" {
		wantRoots = []string{"applications", "jobs", "templates"}
	}
	if !reflect.DeepEqual(present, wantRoots) {
		t.Fatal(present)
	}
	want := testManifest(testFile("jobs/note.txt", "synthetic source"))
	if mode == "physical" {
		want.Files = []File{testFile(databasePath, before[databasePath]), testFile(databasePath+"-journal", before[databasePath+"-journal"]), testFile(databasePath+"-shm", before[databasePath+"-shm"]), testFile(databasePath+"-wal", before[databasePath+"-wal"]), want.Files[0]}
		want.SQLiteSchemaVersion = version
	}
	if !reflect.DeepEqual(manifest, want) {
		t.Fatal("physical manifest differs", manifest, want)
	}
	if version != nil {
		*version = 99
		if *manifest.SQLiteSchemaVersion != 1 {
			t.Fatal("schema aliased caller memory")
		}
	}
}
