package documentmigration

import (
	"bytes"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/documents/legacydpapi"
	"careerhub.local/backend/internal/runtimepaths"
)

const recoveryMigrationID = "11111111111111111111111111111111"

type fakeRecoveryTree struct {
	files     map[string][]byte
	dirs      map[string][]string
	pathError *runtimepaths.Error
	owned     bool
	calls     []string
}

func (f *fakeRecoveryTree) RequireProcessOwnership() *runtimepaths.Error {
	f.calls = append(f.calls, "ownership")
	if !f.owned {
		return &runtimepaths.Error{Code: "OWNERSHIP_REQUIRED"}
	}
	return nil
}

func (f *fakeRecoveryTree) Exists(path string) (bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "exists:"+path)
	if f.pathError != nil {
		return false, f.pathError
	}
	_, file := f.files[path]
	_, directory := f.dirs[path]
	return file || directory, nil
}

func (f *fakeRecoveryTree) ReadFile(path string, _ int64) ([]byte, bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "read:"+path)
	if f.pathError != nil {
		return nil, false, f.pathError
	}
	content, present := f.files[path]
	return append([]byte(nil), content...), present, nil
}

func (f *fakeRecoveryTree) ReadObjectFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "object:"+path)
	if limit != legacydpapi.MaxCiphertextBytes || f.pathError != nil {
		return nil, false, f.pathError
	}
	content, present := f.files[path]
	return append([]byte(nil), content...), present, nil
}

func (f *fakeRecoveryTree) ListDirectoryNames(path string) ([]string, bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "list:"+path)
	if f.pathError != nil {
		return nil, false, f.pathError
	}
	names, present := f.dirs[path]
	return append([]string(nil), names...), present, nil
}

func (f *fakeRecoveryTree) ListDirectoryFileNames(path string) ([]string, bool, *runtimepaths.Error) {
	f.calls = append(f.calls, "list-files:"+path)
	if f.pathError != nil {
		return nil, false, f.pathError
	}
	names, present := f.dirs[path]
	return append([]string(nil), names...), present, nil
}

func recoveryTreeFixture(t *testing.T, documentsPresent bool) *fakeRecoveryTree {
	t.Helper()
	base := recoveryRelative(recoveryMigrationID)
	homeID, keyID := "dh_22222222222222222222222222222222", "key_33333333333333333333333333333333"
	journal := Journal{recoveryMigrationID, JournalStagingVerified, &homeID, &keyID}
	journalBytes, problem := journal.CanonicalBytes()
	if problem != nil {
		t.Fatal(problem)
	}
	marker := []byte("{\"application\":\"CareerHub\",\"channel\":\"stable\",\"format_version\":1}\n")
	plan := Plan{
		PlanID:              "sha256:" + "aabbccdd" + "00000000000000000000000000000000000000000000000000000000",
		SourceFormatVersion: 1, TargetFormatVersion: 2,
	}
	dirs := map[string][]string{
		base + "/legacy_documents":          {"files"},
		base + "/legacy_documents/files":    {},
		base + "/activation_original":       {"files"},
		base + "/activation_original/files": {},
		"documents":                         {"key_state.json"},
		stageRelative(recoveryMigrationID):  {"key_state.json"},
	}
	files := map[string][]byte{
		datahome.DocumentRecoveryPath: journalBytes,
		datahome.MarkerPath:           marker,
		base + "/legacy_marker.json":  marker,
	}
	if documentsPresent {
		ledger, blob := []byte("fictional-ledger"), []byte("fictional-blob")
		plan.DocumentCount, plan.CiphertextFileCount = 1, 2
		plan.TotalCiphertextBytes = len(ledger) + len(blob)
		for _, root := range []string{base + "/legacy_documents", base + "/activation_original"} {
			dirs[root] = []string{"files", "ledger.dpapi"}
			dirs[root+"/files"] = []string{"doc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.dpapi"}
			files[root+"/ledger.dpapi"] = ledger
			files[root+"/files/doc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.dpapi"] = blob
		}
	} else {
		files[base+"/legacy_documents_absent"] = []byte(legacyAbsentMarker)
	}
	manifest, problem := (RecoveryManifest{Plan: plan, DocumentsRootPresent: documentsPresent}).CanonicalBytes()
	if problem != nil {
		t.Fatal(problem)
	}
	files[base+"/recovery_manifest.json"] = manifest
	return &fakeRecoveryTree{files: files, dirs: dirs, owned: true}
}

