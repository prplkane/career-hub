package documentmigration

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func (f *fakeRecoveryCopyStore) ReplaceFile(path string, before, after []byte) (bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "replace:"+path)
	current, present := f.files[path]
	if !present || !bytes.Equal(current, before) {
		return false, &runtimepaths.Error{Code: "FILE_CONFLICT"}
	}
	f.files[path] = bytes.Clone(after)
	return true, nil
}

func (f *fakeRecoveryCopyStore) RemoveFile(path string, expected []byte) (bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "remove:"+path)
	current, present := f.files[path]
	if !present || !bytes.Equal(current, expected) {
		return false, &runtimepaths.Error{Code: "FILE_CONFLICT"}
	}
	delete(f.files, path)
	return true, nil
}

func (f *fakeRecoveryCopyStore) MoveDirectoryNoReplace(source, target string) (runtimepaths.DirectoryMoveOutcome, *runtimepaths.Error) {
	f.calls = append(f.calls, "move:"+source+"->"+target)
	if _, present := f.dirs[target]; present {
		return runtimepaths.DirectoryMoveNotMoved, &runtimepaths.Error{Code: "DIRECTORY_EXISTS"}
	}
	if _, present := f.dirs[source]; !present {
		return runtimepaths.DirectoryMoveNotMoved, &runtimepaths.Error{Code: "DIRECTORY_MISSING"}
	}
	moveRecoveryMap(f.dirs, source, target)
	moveRecoveryMap(f.files, source, target)
	if separator := strings.LastIndexByte(source, '/'); separator > 0 {
		parent, name := source[:separator], source[separator+1:]
		f.dirs[parent] = slices.DeleteFunc(f.dirs[parent], func(candidate string) bool { return candidate == name })
	}
	if separator := strings.LastIndexByte(target, '/'); separator > 0 {
		f.addName(target[:separator], target[separator+1:])
	}
	if f.afterMove != nil {
		f.afterMove()
	}
	return runtimepaths.DirectoryMoveMoved, nil
}

func activationFixture(t *testing.T) (*fakeRecoveryCopyStore, Plan, legacySourceSnapshot, Journal, keys.MasterKey) {
	t.Helper()
	store, plan, snapshot, current, master := stagingFixture(t)
	source := sourceFixture(t)
	store.files[datahome.MarkerPath] = bytes.Clone(source.Marker)
	store.files[legacyLedgerPath] = bytes.Clone(source.LedgerCiphertext)
	store.files[legacyFilesPath+"/"+sourceDocumentID+".dpapi"] = bytes.Clone(source.Files[sourceDocumentID+".dpapi"])
	store.dirs["documents"] = []string{"files", "ledger.dpapi"}
	store.dirs[legacyFilesPath] = []string{sourceDocumentID + ".dpapi"}
	if problem := writeVerifiedStage(context.Background(), store, snapshot, plan, current, master); problem != nil {
		snapshot.clear()
		clear(master.Key)
		t.Fatal(problem.Code)
	}
	return store, plan, snapshot, current, master
}

func TestApplyActivationRechecksSwapsActivatesAndRecovers(t *testing.T) {
	store, plan, snapshot, current, master := activationFixture(t)
	defer snapshot.clear()
	defer clear(master.Key)
	staged, problem := confirmStageSourceAndAdvance(
		context.Background(), store, true, unprotect, snapshot, plan, current, master,
	)
	if problem != nil || staged.State != JournalStagingVerified {
		t.Fatalf("source recheck failed: %#v / %#v", staged, problem)
	}
	swapped, problem := swapVerifiedStage(context.Background(), store, snapshot, plan, staged, master)
	if problem != nil || swapped.State != JournalDocumentsSwapped {
		t.Fatalf("swap failed: %#v / %#v", swapped, problem)
	}
	activated, problem := activateMigrationMarker(context.Background(), store, snapshot, plan, swapped, master)
	if problem != nil || activated.State != JournalMarkerActivated {
		t.Fatalf("marker activation failed: %#v / %#v", activated, problem)
	}
	activeMarker, markerProblem := datahome.MarkerBytes(snapshot.Channel, master.State.DataHomeID)
	if markerProblem != nil || !bytes.Equal(store.files[datahome.MarkerPath], activeMarker) {
		t.Fatal("v2 marker was not activated exactly")
	}
	base := recoveryRelative(recoveryMigrationID)
	if _, present := store.dirs[base+"/activation_original"]; !present {
		t.Fatal("legacy original was not retained")
	}
	if _, present := store.dirs["documents"]; !present {
		t.Fatal("encrypted candidate was not activated")
	}

	trace := []string{}
	target := "CareerHub/dev/document-key/" + master.State.DataHomeID + "/" + master.State.ActiveKeyID
	backend := &migrationKeyBackend{values: map[string][]byte{target: bytes.Clone(master.Key)}, trace: &trace}
	result, recoveryProblem := recoverOwned(context.Background(), store, backend)
	if recoveryProblem != nil || !result.Present || !result.Recovered || len(backend.values) != 0 {
		t.Fatalf("activated rollback failed: %+v / %#v", result, recoveryProblem)
	}
	if !bytes.Equal(store.files[datahome.MarkerPath], snapshot.Marker) {
		t.Fatal("rollback did not restore the v1 marker")
	}
	activeTree, treeProblem := readLegacyTree(store, "documents")
	recoveryTree, recoveryTreeProblem := readLegacyTree(store, base+"/legacy_documents")
	if treeProblem != nil || recoveryTreeProblem != nil || !equalLegacyTrees(activeTree, recoveryTree) {
		t.Fatal("rollback did not restore the verified legacy tree")
	}
}

