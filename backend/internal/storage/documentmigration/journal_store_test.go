package documentmigration

import (
	"bytes"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type fakeJournalStore struct {
	raw                    []byte
	present, owned         bool
	createChanged          bool
	replaceChanged         bool
	removeChanged          bool
	createErr, replaceErr  *runtimepaths.Error
	removeErr, readErr     *runtimepaths.Error
	createWritesWhenFalse  bool
	replaceWritesWhenFalse bool
	removeDeletesWhenFalse bool
	createRetainsWhenTrue  bool
	replaceRetainsWhenTrue bool
	removeRetainsWhenTrue  bool
	calls                  []string
}

func (f *fakeJournalStore) RequireProcessOwnership() *runtimepaths.Error {
	f.calls = append(f.calls, "ownership")
	if !f.owned {
		return &runtimepaths.Error{Code: "OWNERSHIP_REQUIRED"}
	}
	return nil
}

func (f *fakeJournalStore) ReadSingleLinkFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "read:"+path)
	if path != datahome.DocumentRecoveryPath || limit != MaxJournalBytes || f.readErr != nil {
		return nil, false, f.readErr
	}
	return bytes.Clone(f.raw), f.present, nil
}

func (f *fakeJournalStore) CreateFile(path string, raw []byte) (bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "create:"+path)
	if f.createChanged && !f.createRetainsWhenTrue || !f.createChanged && f.createWritesWhenFalse {
		f.raw, f.present = bytes.Clone(raw), true
	}
	return f.createChanged, f.createErr
}

func (f *fakeJournalStore) ReplaceFile(path string, _, next []byte) (bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "replace:"+path)
	if f.replaceChanged && !f.replaceRetainsWhenTrue || !f.replaceChanged && f.replaceWritesWhenFalse {
		f.raw, f.present = bytes.Clone(next), true
	}
	return f.replaceChanged, f.replaceErr
}

func (f *fakeJournalStore) RemoveFile(path string, _ []byte) (bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "remove:"+path)
	if f.removeChanged && !f.removeRetainsWhenTrue || !f.removeChanged && f.removeDeletesWhenFalse {
		f.raw, f.present = nil, false
	}
	return f.removeChanged, f.removeErr
}

func TestJournalStoreLifecycleUsesExactObservedFacts(t *testing.T) {
	store := &fakeJournalStore{owned: true, createChanged: true, replaceChanged: true, removeChanged: true}
	initial := initialMigrationJournal()
	if outcome, problem := publishJournal(store, initial); outcome != journalWritten || problem != nil {
		t.Fatalf("publish failed: %v / %#v", outcome, problem)
	}
	homeID, keyID := "dh_11111111111111111111111111111111", "key_22222222222222222222222222222222"
	planned := Journal{MigrationID: recoveryMigrationID, State: JournalKeyPlanned, DataHomeID: &homeID, KeyID: &keyID}
	if outcome, problem := advanceJournal(store, initial, planned); outcome != journalWritten || problem != nil {
		t.Fatalf("advance failed: %v / %#v", outcome, problem)
	}
	created := Journal{MigrationID: recoveryMigrationID, State: JournalKeyCreated, DataHomeID: &homeID, KeyID: &keyID}
	if outcome, problem := advanceJournal(store, planned, created); outcome != journalWritten || problem != nil {
		t.Fatalf("second advance failed: %v / %#v", outcome, problem)
	}
	if outcome, problem := removeJournal(store, created); outcome != journalRemoved || problem != nil || store.present {
		t.Fatalf("remove failed: %v / %#v", outcome, problem)
	}
	want := []string{
		"ownership", "create:" + datahome.DocumentRecoveryPath, "ownership", "read:" + datahome.DocumentRecoveryPath, "ownership",
		"ownership", "replace:" + datahome.DocumentRecoveryPath, "ownership", "read:" + datahome.DocumentRecoveryPath, "ownership",
		"ownership", "replace:" + datahome.DocumentRecoveryPath, "ownership", "read:" + datahome.DocumentRecoveryPath, "ownership",
		"ownership", "remove:" + datahome.DocumentRecoveryPath, "ownership", "read:" + datahome.DocumentRecoveryPath, "ownership",
	}
	if !reflect.DeepEqual(store.calls, want) {
		t.Fatalf("journal operation order changed: %v", store.calls)
	}
}

