package documentmigration

import (
	"bytes"
	"context"
	"slices"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/documents/envelope"
	"careerhub.local/backend/internal/documents/keys"
	"careerhub.local/backend/internal/documents/ledger"
	"careerhub.local/backend/internal/runtimepaths"
)

type completionStore interface {
	ownedTree
	journalFileStore
}

// completeActivatedMigration verifies the active v2 candidate and retained v1
// recovery facts before publishing completed.json. Cancellation after that
// publication deliberately leaves the live journal for the explicit finalizer.
func completeActivatedMigration(ctx context.Context, store applyMutationStore,
	snapshot legacySourceSnapshot, plan Plan, current Journal,
	master keys.MasterKey) (CompletionRecord, *Error) {
	if ctx == nil || store == nil || store.RequireProcessOwnership() != nil {
		return CompletionRecord{}, fail(CodeOwnershipRequired)
	}
	if !validMasterForJournal(current, JournalMarkerActivated, master) || !validWriterSnapshot(snapshot, plan) {
		return CompletionRecord{}, fail(CodeApplyFailed)
	}
	currentRaw, journalProblem := current.CanonicalBytes()
	if journalProblem != nil || !stagingJournalEquals(store, currentRaw) {
		return CompletionRecord{}, fail(CodeRecoveryFailed)
	}
	activeMarker, markerProblem := datahome.MarkerBytes(snapshot.Channel, master.State.DataHomeID)
	if markerProblem != nil || !singleLinkFileEquals(store, datahome.MarkerPath, activeMarker, datahome.MaxMarkerBytes) {
		return CompletionRecord{}, fail(CodeActivationFailed)
	}
	if problem := verifyStage(ctx, store, "documents", snapshot, master); problem != nil {
		return CompletionRecord{}, fail(CodeActivationFailed)
	}
	manifest, manifestProblem := (RecoveryManifest{
		Plan: plan, DocumentsRootPresent: snapshot.DocumentsRootPresent,
	}).CanonicalBytes()
	if manifestProblem != nil {
		return CompletionRecord{}, manifestProblem
	}
	if problem := verifyRecoveryCopyTreeAfterSwap(ctx, store, current.MigrationID, snapshot, manifest); problem != nil {
		return CompletionRecord{}, problem
	}
	if problem := verifySuccessfulSwapLayout(store, current.MigrationID, snapshot.DocumentsRootPresent); problem != nil {
		return CompletionRecord{}, problem
	}
	completed := CompletionRecord{
		PlanID: plan.PlanID, MigrationID: current.MigrationID, DocumentCount: plan.DocumentCount,
	}
	if problem := publishCompletion(ctx, store, completed); problem != nil {
		return CompletionRecord{}, problem
	}
	if ctx.Err() != nil {
		return CompletionRecord{}, fail(CodeApplyFailed)
	}
	outcome, removalProblem := removeJournal(store, current)
	if removalProblem != nil || outcome != journalRemoved {
		return CompletionRecord{}, fail(CodeApplyFailed)
	}
	return completed, nil
}

func verifySuccessfulSwapLayout(store completionStore, migrationID string, documentsOriginallyPresent bool) *Error {
	base := recoveryRelative(migrationID)
	recoveryTree, problem := readLegacyTree(store, base+"/legacy_documents")
	if problem != nil {
		return problem
	}
	_, originalPresent, pathProblem := store.ListDirectoryNames(base + "/activation_original")
	if pathProblem != nil || originalPresent != documentsOriginallyPresent {
		return fail(CodeRecoveryFailed)
	}
	if originalPresent {
		originalTree, originalProblem := readLegacyTree(store, base+"/activation_original")
		if originalProblem != nil || !equalLegacyTrees(originalTree, recoveryTree) {
			return fail(CodeRecoveryFailed)
		}
	}
	_, stagePresent, pathProblem := store.ListDirectoryNames(stageRelative(migrationID))
	if pathProblem != nil || stagePresent {
		return fail(CodeRecoveryFailed)
	}
	for _, path := range []string{base + "/interrupted_candidate", base + "/interrupted_staging"} {
		present, pathProblem := store.Exists(path)
		if pathProblem != nil || present {
			return fail(CodeRecoveryFailed)
		}
	}
	return nil
}

