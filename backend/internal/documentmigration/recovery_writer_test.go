package documentmigration

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

type fakeRecoveryCopyStore struct {
	*fakeRecoveryTree
	failPath      string
	afterMutation func(string)
	afterMove     func()
}

func newFakeRecoveryCopyStore() *fakeRecoveryCopyStore {
	return &fakeRecoveryCopyStore{fakeRecoveryTree: &fakeRecoveryTree{
		files: map[string][]byte{}, dirs: map[string][]string{}, owned: true,
	}}
}

func (f *fakeRecoveryCopyStore) EnsureDirectory(path string) (bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "mkdir:"+path)
	if path == f.failPath {
		return false, &runtimepaths.Error{Code: "SYNTHETIC_FAILURE"}
	}
	changed := false
	parts := strings.Split(path, "/")
	for index := range parts {
		current := strings.Join(parts[:index+1], "/")
		if _, file := f.files[current]; file {
			return changed, &runtimepaths.Error{Code: "PATH_NOT_DIRECTORY"}
		}
		if _, present := f.dirs[current]; present {
			continue
		}
		f.dirs[current] = []string{}
		if index > 0 {
			parent := strings.Join(parts[:index], "/")
			f.addName(parent, parts[index])
		}
		changed = true
	}
	if changed && f.afterMutation != nil {
		f.afterMutation(path)
	}
	return changed, nil
}

func (f *fakeRecoveryCopyStore) CreateFile(path string, content []byte) (bool, *runtimepaths.Error) {
	return f.create(path, content, false)
}

func (f *fakeRecoveryCopyStore) CreateObjectFile(path string, content []byte) (bool, *runtimepaths.Error) {
	return f.create(path, content, true)
}

func (f *fakeRecoveryCopyStore) ReadObjectFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "object:"+path)
	content, present := f.files[path]
	if int64(len(content)) > limit {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	return bytes.Clone(content), present, nil
}

func (f *fakeRecoveryCopyStore) create(path string, content []byte, object bool) (bool, *runtimepaths.Error) {
	prefix := "create:"
	if object {
		prefix = "create-object:"
	}
	f.calls = append(f.calls, prefix+path)
	if path == f.failPath {
		return false, &runtimepaths.Error{Code: "SYNTHETIC_FAILURE"}
	}
	if _, present := f.files[path]; present {
		return false, &runtimepaths.Error{Code: "FILE_EXISTS"}
	}
	if _, present := f.dirs[path]; present {
		return false, &runtimepaths.Error{Code: "PATH_NOT_REGULAR"}
	}
	separator := strings.LastIndexByte(path, '/')
	if separator < 1 {
		return false, &runtimepaths.Error{Code: "PATH_PARENT_MISSING"}
	}
	parent, name := path[:separator], path[separator+1:]
	if _, present := f.dirs[parent]; !present {
		return false, &runtimepaths.Error{Code: "PATH_PARENT_MISSING"}
	}
	f.files[path] = bytes.Clone(content)
	f.addName(parent, name)
	if f.afterMutation != nil {
		f.afterMutation(path)
	}
	return true, nil
}

func (f *fakeRecoveryCopyStore) addName(parent, name string) {
	names := f.dirs[parent]
	if !slices.Contains(names, name) {
		f.dirs[parent] = append(names, name)
		slices.Sort(f.dirs[parent])
	}
}

func writerSnapshot(t *testing.T, source sourceBytes) (Plan, legacySourceSnapshot) {
	t.Helper()
	names := make([]string, 0, len(source.Files))
	for name := range source.Files {
		names = append(names, name)
	}
	slices.Sort(names)
	plan, snapshot, problem := validateSourceSnapshot(
		source.Marker, source.DocumentsRootPresent, source.LedgerPresent,
		source.LedgerCiphertext, names, source.Windows,
		func(name string) ([]byte, *Error) { return source.Files[name], nil }, unprotect,
	)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	return plan, snapshot
}

