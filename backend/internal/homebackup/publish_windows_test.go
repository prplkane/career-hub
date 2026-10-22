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
)

const syntheticSnapshot = snapshotPrefix + "0123456789abcdef0123456789abcdef"

func TestPublishSnapshotNativeAndPythonReopen(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPublishSnapshotHelper$")
	cmd.Env = append(os.Environ(), "CH_BACKUP_PUBLISH_HELPER=1", "CH_BACKUP_PUBLISH_HOME="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native publisher: %v\n%s", err, out)
	}

}

func TestPublishSnapshotHelper(t *testing.T) {
	if os.Getenv("CH_BACKUP_PUBLISH_HELPER") != "1" {
		return
	}
	dir := os.Getenv("CH_BACKUP_PUBLISH_HOME")
	files := []struct{ name, body string }{{"jobs/job.txt", "synthetic job"}, {"resume_versions/резюме-😀.txt", "synthetic resume"}}
	manifest := testManifest()
	for _, f := range files {
		path := filepath.Join(dir, filepath.FromSlash(syntheticSnapshot+"/"+f.name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(f.body), 0600); err != nil {
			t.Fatal(err)
		}
		manifest.Files = append(manifest.Files, testFile(f.name, f.body))
	}
	if err := os.Mkdir(filepath.Join(dir, "backups"), 0700); err != nil {
		t.Fatal(err)
	}
	root, p := runtimepaths.Open(dir)
	if p != nil {
		t.Fatal(p)
	}
	defer root.Close()
	if p = root.ClaimForProcess(); p != nil {
		t.Fatal(p)
	}
	result, problem := PublishSnapshot(context.Background(), root, syntheticSnapshot, "backups/valid.zip", manifest)
	if problem != nil || result.Outcome != runtimepaths.FilePublished || !result.CleanupConfirmed {
		t.Fatal(result, problem)
	}
	if m, problem := VerifyLocal(context.Background(), root, "backups/valid.zip"); problem != nil || len(m.Files) != 2 {
		t.Fatal("native reopen failed", problem)
	}
	before, err := os.ReadFile(filepath.Join(dir, "backups", "valid.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if result, problem = PublishSnapshot(context.Background(), root, syntheticSnapshot, "backups/valid.zip", manifest); problem == nil || result.Outcome != runtimepaths.FileNotPublished {
		t.Fatal("existing archive replaced")
	}
	corrupt := manifest
	corrupt.Files = append([]File{}, manifest.Files...)
	corrupt.Files[0].SHA256 = testFile("unused", "different").SHA256
	if result, problem = PublishSnapshot(context.Background(), root, syntheticSnapshot, "backups/corrupt.zip", corrupt); problem == nil || result.Outcome != runtimepaths.FileNotPublished || !result.CleanupConfirmed {
		t.Fatal("unverified archive published", result, problem)
	}
	if _, err = os.Stat(filepath.Join(dir, "backups", "corrupt.zip")); !os.IsNotExist(err) {
		t.Fatal("invalid destination retained", err)
	}
	if after, err := os.ReadFile(filepath.Join(dir, "backups", "valid.zip")); err != nil || string(after) != string(before) {
		t.Fatal("existing backup changed on failure")
	}
	if result, problem = PublishSnapshot(context.Background(), root, syntheticSnapshot, "jobs/invalid.zip", manifest); problem == nil || result.Outcome != runtimepaths.FileNotPublished {
		t.Fatal("non-backups target admitted")
	}
}