func TestApplyActivationRefusesSourceDriftBeforeJournalAdvance(t *testing.T) {
	store, plan, snapshot, current, master := activationFixture(t)
	defer snapshot.clear()
	defer clear(master.Key)
	store.files[datahome.MarkerPath] = []byte(`{"application":"CareerHub","channel":"stable","format_version":1}` + "\n")
	next, problem := confirmStageSourceAndAdvance(
		context.Background(), store, true, unprotect, snapshot, plan, current, master,
	)
	if next != (Journal{}) || problem == nil || problem.Code != CodeSourceChanged {
		t.Fatalf("source drift was accepted: %#v / %#v", next, problem)
	}
	if !bytes.Equal(store.files[datahome.DocumentRecoveryPath], mustJournalBytes(t, current)) {
		t.Fatal("source drift advanced the journal")
	}
}

func TestApplyActivationCancellationAfterFirstMoveRemainsRecoverable(t *testing.T) {
	store, plan, snapshot, current, master := activationFixture(t)
	defer snapshot.clear()
	defer clear(master.Key)
	staged, problem := confirmStageSourceAndAdvance(
		context.Background(), store, true, unprotect, snapshot, plan, current, master,
	)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	moves := 0
	store.afterMove = func() {
		moves++
		if moves == 1 {
			cancel()
		}
	}
	if next, swapProblem := swapVerifiedStage(ctx, store, snapshot, plan, staged, master); next != (Journal{}) || swapProblem == nil || swapProblem.Code != CodeApplyFailed {
		t.Fatalf("cancellation was accepted: %#v / %#v", next, swapProblem)
	}
	base := recoveryRelative(recoveryMigrationID)
	if _, original := store.dirs[base+"/activation_original"]; !original {
		t.Fatal("committed original move was lost")
	}
	if _, stage := store.dirs[stageRelative(recoveryMigrationID)]; !stage {
		t.Fatal("unmoved stage was lost")
	}
	if _, active := store.dirs["documents"]; active {
		t.Fatal("canceled swap created an active candidate")
	}
	if !bytes.Equal(store.files[datahome.DocumentRecoveryPath], mustJournalBytes(t, staged)) {
		t.Fatal("canceled swap changed the journal")
	}
}

func TestApplyActivationPreservesOriginallyAbsentDocuments(t *testing.T) {
	source := sourceBytes{Marker: sourceMarker(1), Windows: true}
	plan, snapshot := writerSnapshot(t, source)
	defer snapshot.clear()
	store := newFakeRecoveryCopyStore()
	if _, problem := writeRecoveryCopy(context.Background(), store, recoveryMigrationID, snapshot, plan); problem != nil {
		t.Fatal(problem.Code)
	}
	homeID, keyID := "dh_22222222222222222222222222222222", "key_33333333333333333333333333333333"
	current := Journal{MigrationID: recoveryMigrationID, State: JournalKeyCreated, DataHomeID: &homeID, KeyID: &keyID}
	store.files[datahome.DocumentRecoveryPath] = mustJournalBytes(t, current)
	store.addName("backups/document-encryption-migration", "active.json")
	store.files[datahome.MarkerPath] = bytes.Clone(source.Marker)
	master := keys.MasterKey{
		State: keys.State{Version: keys.Version, Algorithm: keys.Algorithm, DataHomeID: homeID, ActiveKeyID: keyID},
		Key:   bytes.Repeat([]byte{0x5a}, keys.KeyBytes),
	}
	defer clear(master.Key)
	if problem := writeVerifiedStage(context.Background(), store, snapshot, plan, current, master); problem != nil {
		t.Fatal(problem.Code)
	}
	staged, problem := confirmStageSourceAndAdvance(context.Background(), store, true, unprotect, snapshot, plan, current, master)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	swapped, problem := swapVerifiedStage(context.Background(), store, snapshot, plan, staged, master)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	activated, problem := activateMigrationMarker(context.Background(), store, snapshot, plan, swapped, master)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	completed, problem := completeActivatedMigration(context.Background(), store, snapshot, plan, activated, master)
	if problem != nil || completed.DocumentCount != 0 {
		t.Fatalf("absent-source completion failed: %#v / %#v", completed, problem)
	}
	base := recoveryRelative(recoveryMigrationID)
	if _, present := store.dirs[base+"/activation_original"]; present {
		t.Fatal("absent source fabricated an original directory")
	}
	if names := store.dirs["documents"]; !slices.Equal(names, []string{"files", stageKeyStateName}) {
		t.Fatalf("unexpected empty active tree: %#v", names)
	}
}