func TestRecoveryCopyWriterPublishesAndVerifiesExactSource(t *testing.T) {
	source := sourceFixture(t)
	plan, snapshot := writerSnapshot(t, source)
	defer snapshot.clear()
	store := newFakeRecoveryCopyStore()
	journal, problem := writeRecoveryCopy(context.Background(), store, recoveryMigrationID, snapshot, plan)
	if problem != nil || journal.MigrationID != recoveryMigrationID || journal.State != JournalRecoveryVerified ||
		journal.DataHomeID != nil || journal.KeyID != nil {
		t.Fatalf("unexpected recovery result: %#v / %#v", journal, problem)
	}
	if _, present := store.files[datahome.DocumentRecoveryPath]; present {
		t.Fatal("recovery writer published the journal")
	}
	base := recoveryRelative(recoveryMigrationID)
	manifest, _ := (RecoveryManifest{Plan: plan, DocumentsRootPresent: true}).CanonicalBytes()
	for path, expected := range map[string][]byte{
		base + "/legacy_marker.json":                                    snapshot.Marker,
		base + "/legacy_documents/ledger.dpapi":                         snapshot.LedgerCiphertext,
		base + "/legacy_documents/files/" + sourceDocumentID + ".dpapi": snapshot.Documents[0].Ciphertext,
		base + "/recovery_manifest.json":                                manifest,
	} {
		if !bytes.Equal(store.files[path], expected) {
			t.Fatalf("recovery copy changed %s", path)
		}
	}
	tree, treeProblem := readLegacyTree(store, base+"/legacy_documents")
	if treeProblem != nil || !manifestMatchesTree(RecoveryManifest{Plan: plan, DocumentsRootPresent: true}, tree) {
		t.Fatalf("recovery copy is not planner-readable: %#v / %#v", tree, treeProblem)
	}
}

func TestRecoveryCopyWriterMarksOriginallyAbsentDocuments(t *testing.T) {
	source := sourceBytes{Marker: sourceMarker(1), Windows: true}
	plan, snapshot := writerSnapshot(t, source)
	defer snapshot.clear()
	store := newFakeRecoveryCopyStore()
	if _, problem := writeRecoveryCopy(context.Background(), store, recoveryMigrationID, snapshot, plan); problem != nil {
		t.Fatal(problem.Code)
	}
	base := recoveryRelative(recoveryMigrationID)
	if string(store.files[base+"/legacy_documents_absent"]) != legacyAbsentMarker ||
		len(store.dirs[base+"/legacy_documents/files"]) != 0 {
		t.Fatal("originally absent documents were not represented exactly")
	}
}

func TestRecoveryCopyWriterFailsBeforeOrWithoutJournal(t *testing.T) {
	source := sourceFixture(t)
	plan, snapshot := writerSnapshot(t, source)
	defer snapshot.clear()

	t.Run("plan mismatch", func(t *testing.T) {
		store := newFakeRecoveryCopyStore()
		changed := plan
		changed.PlanID = "sha256:" + strings.Repeat("0", 64)
		if journal, problem := writeRecoveryCopy(context.Background(), store, recoveryMigrationID, snapshot, changed); journal != (Journal{}) || problem == nil || problem.Code != CodeRecoveryFailed || len(store.files) != 0 || len(store.dirs) != 0 {
			t.Fatalf("plan mismatch mutated storage: %#v / %#v", journal, problem)
		}
	})

	t.Run("cancel between mutations", func(t *testing.T) {
		store := newFakeRecoveryCopyStore()
		ctx, cancel := context.WithCancel(context.Background())
		store.afterMutation = func(string) { cancel() }
		if journal, problem := writeRecoveryCopy(ctx, store, recoveryMigrationID, snapshot, plan); journal != (Journal{}) || problem == nil || problem.Code != CodeRecoveryFailed || len(store.files) != 0 {
			t.Fatalf("cancellation published content: %#v / %#v", journal, problem)
		}
		if len(store.dirs) != 1 || store.dirs["backups"] == nil {
			t.Fatalf("unexpected retained partial directory: %#v", store.dirs)
		}
	})

	t.Run("verification drift", func(t *testing.T) {
		store := newFakeRecoveryCopyStore()
		base := recoveryRelative(recoveryMigrationID)
		store.afterMutation = func(path string) {
			if path == base+"/recovery_manifest.json" {
				store.files[base+"/legacy_marker.json"] = []byte("changed")
			}
		}
		if journal, problem := writeRecoveryCopy(context.Background(), store, recoveryMigrationID, snapshot, plan); journal != (Journal{}) || problem == nil || problem.Code != CodeRecoveryFailed {
			t.Fatalf("drift was accepted: %#v / %#v", journal, problem)
		}
		if _, present := store.files[datahome.DocumentRecoveryPath]; present {
			t.Fatal("failed verification published a journal")
		}
	})
}
