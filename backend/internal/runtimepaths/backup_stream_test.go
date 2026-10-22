package runtimepaths

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"testing"
)

func TestBackupStreamWriterChunkBoundAndStickyFailure(t *testing.T) {
	calls := 0
	largest := 0
	w := &backupStreamWriter{ctx: context.Background(), active: true, limit: MaxBackupReadChunk * 2, hash: sha256.New(), write: func(_ *os.File, p []byte) (int, error) {
		calls++
		if len(p) > largest {
			largest = len(p)
		}
		return len(p), nil
	}}
	p := make([]byte, MaxBackupReadChunk+1)
	if n, err := w.Write(p); err != nil || n != len(p) {
		t.Fatal(n, err)
	}
	if calls != 2 || largest != MaxBackupReadChunk {
		t.Fatal("native writes not chunked", calls, largest)
	}
	if _, err := w.Write(p); err == nil {
		t.Fatal("total size limit ignored")
	}
	if _, err := w.Write([]byte{1}); err == nil {
		t.Fatal("limit error was not sticky")
	}
	size, _, problem := w.stop()
	if size != int64(len(p)) || problem == nil {
		t.Fatal(size, problem)
	}
	if _, err := w.Write(nil); err == nil {
		t.Fatal("stopped writer accepted")
	}
}

func TestBackupStreamUnsupportedBeforeFilesystem(t *testing.T) {
	if backupReadSupported {
		t.Skip("non-Windows refusal contract")
	}
	var root *Root
	called := false
	result, problem := root.CreateBackupStream(context.Background(), "../outside", 1, func(io.Writer) error { called = true; return nil }, func(io.ReaderAt, int64) error { called = true; return nil })
	if called || result.Outcome != FileNotPublished || !result.CleanupConfirmed {
		t.Fatal("unsupported platform reached callback")
	}
	code(t, problem, "BACKUP_STREAM_UNSUPPORTED")
}
