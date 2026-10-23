package documentmigration

import (
	"bytes"
	"context"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const maxRecoveryExecutionSteps = 8

type recoveryMutationStore interface {
	ownedTree
	journalFileStore
	MoveDirectoryNoReplace(string, string) (runtimepaths.DirectoryMoveOutcome, *runtimepaths.Error)
}

// RecoveryResult contains only non-sensitive execution facts. It deliberately
// omits migration IDs, paths, credential names, and document details.
type RecoveryResult struct {
	Present   bool
	Recovered bool
	Finalized bool
}

// RecoverOwned explicitly rolls back one interrupted document migration under
// an existing process claim. Ordinary startup must never invoke it implicitly.
func RecoverOwned(ctx context.Context, root *runtimepaths.Root, backend keys.Backend) (RecoveryResult, *Error) {
	if root == nil {
		return RecoveryResult{}, fail(CodeOwnershipRequired)
	}
	return recoverOwned(ctx, root, backend)
}

func recoverOwned(ctx context.Context, store recoveryMutationStore, backend keys.Backend) (result RecoveryResult, problem *Error) {
	if ctx == nil || store == nil || store.RequireProcessOwnership() != nil {
		return result, fail(CodeOwnershipRequired)
	}
	finalized, finalizerProblem := finalizeCompletedMigration(ctx, store, backend)
	if finalizerProblem != nil {
		return result, finalizerProblem
	}
	if finalized {
		return RecoveryResult{Present: true, Finalized: true}, nil
	}
	for range maxRecoveryExecutionSteps {
		if ctx.Err() != nil {
			return result, fail(CodeRecoveryFailed)
		}
		plan, present, planProblem := buildRecoveryPlanOwned(store)
		if planProblem != nil {
			return result, planProblem
		}
		if !present {
			return result, nil
		}
		result.Present = true
		if ctx.Err() != nil {
			return result, fail(CodeRecoveryFailed)
		}
		if len(plan.Actions) == 0 {
			return result, fail(CodeRecoveryFailed)
		}
		switch plan.Actions[0] {
		case RecoveryPreserveCandidate, RecoveryRestoreOriginal, RecoveryPreserveStaging:
			if moveProblem := executeRecoveryMove(store, plan); moveProblem != nil {
				return result, moveProblem
			}
		case RecoveryRestoreMarker:
			if len(plan.Actions) != 1 {
				return result, fail(CodeRecoveryFailed)
			}
			if markerProblem := restoreRecoveryMarker(ctx, store, plan); markerProblem != nil {
				return result, markerProblem
			}
		case RecoveryDiscardKey:
			if discardProblem := discardPlannedRecoveryKey(plan, backend); discardProblem != nil {
				return result, discardProblem
			}
			return finishRecovery(ctx, store, plan, result)
		case RecoveryWriteRecord:
			if len(plan.Actions) != 2 || plan.Actions[1] != RecoveryRemoveJournal {
				return result, fail(CodeRecoveryFailed)
			}
			return finishRecovery(ctx, store, plan, result)
		default:
			return result, fail(CodeRecoveryFailed)
		}
	}
	return result, fail(CodeRecoveryFailed)
}

func executeRecoveryMove(store recoveryMutationStore, plan RecoveryPlan) *Error {
	base := recoveryRelative(plan.Journal.MigrationID)
	var source, target string
	switch plan.Actions[0] {
	case RecoveryPreserveCandidate:
		source, target = "documents", base+"/interrupted_candidate"
	case RecoveryRestoreOriginal:
		source, target = base+"/activation_original", "documents"
	case RecoveryPreserveStaging:
		source, target = stageRelative(plan.Journal.MigrationID), base+"/interrupted_staging"
	default:
		return fail(CodeRecoveryFailed)
	}
	outcome, pathProblem := store.MoveDirectoryNoReplace(source, target)
	if pathProblem != nil || outcome != runtimepaths.DirectoryMoveMoved {
		return fail(CodeRecoveryFailed)
	}
	return nil
}

func restoreRecoveryMarker(ctx context.Context, store recoveryMutationStore, plan RecoveryPlan) *Error {
	base := recoveryRelative(plan.Journal.MigrationID)
	legacy, present, problem := readRecoveryFile(store, base+"/legacy_marker.json", datahome.MaxMarkerBytes)
	if problem != nil || !present {
		return fail(CodeRecoveryFailed)
	}
	legacyInfo, markerProblem := datahome.ParseMarkerForDocumentMigration(legacy)
	if markerProblem != nil || legacyInfo.FormatVersion != SourceFormatVersion || legacyInfo.Channel != plan.Channel {
		return fail(CodeRecoveryFailed)
	}
	current, present, problem := readRecoveryFile(store, datahome.MarkerPath, datahome.MaxMarkerBytes)
	if problem != nil || !present {
		return fail(CodeRecoveryFailed)
	}
	if bytes.Equal(current, legacy) {
		return nil
	}
	if plan.Journal.DataHomeID == nil {
		return fail(CodeRecoveryFailed)
	}
	expected, markerBytesProblem := datahome.MarkerBytes(plan.Channel, *plan.Journal.DataHomeID)
	if markerBytesProblem != nil || !bytes.Equal(current, expected) {
		return fail(CodeRecoveryFailed)
	}
	if ctx.Err() != nil {
		return fail(CodeRecoveryFailed)
	}
	committed, pathProblem := store.ReplaceFile(datahome.MarkerPath, current, legacy)
	observed, observedPresent, observationProblem := readRecoveryFile(store, datahome.MarkerPath, datahome.MaxMarkerBytes)
	if observationProblem != nil || !observedPresent || !committed || !bytes.Equal(observed, legacy) || pathProblem != nil {
		return fail(CodeRecoveryFailed)
	}
	return nil
}

func finishRecovery(ctx context.Context, store recoveryMutationStore, plan RecoveryPlan, result RecoveryResult) (RecoveryResult, *Error) {
	if ctx.Err() != nil {
		return result, fail(CodeRecoveryFailed)
	}
	if recordProblem := writeRecoveryRecord(ctx, store, plan.Journal); recordProblem != nil {
		return result, recordProblem
	}
	if ctx.Err() != nil {
		return result, fail(CodeRecoveryFailed)
	}
	outcome, removalProblem := removeJournal(store, plan.Journal)
	result.Recovered = outcome == journalRemoved
	if removalProblem != nil || !result.Recovered {
		return result, fail(CodeRecoveryFailed)
	}
	return result, nil
}

func writeRecoveryRecord(ctx context.Context, store recoveryMutationStore, interrupted Journal) *Error {
	recovered := Journal{MigrationID: interrupted.MigrationID, State: JournalRecoveryVerified}
	raw, problem := recovered.CanonicalBytes()
	if problem != nil {
		return problem
	}
	path := recoveryRelative(interrupted.MigrationID) + "/recovered_journal.json"
	observed, present, observationProblem := readRecoveryFile(store, path, MaxJournalBytes)
	if observationProblem != nil {
		return observationProblem
	}
	if present {
		if bytes.Equal(observed, raw) {
			return nil
		}
		return fail(CodeRecoveryFailed)
	}
	if ctx.Err() != nil {
		return fail(CodeRecoveryFailed)
	}
	created, pathProblem := store.CreateFile(path, raw)
	observed, present, observationProblem = readRecoveryFile(store, path, MaxJournalBytes)
	if observationProblem != nil || !created || !present || !bytes.Equal(observed, raw) || pathProblem != nil {
		return fail(CodeRecoveryFailed)
	}
	return nil
}

func readRecoveryFile(store recoveryMutationStore, path string, limit int64) ([]byte, bool, *Error) {
	if store.RequireProcessOwnership() != nil {
		return nil, false, fail(CodeOwnershipRequired)
	}
	raw, present, pathProblem := store.ReadSingleLinkFile(path, limit)
	if pathProblem != nil || store.RequireProcessOwnership() != nil {
		return nil, false, fail(CodeRecoveryFailed)
	}
	return raw, present, nil
}

var _ recoveryMutationStore = (*runtimepaths.Root)(nil)