func publishCompletion(ctx context.Context, store completionStore, completed CompletionRecord) *Error {
	raw, problem := completed.CanonicalBytes()
	if problem != nil {
		return problem
	}
	path := recoveryRelative(completed.MigrationID) + "/completed.json"
	observed, present, pathProblem := store.ReadSingleLinkFile(path, MaxJournalBytes)
	if pathProblem != nil {
		return fail(CodeApplyFailed)
	}
	if present {
		if bytes.Equal(observed, raw) {
			return nil
		}
		return fail(CodeApplyFailed)
	}
	if ctx.Err() != nil || store.RequireProcessOwnership() != nil {
		return fail(CodeApplyFailed)
	}
	created, pathProblem := store.CreateFile(path, raw)
	observed, present, observationProblem := store.ReadSingleLinkFile(path, MaxJournalBytes)
	if pathProblem != nil || observationProblem != nil || !created || !present || !bytes.Equal(observed, raw) {
		return fail(CodeApplyFailed)
	}
	return nil
}

// finalizeCompletedMigration validates a completion-bound active v2 tree and
// removes only the exact marker_activated journal. It never rolls back data or
// discards the published key. Absence of completed.json is a successful no-op.
func finalizeCompletedMigration(ctx context.Context, store completionStore,
	backend keys.Backend) (bool, *Error) {
	if ctx == nil || store == nil || store.RequireProcessOwnership() != nil {
		return false, fail(CodeOwnershipRequired)
	}
	journalRaw, present, pathProblem := store.ReadSingleLinkFile(datahome.DocumentRecoveryPath, MaxJournalBytes)
	if pathProblem != nil {
		return false, fail(CodeRecoveryFailed)
	}
	if !present {
		return false, nil
	}
	journal, journalProblem := ParseJournal(journalRaw)
	if journalProblem != nil {
		return false, nil // The ordinary recovery planner reports the invalid journal.
	}
	completedPath := recoveryRelative(journal.MigrationID) + "/completed.json"
	completedRaw, completedPresent, pathProblem := store.ReadSingleLinkFile(completedPath, MaxJournalBytes)
	if pathProblem != nil {
		return false, fail(CodeRecoveryFailed)
	}
	if !completedPresent {
		return false, nil
	}
	if journal.State != JournalMarkerActivated || !validJournalIdentity(journal) {
		return false, fail(CodeRecoveryFailed)
	}
	completed, completedProblem := ParseCompletionRecord(completedRaw)
	if completedProblem != nil || completed.MigrationID != journal.MigrationID {
		return false, fail(CodeRecoveryFailed)
	}
	manifest, channel, factsProblem := readCompletedRecoveryFacts(store, journal)
	if factsProblem != nil || completed.PlanID != manifest.Plan.PlanID ||
		completed.DocumentCount != manifest.Plan.DocumentCount {
		return false, fail(CodeRecoveryFailed)
	}
	activeMarker, markerProblem := datahome.MarkerBytes(channel, *journal.DataHomeID)
	if markerProblem != nil || !singleLinkFileEquals(store, datahome.MarkerPath, activeMarker, datahome.MaxMarkerBytes) {
		return false, fail(CodeRecoveryFailed)
	}
	stateRaw, statePresent, pathProblem := store.ReadSingleLinkFile("documents/"+stageKeyStateName, keys.MaxStateBytes)
	state, stateProblem := keys.ParseState(stateRaw)
	if pathProblem != nil || !statePresent || stateProblem != nil ||
		state.DataHomeID != *journal.DataHomeID || state.ActiveKeyID != *journal.KeyID {
		return false, fail(CodeRecoveryFailed)
	}
	manager, managerProblem := keys.NewManager(channel, backend, nil)
	if managerProblem != nil {
		return false, fail(CodeRecoveryFailed)
	}
	master, loadProblem := manager.Load(state)
	if loadProblem != nil {
		return false, fail(CodeRecoveryFailed)
	}
	defer clear(master.Key)
	documentCount, activeProblem := verifySelfContainedActiveTree(ctx, store, state, master.Key)
	if activeProblem != nil || documentCount != completed.DocumentCount {
		return false, fail(CodeRecoveryFailed)
	}
	if !stagingJournalEquals(store, journalRaw) || ctx.Err() != nil {
		return false, fail(CodeRecoveryFailed)
	}
	outcome, removalProblem := removeJournal(store, journal)
	if removalProblem != nil || outcome != journalRemoved {
		return false, fail(CodeRecoveryFailed)
	}
	return true, nil
}