func TestRecoveryPlanPreservesCandidatesInSourceOrder(t *testing.T) {
	fixture := recoveryTreeFixture(t, true)
	base := recoveryRelative(recoveryMigrationID)
	fixture.dirs[base+"/interrupted_candidate"] = nil
	delete(fixture.dirs, base+"/interrupted_candidate")
	plan, present, problem := buildRecoveryPlanOwned(fixture)
	want := []RecoveryAction{
		RecoveryPreserveCandidate, RecoveryRestoreOriginal, RecoveryPreserveStaging,
		RecoveryDiscardKey, RecoveryWriteRecord, RecoveryRemoveJournal,
	}
	if problem != nil || !present || plan.Channel != "stable" || !reflect.DeepEqual(plan.Actions, want) {
		t.Fatalf("unexpected recovery plan: %+v / %v / %#v", plan, present, problem)
	}
	if fixture.calls[0] != "ownership" || fixture.calls[len(fixture.calls)-1] != "ownership" {
		t.Fatalf("ownership did not bracket reads: %v", fixture.calls)
	}
}

func TestRecoveryPlanRequiresObservedMarkerRestoreBeforeCredentialAction(t *testing.T) {
	fixture := recoveryTreeFixture(t, true)
	homeID := "dh_22222222222222222222222222222222"
	activated, markerProblem := datahome.MarkerBytes("stable", homeID)
	if markerProblem != nil {
		t.Fatal(markerProblem)
	}
	fixture.files[datahome.MarkerPath] = activated
	plan, present, problem := buildRecoveryPlanOwned(fixture)
	want := []RecoveryAction{
		RecoveryPreserveCandidate, RecoveryRestoreOriginal, RecoveryPreserveStaging,
		RecoveryRestoreMarker,
	}
	if problem != nil || !present || !reflect.DeepEqual(plan.Actions, want) {
		t.Fatalf("activated marker did not stop recovery before key cleanup: %+v / %v / %#v", plan, present, problem)
	}

	fixture = recoveryTreeFixture(t, true)
	fixture.files[datahome.MarkerPath] = bytes.Replace(activated, []byte(`,"channel"`), []byte(", \"channel\""), 1)
	if plan, present, problem := buildRecoveryPlanOwned(fixture); !emptyRecoveryPlan(plan) || present || problem == nil || problem.Code != CodeRecoveryFailed {
		t.Fatalf("noncanonical active marker was accepted: %+v / %v / %#v", plan, present, problem)
	}
}

