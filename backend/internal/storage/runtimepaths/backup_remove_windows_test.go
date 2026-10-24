//go:build windows

package runtimepaths

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestBackupRemovalNative(t *testing.T) {
	for _, mode := range []string{"file", "large", "empty-file", "directory", "nonempty", "directory-race", "file-as-directory", "directory-as-file",
		"stale-size", "stale-hash", "missing", "hardlink", "locked", "unowned", "closed", "canceled", "cancel-before", "cancel-after",
		"pins", "unknown", "committed-error", "close-error", "replacement", "invalid", "junction", "pending-reader"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBackupRemovalHelper$")
			cmd.Env = append(os.Environ(), "CH_BACKUP_REMOVE_MODE="+mode, "CH_BACKUP_REMOVE_HOME="+t.TempDir())
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, output)
			}
		})
	}
}

func TestBackupRemovalHelper(t *testing.T) {
	mode, dir := os.Getenv("CH_BACKUP_REMOVE_MODE"), os.Getenv("CH_BACKUP_REMOVE_HOME")
	if mode == "" {
		t.Skip("subprocess only")
	}
	root := opened(t, dir)
	if mode != "unowned" {
		if p := root.ClaimForProcess(); p != nil {
			t.Fatal(p)
		}
	}
	name := "stage/target"
	target := filepath.Join(dir, "stage", "target")
	directory := mode == "directory" || mode == "nonempty" || mode == "directory-race" || mode == "directory-as-file"
	if directory {
		if err := os.MkdirAll(target, 0700); err != nil {
			t.Fatal(err)
		}
	} else {
		put(t, dir, name, "synthetic payload")
	}
	if mode == "nonempty" {
		put(t, dir, name+"/keep", "retained child")
	}
	if mode == "large" || mode == "empty-file" {
		size := MaxReadBytes + 1
		if mode == "empty-file" {
			size = 0
		}
		if err := os.Truncate(target, size); err != nil {
			t.Fatal(err)
		}
	}
	var expected *backupRemovalFile
	if mode != "directory" && mode != "nonempty" && mode != "directory-race" && mode != "file-as-directory" {
		expected = &backupRemovalFile{0, strings.Repeat("0", 64)}
		if !directory {
			f, err := os.Open(target)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.New()
			n, err := io.Copy(hash, f)
			closed := f.Close()
			if err != nil || closed != nil {
				t.Fatal(err, closed)
			}
			expected = &backupRemovalFile{n, hex.EncodeToString(hash.Sum(nil))}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ops := backupRemovalOperations{}
	var reader *os.File
	want, wantCode := BackupNotRemoved, ""
	switch mode {
	case "file", "large", "empty-file", "directory", "pins":
		want = BackupRemoved
	case "stale-size":
		expected.size++
		wantCode = "BACKUP_REMOVE_CONTENT_CHANGED"
	case "stale-hash":
		expected.digest = strings.Repeat("0", 64)
		wantCode = "BACKUP_REMOVE_CONTENT_CHANGED"
	case "nonempty":
		wantCode = "BACKUP_REMOVE_DIRECTORY_NOT_EMPTY"
	case "directory-race":
		ops.beforeDelete = func() *Error { put(t, dir, name+"/new", "retain raced child"); return nil }
		want, wantCode = BackupRemovalUnknown, "BACKUP_REMOVE_UNCONFIRMED"
	case "missing":
		name = "stage/missing"
		wantCode = "FILE_MISSING"
	case "hardlink":
		if err := os.Link(target, target+".link"); err != nil {
			t.Fatal(err)
		}
	case "locked":
		f, err := os.OpenFile(target, os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
	case "unowned":
		wantCode = "OWNERSHIP_REQUIRED"
	case "closed":
		if p := root.Close(); p != nil {
			t.Fatal(p)
		}
		wantCode = "ROOT_CLOSED"
	case "canceled":
		cancel()
		wantCode = "BACKUP_REMOVE_CANCELED"
	case "cancel-before":
		ops.beforeDelete = func() *Error { cancel(); return nil }
		wantCode = "BACKUP_REMOVE_CANCELED"
	case "cancel-after":
		ops.close = func(f *os.File) error { err := f.Close(); cancel(); return err }
		want, wantCode = BackupRemoved, "BACKUP_REMOVE_CANCELED"
	case "unknown":
		ops.remove = func(*os.File) error { return errors.New("synthetic native failure") }
		want, wantCode = BackupRemovalUnknown, "BACKUP_REMOVE_UNCONFIRMED"
	case "committed-error":
		ops.remove = func(f *os.File) error {
			if err := markBackupEntryForRemoval(f); err != nil {
				t.Fatal(err)
			}
			return errors.New("synthetic late failure")
		}
		want, wantCode = BackupRemoved, "BACKUP_REMOVE_UNCONFIRMED"
	case "close-error":
		ops.close = func(f *os.File) error {
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			return errors.New("synthetic close failure")
		}
		want, wantCode = BackupRemoved, "BACKUP_REMOVE_UNCONFIRMED"
	case "replacement":
		ops.close = func(f *os.File) error { err := f.Close(); put(t, dir, name, "foreign replacement"); return err }
		want, wantCode = BackupRemovalUnknown, "BACKUP_REMOVE_UNCONFIRMED"
	case "pending-reader":
		name, err := windows.UTF16PtrFromString(target)
		if err != nil {
			t.Fatal(err)
		}
		handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatal(err)
		}
		reader = os.NewFile(uintptr(handle), target)
		defer reader.Close()
		want, wantCode = BackupRemovalUnknown, "BACKUP_REMOVE_UNCONFIRMED"
	case "junction":
		outside := t.TempDir()
		put(t, outside, "keep", "outside payload")
		link := filepath.Join(dir, "stage", "junction")
		quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }
		command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			"New-Item -ItemType Junction -Path "+quote(link)+" -Target "+quote(outside)+" -ErrorAction Stop | Out-Null")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("native junction fixture failed: %v %s", err, output)
		}
		digest := sha256.Sum256([]byte("outside payload"))
		out, p := root.RemoveBackupFile(ctx, "stage/junction/keep", 15, hex.EncodeToString(digest[:]))
		if out != BackupNotRemoved || p == nil || p.Code != "PATH_LINK" {
			t.Fatal("junction child accepted", out, p)
		}
		out, p = root.RemoveEmptyBackupDirectory(ctx, "stage/junction")
		if out != BackupNotRemoved || p == nil || p.Code != "PATH_LINK" {
			t.Fatal("junction directory accepted", out, p)
		}
		raw, err := os.ReadFile(filepath.Join(outside, "keep"))
		if err != nil || string(raw) != "outside payload" {
			t.Fatal("junction target changed", err)
		}
		return
	case "invalid":
		for _, unsafe := range []string{"", ".", "..", "../outside", "stage/../target", target} {
			out, p := root.RemoveEmptyBackupDirectory(ctx, unsafe)
			if out != BackupNotRemoved || p == nil {
				t.Fatal("unsafe removal accepted", unsafe, out, p)
			}
		}
		for _, sha := range []string{"", "not-a-hash", strings.Repeat("A", 64)} {
			out, p := root.RemoveBackupFile(ctx, name, expected.size, sha)
			if out != BackupNotRemoved || p == nil || p.Code != "BACKUP_REMOVE_EXPECTATION_INVALID" {
				t.Fatal("invalid expectation accepted", out, p)
			}
		}
		for _, size := range []int64{-1, MaxBackupReadBytes + 1} {
			out, p := root.RemoveBackupFile(ctx, name, size, expected.digest)
			if out != BackupNotRemoved || p == nil || p.Code != "BACKUP_REMOVE_EXPECTATION_INVALID" {
				t.Fatal("invalid size accepted", out, p)
			}
		}
		return
	}
	if mode == "pins" {
		ops.beforeDelete = func() *Error {
			for _, original := range []string{target, filepath.Dir(target)} {
				if err := os.Rename(original, original+".moved"); err == nil {
					t.Fatal("rename admitted during removal")
				}
			}
			if err := os.WriteFile(target, []byte("foreign"), 0600); err == nil {
				t.Fatal("writer admitted during hash-bound removal")
			}
			return nil
		}
	}
	var out BackupRemovalOutcome
	var p *Error
	if mode == "file" || mode == "large" || mode == "empty-file" {
		out, p = root.RemoveBackupFile(ctx, name, expected.size, expected.digest)
	} else if mode == "directory" {
		out, p = root.RemoveEmptyBackupDirectory(ctx, name)
	} else {
		out, p = root.removeBackupEntry(ctx, name, expected, ops)
	}
	if out != want || (p == nil) != (wantCode == "" && want == BackupRemoved) || wantCode != "" && p.Code != wantCode {
		t.Fatal("wrong removal facts", mode, out, p)
	}
	if reader != nil {
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatal("pending removal did not finish after independent reader closed", err)
		}
		return
	}
	_, err := os.Stat(target)
	if (out == BackupRemoved) != os.IsNotExist(err) {
		t.Fatal("outcome disagrees with retained fixture", out, err)
	}
	if mode == "replacement" {
		raw, err := os.ReadFile(target)
		if err != nil || string(raw) != "foreign replacement" {
			t.Fatal("foreign replacement was removed", err)
		}
	}
}
