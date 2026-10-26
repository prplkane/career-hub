//go:build windows

package homebackup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/envelope"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

func runRestoreValidationHelper(t *testing.T, dir, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRestoreValidationHelper$")
	cmd.Env = append(os.Environ(), "CH_RESTORE_VALIDATION_HOME="+dir, "CH_RESTORE_VALIDATION_MODE="+mode)
	out, err := cmd.CombinedOutput()
	if mode == "seed-wal" {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 86 {
			return
		}
	}
	if err != nil || mode == "seed-wal" {
		t.Fatalf("%s: %v\n%s", mode, err, out)
	}
}

func TestRestoreValidationNativeProcess(t *testing.T) {
	for _, mode := range []string{"empty", "delete", "wal", "encrypted", "missing-key", "corrupt", "future", "orphan", "alias", "wrong-home", "no-owner", "existing-stage", "drift", "same-size-drift", "cancel", "pending-journal", "hardlink"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if mode == "wal" {
				runRestoreValidationHelper(t, dir, "seed-wal")
			}
			runRestoreValidationHelper(t, dir, mode)
		})
	}
}

func TestRestoreValidationHelper(t *testing.T) {
	dir, mode := os.Getenv("CH_RESTORE_VALIDATION_HOME"), os.Getenv("CH_RESTORE_VALIDATION_MODE")
	if dir == "" || mode == "" {
		return
	}
	write := func(name string, raw []byte) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if mode == "seed-wal" || mode == "delete" || mode == "future" {
		write(databasePath, nil)
		db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(dir, filepath.FromSlash(databasePath)))
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		journal := "DELETE"
		if mode == "seed-wal" {
			journal = "WAL"
		}
		if _, err := db.Exec("PRAGMA journal_mode=" + journal + "; PRAGMA wal_autocheckpoint=0; PRAGMA user_version=1; CREATE TABLE fixture(value TEXT); INSERT INTO fixture VALUES ('committed in WAL')"); err != nil {
			t.Fatal(err)
		}
		if mode == "future" {
			if _, err := db.Exec("PRAGMA user_version=2147483647"); err != nil {
				t.Fatal(err)
			}
		}
		if mode == "seed-wal" {
			for _, suffix := range []string{"-wal", "-shm"} {
				if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(databasePath+suffix))); err != nil || info.Size() == 0 {
					t.Fatal("missing live WAL fixture", suffix, err)
				}
			}
			// Leave real committed WAL and SHM bytes; the fresh validator process
			// has no live SQLite handles on the active source.
			os.Exit(86)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	marker, p := datahome.MarkerBytes("stable", testHomeID)
	if p != nil {
		t.Fatal(p)
	}
	write(datahome.MarkerPath, marker)
	home, p := datahome.ParseMarkerForDocumentMigration(marker)
	if p != nil {
		t.Fatal(p)
	}
	write("jobs/note.txt", []byte("before"))
	if err := os.MkdirAll(filepath.Join(dir, "templates/empty"), 0700); err != nil {
		t.Fatal(err)
	}
	if mode == "corrupt" {
		write(databasePath, []byte("not SQLite"))
	}
	if mode == "orphan" {
		write(databasePath+"-wal", []byte("orphan"))
	}
	if mode == "alias" {
		write("applications/Career_Hub.sqlite3", []byte("alias"))
	}
	if mode == "wrong-home" {
		home.DataHomeID = "dh_" + strings.Repeat("f", 32)
	}
	if mode == "pending-journal" {
		write(datahome.RestoreRecoveryPath, []byte("synthetic journal admitted separately by owner"))
	}
	if mode == "existing-stage" {
		write(syntheticSnapshot+"/untouched", []byte("preserve"))
	}
	if mode == "hardlink" {
		if err := os.Link(filepath.Join(dir, "jobs/note.txt"), filepath.Join(dir, "jobs/linked.txt")); err != nil {
			t.Fatal(err)
		}
	}
	backend := &syntheticValidationKeys{t: t, missing: mode == "missing-key"}
	if mode == "encrypted" || mode == "missing-key" {
		writeRestoreValidationDocument(t, write)
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
	// Capture only active source, marker and sentinel; .career_hub_staging is
	// deliberately written, retained, and never mistaken for active data.
	readActive := func() map[string]string {
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
			rel = filepath.ToSlash(rel)
			if rel == ".career_hub_staging" {
				return filepath.SkipDir
			}
			if entry.IsDir() {
				out[rel+"/"] = ""
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[rel] = string(raw)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := readActive()
	mutate := func() {
		if mode == "cancel" {
			cancel()
		}
		if mode == "drift" {
			write("jobs/note.txt", []byte("changed content"))
		}
		if mode == "same-size-drift" {
			path := filepath.Join(dir, "jobs/note.txt")
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			write("jobs/note.txt", []byte("after!"))
			if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
		}
	}
	result, problem := validateRestoreCopyWith(ctx, dir, syntheticSnapshot, *home, backend, mutate)
	if mode != "drift" && mode != "same-size-drift" && !reflect.DeepEqual(before, readActive()) {
		t.Fatal("validation altered active source")
	}
	want := mode == "empty" || mode == "delete" || mode == "wal" || mode == "encrypted" || mode == "pending-journal"
	if (problem == nil) != want {
		t.Fatal(mode, result, problem)
	}
	if !want {
		if result.Physical != nil || result.PresentRoots != nil || result.cleanup != nil {
			t.Fatal("failed validation granted physical witness", result)
		}
		if mode == "missing-key" && problem.Code != "BACKUP_DOCUMENT_KEY_UNAVAILABLE" {
			t.Fatal(problem)
		}
		if (mode == "wrong-home" || mode == "alias") && backend.reads != 0 {
			t.Fatal("keys consulted before identity/path admission")
		}
		return
	}
	if result.Physical == nil || result.Validation == nil || result.cleanup == nil || result.Preparation.Namespace != runtimepaths.DirectoryCreated {
		t.Fatal(result)
	}
	for _, file := range result.Physical.Files {
		if expected := testFile(file.RelativePath, before[file.RelativePath]); file != expected {
			t.Fatal("physical evidence differs", file)
		}
	}
	if mode == "encrypted" && result.Validation.DocumentsValidated != 1 {
		t.Fatal("encrypted content not validated", result.Validation)
	}
	if mode == "wal" || mode == "delete" {
		if result.Physical.SQLiteSchemaVersion == nil || *result.Physical.SQLiteSchemaVersion != 1 {
			t.Fatal("schema not preserved")
		}
		db, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(dir, filepath.FromSlash(syntheticSnapshot+"/"+databasePath)))
		if err != nil {
			t.Fatal(err)
		}
		var value, journal string
		if err := db.QueryRow("SELECT value FROM fixture").Scan(&value); err != nil || value != "committed in WAL" {
			t.Fatal("lost committed data", value, err)
		}
		if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "delete" {
			t.Fatal("copy not standalone", journal, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if mode == "wal" {
			if _, ok := before[databasePath+"-wal"]; !ok {
				t.Fatal("WAL fixture disappeared before check")
			}
		}
	}
}

func writeRestoreValidationDocument(t *testing.T, write func(string, []byte)) {
	t.Helper()
	keyID, docID := "key_"+strings.Repeat("a", 32), "doc_"+strings.Repeat("b", 32)
	key := bytes.Repeat([]byte{7}, 32)
	state := keys.State{Version: 1, Algorithm: keys.Algorithm, DataHomeID: testHomeID, ActiveKeyID: keyID}
	raw, err := state.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	write("documents/key_state.json", raw)
	plain := []byte("synthetic encrypted document")
	blob, err := envelope.Encrypt(plain, key, envelope.Context{DataHomeID: testHomeID, KeyID: keyID, Purpose: "blob", ObjectID: docID})
	if err != nil {
		t.Fatal(err)
	}
	write("documents/files/"+docID+".chdoc", blob)
	f := testFile("unused", string(plain))
	record := map[string]any{"id": docID, "title": "Synthetic", "category": "Identity", "issued_on": "", "expires_on": "", "notes": "", "filename": "fixture.pdf", "content_type": "application/pdf", "size_bytes": len(plain), "sha256": f.SHA256, "created_at": "2026-09-08T12:00:00+00:00", "updated_at": "2026-09-08T12:00:00+00:00", "archived": false}
	raw, err = json.Marshal(map[string]any{"version": 1, "documents": []any{record}})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := envelope.Encrypt(raw, key, envelope.Context{DataHomeID: testHomeID, KeyID: keyID, Purpose: "ledger", ObjectID: "ledger"})
	if err != nil {
		t.Fatal(err)
	}
	write("documents/ledger.chdoc", sealed)
}
