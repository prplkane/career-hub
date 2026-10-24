//go:build windows

package documentmigration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const recoveryExecuteChild = "CAREERHUB_DOCUMENT_MIGRATION_RECOVERY_EXECUTE_CHILD"

func TestRecoveryExecutorWindowsChild(t *testing.T) {
	if os.Getenv(recoveryExecuteChild) == "1" {
		root, pathProblem := runtimepaths.Open(os.Getenv("CAREERHUB_DOCUMENT_MIGRATION_RECOVERY_ROOT"))
		if pathProblem != nil {
			t.Fatal(pathProblem.Code)
		}
		if pathProblem = root.ClaimForProcess(); pathProblem != nil {
			t.Fatal(pathProblem.Code)
		}
		homeID, keyID := "dh_22222222222222222222222222222222", "key_33333333333333333333333333333333"
		target := "CareerHub/stable/document-key/" + homeID + "/" + keyID
		trace := []string{}
		backend := &migrationKeyBackend{values: map[string][]byte{target: bytes.Repeat([]byte{1}, keys.KeyBytes)}, trace: &trace}
		result, problem := RecoverOwned(context.Background(), root, backend)
		if problem != nil || !result.Present || !result.Recovered || len(backend.values) != 0 {
			t.Fatalf("native recovery failed: %+v / %#v", result, problem)
		}
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}

	fixture := recoveryTreeFixture(t, true)
	homeID := "dh_22222222222222222222222222222222"
	activated, markerProblem := datahome.MarkerBytes("stable", homeID)
	if markerProblem != nil {
		t.Fatal(markerProblem)
	}
	legacyMarker := bytes.Clone(fixture.files[datahome.MarkerPath])
	fixture.files[datahome.MarkerPath] = activated
	rootPath := t.TempDir()
	for relative := range fixture.dirs {
		if err := os.MkdirAll(filepath.Join(rootPath, filepath.FromSlash(relative)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for relative, content := range fixture.files {
		path := filepath.Join(rootPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(os.Args[0], "-test.run=^TestRecoveryExecutorWindowsChild$")
	command.Env = append(os.Environ(), recoveryExecuteChild+"=1", "CAREERHUB_DOCUMENT_MIGRATION_RECOVERY_ROOT="+rootPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native recovery child failed: %v: %.1500s", err, output)
	}
	base := filepath.Join(rootPath, filepath.FromSlash(recoveryRelative(recoveryMigrationID)))
	if _, err := os.Stat(filepath.Join(rootPath, filepath.FromSlash(datahome.DocumentRecoveryPath))); !os.IsNotExist(err) {
		t.Fatal("recovery journal remains")
	}
	if marker, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(datahome.MarkerPath))); err != nil || !bytes.Equal(marker, legacyMarker) {
		t.Fatal("legacy marker was not restored")
	}
	if ledger, err := os.ReadFile(filepath.Join(rootPath, "documents", "ledger.dpapi")); err != nil ||
		!bytes.Equal(ledger, fixture.files[recoveryRelative(recoveryMigrationID)+"/legacy_documents/ledger.dpapi"]) {
		t.Fatal("legacy documents were not restored")
	}
	for _, path := range []string{"interrupted_candidate", "interrupted_staging", "recovered_journal.json"} {
		if _, err := os.Stat(filepath.Join(base, path)); err != nil {
			t.Fatalf("recovery artifact missing: %s", path)
		}
	}
}
