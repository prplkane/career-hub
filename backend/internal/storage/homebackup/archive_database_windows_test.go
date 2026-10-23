//go:build windows

package homebackup

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

func TestArchiveDatabaseNativeProcess(t *testing.T) {
	for _, mode := range []string{"wal", "delete", "empty", "absent", "orphan", "corrupt", "hash", "schema", "existing", "unowned", "cancel", "resume", "missing-resume"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if mode == "wal" {
				runRestoreValidationHelper(t, dir, "seed-wal")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestArchiveDatabaseHelper$")
			cmd.Env = append(os.Environ(), "CH_ARCHIVE_DATABASE_HOME="+dir, "CH_ARCHIVE_DATABASE_MODE="+mode)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", mode, err, out)
			}
		})
	}
}

func TestArchiveDatabaseHelper(t *testing.T) {
	dir, mode := os.Getenv("CH_ARCHIVE_DATABASE_HOME"), os.Getenv("CH_ARCHIVE_DATABASE_MODE")
	if dir == "" || mode == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stage := restorePrefix + strings.Repeat("b", 32)
	write := func(relative string, raw []byte) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(stage+"/"+relative))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	wantVersion := int64(1)
	var expected *int64 = &wantVersion
	if mode == "wal" {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(databasePath+suffix)))
			if err != nil {
				t.Fatal(err)
			}
			write(databasePath+suffix, raw)
		}
	} else if mode == "absent" || mode == "orphan" {
		expected = nil
		if mode == "orphan" {
			write(databasePath+"-wal", []byte("orphan"))
		}
	} else {
		write(databasePath, nil)
		if mode == "empty" {
			wantVersion = 0
		} else if mode == "corrupt" {
			write(databasePath, []byte("not sqlite"))
		} else {
			db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(dir, filepath.FromSlash(stage+"/"+databasePath)))
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec("PRAGMA journal_mode=DELETE; PRAGMA user_version=1; CREATE TABLE fixture(value TEXT); INSERT INTO fixture VALUES ('committed in WAL')")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "resume" || mode == "missing-resume" {
				resume := testFile("applications/resumes/library/fixture.txt", "fictional resume")
				_, err = db.Exec("CREATE TABLE resume_library(relative_path TEXT, sha256 TEXT, size_bytes INTEGER); INSERT INTO resume_library VALUES (?, ?, ?)", resume.RelativePath, resume.SHA256, resume.SizeBytes)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "resume" {
					write(resume.RelativePath, []byte("fictional resume"))
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	files, before := map[string]File{}, map[string][]byte{}
	for _, name := range []string{databasePath, databasePath + "-wal", databasePath + "-shm", databasePath + "-journal", "applications/resumes/library/fixture.txt"} {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(stage+"/"+name)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		files[name], before[name] = testFile(name, string(raw)), raw
	}
	if mode == "hash" {
		f := files[databasePath]
		f.SHA256 = strings.Repeat("0", 64)
		files[databasePath] = f
	}
	if mode == "schema" {
		wantVersion = 2
	}
	root, q := runtimepaths.Open(dir)
	if q != nil {
		t.Fatal(q)
	}
	defer root.Close()
	if mode != "unowned" {
		if q := root.ClaimForProcess(); q != nil {
			t.Fatal(q)
		}
	}
	if mode == "existing" {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(syntheticSnapshot)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "cancel" {
		cancel()
	}
	source := payloadSource{ctx: ctx, root: root, prefix: stage + "/", files: files}
	version, count, facts, witness, problem := validateArchiveDatabase(source, dir, syntheticSnapshot, expected)
	want := mode == "wal" || mode == "delete" || mode == "empty" || mode == "absent" || mode == "resume"
	if (problem == nil) != want {
		t.Fatal(mode, version, count, facts, problem)
	}
	if (witness != nil) != (want && mode != "absent") {
		t.Fatal("invalid cleanup witness availability")
	}
	wantCodes := map[string]string{
		"orphan": "BACKUP_DATABASE_NOT_STANDALONE", "hash": "BACKUP_DATABASE_COPY_FAILED",
		"schema": "BACKUP_SCHEMA_UNSUPPORTED", "existing": "BACKUP_SNAPSHOT_RESERVATION_FAILED",
		"unowned": "BACKUP_OWNERSHIP_REQUIRED", "cancel": "BACKUP_CANCELED",
		"missing-resume": "BACKUP_RESUME_REFERENCE_INVALID", "corrupt": "BACKUP_DATABASE_OPEN_FAILED",
	}
	if code := wantCodes[mode]; code != "" && (problem == nil || problem.Code != code) {
		t.Fatal("wrong refusal boundary", mode, code, problem)
	}
	if !want && (version != nil || count != 0) {
		t.Fatal("failed validation leaked success", version, count)
	}
	if want && mode != "absent" && (version == nil || *version != wantVersion) {
		t.Fatal("schema lost", version, wantVersion)
	}
	if mode == "resume" && count != 1 {
		t.Fatal("original resume source not validated", count)
	}
	if mode == "absent" && (version != nil || facts.Namespace != runtimepaths.DirectoryNotCreated) {
		t.Fatal("invented database", version, facts)
	}
	for name, raw := range before {
		after, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(stage+"/"+name)))
		if err != nil || !bytes.Equal(after, raw) {
			t.Fatal("archived physical bytes changed", name, err)
		}
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, exists := before[databasePath+suffix]; !exists {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(stage+"/"+databasePath+suffix))); !os.IsNotExist(err) {
				t.Fatal("source companion created", suffix, err)
			}
		}
	}
	if want && mode != "absent" {
		db, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(dir, filepath.FromSlash(syntheticSnapshot+"/"+databasePath)))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var journal string
		if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "delete" {
			t.Fatal(journal, err)
		}
		if mode != "empty" {
			var value string
			if err := db.QueryRow("SELECT value FROM fixture").Scan(&value); err != nil || value != "committed in WAL" {
				t.Fatal("lost logical data", value, err)
			}
		}
	}
}
