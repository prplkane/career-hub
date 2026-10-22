//go:build windows

package runtimepaths

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func verifySyntheticStream(reader io.ReaderAt, size int64) error {
	raw, err := io.ReadAll(io.NewSectionReader(reader, 0, size))
	if err != nil {
		return err
	}
	if string(raw) != "synthetic backup" {
		return errors.New("invalid synthetic content")
	}
	return nil
}

func TestBackupStreamNativeNoReplaceAndCallbackLifetime(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "backups/source", "synthetic backup")
	r := opened(t, dir)
	lease := backupLease(t, r, context.Background(), "backups/source", 100)
	var retainedWriter io.Writer
	var retainedReader io.ReaderAt
	produce := func(w io.Writer) error {
		retainedWriter = w
		_, err := io.Copy(w, io.NewSectionReader(lease, 0, lease.Size()))
		return err
	}
	verify := func(reader io.ReaderAt, size int64) error {
		retainedReader = reader
		// This would deadlock if CreateBackupStream held Root's lock here.
		if _, _, p := r.ReadFile("backups/source", 100); p != nil {
			return p
		}
		return verifySyntheticStream(reader, size)
	}
	result, p := r.createBackupStream(context.Background(), "backups/archive.zip", 100, produce, verify, backupStreamOperations{})
	if p != nil || result.Outcome != FilePublished || !result.CleanupConfirmed {
		t.Fatal(result, p)
	}
	if _, err := retainedWriter.Write([]byte("late mutation")); err == nil {
		t.Fatal("retained writer remained active")
	}
	if _, err := retainedReader.ReadAt(make([]byte, 1), 0); err == nil {
		t.Fatal("retained reader remained active")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "backups", "archive.zip"))
	if err != nil || string(raw) != "synthetic backup" {
		t.Fatal("published data changed")
	}
	called := false
	result, p = r.createBackupStream(context.Background(), "backups/archive.zip", 100, func(io.Writer) error { called = true; return nil }, verify, backupStreamOperations{})
	if p == nil || p.Code != "FILE_EXISTS" || called || result.Outcome != FileNotPublished || !result.CleanupConfirmed {
		t.Fatal("existing archive not preserved", result, p)
	}
	if p := lease.Close(); p != nil {
		t.Fatal(p)
	}
	if err := os.Rename(filepath.Join(dir, "backups"), filepath.Join(dir, "released")); err != nil {
		t.Fatal("publication leaked parent pins", err)
	}
}

