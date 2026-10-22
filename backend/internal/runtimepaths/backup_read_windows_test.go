//go:build windows

package runtimepaths

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func backupLease(t *testing.T, r *Root, ctx context.Context, name string, limit int64) *BackupRead {
	t.Helper()
	r.mu.RLock()
	g, problem := openBackupRead(r, ctx, name, limit)
	r.mu.RUnlock()
	if problem != nil {
		t.Fatal(problem)
	}
	t.Cleanup(func() { _ = g.Close() })
	return g
}

func TestBackupReadNativeLifetimeAndBounds(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "backups/test.zip", "synthetic archive")
	root := opened(t, dir)
	g := backupLease(t, root, context.Background(), "backups/test.zip", 100)
	if g.Size() != 17 {
		t.Fatal("incorrect size", g.Size())
	}
	p := make([]byte, 32)
	n, err := g.ReadAt(p, 10)
	if n != 7 || err != io.EOF || string(p[:n]) != "archive" {
		t.Fatalf("short read: %d %v %q", n, err, p[:n])
	}
	if n, err := g.ReadAt(p, 17); n != 0 || err != io.EOF {
		t.Fatal("EOF", n, err)
	}
	if _, err := g.ReadAt(p, -1); err == nil {
		t.Fatal("negative offset accepted")
	}
	if _, err := g.ReadAt(make([]byte, MaxBackupReadChunk+1), 0); err == nil {
		t.Fatal("oversized read accepted")
	}
	main := filepath.Join(dir, "backups", "test.zip")
	if err := os.WriteFile(main, []byte("changed"), 0600); err == nil {
		t.Fatal("write sharing allowed")
	}
	if err := os.Rename(main, main+".moved"); err == nil {
		t.Fatal("file rename allowed")
	}
	if err := os.Remove(main); err == nil {
		t.Fatal("file deletion allowed")
	}
	parent := filepath.Dir(main)
	if err := os.Rename(parent, parent+".moved"); err == nil {
		t.Fatal("parent rename allowed")
	}
	// Independent read-only access is still allowed; this is not an access ban.
	if raw, err := os.ReadFile(main); err != nil || string(raw) != "synthetic archive" {
		t.Fatal("read sharing failed", err)
	}
	file := g.file
	if problem := g.Close(); problem != nil {
		t.Fatal(problem)
	}
	if problem := g.Close(); problem != nil {
		t.Fatal(problem)
	}
	if !errors.Is(file.Close(), os.ErrClosed) {
		t.Fatal("file handle leaked")
	}
	if _, err := g.ReadAt(p, 0); err == nil {
		t.Fatal("read after close accepted")
	}
	if err := os.Rename(main, main+".moved"); err != nil {
		t.Fatal("closed lease retained pin", err)
	}
	if err := os.Rename(main+".moved", main); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, []byte("after close"), 0600); err != nil {
		t.Fatal("closed lease retained write exclusion", err)
	}
}

func TestBackupReadExistingWriterAndHardlinkRefusal(t *testing.T) {
	for _, hardlink := range []bool{false, true} {
		dir := testHome(t)
		put(t, dir, "backups/data", "unchanged")
		path := filepath.Join(dir, "backups", "data")
		var writer *os.File
		if hardlink {
			if err := os.Link(path, filepath.Join(dir, "alias")); err != nil {
				t.Fatal(err)
			}
		} else {
			var err error
			writer, err = os.OpenFile(path, os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = writer.Close() })
		}
		g, problem := openBackupRead(opened(t, dir), context.Background(), "backups/data", 100)
		if g != nil || problem == nil {
			t.Fatal("unsafe stream admitted", problem)
		}
		if hardlink {
			code(t, problem, "PATH_HARDLINK")
		} else {
			code(t, problem, "FILE_OPEN")
			_ = writer.Close()
		}
		if err := os.Rename(path, path+".moved"); err != nil {
			t.Fatal("failed acquisition leaked handle", err)
		}
		raw, err := os.ReadFile(path + ".moved")
		if err != nil || string(raw) != "unchanged" {
			t.Fatal("acquisition changed data")
		}
	}
}