func TestRecoveryPlanSupportsInterruptedRecoveryRetries(t *testing.T) {
	base := recoveryRelative(recoveryMigrationID)
	t.Run("candidate already preserved", func(t *testing.T) {
		fixture := recoveryTreeFixture(t, true)
		delete(fixture.dirs, "documents")
		fixture.dirs[base+"/interrupted_candidate"] = []string{"key_state.json"}
		plan, present, problem := buildRecoveryPlanOwned(fixture)
		want := []RecoveryAction{RecoveryRestoreOriginal, RecoveryPreserveStaging, RecoveryDiscardKey, RecoveryWriteRecord, RecoveryRemoveJournal}
		if problem != nil || !present || !reflect.DeepEqual(plan.Actions, want) {
			t.Fatalf("unexpected retry plan: %+v / %#v", plan, problem)
		}
	})
	t.Run("legacy already restored", func(t *testing.T) {
		fixture := recoveryTreeFixture(t, true)
		delete(fixture.dirs, base+"/activation_original")
		delete(fixture.dirs, base+"/activation_original/files")
		fixture.dirs["documents"] = append([]string(nil), fixture.dirs[base+"/legacy_documents"]...)
		fixture.dirs["documents/files"] = append([]string(nil), fixture.dirs[base+"/legacy_documents/files"]...)
		fixture.files["documents/ledger.dpapi"] = fixture.files[base+"/legacy_documents/ledger.dpapi"]
		fixture.files["documents/files/doc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.dpapi"] = fixture.files[base+"/legacy_documents/files/doc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.dpapi"]
		fixture.dirs[base+"/interrupted_candidate"] = []string{"key_state.json"}
		plan, present, problem := buildRecoveryPlanOwned(fixture)
		want := []RecoveryAction{RecoveryPreserveStaging, RecoveryDiscardKey, RecoveryWriteRecord, RecoveryRemoveJournal}
		if problem != nil || !present || !reflect.DeepEqual(plan.Actions, want) {
			t.Fatalf("unexpected restored retry: %+v / %#v", plan, problem)
		}
	})
}

func TestRecoveryPlanOriginallyAbsentPreservesOnlyActiveCandidate(t *testing.T) {
	fixture := recoveryTreeFixture(t, false)
	base := recoveryRelative(recoveryMigrationID)
	delete(fixture.dirs, base+"/activation_original")
	delete(fixture.dirs, base+"/activation_original/files")
	delete(fixture.dirs, stageRelative(recoveryMigrationID))
	plan, present, problem := buildRecoveryPlanOwned(fixture)
	want := []RecoveryAction{RecoveryPreserveCandidate, RecoveryDiscardKey, RecoveryWriteRecord, RecoveryRemoveJournal}
	if problem != nil || !present || !reflect.DeepEqual(plan.Actions, want) {
		t.Fatalf("unexpected absent-source plan: %+v / %#v", plan, problem)
	}
}

func TestRecoveryPlanRefusesAmbiguityAndDrift(t *testing.T) {
	base := recoveryRelative(recoveryMigrationID)
	tests := map[string]func(*fakeRecoveryTree){
		"original mismatch":        func(f *fakeRecoveryTree) { f.files[base+"/activation_original/ledger.dpapi"] = []byte("changed") },
		"two active candidates":    func(f *fakeRecoveryTree) { f.dirs[base+"/interrupted_candidate"] = nil },
		"two staging candidates":   func(f *fakeRecoveryTree) { f.dirs[base+"/interrupted_staging"] = nil },
		"manifest mismatch":        func(f *fakeRecoveryTree) { f.files[base+"/legacy_documents/ledger.dpapi"] = []byte("changed") },
		"completed boundary":       func(f *fakeRecoveryTree) { f.files[base+"/completed.json"] = []byte("synthetic") },
		"recovery record conflict": func(f *fakeRecoveryTree) { f.files[base+"/recovered_journal.json"] = []byte("foreign") },
		"invalid absent marker":    func(f *fakeRecoveryTree) { f.files[base+"/legacy_documents_absent"] = []byte("bad") },
		"restored source missing": func(f *fakeRecoveryTree) {
			delete(f.dirs, base+"/activation_original")
			delete(f.dirs, base+"/activation_original/files")
			delete(f.dirs, "documents")
		},
		"restored source drift": func(f *fakeRecoveryTree) {
			delete(f.dirs, base+"/activation_original")
			delete(f.dirs, base+"/activation_original/files")
			f.dirs["documents"] = append([]string(nil), f.dirs[base+"/legacy_documents"]...)
			f.dirs["documents/files"] = append([]string(nil), f.dirs[base+"/legacy_documents/files"]...)
			f.files["documents/ledger.dpapi"] = []byte("changed")
			f.files["documents/files/doc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.dpapi"] = f.files[base+"/legacy_documents/files/doc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.dpapi"]
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			documentsPresent := name != "invalid absent marker"
			fixture := recoveryTreeFixture(t, documentsPresent)
			if name == "two staging candidates" {
				delete(fixture.dirs, base+"/interrupted_candidate")
			}
			mutate(fixture)
			if plan, present, problem := buildRecoveryPlanOwned(fixture); !emptyRecoveryPlan(plan) || present || problem == nil || problem.Code != CodeRecoveryFailed {
				t.Fatalf("unsafe recovery accepted: %+v / %v / %#v", plan, present, problem)
			}
		})
	}
}

