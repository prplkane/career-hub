package documentmigration

import (
	"bytes"
	"context"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/documents/envelope"
	"careerhub.local/backend/internal/documents/keys"
	"careerhub.local/backend/internal/runtimepaths"
)

func (f *fakeRecoveryCopyStore) ReadSingleLinkFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "single:"+path)
	content, present := f.files[path]
	if int64(len(content)) > limit {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	return bytes.Clone(content), present, nil
}

func stagingFixture(t *testing.T) (*fakeRecoveryCopyStore, Plan, legacySourceSnapshot, Journal, keys.MasterKey) {
	t.Helper()
	plan, snapshot := writerSnapshot(t, sourceFixture(t))
	store := newFakeRecoveryCopyStore()
	initial, problem := writeRecoveryCopy(context.Background(), store, recoveryMigrationID, snapshot, plan)
	if problem != nil {
		snapshot.clear()
		t.Fatal(problem.Code)
	}
	homeID, keyID := "dh_22222222222222222222222222222222", "key_33333333333333333333333333333333"
	created := Journal{
		MigrationID: recoveryMigrationID, State: JournalKeyCreated,
		DataHomeID: &homeID, KeyID: &keyID,
	}
	if !initial.CanAdvanceTo(Journal{MigrationID: recoveryMigrationID, State: JournalKeyPlanned, DataHomeID: &homeID, KeyID: &keyID}) {
		snapshot.clear()
		t.Fatal("invalid staging fixture transition")
	}
	createdRaw, journalProblem := created.CanonicalBytes()
	if journalProblem != nil {
		snapshot.clear()
		t.Fatal(journalProblem.Code)
	}
	store.files[datahome.DocumentRecoveryPath] = createdRaw
	store.addName("backups/document-encryption-migration", "active.json")
	master := keys.MasterKey{
		State: keys.State{
			Version: keys.Version, Algorithm: keys.Algorithm,
			DataHomeID: homeID, ActiveKeyID: keyID,
		},
		Key: bytes.Repeat([]byte{0x5a}, keys.KeyBytes),
	}
	return store, plan, snapshot, created, master
}

func TestStageWriterEncryptsAndVerifiesWithoutAdvancingJournal(t *testing.T) {
	store, plan, snapshot, current, master := stagingFixture(t)
	defer snapshot.clear()
	defer clear(master.Key)
	currentRaw := bytes.Clone(store.files[datahome.DocumentRecoveryPath])
	if problem := writeVerifiedStage(context.Background(), store, snapshot, plan, current, master); problem != nil {
		t.Fatal(problem.Code)
	}
	if !bytes.Equal(store.files[datahome.DocumentRecoveryPath], currentRaw) {
		t.Fatal("stage writer advanced the journal")
	}
	stage := stageRelative(recoveryMigrationID)
	state, stateError := keys.ParseState(store.files[stage+"/"+stageKeyStateName])
	if stateError != nil || state != master.State {
		t.Fatal("stage key state changed")
	}
	for path, objectContext := range map[string]envelope.Context{
		stage + "/" + stageLedgerName: {
			DataHomeID: master.State.DataHomeID, KeyID: master.State.ActiveKeyID,
			Purpose: "ledger", ObjectID: "ledger",
		},
		stage + "/files/" + sourceDocumentID + ".chdoc": {
			DataHomeID: master.State.DataHomeID, KeyID: master.State.ActiveKeyID,
			Purpose: "blob", ObjectID: sourceDocumentID,
		},
	} {
		decrypted, err := envelope.Decrypt(store.files[path], master.Key, objectContext)
		if err != nil || len(decrypted.Plaintext) == 0 {
			t.Fatalf("stage object did not authenticate: %s", path)
		}
		clear(decrypted.Plaintext)
	}
}

func TestStageWriterFailsClosedAndRetainsRecoverableFacts(t *testing.T) {
	t.Run("journal drift before stage", func(t *testing.T) {
		store, plan, snapshot, current, master := stagingFixture(t)
		defer snapshot.clear()
		defer clear(master.Key)
		store.files[datahome.DocumentRecoveryPath] = []byte("changed")
		if problem := writeVerifiedStage(context.Background(), store, snapshot, plan, current, master); problem == nil || problem.Code != CodeRecoveryFailed {
			t.Fatalf("journal drift was accepted: %#v", problem)
		}
		if _, present := store.dirs[stageRelative(recoveryMigrationID)]; present {
			t.Fatal("journal drift created staging")
		}
	})

	t.Run("cancel between mutations", func(t *testing.T) {
		store, plan, snapshot, current, master := stagingFixture(t)
		defer snapshot.clear()
		defer clear(master.Key)
		ctx, cancel := context.WithCancel(context.Background())
		store.afterMutation = func(path string) {
			if path == stageRelative(recoveryMigrationID) {
				cancel()
			}
		}
		if problem := writeVerifiedStage(ctx, store, snapshot, plan, current, master); problem == nil || problem.Code != CodeStagingFailed {
			t.Fatalf("cancellation was accepted: %#v", problem)
		}
		stage := stageRelative(recoveryMigrationID)
		if names, present := store.dirs[stage]; !present || len(names) != 0 {
			t.Fatalf("unexpected partial stage: %#v", store.dirs[stage])
		}
		if !bytes.Equal(store.files[datahome.DocumentRecoveryPath], mustJournalBytes(t, current)) {
			t.Fatal("cancellation changed the journal")
		}
	})

	t.Run("on-disk envelope drift", func(t *testing.T) {
		store, plan, snapshot, current, master := stagingFixture(t)
		defer snapshot.clear()
		defer clear(master.Key)
		stage := stageRelative(recoveryMigrationID)
		store.afterMutation = func(path string) {
			if path == stage+"/files/"+sourceDocumentID+".chdoc" {
				store.files[path][len(store.files[path])-1] ^= 0xff
			}
		}
		if problem := writeVerifiedStage(context.Background(), store, snapshot, plan, current, master); problem == nil || problem.Code != CodeStagingFailed {
			t.Fatalf("envelope drift was accepted: %#v", problem)
		}
		if !bytes.Equal(store.files[datahome.DocumentRecoveryPath], mustJournalBytes(t, current)) {
			t.Fatal("failed staging changed the journal")
		}
	})
}

func mustJournalBytes(t *testing.T, journal Journal) []byte {
	t.Helper()
	raw, problem := journal.CanonicalBytes()
	if problem != nil {
		t.Fatal(problem.Code)
	}
	return raw
}
