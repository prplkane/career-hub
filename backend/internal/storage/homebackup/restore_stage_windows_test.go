//go:build windows

package homebackup

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestRestoreExtractionNativeProcess(t *testing.T) {
	modes := []string{"store", "deflate", "empty", "corrupt", "existing", "prefix", "portable-prefix", "nfc-collision", "device", "unowned", "invalid-stage", "consumer-failure", "cancel-after-copy"}
	for mode := range reservedAliasFixtures {
		modes = append(modes, mode)
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRestoreExtractionHelper$")
			cmd.Env = append(os.Environ(), "CH_RESTORE_STAGE_HOME="+dir, "CH_RESTORE_STAGE_MODE="+mode)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("native extraction: %v\n%s", err, out)
			}
		})
	}
}

func TestRestoreExtractionHelper(t *testing.T) {
	dir, mode := os.Getenv("CH_RESTORE_STAGE_HOME"), os.Getenv("CH_RESTORE_STAGE_MODE")
	if dir == "" || mode == "" {
		return
	}
	write := func(relative string, raw []byte) {
		t.Helper()
		target := filepath.Join(dir, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	files := []File{testFile("jobs/raw_jd/fictional.txt", "testing"), testFile("logs/empty.txt", "")}
	entries := []fixtureEntry{{files[0].RelativePath, "testing", 0600}, {files[1].RelativePath, "", 0600}}
	if mode == "empty" {
		files, entries = nil, nil
	}
	if mode == "corrupt" {
		entries[0].content = "failing"
	}
	if mode == "prefix" {
		files = []File{testFile("jobs/a", "a"), testFile("jobs/a/b", "b")}
		entries = []fixtureEntry{{"jobs/a", "a", 0600}, {"jobs/a/b", "b", 0600}}
	}
	if mode == "portable-prefix" {
		files = []File{testFile("jobs/A", "a"), testFile("jobs/a/b", "b")}
		entries = []fixtureEntry{{"jobs/A", "a", 0600}, {"jobs/a/b", "b", 0600}}
	}
	if mode == "nfc-collision" {
		files = []File{testFile("jobs/e\u0301.txt", "a"), testFile("jobs/é.txt", "b")}
		entries = []fixtureEntry{{files[0].RelativePath, "a", 0600}, {files[1].RelativePath, "b", 0600}}
	}
	if mode == "device" {
		files = []File{testFile("jobs/NUL.txt", "a")}
		entries = []fixtureEntry{{files[0].RelativePath, "a", 0600}}
	}
	if path, alias := reservedAliasFixtures[mode]; alias {
		files = []File{testFile(path, "invalid")}
		entries = []fixtureEntry{{path, "invalid", 0600}}
	}
	manifest := testManifest(files...)
	raw := fixtureZIP(t, manifestJSON(t, manifest), entries...)
	if mode == "deflate" {
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		all := append([]fixtureEntry{{ManifestName, string(manifestJSON(t, manifest)), 0600}}, entries...)
		for _, entry := range all {
			out, err := writer.Create(entry.name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = out.Write([]byte(entry.content)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		raw = buffer.Bytes()
	}
	write("backups/input.zip", raw)
	write("jobs/active.txt", []byte("unchanged active data"))
	snapshot := restorePrefix + strings.Repeat("c", 32)
	if mode == "existing" {
		write(snapshot+"/foreign.txt", []byte("do not adopt"))
	}
	if mode == "invalid-stage" {
		snapshot = snapshotPrefix + strings.Repeat("c", 32)
	}
	root, pathProblem := runtimepaths.Open(dir)
	if pathProblem != nil {
		t.Fatal(pathProblem)
	}
	defer root.Close()
	if mode != "unowned" {
		if pathProblem = root.ClaimForProcess(); pathProblem != nil {
			t.Fatal(pathProblem)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := false
	result, p := withExtractedRestore(ctx, root, "backups/input.zip", snapshot, func(got Manifest) *Error {
		called = true
		if len(got.Files) != len(files) {
			t.Fatal("manifest changed")
		}
		// Existing namespace and input bytes remain guarded through validation.
		if f, err := os.OpenFile(filepath.Join(dir, "backups/input.zip"), os.O_WRONLY, 0600); err == nil {
			f.Close()
			t.Fatal("archive writer admitted while source is pinned")
		}
		if err := os.Rename(filepath.Join(dir, filepath.FromSlash(snapshot)), filepath.Join(dir, "renamed-stage")); err == nil {
			t.Fatal("reserved namespace moved during extraction")
		}
		for _, entry := range entries {
			actual, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(snapshot+"/"+entry.name)))
			if err != nil || string(actual) != entry.content {
				t.Fatal("staged bytes differ", err)
			}
		}
		if _, problem := ValidateSnapshot(ctx, dir, snapshot, got, nil); problem != nil {
			t.Fatal("restore stage semantics", problem)
		}
		if mode == "consumer-failure" {
			return failure("BACKUP_FIXTURE_VALIDATION_FAILED")
		}
		if mode == "cancel-after-copy" {
			cancel()
		}
		return nil
	})
	expected := map[string]string{"corrupt": "BACKUP_PAYLOAD_INVALID", "existing": "BACKUP_SNAPSHOT_RESERVATION_FAILED",
		"prefix": "BACKUP_PAYLOAD_PATH_INVALID", "portable-prefix": "BACKUP_PAYLOAD_PATH_INVALID", "nfc-collision": "BACKUP_PAYLOAD_PATH_INVALID", "device": "BACKUP_PAYLOAD_PATH_INVALID",
		"unowned": "BACKUP_OWNERSHIP_REQUIRED", "invalid-stage": "BACKUP_SNAPSHOT_INVALID",
		"consumer-failure": "BACKUP_FIXTURE_VALIDATION_FAILED", "cancel-after-copy": "BACKUP_CANCELED"}[mode]
	if _, alias := reservedAliasFixtures[mode]; alias {
		expected = "BACKUP_PAYLOAD_PATH_INVALID"
	}
	if expected == "" {
		if p != nil {
			t.Fatal(p)
		}
	} else if p == nil || p.Code != expected {
		t.Fatalf("want %s, got %v", expected, p)
	}
	prepared := mode == "store" || mode == "deflate" || mode == "empty" || mode == "consumer-failure" || mode == "cancel-after-copy"
	if called != prepared {
		t.Fatal("consumer admission differs")
	}
	if prepared {
		if result.Namespace != runtimepaths.DirectoryCreated || result.CopiedFiles != len(files) || !result.LastFile.CleanupConfirmed {
			t.Fatal("lost preparation facts", result)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(snapshot))); err != nil {
			t.Fatal("stage was removed", err)
		}
	} else if result.Namespace != runtimepaths.DirectoryNotCreated || result.CopiedFiles != 0 {
		t.Fatal("unexpected staging", result)
	}
	if !prepared && mode != "existing" {
		if _, err := os.Stat(filepath.Join(dir, ".career_hub_staging")); !os.IsNotExist(err) {
			t.Fatal("preflight created stage", err)
		}
	}
	if mode == "existing" {
		foreign, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(snapshot+"/foreign.txt")))
		if err != nil || string(foreign) != "do not adopt" {
			t.Fatal("existing namespace changed", err)
		}
	}
	active, err := os.ReadFile(filepath.Join(dir, "jobs/active.txt"))
	if err != nil || string(active) != "unchanged active data" {
		t.Fatal("active data changed", err)
	}
	archive, err := os.ReadFile(filepath.Join(dir, "backups/input.zip"))
	if err != nil || !bytes.Equal(archive, raw) {
		t.Fatal("archive changed", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "backups/input.zip"), os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("source lease leaked", err)
	}
	f.Close()
	if prepared {
		if err := os.Rename(filepath.Join(dir, filepath.FromSlash(snapshot)), filepath.Join(dir, "released-stage")); err != nil {
			t.Fatal("namespace lease leaked", err)
		}
	}
}
