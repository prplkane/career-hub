package documentmigration

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/documents/keys"
	"careerhub.local/backend/internal/runtimepaths"
)

type fakeRecoveryStore struct {
	*fakeRecoveryTree
	afterMove func()
	afterRead func(string)
}

func (f *fakeRecoveryStore) ReadSingleLinkFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "single:"+path)
	if f.pathError != nil {
		return nil, false, f.pathError
	}
	raw, present := f.files[path]
	if int64(len(raw)) > limit {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	if f.afterRead != nil {
		f.afterRead(path)
	}
	return bytes.Clone(raw), present, nil
}

func (f *fakeRecoveryStore) CreateFile(path string, raw []byte) (bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "create:"+path)
	if _, present := f.files[path]; present {
		return false, &runtimepaths.Error{Code: "FILE_EXISTS"}
	}
	f.files[path] = bytes.Clone(raw)
	return true, nil
}

func (f *fakeRecoveryStore) ReplaceFile(path string, before, after []byte) (bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "replace:"+path)
	current, present := f.files[path]
	if !present || !bytes.Equal(current, before) {
		return false, &runtimepaths.Error{Code: "FILE_CONFLICT"}
	}
	f.files[path] = bytes.Clone(after)
	return true, nil
}

func (f *fakeRecoveryStore) RemoveFile(path string, expected []byte) (bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "remove:"+path)
	current, present := f.files[path]
	if !present || !bytes.Equal(current, expected) {
		return false, &runtimepaths.Error{Code: "FILE_CONFLICT"}
	}
	delete(f.files, path)
	return true, nil
}

func (f *fakeRecoveryStore) MoveDirectoryNoReplace(source, target string) (runtimepaths.DirectoryMoveOutcome, *runtimepaths.Error) {
	f.calls = append(f.calls, "move:"+source+"->"+target)
	if _, present := f.dirs[target]; present {
		return runtimepaths.DirectoryMoveNotMoved, &runtimepaths.Error{Code: "DIRECTORY_EXISTS"}
	}
	if _, present := f.dirs[source]; !present {
		return runtimepaths.DirectoryMoveNotMoved, &runtimepaths.Error{Code: "DIRECTORY_MISSING"}
	}
	moveRecoveryMap(f.dirs, source, target)
	moveRecoveryMap(f.files, source, target)
	if f.afterMove != nil {
		f.afterMove()
	}
	return runtimepaths.DirectoryMoveMoved, nil
}

func moveRecoveryMap[T any](values map[string]T, source, target string) {
	keys := make([]string, 0)
	for name := range values {
		if name == source || strings.HasPrefix(name, source+"/") {
			keys = append(keys, name)
		}
	}
	for _, name := range keys {
		value := values[name]
		delete(values, name)
		values[target+strings.TrimPrefix(name, source)] = value
	}
}

func TestRecoveryExecutorOrdersMarkerBeforeCredentialAndResumesCancellation(t *testing.T) {
	fixture := recoveryTreeFixture(t, true)
	homeID, keyID := "dh_22222222222222222222222222222222", "key_33333333333333333333333333333333"
	activated, markerProblem := datahome.MarkerBytes("stable", homeID)
	if markerProblem != nil {
		t.Fatal(markerProblem)
	}
	fixture.files[datahome.MarkerPath] = activated
	store := &fakeRecoveryStore{fakeRecoveryTree: fixture}
	ctx, cancel := context.WithCancel(context.Background())
	moveCount := 0
	store.afterMove = func() {
		moveCount++
		if moveCount == 1 {
			cancel()
		}
	}
	target := "CareerHub/stable/document-key/" + homeID + "/" + keyID
	backend := &migrationKeyBackend{values: map[string][]byte{target: bytes.Repeat([]byte{1}, keys.KeyBytes)}, trace: &store.calls}

	first, problem := recoverOwned(ctx, store, backend)
	_, journalPresent := store.files[datahome.DocumentRecoveryPath]
	if problem == nil || problem.Code != CodeRecoveryFailed || !first.Present || first.Recovered ||
		!journalPresent {
		t.Fatalf("cancellation did not retain recovery: %+v / %#v", first, problem)
	}
	store.afterMove = nil
	result, problem := recoverOwned(context.Background(), store, backend)
	if problem != nil || !result.Present || !result.Recovered {
		t.Fatalf("recovery retry failed: %+v / %#v", result, problem)
	}
	if _, present := store.files[datahome.DocumentRecoveryPath]; present || len(backend.values) != 0 {
		t.Fatal("terminal recovery state was not cleaned")
	}
	base := recoveryRelative(recoveryMigrationID)
	if _, present := store.dirs[base+"/interrupted_candidate"]; !present {
		t.Fatal("active candidate was not preserved")
	}
	if _, present := store.dirs[base+"/interrupted_staging"]; !present {
		t.Fatal("staging candidate was not preserved")
	}
	if !bytes.Equal(store.files["documents/ledger.dpapi"], store.files[base+"/legacy_documents/ledger.dpapi"]) ||
		!bytes.Equal(store.files[datahome.MarkerPath], store.files[base+"/legacy_marker.json"]) {
		t.Fatal("legacy data or marker was not restored")
	}
	record := store.files[base+"/recovered_journal.json"]
	parsed, recordProblem := ParseJournal(record)
	if recordProblem != nil || parsed.State != JournalRecoveryVerified || parsed.DataHomeID != nil || parsed.KeyID != nil {
		t.Fatalf("recovery record is invalid: %+v / %#v", parsed, recordProblem)
	}
	markerIndex, keyIndex := callIndex(store.calls, "replace:"+datahome.MarkerPath), callIndex(store.calls, "credential_delete")
	if markerIndex < 0 || keyIndex < 0 || markerIndex >= keyIndex {
		t.Fatalf("credential cleanup crossed marker boundary: %v", store.calls)
	}
}