func TestBackupStreamNativeFaultFacts(t *testing.T) {
	for _, name := range []string{"producer", "verifier", "ignored-limit", "short-write", "changed-write", "sync", "first-close", "publication-close", "cancel-producer", "cancel-verifier", "root-close", "race-existing", "committed-error", "unknown"} {
		t.Run(name, func(t *testing.T) {
			dir := testHome(t)
			put(t, dir, "backups/keep", "unrelated")
			r := opened(t, dir)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ops := backupStreamOperations{}
			injected := errors.New("private synthetic fault text must not escape")
			limit := int64(100)
			produce := func(w io.Writer) error { _, err := io.WriteString(w, "synthetic backup"); return err }
			verify := verifySyntheticStream
			wantOutcome := FileNotPublished
			wantCleanup := true
			wantCode := ""
			switch name {
			case "producer":
				produce = func(w io.Writer) error { _, _ = io.WriteString(w, "partial"); return injected }
				wantCode = "BACKUP_PRODUCER_FAILED"
			case "verifier":
				verify = func(io.ReaderAt, int64) error { return injected }
				wantCode = "BACKUP_VERIFIER_FAILED"
			case "ignored-limit":
				limit = 2
				produce = func(w io.Writer) error { _, _ = io.WriteString(w, "too large"); return nil }
				wantCode = "FILE_SIZE_LIMIT"
			case "short-write":
				ops.write = func(f *os.File, p []byte) (int, error) { return f.Write(p[:1]) }
				wantCode = "FILE_WRITE"
			case "changed-write":
				ops.write = func(f *os.File, p []byte) (int, error) {
					copy := append([]byte{}, p...)
					copy[0] ^= 1
					return f.Write(copy)
				}
				wantCode = "FILE_STAGE"
			case "sync":
				ops.sync = func(*os.File) error { return injected }
				wantCode = "FILE_SYNC"
			case "first-close", "publication-close":
				count := 0
				failAt := 1
				if name == "publication-close" {
					failAt = 2
					wantOutcome = FilePublished
				}
				ops.close = func(f *os.File) error {
					count++
					err := f.Close()
					if count == failAt {
						return injected
					}
					return err
				}
				wantCode = "FILE_CLOSE"
			case "cancel-producer":
				produce = func(w io.Writer) error { _, err := io.WriteString(w, "synthetic backup"); cancel(); return err }
				wantCode = "BACKUP_STREAM_CANCELED"
			case "cancel-verifier":
				verify = func(reader io.ReaderAt, size int64) error {
					err := verifySyntheticStream(reader, size)
					cancel()
					return err
				}
				wantCode = "BACKUP_STREAM_CANCELED"
			case "root-close":
				produce = func(w io.Writer) error { _, err := io.WriteString(w, "synthetic backup"); _ = r.Close(); return err }
				wantCode = "ROOT_CLOSED"
			case "race-existing":
				ops.rename = func(f *os.File, parent *os.Root, target string) error {
					other, err := parent.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = io.WriteString(other, "foreign"); err != nil {
						t.Fatal(err)
					}
					if err = other.Close(); err != nil {
						t.Fatal(err)
					}
					return renameBackupFile(f, parent, target)
				}
				wantCode = "FILE_PUBLISH"
			case "committed-error":
				ops.rename = func(f *os.File, parent *os.Root, target string) error {
					if err := renameBackupFile(f, parent, target); err != nil {
						t.Fatal(err)
					}
					return injected
				}
				wantOutcome = FilePublished
				wantCode = "FILE_PUBLISH"
			case "unknown":
				ops.rename = func(f *os.File, parent *os.Root, _ string) error {
					if err := renameBackupFile(f, parent, "retained-unknown"); err != nil {
						t.Fatal(err)
					}
					return injected
				}
				wantOutcome = FilePublicationUnknown
				wantCleanup = false
				wantCode = "FILE_PUBLISH"
			}
			result, p := r.createBackupStream(ctx, "backups/archive.zip", limit, produce, verify, ops)
			if p == nil || p.Code != wantCode || result.Outcome != wantOutcome || result.CleanupConfirmed != wantCleanup {
				t.Fatalf("got %+v %v; want %s %s cleanup=%v", result, p, wantCode, wantOutcome, wantCleanup)
			}
			if strings.Contains(p.Error(), "private") {
				t.Fatal("private callback text leaked")
			}
			target := filepath.Join(dir, "backups", "archive.zip")
			if wantOutcome == FilePublished {
				raw, err := os.ReadFile(target)
				if err != nil || string(raw) != "synthetic backup" {
					t.Fatal("committed archive removed or changed")
				}
			} else if name == "race-existing" {
				raw, err := os.ReadFile(target)
				if err != nil || string(raw) != "foreign" {
					t.Fatal("foreign target overwritten")
				}
			} else if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("unexpected published target", err)
			}
			if name == "unknown" {
				if raw, err := os.ReadFile(filepath.Join(dir, "backups", "retained-unknown")); err != nil || string(raw) != "synthetic backup" {
					t.Fatal("unknown evidence lost")
				}
			}
			entries, err := os.ReadDir(filepath.Join(dir, "backups"))
			if err != nil {
				t.Fatal(err)
			}
			if wantCleanup {
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".careerhub-create-") {
						t.Fatal("cleanup falsely confirmed")
					}
				}
			}
			if err := os.Rename(filepath.Join(dir, "backups"), filepath.Join(dir, "released")); err != nil {
				t.Fatal("fault path leaked parent pin", err)
			}
		})
	}
}

func TestBackupStreamOwnedProcess(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "backups/keep", "unchanged")
	r := opened(t, dir)
	produce := func(w io.Writer) error { _, err := io.WriteString(w, "synthetic backup"); return err }
	if result, p := r.CreateBackupStream(context.Background(), "backups/archive.zip", 100, produce, verifySyntheticStream); p == nil || p.Code != "OWNERSHIP_REQUIRED" || result.Outcome != FileNotPublished {
		t.Fatal("unowned publication admitted", p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBackupStreamOwnedHelper$")
	cmd.Env = append(os.Environ(), "CH_BACKUP_STREAM_HELPER=1", "CH_BACKUP_STREAM_HOME="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native helper: %v\n%s", err, out)
	}
}

func TestBackupStreamOwnedHelper(t *testing.T) {
	if os.Getenv("CH_BACKUP_STREAM_HELPER") != "1" {
		return
	}
	r := opened(t, os.Getenv("CH_BACKUP_STREAM_HOME"))
	if p := r.ClaimForProcess(); p != nil {
		t.Fatal(p)
	}
	result, p := r.CreateBackupStream(context.Background(), "backups/archive.zip", 100, func(w io.Writer) error { _, err := io.WriteString(w, "synthetic backup"); return err }, verifySyntheticStream)
	if p != nil || result.Outcome != FilePublished || !result.CleanupConfirmed {
		t.Fatal(result, p)
	}
}
