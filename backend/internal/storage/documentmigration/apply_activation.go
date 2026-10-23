package documentmigration

import (
	"bytes"
	"context"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const (
	CodeSourceChanged    = "DOCUMENT_MIGRATION_SOURCE_CHANGED"
	CodeActivationFailed = "DOCUMENT_MIGRATION_ACTIVATION_FAILED"
)

type applyMutationStore interface {
	stagingStore
	journalFileStore
	MoveDirectoryNoReplace(string, string) (runtimepaths.DirectoryMoveOutcome, *runtimepaths.Error)
}

// confirmStageSourceAndAdvance repeats the full DPAPI/source validation after
// staging, then advances exactly key_created -> staging_verified. It performs
// no directory or marker mutation.
func confirmStageSourceAndAdvance(ctx context.Context, store applyMutationStore, windows bool,
	unprotect unprotectFunc, snapshot legacySourceSnapshot, plan Plan,
	current Journal, master keys.MasterKey) (Journal, *Error) {
	if ctx == nil || store == nil || store.RequireProcessOwnership() != nil {
		return Journal{}, fail(CodeOwnershipRequired)
	}
	if !validMasterForJournal(current, JournalKeyCreated, master) || !validWriterSnapshot(snapshot, plan) {
		return Journal{}, fail(CodeApplyFailed)
	}
	freshPlan, freshSnapshot, problem := readSnapshotDuringApply(store, windows, unprotect, current)
	if problem != nil {
		return Journal{}, problem
	}
	freshSnapshot.clear()
	if freshPlan != plan {
		return Journal{}, fail(CodeSourceChanged)
	}
	if problem := verifyStage(ctx, store, stageRelative(current.MigrationID), snapshot, master); problem != nil {
		return Journal{}, problem
	}
	next := journalWithState(current, JournalStagingVerified)
	return advanceApplyJournal(ctx, store, current, next)
}

func readSnapshotDuringApply(store applyMutationStore, windows bool, unprotect unprotectFunc,
	current Journal) (Plan, legacySourceSnapshot, *Error) {
	raw, problem := current.CanonicalBytes()
	if problem != nil || !stagingJournalEquals(store, raw) {
		return Plan{}, legacySourceSnapshot{}, fail(CodeRecoveryFailed)
	}
	plan, snapshot, sourceProblem := readSnapshotOwnedWithRecovery(store, windows, unprotect, true)
	if sourceProblem != nil {
		return Plan{}, legacySourceSnapshot{}, sourceProblem
	}
	if !stagingJournalEquals(store, raw) {
		snapshot.clear()
		return Plan{}, legacySourceSnapshot{}, fail(CodeRecoveryFailed)
	}
	return plan, snapshot, nil
}

// swapVerifiedStage moves only the exact rechecked legacy tree into the fixed
// recovery slot and the verified stage into active position. Either committed
// move remains recoverable under staging_verified until its journal advance.
func swapVerifiedStage(ctx context.Context, store applyMutationStore, snapshot legacySourceSnapshot,
	plan Plan, current Journal, master keys.MasterKey) (Journal, *Error) {
	if ctx == nil || store == nil || store.RequireProcessOwnership() != nil {
		return Journal{}, fail(CodeOwnershipRequired)
	}
	if !validMasterForJournal(current, JournalStagingVerified, master) || !validWriterSnapshot(snapshot, plan) {
		return Journal{}, fail(CodeApplyFailed)
	}
	currentRaw, journalProblem := current.CanonicalBytes()
	if journalProblem != nil || !stagingJournalEquals(store, currentRaw) {
		return Journal{}, fail(CodeRecoveryFailed)
	}
	manifest, manifestProblem := (RecoveryManifest{
		Plan: plan, DocumentsRootPresent: snapshot.DocumentsRootPresent,
	}).CanonicalBytes()
	if manifestProblem != nil {
		return Journal{}, manifestProblem
	}
	if problem := verifyRecoveryCopyTree(ctx, store, current.MigrationID, snapshot, manifest); problem != nil {
		return Journal{}, problem
	}
	stage := stageRelative(current.MigrationID)
	if problem := verifyStage(ctx, store, stage, snapshot, master); problem != nil {
		return Journal{}, problem
	}
	base := recoveryRelative(current.MigrationID)
	recoveryTree, recoveryProblem := readLegacyTree(store, base+"/legacy_documents")
	if recoveryProblem != nil {
		return Journal{}, recoveryProblem
	}
	if snapshot.DocumentsRootPresent {
		activeTree, activeProblem := readLegacyTree(store, "documents")
		if activeProblem != nil || !equalLegacyTrees(activeTree, recoveryTree) {
			return Journal{}, fail(CodeSourceChanged)
		}
		if ctx.Err() != nil || store.RequireProcessOwnership() != nil {
			return Journal{}, fail(CodeApplyFailed)
		}
		outcome, pathProblem := store.MoveDirectoryNoReplace("documents", base+"/activation_original")
		if pathProblem != nil || outcome != runtimepaths.DirectoryMoveMoved {
			return Journal{}, fail(CodeApplyFailed)
		}
		originalTree, originalProblem := readLegacyTree(store, base+"/activation_original")
		if originalProblem != nil || !equalLegacyTrees(originalTree, recoveryTree) {
			return Journal{}, fail(CodeRecoveryFailed)
		}
	} else {
		_, activePresent, pathProblem := store.ListDirectoryNames("documents")
		if pathProblem != nil || activePresent {
			return Journal{}, fail(CodeSourceChanged)
		}
	}
	if ctx.Err() != nil || store.RequireProcessOwnership() != nil {
		return Journal{}, fail(CodeApplyFailed)
	}
	outcome, pathProblem := store.MoveDirectoryNoReplace(stage, "documents")
	if pathProblem != nil || outcome != runtimepaths.DirectoryMoveMoved {
		return Journal{}, fail(CodeApplyFailed)
	}
	if problem := verifyStage(ctx, store, "documents", snapshot, master); problem != nil {
		return Journal{}, fail(CodeActivationFailed)
	}
	next := journalWithState(current, JournalDocumentsSwapped)
	return advanceApplyJournal(ctx, store, current, next)
}

// activateMigrationMarker compares and replaces only the exact retained v1
// marker, verifies the v2 bytes, then advances documents_swapped ->
// marker_activated. Active document verification precedes the marker mutation.
func activateMigrationMarker(ctx context.Context, store applyMutationStore, snapshot legacySourceSnapshot,
	plan Plan, current Journal, master keys.MasterKey) (Journal, *Error) {
	if ctx == nil || store == nil || store.RequireProcessOwnership() != nil {
		return Journal{}, fail(CodeOwnershipRequired)
	}
	if !validMasterForJournal(current, JournalDocumentsSwapped, master) || !validWriterSnapshot(snapshot, plan) {
		return Journal{}, fail(CodeApplyFailed)
	}
	currentRaw, journalProblem := current.CanonicalBytes()
	if journalProblem != nil || !stagingJournalEquals(store, currentRaw) {
		return Journal{}, fail(CodeRecoveryFailed)
	}
	if problem := verifyStage(ctx, store, "documents", snapshot, master); problem != nil {
		return Journal{}, fail(CodeActivationFailed)
	}
	legacy, present, pathProblem := store.ReadSingleLinkFile(datahome.MarkerPath, datahome.MaxMarkerBytes)
	if pathProblem != nil || !present || !bytes.Equal(legacy, snapshot.Marker) {
		return Journal{}, fail(CodeSourceChanged)
	}
	active, markerProblem := datahome.MarkerBytes(snapshot.Channel, master.State.DataHomeID)
	if markerProblem != nil || ctx.Err() != nil || store.RequireProcessOwnership() != nil {
		return Journal{}, fail(CodeActivationFailed)
	}
	committed, pathProblem := store.ReplaceFile(datahome.MarkerPath, legacy, active)
	observed, observedPresent, observationProblem := store.ReadSingleLinkFile(datahome.MarkerPath, datahome.MaxMarkerBytes)
	if pathProblem != nil || observationProblem != nil || !committed || !observedPresent || !bytes.Equal(observed, active) {
		return Journal{}, fail(CodeActivationFailed)
	}
	next := journalWithState(current, JournalMarkerActivated)
	return advanceApplyJournal(ctx, store, current, next)
}

func advanceApplyJournal(ctx context.Context, store applyMutationStore, current, next Journal) (Journal, *Error) {
	if ctx.Err() != nil || store.RequireProcessOwnership() != nil || !current.CanAdvanceTo(next) {
		return Journal{}, fail(CodeApplyFailed)
	}
	outcome, problem := advanceJournal(store, current, next)
	if problem != nil || outcome != journalWritten {
		return Journal{}, fail(CodeApplyFailed)
	}
	return next, nil
}

func journalWithState(current Journal, state JournalState) Journal {
	return Journal{
		MigrationID: current.MigrationID, State: state,
		DataHomeID: current.DataHomeID, KeyID: current.KeyID,
	}
}

var _ applyMutationStore = (*runtimepaths.Root)(nil)