func TestRecoveryExecutorAcceptsExactExistingRecordAndRejectsConflict(t *testing.T) {
	for name, conflict := range map[string]bool{"exact retry": false, "record conflict": true} {
		t.Run(name, func(t *testing.T) {
			fixture := recoveryTreeFixture(t, false)
			base := recoveryRelative(recoveryMigrationID)
			delete(fixture.dirs, base+"/activation_original")
			delete(fixture.dirs, base+"/activation_original/files")
			delete(fixture.dirs, "documents")
			delete(fixture.dirs, stageRelative(recoveryMigrationID))
			journalRaw, _ := initialMigrationJournal().CanonicalBytes()
			fixture.files[datahome.DocumentRecoveryPath] = journalRaw
			recovered, _ := (Journal{MigrationID: recoveryMigrationID, State: JournalRecoveryVerified}).CanonicalBytes()
			if conflict {
				recovered = []byte("foreign")
			}
			fixture.files[base+"/recovered_journal.json"] = recovered
			store := &fakeRecoveryStore{fakeRecoveryTree: fixture}
			result, problem := recoverOwned(context.Background(), store, nil)
			if conflict {
				if problem == nil || problem.Code != CodeRecoveryFailed || result.Recovered {
					t.Fatalf("conflicting record was accepted: %+v / %#v", result, problem)
				}
				if _, present := store.files[datahome.DocumentRecoveryPath]; !present {
					t.Fatal("conflict removed recovery journal")
				}
				return
			}
			if problem != nil || !result.Recovered {
				t.Fatalf("exact recovery record was not idempotent: %+v / %#v", result, problem)
			}
		})
	}
}

func TestRecoveryExecutorHonorsCancellationImmediatelyBeforeMarkerMutation(t *testing.T) {
	fixture := recoveryTreeFixture(t, true)
	base := recoveryRelative(recoveryMigrationID)
	delete(fixture.dirs, base+"/activation_original")
	delete(fixture.dirs, base+"/activation_original/files")
	fixture.dirs["documents"] = append([]string(nil), fixture.dirs[base+"/legacy_documents"]...)
	fixture.dirs["documents/files"] = append([]string(nil), fixture.dirs[base+"/legacy_documents/files"]...)
	fixture.files["documents/ledger.dpapi"] = fixture.files[base+"/legacy_documents/ledger.dpapi"]
	fixture.files["documents/files/doc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.dpapi"] = fixture.files[base+"/legacy_documents/files/doc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.dpapi"]
	delete(fixture.dirs, stageRelative(recoveryMigrationID))
	homeID := "dh_22222222222222222222222222222222"
	activated, _ := datahome.MarkerBytes("stable", homeID)
	fixture.files[datahome.MarkerPath] = activated
	ctx, cancel := context.WithCancel(context.Background())
	store := &fakeRecoveryStore{fakeRecoveryTree: fixture}
	store.afterRead = func(path string) {
		if path == datahome.MarkerPath {
			cancel()
		}
	}
	result, problem := recoverOwned(ctx, store, nil)
	if problem == nil || problem.Code != CodeRecoveryFailed || result.Recovered ||
		!bytes.Equal(store.files[datahome.MarkerPath], activated) || callIndex(store.calls, "replace:"+datahome.MarkerPath) >= 0 {
		t.Fatalf("cancellation crossed marker mutation boundary: %+v / %#v / %v", result, problem, store.calls)
	}
}

func callIndex(calls []string, target string) int {
	for index, call := range calls {
		if call == target {
			return index
		}
	}
	return -1
}
