package documentmigration

import (
	"reflect"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/documents/legacydpapi"
	"careerhub.local/backend/internal/runtimepaths"
)

type fakeOwnedTree struct {
	source            sourceBytes
	documentRecovery  bool
	restoreRecovery   bool
	structuralError   *runtimepaths.Error
	driftAfterRead    bool
	documentListCalls int
	calls             []string
}

func (f *fakeOwnedTree) RequireProcessOwnership() *runtimepaths.Error {
	f.calls = append(f.calls, "ownership")
	return nil
}
func (f *fakeOwnedTree) Exists(path string) (bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "exists:"+path)
	switch path {
	case datahome.DocumentRecoveryPath:
		return f.documentRecovery, nil
	case datahome.RestoreRecoveryPath:
		return f.restoreRecovery, nil
	default:
		panic("unexpected existence path")
	}
}
func (f *fakeOwnedTree) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "read:"+path)
	if path != datahome.MarkerPath || limit != datahome.MaxMarkerBytes {
		panic("unexpected marker read")
	}
	return f.source.Marker, true, nil
}
func (f *fakeOwnedTree) ReadObjectFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "object:"+path)
	if limit != legacydpapi.MaxCiphertextBytes {
		panic("unexpected ciphertext limit")
	}
	if path == legacyLedgerPath {
		return f.source.LedgerCiphertext, f.source.LedgerPresent, nil
	}
	content, present := f.source.Files[path[len(legacyFilesPath)+1:]]
	return content, present, nil
}
func (f *fakeOwnedTree) ListDirectoryNames(path string) ([]string, bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "list:"+path)
	if f.structuralError != nil {
		return nil, false, f.structuralError
	}
	f.documentListCalls++
	if f.driftAfterRead && f.documentListCalls > 1 {
		return []string{"files", "ledger.dpapi", "unexpected"}, true, nil
	}
	return []string{"files", "ledger.dpapi"}, f.source.DocumentsRootPresent, nil
}
func (f *fakeOwnedTree) ListDirectoryFileNames(path string) ([]string, bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "list-files:"+path)
	if path != legacyFilesPath {
		panic("unexpected files path")
	}
	names := make([]string, 0, len(f.source.Files))
	for name := range f.source.Files {
		names = append(names, name)
	}
	return names, true, nil
}

func TestBuildPlanOwnedUsesOneReadOnlyOwnedPass(t *testing.T) {
	reader := &fakeOwnedTree{source: sourceFixture(t)}
	plan, problem := buildPlanOwned(reader, true, unprotect)
	if problem != nil || plan.DocumentCount != 1 || plan.CiphertextFileCount != 2 {
		t.Fatalf("unexpected plan: %+v / %#v", plan, problem)
	}
	want := []string{
		"ownership", "exists:" + datahome.DocumentRecoveryPath,
		"read:" + datahome.MarkerPath, "exists:" + datahome.RestoreRecoveryPath,
		"list:documents", "object:" + legacyLedgerPath,
		"list-files:" + legacyFilesPath,
		"object:" + legacyFilesPath + "/" + sourceDocumentID + ".dpapi",
		"list:documents", "list-files:" + legacyFilesPath, "ownership",
	}
	if !reflect.DeepEqual(reader.calls, want) {
		t.Fatalf("unexpected owner calls: %v", reader.calls)
	}
}

func TestBuildPlanOwnedPreservesRecoveryAndDriftOrder(t *testing.T) {
	recovery := &fakeOwnedTree{source: sourceFixture(t), documentRecovery: true}
	if plan, problem := buildPlanOwned(recovery, true, unprotect); plan != (Plan{}) || problem == nil || problem.Code != CodeRecoveryRequired {
		t.Fatalf("recovery was not first: %+v / %#v", plan, problem)
	}
	if len(recovery.calls) != 2 {
		t.Fatalf("recovery path read private bytes: %v", recovery.calls)
	}
	restore := &fakeOwnedTree{source: sourceFixture(t), restoreRecovery: true}
	if plan, problem := buildPlanOwned(restore, true, unprotect); plan != (Plan{}) || problem == nil || problem.Code != CodeDataHomeInvalid {
		t.Fatalf("restore recovery was not refused: %+v / %#v", plan, problem)
	}
	drift := &fakeOwnedTree{source: sourceFixture(t), driftAfterRead: true}
	if plan, problem := buildPlanOwned(drift, true, unprotect); plan != (Plan{}) || problem == nil || problem.Code != CodeSourceInvalid {
		t.Fatalf("membership drift was not refused: %+v / %#v", plan, problem)
	}
	structure := &fakeOwnedTree{source: sourceFixture(t), structuralError: &runtimepaths.Error{Code: "PATH_NOT_DIRECTORY"}}
	if plan, problem := buildPlanOwned(structure, true, unprotect); plan != (Plan{}) || problem == nil || problem.Code != CodeSourceInvalid {
		t.Fatalf("invalid structure was not mapped: %+v / %#v", plan, problem)
	}
}

func TestReadSnapshotOwnedRetainsOnlyStableSource(t *testing.T) {
	t.Run("stable", func(t *testing.T) {
		reader := &fakeOwnedTree{source: sourceFixture(t)}
		plan, snapshot, problem := readSnapshotOwned(reader, true, unprotect)
		if problem != nil || plan.DocumentCount != 1 || len(snapshot.Documents) != 1 ||
			snapshot.Documents[0].ID != sourceDocumentID {
			t.Fatalf("unexpected snapshot: %+v / %#v / %#v", plan, snapshot, problem)
		}
		snapshot.clear()
	})

	t.Run("membership drift", func(t *testing.T) {
		reader := &fakeOwnedTree{source: sourceFixture(t), driftAfterRead: true}
		var plaintexts [][]byte
		trackedUnprotect := func(content []byte) ([]byte, error) {
			plaintext, err := unprotect(content)
			plaintexts = append(plaintexts, plaintext)
			return plaintext, err
		}
		plan, snapshot, problem := readSnapshotOwned(reader, true, trackedUnprotect)
		if plan != (Plan{}) || problem == nil || problem.Code != CodeSourceInvalid ||
			snapshot.Channel != "" || len(plaintexts) != 2 {
			t.Fatalf("drift retained snapshot: %+v / %#v / %#v", plan, snapshot, problem)
		}
		for index, plaintext := range plaintexts {
			if !allZero(plaintext) {
				t.Fatalf("plaintext %d survived membership drift", index)
			}
		}
	})
}
