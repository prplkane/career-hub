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
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const recoveryWriterChild = "CAREERHUB_DOCUMENT_MIGRATION_RECOVERY_WRITER_CHILD"

func TestRecoveryCopyWriterWindowsChild(t *testing.T) {
	if os.Getenv(recoveryWriterChild) == "1" {
		root, pathProblem := runtimepaths.Open(os.Getenv("CAREERHUB_DOCUMENT_MIGRATION_WRITER_ROOT"))
		if pathProblem != nil {
			t.Fatal(pathProblem.Code)
		}
		if pathProblem = root.ClaimForProcess(); pathProblem != nil {
			t.Fatal(pathProblem.Code)
		}
		plan, snapshot, problem := readSnapshotOwned(root, true, unprotect)
		if problem != nil {
			t.Fatal(problem.Code)
		}
		defer snapshot.clear()
		journal, problem := writeRecoveryCopy(context.Background(), root, recoveryMigrationID, snapshot, plan)
		if problem != nil || journal.State != JournalRecoveryVerified {
			t.Fatalf("native recovery copy failed: %#v / %#v", journal, problem)
		}
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}

	source := sourceFixture(t)
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, ".career_hub_data_home.json"), source.Marker, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rootPath, "documents", "files"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, filepath.FromSlash(legacyLedgerPath)), source.LedgerCiphertext, 0600); err != nil {
		t.Fatal(err)
	}
	for name, content := range source.Files {
		if err := os.WriteFile(filepath.Join(rootPath, "documents", "files", name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(os.Args[0], "-test.run=^TestRecoveryCopyWriterWindowsChild$")
	command.Env = append(os.Environ(), recoveryWriterChild+"=1", "CAREERHUB_DOCUMENT_MIGRATION_WRITER_ROOT="+rootPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native recovery writer child failed: %v: %.1500s", err, output)
	}
	base := filepath.Join(rootPath, filepath.FromSlash(recoveryRelative(recoveryMigrationID)))
	if marker, err := os.ReadFile(filepath.Join(base, "legacy_marker.json")); err != nil || !bytes.Equal(marker, source.Marker) {
		t.Fatal("recovery marker changed")
	}
	if ledger, err := os.ReadFile(filepath.Join(base, "legacy_documents", "ledger.dpapi")); err != nil ||
		!bytes.Equal(ledger, source.LedgerCiphertext) {
		t.Fatal("recovery ledger changed")
	}
	if blob, err := os.ReadFile(filepath.Join(base, "legacy_documents", "files", sourceDocumentID+".dpapi")); err != nil ||
		!bytes.Equal(blob, source.Files[sourceDocumentID+".dpapi"]) {
		t.Fatal("recovery document changed")
	}
	if _, err := os.Stat(filepath.Join(rootPath, filepath.FromSlash(datahome.DocumentRecoveryPath))); !os.IsNotExist(err) {
		t.Fatal("recovery writer published a journal")
	}
	if ledger, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(legacyLedgerPath))); err != nil ||
		!bytes.Equal(ledger, source.LedgerCiphertext) {
		t.Fatal("recovery writer changed active documents")
	}
}
