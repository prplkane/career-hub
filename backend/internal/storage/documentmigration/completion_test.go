package documentmigration

import (
	"bytes"
	"context"
	"testing"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/keys"
)

func activatedFixture(t *testing.T) (*fakeRecoveryCopyStore, Plan, legacySourceSnapshot, Journal, []byte) {
	t.Helper()
	store, plan, snapshot, current, master := activationFixture(t)
	staged, problem := confirmStageSourceAndAdvance(
		context.Background(), store, true, unprotect, snapshot, plan, current, master,
	)
	if problem != nil {
		snapshot.clear()
		clear(master.Key)
		t.Fatal(problem.Code)
	}
	swapped, problem := swapVerifiedStage(context.Background(), store, snapshot, plan, staged, master)
	if problem != nil {
		snapshot.clear()
		clear(master.Key)
		t.Fatal(problem.Code)
	}
	activated, problem := activateMigrationMarker(context.Background(), store, snapshot, plan, swapped, master)
	if problem != nil {
		snapshot.clear()
		clear(master.Key)
		t.Fatal(problem.Code)
	}
	return store, plan, snapshot, activated, master.Key
}

func TestCompletionVerifiesActiveTreeBeforeRemovingJournal(t *testing.T) {
	store, plan, snapshot, current, key := activatedFixture(t)
	defer snapshot.clear()
	defer clear(key)
	master := masterForActivated(current, key)
	completed, problem := completeActivatedMigration(context.Background(), store, snapshot, plan, current, master)
	if problem != nil || completed.PlanID != plan.PlanID || completed.MigrationID != recoveryMigrationID ||
		completed.DocumentCount != plan.DocumentCount {
		t.Fatalf("completion failed: %#v / %#v", completed, problem)
	}
	if _, present := store.files[datahome.DocumentRecoveryPath]; present {
		t.Fatal("completion retained the journal")
	}
	raw, recordProblem := completed.CanonicalBytes()
	if recordProblem != nil || !bytes.Equal(store.files[recoveryRelative(recoveryMigrationID)+"/completed.json"], raw) {
		t.Fatal("completion record changed")
	}
	if markerInfo, markerProblem := datahome.ParseMarkerForDocumentMigration(store.files[datahome.MarkerPath]); markerProblem != nil ||
		markerInfo.FormatVersion != TargetFormatVersion {
		t.Fatal("completion did not retain active v2 data")
	}
}

func TestExplicitRecoveryFinalizesCompletionCrashWithoutRollback(t *testing.T) {
	store, plan, snapshot, current, key := activatedFixture(t)
	defer snapshot.clear()
	defer clear(key)
	completed := CompletionRecord{PlanID: plan.PlanID, MigrationID: current.MigrationID, DocumentCount: plan.DocumentCount}
	if problem := publishCompletion(context.Background(), store, completed); problem != nil {
		t.Fatal(problem.Code)
	}
	activeMarker := bytes.Clone(store.files[datahome.MarkerPath])
	activeLedger := bytes.Clone(store.files["documents/"+stageLedgerName])
	trace := []string{}
	target := "CareerHub/dev/document-key/" + *current.DataHomeID + "/" + *current.KeyID
	backend := &migrationKeyBackend{values: map[string][]byte{target: bytes.Clone(key)}, trace: &trace}
	result, problem := recoverOwned(context.Background(), store, backend)
	if problem != nil || !result.Present || result.Recovered || !result.Finalized {
		t.Fatalf("completion finalizer failed: %+v / %#v", result, problem)
	}
	if _, present := store.files[datahome.DocumentRecoveryPath]; present || len(backend.values) != 1 {
		t.Fatal("finalizer removed the published key or retained journal")
	}
	if !bytes.Equal(store.files[datahome.MarkerPath], activeMarker) ||
		!bytes.Equal(store.files["documents/"+stageLedgerName], activeLedger) {
		t.Fatal("finalizer rolled back active v2 data")
	}
}

func TestCompletionFinalizerRefusesTamperedActiveTree(t *testing.T) {
	store, plan, snapshot, current, key := activatedFixture(t)
	defer snapshot.clear()
	defer clear(key)
	completed := CompletionRecord{PlanID: plan.PlanID, MigrationID: current.MigrationID, DocumentCount: plan.DocumentCount}
	if problem := publishCompletion(context.Background(), store, completed); problem != nil {
		t.Fatal(problem.Code)
	}
	path := "documents/files/" + sourceDocumentID + ".chdoc"
	store.files[path][len(store.files[path])-1] ^= 0xff
	trace := []string{}
	target := "CareerHub/dev/document-key/" + *current.DataHomeID + "/" + *current.KeyID
	backend := &migrationKeyBackend{values: map[string][]byte{target: bytes.Clone(key)}, trace: &trace}
	result, problem := recoverOwned(context.Background(), store, backend)
	if result != (RecoveryResult{}) || problem == nil || problem.Code != CodeRecoveryFailed {
		t.Fatalf("tampered completion was accepted: %+v / %#v", result, problem)
	}
	if _, present := store.files[datahome.DocumentRecoveryPath]; !present || len(backend.values) != 1 {
		t.Fatal("failed finalizer destroyed recovery authority")
	}
}

func masterForActivated(journal Journal, key []byte) keys.MasterKey {
	return keys.MasterKey{
		State: keys.State{
			Version: keys.Version, Algorithm: keys.Algorithm,
			DataHomeID: *journal.DataHomeID, ActiveKeyID: *journal.KeyID,
		},
		Key: key,
	}
}
