package documentmigration

import (
	"bytes"
	"context"
	"slices"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/envelope"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const (
	CodeStagingFailed = "DOCUMENT_MIGRATION_STAGING_FAILED"
	stageKeyStateName = "key_state.json"
	stageLedgerName   = "ledger.chdoc"
)

type stagingStore interface {
	recoveryCopyStore
	ReadSingleLinkFile(string, int64) ([]byte, bool, *runtimepaths.Error)
}

type journalObservationStore interface {
	RequireProcessOwnership() *runtimepaths.Error
	ReadSingleLinkFile(string, int64) ([]byte, bool, *runtimepaths.Error)
}

// writeVerifiedStage creates one fresh encrypted candidate from the retained
// validated plaintext. It never advances the journal or changes active data;
// the caller must re-read the legacy source before authorizing that transition.
func writeVerifiedStage(ctx context.Context, store stagingStore, snapshot legacySourceSnapshot,
	plan Plan, current Journal, master keys.MasterKey) *Error {
	if ctx == nil || store == nil || store.RequireProcessOwnership() != nil {
		return fail(CodeOwnershipRequired)
	}
	stateRaw, stateError := master.State.CanonicalBytes()
	if stateError != nil || !validMasterForJournal(current, JournalKeyCreated, master) || !validWriterSnapshot(snapshot, plan) {
		return fail(CodeStagingFailed)
	}
	currentRaw, journalProblem := current.CanonicalBytes()
	if journalProblem != nil || !stagingJournalEquals(store, currentRaw) {
		return fail(CodeRecoveryFailed)
	}
	manifest, manifestProblem := (RecoveryManifest{
		Plan: plan, DocumentsRootPresent: snapshot.DocumentsRootPresent,
	}).CanonicalBytes()
	if manifestProblem != nil {
		return manifestProblem
	}
	if problem := verifyRecoveryCopyTree(ctx, store, current.MigrationID, snapshot, manifest); problem != nil {
		return problem
	}
	stage := stageRelative(current.MigrationID)
	present, pathProblem := store.Exists(stage)
	if pathProblem != nil || present {
		return fail(CodeStagingFailed)
	}
	if problem := ensureStageDirectory(ctx, store, stage); problem != nil {
		return problem
	}
	names, directoryPresent, pathProblem := store.ListDirectoryNames(stage)
	if pathProblem != nil || !directoryPresent || len(names) != 0 {
		return fail(CodeStagingFailed)
	}
	if problem := ensureStageDirectory(ctx, store, stage+"/files"); problem != nil {
		return problem
	}
	if problem := createStageFile(ctx, store, stage+"/"+stageKeyStateName, stateRaw, false); problem != nil {
		return problem
	}
	if snapshot.LedgerPlaintext != nil {
		if problem := encryptStageObject(ctx, store, stage+"/"+stageLedgerName,
			snapshot.LedgerPlaintext, master.Key, envelope.Context{
				DataHomeID: master.State.DataHomeID, KeyID: master.State.ActiveKeyID,
				Purpose: "ledger", ObjectID: "ledger",
			}); problem != nil {
			return problem
		}
	}
	for _, document := range snapshot.Documents {
		if problem := encryptStageObject(ctx, store, stage+"/files/"+document.ID+".chdoc",
			document.Plaintext, master.Key, envelope.Context{
				DataHomeID: master.State.DataHomeID, KeyID: master.State.ActiveKeyID,
				Purpose: "blob", ObjectID: document.ID,
			}); problem != nil {
			return problem
		}
	}
	if problem := verifyStage(ctx, store, stage, snapshot, master); problem != nil {
		return problem
	}
	if !stagingJournalEquals(store, currentRaw) || ctx.Err() != nil || store.RequireProcessOwnership() != nil {
		return fail(CodeRecoveryFailed)
	}
	return nil
}

func validMasterForJournal(current Journal, expected JournalState, master keys.MasterKey) bool {
	stateRaw, stateError := master.State.CanonicalBytes()
	parsedState, parseError := keys.ParseState(stateRaw)
	return stateError == nil && parseError == nil && parsedState == master.State && len(master.Key) == keys.KeyBytes &&
		current.State == expected && validJournalIdentity(current) &&
		*current.DataHomeID == master.State.DataHomeID && *current.KeyID == master.State.ActiveKeyID
}

func ensureStageDirectory(ctx context.Context, store stagingStore, path string) *Error {
	if ctx.Err() != nil || store.RequireProcessOwnership() != nil {
		return fail(CodeStagingFailed)
	}
	if _, pathProblem := store.EnsureDirectory(path); pathProblem != nil {
		return fail(CodeStagingFailed)
	}
	return nil
}