func TestBackupReadMissingLimitCanceledAndClosedRoot(t *testing.T) {
	dir := testHome(t)
	r := opened(t, dir)
	if g, p := openBackupRead(r, context.Background(), "missing/archive.zip", 100); g != nil || p == nil {
		t.Fatal("missing file admitted")
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatal("read created missing paths")
	}
	put(t, dir, "backups/data", "content")
	put(t, dir, "backups/empty", "")
	empty := backupLease(t, r, context.Background(), "backups/empty", 0)
	if n, err := empty.ReadAt(make([]byte, 1), 0); n != 0 || err != io.EOF {
		t.Fatal("empty stream", n, err)
	}
	if p := empty.Close(); p != nil {
		t.Fatal(p)
	}
	if g, p := openBackupRead(r, context.Background(), "backups/data", 2); g != nil || p == nil {
		t.Fatal("size limit ignored")
	}
	if g, p := openBackupRead(r, context.Background(), "../outside", 100); g != nil || p == nil {
		t.Fatal("traversal admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	g := backupLease(t, r, ctx, "backups/data", 100)
	cancel()
	if _, err := g.ReadAt(make([]byte, 1), 0); err == nil {
		t.Fatal("cancellation ignored")
	}
	code(t, g.Check(), "BACKUP_READ_CANCELED")
	if p := g.Close(); p != nil {
		t.Fatal("cancellation prevented cleanup", p)
	}
	g = backupLease(t, r, context.Background(), "backups/data", 100)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	code(t, g.Check(), "ROOT_CLOSED")
	code(t, g.Close(), "ROOT_CLOSED")
	code(t, g.Close(), "ROOT_CLOSED")
	if err := os.WriteFile(filepath.Join(dir, "backups", "data"), []byte("after"), 0600); err != nil {
		t.Fatal("failed final check leaked pin", err)
	}
	if _, err := (&BackupRead{}).ReadAt(nil, 0); err == nil {
		t.Fatal("zero lease accepted")
	}
}

func TestBackupReadBeyondOrdinaryLimitWithoutLoadingWholeFile(t *testing.T) {
	dir := testHome(t)
	path := filepath.Join(dir, "large")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// A 16 MiB + 1 sparse fixture tests the separate contract, not memory usage.
	if err = f.Truncate(MaxReadBytes + 1); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if _, err = f.WriteAt([]byte{'Z'}, MaxReadBytes); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	r := opened(t, dir)
	if _, _, p := r.ReadFile("large", MaxReadBytes); p == nil {
		t.Fatal("ordinary limit unexpectedly changed")
	}
	g := backupLease(t, r, context.Background(), "large", MaxReadBytes+1)
	p := make([]byte, 1)
	if n, err := g.ReadAt(p, MaxReadBytes); err != nil || n != 1 || p[0] != 'Z' {
		t.Fatal("bounded tail read", n, err)
	}
}

func TestBackupReadOwnedProcessAndZIPConsumer(t *testing.T) {
	dir := testHome(t)
	r := opened(t, dir)
	if g, p := r.OpenBackupRead(context.Background(), "fixture.zip", 1024); g != nil || p == nil || p.Code != "OWNERSHIP_REQUIRED" {
		t.Fatal("unowned read admitted", p)
	}
	file, err := os.Create(filepath.Join(dir, "fixture.zip"))
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(file)
	entry, err := w.Create("synthetic.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(entry, "verified synthetic payload"); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBackupReadOwnedHelper$")
	cmd.Env = append(os.Environ(), "CH_BACKUP_READ_HELPER=1", "CH_BACKUP_READ_HOME="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native helper: %v\n%s", err, out)
	}
}

func TestBackupReadOwnedHelper(t *testing.T) {
	if os.Getenv("CH_BACKUP_READ_HELPER") != "1" {
		return
	}
	r := opened(t, os.Getenv("CH_BACKUP_READ_HOME"))
	if p := r.ClaimForProcess(); p != nil {
		t.Fatal(p)
	}
	g, p := r.OpenBackupRead(context.Background(), "fixture.zip", 1024)
	if p != nil {
		t.Fatal(p)
	}
	t.Cleanup(func() { _ = g.Close() })
	z, err := zip.NewReader(g, g.Size())
	if err != nil || len(z.File) != 1 {
		t.Fatal("ZIP consumer", err)
	}
	f, err := z.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(f)
	closeErr := f.Close()
	if err != nil || closeErr != nil || string(raw) != "verified synthetic payload" {
		t.Fatal("payload invalid")
	}
	if p := g.Close(); p != nil {
		t.Fatal(p)
	}
}