func TestRecoveryPlanDiscardsOnlyACompleteJournalIdentity(t *testing.T) {
	homeID, keyID := "dh_22222222222222222222222222222222", "key_33333333333333333333333333333333"
	for name, journal := range map[string]Journal{
		"none":                     {MigrationID: recoveryMigrationID, State: JournalRecoveryVerified},
		"partial":                  {MigrationID: recoveryMigrationID, State: JournalKeyPlanned, DataHomeID: &homeID},
		"identity before planning": {MigrationID: recoveryMigrationID, State: JournalRecoveryVerified, DataHomeID: &homeID, KeyID: &keyID},
	} {
		t.Run(name, func(t *testing.T) {
			plan, problem := planRecovery(recoveryFacts{
				journal: journal, channel: "stable", legacyOriginallyAbsent: true,
			})
			if name != "none" {
				if problem == nil || problem.Code != CodeRecoveryFailed || !emptyRecoveryPlan(plan) {
					t.Fatalf("impossible journal identity was accepted: %+v / %#v", plan, problem)
				}
				return
			}
			if problem != nil {
				t.Fatal(problem)
			}
			for _, action := range plan.Actions {
				if action == RecoveryDiscardKey {
					t.Fatal("incomplete journal identity authorized key deletion")
				}
			}
		})
	}
}

func TestRecoveryPlanAbsentJournalAndOwnershipAreReadOnly(t *testing.T) {
	fixture := &fakeRecoveryTree{files: map[string][]byte{}, dirs: map[string][]string{}, owned: true}
	if plan, present, problem := buildRecoveryPlanOwned(fixture); !emptyRecoveryPlan(plan) || present || problem != nil {
		t.Fatalf("absent recovery was not a no-op: %+v / %v / %#v", plan, present, problem)
	}
	if !reflect.DeepEqual(fixture.calls, []string{"ownership", "read:" + datahome.DocumentRecoveryPath}) {
		t.Fatalf("absent recovery read extra state: %v", fixture.calls)
	}
	fixture = recoveryTreeFixture(t, true)
	fixture.owned = false
	if _, present, problem := buildRecoveryPlanOwned(fixture); present || problem == nil || problem.Code != CodeOwnershipRequired || !reflect.DeepEqual(fixture.calls, []string{"ownership"}) {
		t.Fatalf("ownership refusal was not first: %v / %#v / %v", present, problem, fixture.calls)
	}
}

func TestReadLegacyTreeIgnoresEmptyFilesDirectoryPresenceLikeSource(t *testing.T) {
	fixture := recoveryTreeFixture(t, false)
	base := recoveryRelative(recoveryMigrationID) + "/legacy_documents"
	one, problem := readLegacyTree(fixture, base)
	if problem != nil {
		t.Fatal(problem)
	}
	delete(fixture.dirs, base+"/files")
	fixture.dirs[base] = nil
	two, problem := readLegacyTree(fixture, base)
	if problem != nil || !equalLegacyTrees(one, two) {
		t.Fatalf("empty files directory changed logical tree: %+v / %#v", two, problem)
	}
}

func emptyRecoveryPlan(plan RecoveryPlan) bool {
	return plan.Journal == (Journal{}) && plan.Channel == "" && plan.Actions == nil
}