func readCompletedRecoveryFacts(store completionStore, journal Journal) (RecoveryManifest, string, *Error) {
	base := recoveryRelative(journal.MigrationID)
	marker, present, pathProblem := store.ReadFile(base+"/legacy_marker.json", datahome.MaxMarkerBytes)
	if pathProblem != nil || !present {
		return RecoveryManifest{}, "", fail(CodeRecoveryFailed)
	}
	markerInfo, markerProblem := datahome.ParseMarkerForDocumentMigration(marker)
	if markerProblem != nil || markerInfo.FormatVersion != SourceFormatVersion {
		return RecoveryManifest{}, "", fail(CodeRecoveryFailed)
	}
	manifestRaw, present, pathProblem := store.ReadFile(base+"/recovery_manifest.json", MaxJournalBytes)
	if pathProblem != nil || !present {
		return RecoveryManifest{}, "", fail(CodeRecoveryFailed)
	}
	manifest, manifestProblem := ParseRecoveryManifest(manifestRaw)
	if manifestProblem != nil {
		return RecoveryManifest{}, "", manifestProblem
	}
	absentRaw, absentPresent, pathProblem := store.ReadFile(base+"/legacy_documents_absent", int64(len(legacyAbsentMarker)))
	if pathProblem != nil || absentPresent && string(absentRaw) != legacyAbsentMarker ||
		absentPresent == manifest.DocumentsRootPresent {
		return RecoveryManifest{}, "", fail(CodeRecoveryFailed)
	}
	recoveryTree, treeProblem := readLegacyTree(store, base+"/legacy_documents")
	if treeProblem != nil || !manifestMatchesTree(manifest, recoveryTree) {
		return RecoveryManifest{}, "", fail(CodeRecoveryFailed)
	}
	if layoutProblem := verifySuccessfulSwapLayout(store, journal.MigrationID, manifest.DocumentsRootPresent); layoutProblem != nil {
		return RecoveryManifest{}, "", layoutProblem
	}
	return manifest, markerInfo.Channel, nil
}

