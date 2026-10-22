//go:build windows

package documentmigration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/documents/envelope"
	"careerhub.local/backend/internal/documents/keys"
	"careerhub.local/backend/internal/runtimepaths"
)

const stageWriterChild = "CAREERHUB_DOCUMENT_MIGRATION_STAGE_WRITER_CHILD"

func TestStageWriterWindowsChild(t *testing.T) {
	if os.Getenv(stageWriterChild) == "1" {
		root, pathProblem := runtimepaths.Open(os.Getenv("CAREERHUB_DOCUMENT_MIGRATION_STAGE_ROOT"))
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
		if keyProblem != nil || keyResult.LastProven.State != JournalKeyCreated {
			t.Fatalf("journaled key failed: %#v / %#v", keyResult, keyProblem)
		}
		defer clear(keyResult.Master.Key)
		if problem = writeVerifiedStage(context.Background(), root, snapshot, plan, keyResult.LastProven, keyResult.Master); problem != nil {
			t.Fatal(problem.Code)
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
	command := exec.Command(os.Args[0], "-test.run=^TestStageWriterWindowsChild$")
	command.Env = append(os.Environ(), stageWriterChild+"=1", "CAREERHUB_DOCUMENT_MIGRATION_STAGE_ROOT="+rootPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native stage writer child failed: %v: %.1800s", err, output)
	}
	journalRaw, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(datahome.DocumentRecoveryPath)))
	if err != nil {
		t.Fatal(err)
	}
	journal, journalProblem := ParseJournal(journalRaw)
	if journalProblem != nil || journal.State != JournalKeyCreated {
		t.Fatalf("stage writer changed journal state: %#v / %#v", journal, journalProblem)
	}
	stage := filepath.Join(rootPath, filepath.FromSlash(stageRelative(recoveryMigrationID)))
	stateRaw, err := os.ReadFile(filepath.Join(stage, stageKeyStateName))
	if err != nil {
		t.Fatal(err)
	}
	state, err := keys.ParseState(stateRaw)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x33}, keys.KeyBytes)
	defer clear(key)
	ledgerEnvelope, err := os.ReadFile(filepath.Join(stage, stageLedgerName))
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := envelope.Decrypt(ledgerEnvelope, key, envelope.Context{
		DataHomeID: state.DataHomeID, KeyID: state.ActiveKeyID, Purpose: "ledger", ObjectID: "ledger",
	})
	if err != nil {
		t.Fatal(err)
	}
	expectedLedger, err := unprotect(source.LedgerCiphertext)
	if err != nil || !bytes.Equal(decrypted.Plaintext, expectedLedger) {
		t.Fatal("staged ledger plaintext changed")
	}
	clear(decrypted.Plaintext)
	clear(expectedLedger)
	if active, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(legacyLedgerPath))); err != nil ||
		!bytes.Equal(active, source.LedgerCiphertext) {
		t.Fatal("stage writer changed active documents")
	}
}
