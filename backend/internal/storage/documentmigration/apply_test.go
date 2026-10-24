package documentmigration

import (
	"bytes"
	"context"
	"testing"

	"careerhub.local/backend/internal/storage/datahome"
)

func applyFixture(t *testing.T) (*fakeRecoveryCopyStore, Plan) {
	t.Helper()
	source := sourceFixture(t)
	plan, problem := validateSourceBytes(source, unprotect)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	store := newFakeRecoveryCopyStore()
	store.files[datahome.MarkerPath] = bytes.Clone(source.Marker)
	store.files[legacyLedgerPath] = bytes.Clone(source.LedgerCiphertext)
	store.files[legacyFilesPath+"/"+sourceDocumentID+".dpapi"] = bytes.Clone(source.Files[sourceDocumentID+".dpapi"])
	store.dirs["documents"] = []string{"files", "ledger.dpapi"}
	store.dirs[legacyFilesPath] = []string{sourceDocumentID + ".dpapi"}
	return store, plan
}

func deterministicApplyRandom() (applyRandom, *int) {
	calls := 0
	random := func(size int) ([]byte, error) {
		calls++
		return bytes.Repeat([]byte{byte(calls * 0x11)}, size), nil
	}
	return random, &calls
}

func TestApplyOwnedCompletesExactReviewedPlan(t *testing.T) {
	store, plan := applyFixture(t)
	random, _ := deterministicApplyRandom()
	trace := []string{}
	backend := &migrationKeyBackend{values: map[string][]byte{}, trace: &trace}

	result, problem := applyOwned(context.Background(), store, backend, random, unprotect, true, plan.PlanID)
	if problem != nil || result.Plan != plan {
		t.Fatalf("apply failed: %+v / %#v", result, problem)
	}
	if _, present := store.files[datahome.DocumentRecoveryPath]; present {
		t.Fatal("completed apply retained the live journal")
	}
	marker, markerProblem := datahome.ParseMarkerForDocumentMigration(store.files[datahome.MarkerPath])
	if markerProblem != nil || marker.FormatVersion != TargetFormatVersion || len(backend.values) != 1 {
		t.Fatalf("apply did not publish one usable v2 identity: %#v / %d", markerProblem, len(backend.values))
	}
	base := recoveryRelative(recoveryMigrationID)
	if _, present := store.files[base+"/completed.json"]; !present {
		t.Fatal("completed apply did not retain its completion proof")
	}
	if _, present := store.dirs[base+"/activation_original"]; !present {
		t.Fatal("completed apply did not retain the exact legacy original")
	}
}

func TestApplyOwnedPlanMismatchHasNoSideEffects(t *testing.T) {
	store, _ := applyFixture(t)
	random, calls := deterministicApplyRandom()
	trace := []string{}
	backend := &migrationKeyBackend{values: map[string][]byte{}, trace: &trace}

	result, problem := applyOwned(context.Background(), store, backend, random, unprotect, true, "different-plan")
	if result != (ApplyResult{}) || problem == nil || problem.Code != CodePlanMismatch {
		t.Fatalf("plan mismatch was not rejected: %+v / %#v", result, problem)
	}
	if *calls != 0 || len(backend.values) != 0 {
		t.Fatal("plan mismatch consumed entropy or touched credential storage")
	}
	if _, present := store.dirs["backups/document-encryption-migration"]; present {
		t.Fatal("plan mismatch created recovery state")
	}
	if !bytes.Equal(store.files[datahome.MarkerPath], sourceMarker(1)) {
		t.Fatal("plan mismatch changed the marker")
	}
}

func TestApplyOwnedFailureAfterFirstSwapRecoversItsOwnJournal(t *testing.T) {
	store, plan := applyFixture(t)
	random, _ := deterministicApplyRandom()
	trace := []string{}
	backend := &migrationKeyBackend{values: map[string][]byte{}, trace: &trace}
	ctx, cancel := context.WithCancel(context.Background())
	moves := 0
	store.afterMove = func() {
		moves++
		if moves == 1 {
			cancel()
		}
	}

	result, problem := applyOwned(ctx, store, backend, random, unprotect, true, plan.PlanID)
	if result != (ApplyResult{}) || problem == nil || problem.Code != CodeApplyFailed {
		t.Fatalf("interrupted apply returned an unsafe result: %+v / %#v", result, problem)
	}
	if _, present := store.files[datahome.DocumentRecoveryPath]; present || len(backend.values) != 0 {
		t.Fatal("owned recovery retained a live journal or orphaned key")
	}
	if !bytes.Equal(store.files[datahome.MarkerPath], sourceMarker(1)) {
		t.Fatal("owned recovery did not preserve the v1 marker")
	}
	active, activeProblem := readLegacyTree(store, "documents")
	base := recoveryRelative(recoveryMigrationID)
	recovery, recoveryProblem := readLegacyTree(store, base+"/legacy_documents")
	if activeProblem != nil || recoveryProblem != nil || !equalLegacyTrees(active, recovery) {
		t.Fatal("owned recovery did not restore the exact legacy tree")
	}
	if _, present := store.files[base+"/recovered_journal.json"]; !present {
		t.Fatal("owned recovery did not retain its recovery proof")
	}
}
