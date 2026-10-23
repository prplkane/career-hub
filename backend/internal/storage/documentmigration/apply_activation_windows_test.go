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

const activationWriterChild = "CAREERHUB_DOCUMENT_MIGRATION_ACTIVATION_WRITER_CHILD"

func TestApplyActivationWindowsChild(t *testing.T) {
	if os.Getenv(activationWriterChild) == "1" {
		root, pathProblem := runtimepaths.Open(os.Getenv("CAREERHUB_DOCUMENT_MIGRATION_ACTIVATION_ROOT"))
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
		initial, problem := writeRecoveryCopy(context.Background(), root, recoveryMigrationID, snapshot, plan)
		if problem != nil {
			t.Fatal(problem.Code)
		}
		if outcome, publishProblem := publishJournal(root, initial); publishProblem != nil || outcome != journalWritten {
			t.Fatalf("journal publication failed: %v / %#v", outcome, publishProblem)
		}
		trace := []string{}
		backend := &migrationKeyBackend{values: map[string][]byte{}, trace: &trace}
		keyResult, keyProblem := createJournaledKey(
			snapshot.Channel, backend, migrationRandom,
			"dh_22222222222222222222222222222222", initial,
			func(current, next Journal) (journalWriteOutcome, *Error) {
				return advanceJournal(root, current, next)
			},
		)
		if keyProblem != nil {
			t.Fatal(keyProblem.Code)
		}
		defer clear(keyResult.Master.Key)
		if problem = writeVerifiedStage(context.Background(), root, snapshot, plan, keyResult.LastProven, keyResult.Master); problem != nil {
			t.Fatal(problem.Code)
		}
		staged, problem := confirmStageSourceAndAdvance(
			context.Background(), root, true, unprotect, snapshot, plan, keyResult.LastProven, keyResult.Master,
		)
		if problem != nil {
			t.Fatal(problem.Code)
		}
		swapped, problem := swapVerifiedStage(context.Background(), root, snapshot, plan, staged, keyResult.Master)
		if problem != nil {
			t.Fatal(problem.Code)
		}
		activated, problem := activateMigrationMarker(context.Background(), root, snapshot, plan, swapped, keyResult.Master)
		if problem != nil || activated.State != JournalMarkerActivated {
			t.Fatalf("activation failed: %#v / %#v", activated, problem)
		}
		result, recoveryProblem := recoverOwned(context.Background(), root, backend)
		if recoveryProblem != nil || !result.Present || !result.Recovered || len(backend.values) != 0 {
			t.Fatalf("activation rollback failed: %+v / %#v", result, recoveryProblem)
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
	command := exec.Command(os.Args[0], "-test.run=^TestApplyActivationWindowsChild$")
	command.Env = append(os.Environ(), activationWriterChild+"=1", "CAREERHUB_DOCUMENT_MIGRATION_ACTIVATION_ROOT="+rootPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native activation child failed: %v: %.2200s", err, output)
	}
	if marker, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(datahome.MarkerPath))); err != nil ||
		!bytes.Equal(marker, source.Marker) {
		t.Fatal("real rollback did not restore legacy marker")
	}
	if ledger, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(legacyLedgerPath))); err != nil ||
		!bytes.Equal(ledger, source.LedgerCiphertext) {
		t.Fatal("real rollback did not restore legacy documents")
	}
	if _, err := os.Stat(filepath.Join(rootPath, filepath.FromSlash(datahome.DocumentRecoveryPath))); !os.IsNotExist(err) {
		t.Fatal("real rollback retained journal")
	}
	base := filepath.Join(rootPath, filepath.FromSlash(recoveryRelative(recoveryMigrationID)))
	for _, relative := range []string{"interrupted_candidate", "recovered_journal.json"} {
		if _, err := os.Stat(filepath.Join(base, relative)); err != nil {
			t.Fatalf("real rollback artifact missing: %s", relative)
		}
	}
}
