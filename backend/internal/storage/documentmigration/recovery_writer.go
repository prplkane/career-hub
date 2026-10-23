package documentmigration

import (
	"bytes"
	"context"
	"slices"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/ledger"
	"careerhub.local/backend/internal/storage/documents/legacydpapi"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type recoveryCopyStore interface {
	ownedTree
	EnsureDirectory(string) (bool, *runtimepaths.Error)
	CreateFile(string, []byte) (bool, *runtimepaths.Error)
	CreateObjectFile(string, []byte) (bool, *runtimepaths.Error)
}

// writeRecoveryCopy writes only the validated legacy marker and ciphertexts.
// It never writes plaintext, publishes a journal, or removes a partial copy.
// A failure can therefore leave one secret-free, unreferenced recovery folder.
func writeRecoveryCopy(ctx context.Context, store recoveryCopyStore, migrationID string,
	snapshot legacySourceSnapshot, plan Plan) (Journal, *Error) {
	initial := Journal{MigrationID: migrationID, State: JournalRecoveryVerified}
	if ctx == nil || store == nil || store.RequireProcessOwnership() != nil {
		return Journal{}, fail(CodeOwnershipRequired)
	}
	if _, problem := initial.CanonicalBytes(); problem != nil || !validWriterSnapshot(snapshot, plan) {
		return Journal{}, fail(CodeRecoveryFailed)
	}
	journalPresent, pathProblem := store.Exists(datahome.DocumentRecoveryPath)
	if pathProblem != nil || journalPresent {
		return Journal{}, fail(CodeRecoveryFailed)
	}
	base := recoveryRelative(migrationID)
	basePresent, pathProblem := store.Exists(base)
	if pathProblem != nil || basePresent {
		return Journal{}, fail(CodeRecoveryFailed)
	}

	for _, path := range []string{"backups", "backups/document-encryption-migration", base} {
		if problem := ensureRecoveryDirectory(ctx, store, path); problem != nil {
			return Journal{}, problem
		}
	}
	names, present, pathProblem := store.ListDirectoryNames(base)
	if pathProblem != nil || !present || len(names) != 0 {
		return Journal{}, fail(CodeRecoveryFailed)
	}
	if problem := createRecoveryFile(ctx, store, base+"/legacy_marker.json", snapshot.Marker, false); problem != nil {
		return Journal{}, problem
	}
	if !snapshot.DocumentsRootPresent {
		if problem := createRecoveryFile(ctx, store, base+"/legacy_documents_absent", []byte(legacyAbsentMarker), false); problem != nil {
			return Journal{}, problem
		}
	}
	for _, path := range []string{base + "/legacy_documents", base + "/legacy_documents/files"} {
		if problem := ensureRecoveryDirectory(ctx, store, path); problem != nil {
			return Journal{}, problem
		}
	}
	if snapshot.LedgerCiphertext != nil {
		if problem := createRecoveryFile(ctx, store, base+"/legacy_documents/ledger.dpapi", snapshot.LedgerCiphertext, true); problem != nil {
			return Journal{}, problem
		}
	}
	for _, document := range snapshot.Documents {
		path := base + "/legacy_documents/files/" + document.ID + ".dpapi"
		if problem := createRecoveryFile(ctx, store, path, document.Ciphertext, true); problem != nil {
			return Journal{}, problem
		}
	}
	manifest, manifestProblem := (RecoveryManifest{
		Plan: plan, DocumentsRootPresent: snapshot.DocumentsRootPresent,
	}).CanonicalBytes()
	if manifestProblem != nil {
		return Journal{}, manifestProblem
	}
	if problem := createRecoveryFile(ctx, store, base+"/recovery_manifest.json", manifest, false); problem != nil {
		return Journal{}, problem
	}
	if problem := verifyRecoveryCopyTree(ctx, store, migrationID, snapshot, manifest); problem != nil {
		return Journal{}, problem
	}
	journalPresent, pathProblem = store.Exists(datahome.DocumentRecoveryPath)
	if pathProblem != nil || journalPresent || ctx.Err() != nil || store.RequireProcessOwnership() != nil {
		return Journal{}, fail(CodeRecoveryFailed)
	}
	return initial, nil
}

func validWriterSnapshot(snapshot legacySourceSnapshot, plan Plan) bool {
	info, markerProblem := datahome.ParseMarkerForDocumentMigration(snapshot.Marker)
	if markerProblem != nil || info.FormatVersion != SourceFormatVersion || info.Channel != snapshot.Channel {
		return false
	}
	validated := validatedSnapshot{
		MarkerSHA256: digest(snapshot.Marker), DocumentsRootPresent: snapshot.DocumentsRootPresent,
	}
	if !snapshot.DocumentsRootPresent {
		return snapshot.LedgerCiphertext == nil && snapshot.LedgerPlaintext == nil &&
			len(snapshot.Documents) == 0 && planForValidatedSnapshot(validated) == plan
	}
	if snapshot.LedgerCiphertext == nil || snapshot.LedgerPlaintext == nil {
		return snapshot.LedgerCiphertext == nil && snapshot.LedgerPlaintext == nil &&
			len(snapshot.Documents) == 0 && planForValidatedSnapshot(validated) == plan
	}
	if !validCiphertext(snapshot.LedgerCiphertext) {
		return false
	}
	records, parseProblem := ledger.Parse(snapshot.LedgerPlaintext)
	if parseProblem != nil || len(records) != len(snapshot.Documents) {
		return false
	}
	ledgerCiphertext := observedCiphertext(snapshot.LedgerCiphertext)
	validated.Ledger = &ledgerCiphertext
	validated.Documents = make([]validatedDocument, 0, len(snapshot.Documents))
	for index, document := range snapshot.Documents {
		record := records[index]
		if document.ID != record.ID || !validCiphertext(document.Ciphertext) ||
			len(document.Plaintext) != record.SizeBytes || digest(document.Plaintext) != record.SHA256 {
			return false
		}
		validated.Documents = append(validated.Documents, validatedDocument{
			ID: document.ID, Ciphertext: observedCiphertext(document.Ciphertext),
		})
	}
	return planForValidatedSnapshot(validated) == plan
}

func ensureRecoveryDirectory(ctx context.Context, store recoveryCopyStore, path string) *Error {
	if ctx.Err() != nil || store.RequireProcessOwnership() != nil {
		return fail(CodeRecoveryFailed)
	}
	if _, pathProblem := store.EnsureDirectory(path); pathProblem != nil {
		return fail(CodeRecoveryFailed)
	}
	return nil
}

func createRecoveryFile(ctx context.Context, store recoveryCopyStore, path string, content []byte, object bool) *Error {
	if ctx.Err() != nil || store.RequireProcessOwnership() != nil {
		return fail(CodeRecoveryFailed)
	}
	var created bool
	var pathProblem *runtimepaths.Error
	if object {
		created, pathProblem = store.CreateObjectFile(path, content)
	} else {
		created, pathProblem = store.CreateFile(path, content)
	}
	if pathProblem != nil || !created {
		return fail(CodeRecoveryFailed)
	}
	return nil
}

func verifyRecoveryCopyTree(ctx context.Context, store recoveryCopyStore, migrationID string,
	snapshot legacySourceSnapshot, manifest []byte) *Error {
	return verifyRecoveryCopyTreeLayout(ctx, store, migrationID, snapshot, manifest, false)
}

func verifyRecoveryCopyTreeAfterSwap(ctx context.Context, store recoveryCopyStore, migrationID string,
	snapshot legacySourceSnapshot, manifest []byte) *Error {
	return verifyRecoveryCopyTreeLayout(ctx, store, migrationID, snapshot, manifest, snapshot.DocumentsRootPresent)
}

func verifyRecoveryCopyTreeLayout(ctx context.Context, store recoveryCopyStore, migrationID string,
	snapshot legacySourceSnapshot, manifest []byte, activationOriginal bool) *Error {
	if ctx.Err() != nil || store.RequireProcessOwnership() != nil {
		return fail(CodeRecoveryFailed)
	}
	base := recoveryRelative(migrationID)
	wantBase := []string{"legacy_documents", "legacy_marker.json", "recovery_manifest.json"}
	if !snapshot.DocumentsRootPresent {
		wantBase = append(wantBase, "legacy_documents_absent")
	}
	if activationOriginal {
		wantBase = append(wantBase, "activation_original")
	}
	slices.Sort(wantBase)
	names, present, pathProblem := store.ListDirectoryNames(base)
	if pathProblem != nil || !present || !slices.Equal(names, wantBase) {
		return fail(CodeRecoveryFailed)
	}
	wantLegacy := []string{"files"}
	if snapshot.LedgerCiphertext != nil {
		wantLegacy = append(wantLegacy, "ledger.dpapi")
	}
	names, present, pathProblem = store.ListDirectoryNames(base + "/legacy_documents")
	if pathProblem != nil || !present || !slices.Equal(names, wantLegacy) {
		return fail(CodeRecoveryFailed)
	}
	wantFiles := make([]string, 0, len(snapshot.Documents))
	for _, document := range snapshot.Documents {
		wantFiles = append(wantFiles, document.ID+".dpapi")
	}
	slices.Sort(wantFiles)
	names, present, pathProblem = store.ListDirectoryFileNames(base + "/legacy_documents/files")
	if pathProblem != nil || !present || !slices.Equal(names, wantFiles) {
		return fail(CodeRecoveryFailed)
	}
	if !recoveryFileEquals(store, base+"/legacy_marker.json", snapshot.Marker, false) ||
		!recoveryFileEquals(store, base+"/recovery_manifest.json", manifest, false) {
		return fail(CodeRecoveryFailed)
	}
	if !snapshot.DocumentsRootPresent &&
		!recoveryFileEquals(store, base+"/legacy_documents_absent", []byte(legacyAbsentMarker), false) {
		return fail(CodeRecoveryFailed)
	}
	if snapshot.LedgerCiphertext != nil &&
		!recoveryFileEquals(store, base+"/legacy_documents/ledger.dpapi", snapshot.LedgerCiphertext, true) {
		return fail(CodeRecoveryFailed)
	}
	for _, document := range snapshot.Documents {
		path := base + "/legacy_documents/files/" + document.ID + ".dpapi"
		if !recoveryFileEquals(store, path, document.Ciphertext, true) {
			return fail(CodeRecoveryFailed)
		}
	}
	if ctx.Err() != nil || store.RequireProcessOwnership() != nil {
		return fail(CodeRecoveryFailed)
	}
	return nil
}

func recoveryFileEquals(store recoveryCopyStore, path string, expected []byte, object bool) bool {
	var content []byte
	var present bool
	var pathProblem *runtimepaths.Error
	if object {
		content, present, pathProblem = store.ReadObjectFile(path, legacydpapi.MaxCiphertextBytes)
	} else {
		content, present, pathProblem = store.ReadFile(path, int64(len(expected)))
	}
	return pathProblem == nil && present && bytes.Equal(content, expected)
}

var _ recoveryCopyStore = (*runtimepaths.Root)(nil)
