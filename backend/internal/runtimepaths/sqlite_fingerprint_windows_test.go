//go:build windows

package runtimepaths

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteGuardSyncAndFingerprint(t *testing.T) {
	dir := testHome(t)
	content := "synthetic recovery bytes\x00\xff"
	put(t, dir, sqliteFixtureRelative, content)
	r := opened(t, dir)
	g, e := guardSQLiteFiles(r, sqliteFixtureRelative)
	if e != nil {
		t.Fatal(e)
	}
	defer g.Close()
	fingerprint, e := g.SyncAndFingerprint(context.Background(), 1024)
	digest := sha256.Sum256([]byte(content))
	if e != nil || fingerprint.SizeBytes != int64(len(content)) || fingerprint.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("wrong recovery fingerprint: %+v, %v", fingerprint, e)
	}
	_, e = g.SyncAndFingerprint(context.Background(), 1)
	code(t, e, "SQLITE_FILE_LIMIT")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = g.SyncAndFingerprint(ctx, 1024)
	code(t, e, "SQLITE_FILE_CANCELED")
	if e := g.Close(); e != nil {
		t.Fatal(e)
	}
	_, e = g.SyncAndFingerprint(context.Background(), 1024)
	code(t, e, "SQLITE_FILES_CLOSED")
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(sqliteFixtureRelative)))
	if err != nil || string(raw) != content {
		t.Fatal("flush/fingerprint changed original bytes")
	}
}