func createStageFile(ctx context.Context, store stagingStore, path string, content []byte, object bool) *Error {
	if ctx.Err() != nil || store.RequireProcessOwnership() != nil {
		return fail(CodeStagingFailed)
	}
	var created bool
	var pathProblem *runtimepaths.Error
	if object {
		created, pathProblem = store.CreateObjectFile(path, content)
	} else {
		created, pathProblem = store.CreateFile(path, content)
	}
	if pathProblem != nil || !created {
		return fail(CodeStagingFailed)
	}
	return nil
}

func encryptStageObject(ctx context.Context, store stagingStore, path string,
	plaintext, key []byte, objectContext envelope.Context) *Error {
	if ctx.Err() != nil {
		return fail(CodeStagingFailed)
	}
	sealed, encryptionError := envelope.Encrypt(plaintext, key, objectContext)
	if encryptionError != nil {
		return fail(CodeStagingFailed)
	}
	problem := createStageFile(ctx, store, path, sealed, true)
	clear(sealed)
	return problem
}

func verifyStage(ctx context.Context, store stagingStore, stage string,
	snapshot legacySourceSnapshot, master keys.MasterKey) *Error {
	if ctx.Err() != nil || store.RequireProcessOwnership() != nil {
		return fail(CodeStagingFailed)
	}
	wantStage := []string{"files", stageKeyStateName}
	if snapshot.LedgerPlaintext != nil {
		wantStage = append(wantStage, stageLedgerName)
	}
	names, present, pathProblem := store.ListDirectoryNames(stage)
	if pathProblem != nil || !present || !slices.Equal(names, wantStage) {
		return fail(CodeStagingFailed)
	}
	wantFiles := make([]string, 0, len(snapshot.Documents))
	for _, document := range snapshot.Documents {
		wantFiles = append(wantFiles, document.ID+".chdoc")
	}
	slices.Sort(wantFiles)
	names, present, pathProblem = store.ListDirectoryFileNames(stage + "/files")
	if pathProblem != nil || !present || !slices.Equal(names, wantFiles) {
		return fail(CodeStagingFailed)
	}
	stateRaw, present, pathProblem := store.ReadFile(stage+"/"+stageKeyStateName, keys.MaxStateBytes)
	state, stateError := keys.ParseState(stateRaw)
	if pathProblem != nil || !present || stateError != nil || state != master.State {
		return fail(CodeStagingFailed)
	}
	if snapshot.LedgerPlaintext != nil && !stageObjectMatches(store, stage+"/"+stageLedgerName,
		snapshot.LedgerPlaintext, master.Key, envelope.Context{
			DataHomeID: master.State.DataHomeID, KeyID: master.State.ActiveKeyID,
			Purpose: "ledger", ObjectID: "ledger",
		}, true) {
		return fail(CodeStagingFailed)
	}
	for _, document := range snapshot.Documents {
		if !stageObjectMatches(store, stage+"/files/"+document.ID+".chdoc",
			document.Plaintext, master.Key, envelope.Context{
				DataHomeID: master.State.DataHomeID, KeyID: master.State.ActiveKeyID,
				Purpose: "blob", ObjectID: document.ID,
			}, false) {
			return fail(CodeStagingFailed)
		}
	}
	if ctx.Err() != nil || store.RequireProcessOwnership() != nil {
		return fail(CodeStagingFailed)
	}
	return nil
}

func stageObjectMatches(store stagingStore, path string, expected, key []byte,
	objectContext envelope.Context, exact bool) bool {
	sealed, present, pathProblem := store.ReadObjectFile(path, envelope.MaxEnvelopeBytes)
	if pathProblem != nil || !present {
		return false
	}
	decrypted, decryptionError := envelope.Decrypt(sealed, key, objectContext)
	clear(sealed)
	if decryptionError != nil {
		return false
	}
	defer clear(decrypted.Plaintext)
	if exact {
		return bytes.Equal(decrypted.Plaintext, expected)
	}
	return len(decrypted.Plaintext) == len(expected) && digest(decrypted.Plaintext) == digest(expected)
}

func stagingJournalEquals(store journalObservationStore, expected []byte) bool {
	if store.RequireProcessOwnership() != nil {
		return false
	}
	observed, present, pathProblem := store.ReadSingleLinkFile(datahome.DocumentRecoveryPath, MaxJournalBytes)
	return pathProblem == nil && present && bytes.Equal(observed, expected) && store.RequireProcessOwnership() == nil
}

var _ stagingStore = (*runtimepaths.Root)(nil)
