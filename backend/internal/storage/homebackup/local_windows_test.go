//go:build windows

package homebackup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestLocalVerificationOwnedNativeProcess(t *testing.T) {
	dir := t.TempDir()
	file := testFile("jobs/a.txt", "data")
	for _, tc := range []struct{ name, body string }{{"valid.zip", "data"}, {"corrupt.zip", "oops"}} {
		raw := fixtureZIP(t, manifestJSON(t, testManifest(file)), fixtureEntry{file.RelativePath, tc.body, 0600})
		if err := os.WriteFile(filepath.Join(dir, tc.name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, p := runtimepaths.Open(dir)
	if p != nil {
		t.Fatal(p)
	}
	if _, p := VerifyLocal(context.Background(), root, "valid.zip"); p == nil || p.Code != "BACKUP_SOURCE_UNAVAILABLE" {
		t.Fatal("unowned source admitted", p)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLocalVerificationHelper$")
	cmd.Env = append(os.Environ(), "CH_BACKUP_LOCAL_HELPER=1", "CH_BACKUP_LOCAL_HOME="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native helper: %v\n%s", err, out)
	}
}

func TestLocalVerificationHelper(t *testing.T) {
	if os.Getenv("CH_BACKUP_LOCAL_HELPER") != "1" {
		return
	}
	dir := os.Getenv("CH_BACKUP_LOCAL_HOME")
	root, problem := runtimepaths.Open(dir)
	if problem != nil {
		t.Fatal(problem)
	}
	defer root.Close()
	if problem = root.ClaimForProcess(); problem != nil {
		t.Fatal(problem)
	}
	m, p := VerifyLocal(context.Background(), root, "valid.zip")
	if p != nil || len(m.Files) != 1 || m.Files[0] != testFile("jobs/a.txt", "data") {
		t.Fatal("valid native archive", p)
	}
	if m, p = VerifyLocal(context.Background(), root, "corrupt.zip"); p == nil || p.Code != "BACKUP_PAYLOAD_INVALID" || len(m.Files) != 0 {
		t.Fatal("corrupt native archive admitted", p)
	}
	if _, p = VerifyLocal(context.Background(), root, "../outside.zip"); p == nil {
		t.Fatal("outside path admitted")
	}
	if _, p = VerifyLocal(context.Background(), root, "missing.zip"); p == nil {
		t.Fatal("missing source admitted")
	}
	for _, name := range []string{"valid.zip", "corrupt.zip"} {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal("verification leaked write-denying handle", err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
