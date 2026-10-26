package homebackup

import (
	"context"
	"slices"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type RestoreRequest struct {
	DataRoot              string
	Channel               string
	ArchiveRelativePath   string
	ConfirmWritersStopped bool
}

type RecoverRequest struct {
	DataRoot              string
	Channel               string
	ConfirmWritersStopped bool
}

type RestoreValidationPhase struct {
	SnapshotRelativePath string
	Preparation          snapshotPreparation
	Summary              *PayloadValidation
	CleanupFiles         int
	Cleanup              SnapshotCleanupResult
	cleanup              *validationCleanupWitness
}

// RestoreResult retains stage/journal/root/commit facts even on failure. Complete
// is inside Finish and must never be inferred from root moves or a receipt.
// These are internal-domain facts, not an unrestricted renderer/HTTP response.
type RestoreResult struct {
	RestoreID            string
	StagingRelativePath  string
	RecoveryRelativePath string
	InitialValidation    RestoreValidationPhase
	Extracted            snapshotPreparation
	StagedValidation     *PayloadValidation
	ArchiveValidation    RestoreValidationPhase
	Journal              restoreJournalPublication
	Roots                restoreRootResult
	FinalValidation      RestoreValidationPhase
	Finish               restoreFinishResult
}

type restoreOperations struct {
	newID      func() (string, error)
	checkpoint func(string) *Error
	roots      restoreRootOperations
	finish     restoreFinishOperations
	cleanup    snapshotCleanupOperations
}

// RestoreOwned runs only in an already-owned dedicated maintenance process with
// managed and legacy writers stopped. It neither claims ownership nor starts a
// listener, schema migration, email sync or key creation. A failure after intent
// publication leaves recovery stopped and evidence retained, never auto-rolls
// back or resumes ambiguous writes. RecoverOwned is the separate explicit path.
// No installed desktop/CLI route is enabled merely by adding this domain owner.
func RestoreOwned(ctx context.Context, request RestoreRequest, backend keys.Backend) (RestoreResult, *Error) {
	return restoreOwned(ctx, request, backend, restoreOperations{newID: newBackupID})
}

func RecoverOwned(ctx context.Context, request RecoverRequest, backend keys.Backend) (RestoreResult, *Error) {
	return recoverOwned(ctx, request, backend, restoreOperations{newID: newBackupID})
}

// UnstartedRestoreResult records known absence of effects before owner dispatch.
// Never use it to replace a lost/unknown result after dispatch or a panic.
func UnstartedRestoreResult() RestoreResult {
	stream := runtimepaths.StreamPublication{Outcome: runtimepaths.FileNotPublished, CleanupConfirmed: true}
	stage := snapshotPreparation{Namespace: runtimepaths.DirectoryNotCreated, LastFile: stream}
	validation := RestoreValidationPhase{Preparation: stage, Cleanup: unstartedSnapshotCleanup()}
	return RestoreResult{
		InitialValidation: validation, ArchiveValidation: validation, Extracted: stage,
		Journal:         restoreJournalPublication{Namespace: runtimepaths.DirectoryNotCreated, LastEvidence: stream, Journal: stream},
		Roots:           restoreRootResult{LastMove: runtimepaths.DirectoryMoveNotMoved},
		FinalValidation: validation,
		Finish:          restoreFinishResult{MarkerPublication: runtimepaths.FileNotPublished, Receipt: stream, JournalRemoval: restoreNotRemoved},
	}
}

func restorePreconditions(ctx context.Context, channel string, confirmed bool) *Error {
	if ctx == nil || ctx.Err() != nil {
		return failure("BACKUP_CANCELED")
	}
	if !confirmed {
		return failure("BACKUP_WRITER_CONFIRMATION_REQUIRED")
	}
	if channel != "dev" && channel != "stable" {
		return failure("BACKUP_CHANNEL_INVALID")
	}
	return nil
}

func restoreCheckpoint(ctx context.Context, ops restoreOperations, name string) *Error {
	if ops.checkpoint != nil {
		if p := ops.checkpoint(name); p != nil {
			return p
		}
	}
	if ctx.Err() != nil {
		return failure("BACKUP_CANCELED")
	}
	return nil
}

func runRestoreValidation(ctx context.Context, dataRoot string, home datahome.Info, backend keys.Backend, ops restoreOperations, phase *RestoreValidationPhase) (restoreValidationResult, *Error) {
	id, err := ops.newID()
	if err != nil || !lowerHex(id, 32) {
		return restoreValidationResult{}, failure("BACKUP_ID_UNAVAILABLE")
	}
	phase.SnapshotRelativePath = snapshotPrefix + id
	validated, p := validateRestoreCopy(ctx, dataRoot, phase.SnapshotRelativePath, home, backend)
	phase.Preparation, phase.Summary = validated.Preparation, validated.Validation
	phase.cleanup = validated.cleanup
	if validated.cleanup != nil {
		phase.CleanupFiles = len(validated.cleanup.files)
	}
	return validated, p
}

func restoreOwned(ctx context.Context, request RestoreRequest, backend keys.Backend, ops restoreOperations) (result RestoreResult, problem *Error) {
	result = UnstartedRestoreResult()
	if p := restorePreconditions(ctx, request.Channel, request.ConfirmWritersStopped); p != nil {
		return result, p
	}
	if !validArchiveDestination(request.ArchiveRelativePath) {
		return result, failure("BACKUP_SOURCE_UNAVAILABLE")
	}
	root, q := runtimepaths.Open(request.DataRoot)
	if q != nil {
		return result, failure("BACKUP_SOURCE_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			problem = failure("BACKUP_SOURCE_CLOSE_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return result, failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	home, admission := datahome.Inspect(request.DataRoot, &request.Channel)
	if admission != nil {
		return result, failure(admission.Code)
	}
	initial, p := runRestoreValidation(ctx, request.DataRoot, *home, backend, ops, &result.InitialValidation)
	if p != nil {
		return result, p
	}
	if p := restoreCheckpoint(ctx, ops, "after_initial_validation"); p != nil {
		return result, p
	}
	id, err := ops.newID()
	if err != nil || !lowerHex(id, 32) {
		return result, failure("BACKUP_ID_UNAVAILABLE")
	}
	result.RestoreID, result.StagingRelativePath = id, restorePrefix+id
	var candidate Manifest
	result.Extracted, p = withExtractedRestore(ctx, root, request.ArchiveRelativePath, result.StagingRelativePath, func(manifest Manifest) *Error {
		if manifest.DataHomeChannel != request.Channel {
			return failure("BACKUP_CHANNEL_INVALID")
		}
		var p *Error
		result.StagedValidation, p = validateSnapshot(ctx, request.DataRoot, result.StagingRelativePath, manifest, backend, func(source payloadSource, dataRoot string, expected *int64) (*int64, int, *Error) {
			if _, present := source.files[databasePath]; !present {
				return validatePayloadDatabase(source, dataRoot, expected)
			}
			id, err := ops.newID()
			if err != nil || !lowerHex(id, 32) {
				return nil, 0, failure("BACKUP_ID_UNAVAILABLE")
			}
			result.ArchiveValidation.SnapshotRelativePath = snapshotPrefix + id
			version, count, preparation, witness, problem := validateArchiveDatabase(source, dataRoot, result.ArchiveValidation.SnapshotRelativePath, expected)
			result.ArchiveValidation.Preparation = preparation
			result.ArchiveValidation.cleanup = witness
			if witness != nil {
				result.ArchiveValidation.CleanupFiles = len(witness.files)
			}
			return version, count, problem
		})
		if p == nil {
			result.ArchiveValidation.Summary = result.StagedValidation
			candidate = manifest
		}
		return p
	})
	if p != nil {
		return result, p
	}
	// Extraction/semantic handles are closed before any intent or root movement.
	if p := restoreCheckpoint(ctx, ops, "after_extraction"); p != nil {
		return result, p
	}
	current, admission := datahome.Inspect(request.DataRoot, &request.Channel)
	if admission != nil || *current != *home {
		return result, failure("BACKUP_RESTORE_HOME_CHANGED")
	}
	// No SQLite handles remain. Bind the original quiescent physical bytes,
	// refusing even newly appearing roots or same-size edits since validation.
	original, presentRoots, p := captureRestoreOriginal(ctx, root, *home, initial.Physical.SQLiteSchemaVersion)
	if p != nil {
		return result, p
	}
	if !slices.Equal(original.Files, initial.Physical.Files) || !slices.Equal(presentRoots, initial.PresentRoots) {
		return result, failure("BACKUP_SOURCE_CHANGED")
	}
	marker, present, q := root.ReadObjectFile(datahome.MarkerPath, datahome.MaxMarkerBytes)
	if q != nil || !present {
		return result, failure("BACKUP_HOME_UNAVAILABLE")
	}
	scope, q := root.CredentialScope()
	if q != nil {
		return result, failure("BACKUP_RESTORE_SCOPE_UNAVAILABLE")
	}
	active := map[string]bool{}
	for _, name := range restoreManifestRoots(candidate) {
		active[name] = slices.Contains(initial.PresentRoots, name)
	}
	j, evidence, p := makeRestoreJournal(id, scope.Fingerprint(), marker, original, candidate, active)
	if p != nil {
		return result, p
	}
	result.RecoveryRelativePath = j.recoveryPath()
	// Extraction has released its namespace pin; recheck bound candidate bytes
	// and their future path limits before publishing intent, not only on moving.
	for _, item := range j.Roots {
		activePath := restoreRootPath(j, item.Name, restoreActive)
		retainedPath := restoreRootPath(j, item.Name, restoreCandidate)
		if p := verifyRestoreTree(ctx, root, j.stagePath()+"/"+item.Name, item.Name, candidate.Files, []string{activePath, retainedPath}); p != nil {
			return result, p
		}
	}
	result.Journal, p = publishRestoreJournal(ctx, root, j, evidence, restoreJournalOperations{})
	if p != nil {
		return result, p
	}
	if p := restoreCheckpoint(ctx, ops, "after_journal"); p != nil {
		return result, p
	}
	result.Roots, p = moveRestoreRoots(ctx, root, request.Channel, true, false, ops.roots)
	if p != nil {
		return result, p
	}
	if p := restoreCheckpoint(ctx, ops, "after_roots"); p != nil {
		return result, p
	}
	result.Finish, p = finishRestore(ctx, root, request.Channel, true, false, func(ctx context.Context, _ *runtimepaths.Root, home datahome.Info) *Error {
		_, p := runRestoreValidation(ctx, request.DataRoot, home, backend, ops, &result.FinalValidation)
		return p
	}, ops.finish)
	if p == nil {
		p = cleanupRestoreValidation(ctx, root, request.Channel, &result, false, ops.cleanup)
	}
	return result, p
}

func recoverOwned(ctx context.Context, request RecoverRequest, backend keys.Backend, ops restoreOperations) (result RestoreResult, problem *Error) {
	result = UnstartedRestoreResult()
	if p := restorePreconditions(ctx, request.Channel, request.ConfirmWritersStopped); p != nil {
		return result, p
	}
	root, q := runtimepaths.Open(request.DataRoot)
	if q != nil {
		return result, failure("BACKUP_SOURCE_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			problem = failure("BACKUP_SOURCE_CLOSE_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return result, failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	j, _, p := readRestoreJournal(ctx, root, request.Channel)
	if p != nil {
		return result, p
	}
	result.RestoreID, result.StagingRelativePath, result.RecoveryRelativePath = j.RestoreID, j.stagePath(), j.recoveryPath()
	result.Roots, p = moveRestoreRoots(ctx, root, request.Channel, true, true, ops.roots)
	if p != nil {
		return result, p
	}
	if p := restoreCheckpoint(ctx, ops, "after_roots"); p != nil {
		return result, p
	}
	result.Finish, p = finishRestore(ctx, root, request.Channel, true, true, func(ctx context.Context, _ *runtimepaths.Root, home datahome.Info) *Error {
		_, p := runRestoreValidation(ctx, request.DataRoot, home, backend, ops, &result.FinalValidation)
		return p
	}, ops.finish)
	if p == nil {
		p = cleanupRestoreValidation(ctx, root, request.Channel, &result, true, ops.cleanup)
	}
	return result, p
}

// Cleanup follows a fully returned finish call, including its deferred closes.
// It never changes Finish: a committed restore remains committed on cleanup error.
// A rollback invocation owns only its fresh final validation copy, not witnesses
// or directories left by the failed process. Stop on the first uncertain effect.
func cleanupRestoreValidation(ctx context.Context, root *runtimepaths.Root, channel string, result *RestoreResult, recovery bool, ops snapshotCleanupOperations) *Error {
	phases := []*RestoreValidationPhase{&result.InitialValidation, &result.ArchiveValidation, &result.FinalValidation}
	if recovery {
		phases = phases[2:]
	}
	for _, phase := range phases {
		if phase == &result.ArchiveValidation && phase.Preparation.Namespace == runtimepaths.DirectoryNotCreated && phase.SnapshotRelativePath == "" && phase.Summary != nil {
			continue // Explicitly validated archive without a database copy.
		}
		if phase.cleanup == nil || phase.cleanup.snapshot != phase.SnapshotRelativePath || phase.Preparation.Namespace != runtimepaths.DirectoryCreated || phase.Summary == nil || phase.CleanupFiles != len(phase.cleanup.files) {
			return failure("BACKUP_CLEANUP_PERMIT_INVALID")
		}
		var p *Error
		phase.Cleanup, p = cleanupValidationCopy(ctx, root, channel, phase.cleanup, result.Finish, ops)
		if p != nil {
			return p
		}
	}
	return nil
}