func verifySelfContainedActiveTree(ctx context.Context, store completionStore,
	state keys.State, key []byte) (int, *Error) {
	if ctx.Err() != nil || store.RequireProcessOwnership() != nil {
		return 0, fail(CodeRecoveryFailed)
	}
	stateRaw, stateError := state.CanonicalBytes()
	if stateError != nil || !singleLinkFileEquals(store, "documents/"+stageKeyStateName, stateRaw, keys.MaxStateBytes) {
		return 0, fail(CodeRecoveryFailed)
	}
	names, present, pathProblem := store.ListDirectoryNames("documents")
	if pathProblem != nil || !present {
		return 0, fail(CodeRecoveryFailed)
	}
	ledgerPresent := false
	for _, name := range names {
		switch name {
		case "files", stageKeyStateName:
		case stageLedgerName:
			ledgerPresent = true
		default:
			return 0, fail(CodeRecoveryFailed)
		}
	}
	if !slices.Contains(names, "files") || !slices.Contains(names, stageKeyStateName) {
		return 0, fail(CodeRecoveryFailed)
	}
	fileNames, filesPresent, pathProblem := store.ListDirectoryFileNames("documents/files")
	if pathProblem != nil || !filesPresent {
		return 0, fail(CodeRecoveryFailed)
	}
	if !ledgerPresent {
		if len(fileNames) != 0 {
			return 0, fail(CodeRecoveryFailed)
		}
		if !stableActiveMembership(store, names, fileNames, stateRaw) {
			return 0, fail(CodeRecoveryFailed)
		}
		return 0, nil
	}
	ledgerEnvelope, present, pathProblem := store.ReadObjectFile("documents/"+stageLedgerName, envelope.MaxEnvelopeBytes)
	if pathProblem != nil || !present {
		return 0, fail(CodeRecoveryFailed)
	}
	decryptedLedger, decryptionError := envelope.Decrypt(ledgerEnvelope, key, envelope.Context{
		DataHomeID: state.DataHomeID, KeyID: state.ActiveKeyID, Purpose: "ledger", ObjectID: "ledger",
	})
	clear(ledgerEnvelope)
	if decryptionError != nil {
		return 0, fail(CodeRecoveryFailed)
	}
	records, ledgerProblem := ledger.Parse(decryptedLedger.Plaintext)
	clear(decryptedLedger.Plaintext)
	if ledgerProblem != nil || len(records) != len(fileNames) {
		return 0, fail(CodeRecoveryFailed)
	}
	wantFiles := make([]string, 0, len(records))
	for _, record := range records {
		wantFiles = append(wantFiles, record.ID+".chdoc")
	}
	slices.Sort(wantFiles)
	if !slices.Equal(fileNames, wantFiles) {
		return 0, fail(CodeRecoveryFailed)
	}
	for _, record := range records {
		sealed, found, readProblem := store.ReadObjectFile("documents/files/"+record.ID+".chdoc", envelope.MaxEnvelopeBytes)
		if readProblem != nil || !found {
			return 0, fail(CodeRecoveryFailed)
		}
		decrypted, decryptProblem := envelope.Decrypt(sealed, key, envelope.Context{
			DataHomeID: state.DataHomeID, KeyID: state.ActiveKeyID, Purpose: "blob", ObjectID: record.ID,
		})
		clear(sealed)
		if decryptProblem != nil {
			return 0, fail(CodeRecoveryFailed)
		}
		valid := len(decrypted.Plaintext) == record.SizeBytes && digest(decrypted.Plaintext) == record.SHA256
		clear(decrypted.Plaintext)
		if !valid {
			return 0, fail(CodeRecoveryFailed)
		}
	}
	if !stableActiveMembership(store, names, fileNames, stateRaw) {
		return 0, fail(CodeRecoveryFailed)
	}
	return len(records), nil
}

func stableActiveMembership(store completionStore, names, fileNames []string, stateRaw []byte) bool {
	afterNames, afterPresent, pathProblem := store.ListDirectoryNames("documents")
	afterFiles, afterFilesPresent, filesProblem := store.ListDirectoryFileNames("documents/files")
	return pathProblem == nil && filesProblem == nil && afterPresent && afterFilesPresent &&
		slices.Equal(names, afterNames) && slices.Equal(fileNames, afterFiles) &&
		singleLinkFileEquals(store, "documents/"+stageKeyStateName, stateRaw, keys.MaxStateBytes) &&
		store.RequireProcessOwnership() == nil
}

func singleLinkFileEquals(store completionStore, path string, expected []byte, limit int64) bool {
	observed, present, problem := store.ReadSingleLinkFile(path, limit)
	return problem == nil && present && bytes.Equal(observed, expected)
}

var _ applyMutationStore = (*runtimepaths.Root)(nil)
var _ completionStore = (*runtimepaths.Root)(nil)