func TestJournalStoreClassifiesUncertainWritesWithoutGuessing(t *testing.T) {
	initial := initialMigrationJournal()
	initialRaw, _ := initial.CanonicalBytes()
	homeID, keyID := "dh_11111111111111111111111111111111", "key_22222222222222222222222222222222"
	planned := Journal{MigrationID: recoveryMigrationID, State: JournalKeyPlanned, DataHomeID: &homeID, KeyID: &keyID}
	ioProblem := &runtimepaths.Error{Code: "SYNTHETIC"}

	for name, store := range map[string]*fakeJournalStore{
		"publish error after commit": {owned: true, createChanged: true, createErr: ioProblem},
		"unattributed publish":       {owned: true, createWritesWhenFalse: true, createErr: ioProblem},
		"advance stayed old":         {owned: true, raw: initialRaw, present: true, replaceErr: ioProblem},
		"unattributed advance":       {owned: true, raw: initialRaw, present: true, replaceWritesWhenFalse: true, replaceErr: ioProblem},
		"false committed advance":    {owned: true, raw: initialRaw, present: true, replaceChanged: true, replaceRetainsWhenTrue: true},
		"unobservable advance":       {owned: true, raw: initialRaw, present: true, replaceChanged: true, readErr: ioProblem},
	} {
		t.Run(name, func(t *testing.T) {
			var outcome journalWriteOutcome
			var problem *Error
			if name == "publish error after commit" || name == "unattributed publish" {
				outcome, problem = publishJournal(store, initial)
			} else {
				outcome, problem = advanceJournal(store, initial, planned)
			}
			want := journalWriteUnknown
			if name == "publish error after commit" {
				want = journalWritten
			} else if name == "advance stayed old" {
				want = journalNotWritten
			}
			if outcome != want || problem == nil || problem.Code != CodeApplyFailed {
				t.Fatalf("uncertain write was misclassified: %v / %#v", outcome, problem)
			}
		})
	}
}

func TestJournalStoreRemovalFactsAndInvalidAuthority(t *testing.T) {
	homeID, keyID := "dh_11111111111111111111111111111111", "key_22222222222222222222222222222222"
	journal := Journal{MigrationID: recoveryMigrationID, State: JournalKeyCreated, DataHomeID: &homeID, KeyID: &keyID}
	raw, _ := journal.CanonicalBytes()
	ioProblem := &runtimepaths.Error{Code: "SYNTHETIC"}
	for name, store := range map[string]*fakeJournalStore{
		"error after removal":  {owned: true, raw: raw, present: true, removeChanged: true, removeErr: ioProblem},
		"not removed":          {owned: true, raw: raw, present: true, removeErr: ioProblem},
		"unattributed removal": {owned: true, raw: raw, present: true, removeDeletesWhenFalse: true, removeErr: ioProblem},
		"false removal report": {owned: true, raw: raw, present: true, removeChanged: true, removeRetainsWhenTrue: true},
		"unobservable removal": {owned: true, raw: raw, present: true, removeChanged: true, readErr: ioProblem},
	} {
		t.Run(name, func(t *testing.T) {
			outcome, problem := removeJournal(store, journal)
			want := journalRemovalUnknown
			if name == "error after removal" {
				want = journalRemoved
			} else if name == "not removed" {
				want = journalNotRemoved
			}
			if outcome != want || problem == nil || problem.Code != CodeRecoveryFailed {
				t.Fatalf("removal was misclassified: %v / %#v", outcome, problem)
			}
		})
	}

	invalid := &fakeJournalStore{owned: true, createChanged: true}
	if outcome, problem := publishJournal(invalid, Journal{MigrationID: "bad", State: JournalRecoveryVerified}); outcome != journalNotWritten || problem == nil || len(invalid.calls) != 0 {
		t.Fatalf("invalid journal reached storage: %v / %#v / %v", outcome, problem, invalid.calls)
	}
}
