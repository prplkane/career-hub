package documentmigration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"runtime"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/documents/keys"
	"careerhub.local/backend/internal/documents/legacydpapi"
	"careerhub.local/backend/internal/runtimepaths"
)

const CodePlanMismatch = "DOCUMENT_MIGRATION_PLAN_MISMATCH"

// ApplyResult is content-minimized. Recovery identifiers, paths, key identity,
// ciphertext facts, and document details never leave the coordinator.
type ApplyResult struct {
	Plan Plan
}

type applyRandom func(int) ([]byte, error)

// ApplyOwned applies one exact reviewed plan while the caller already owns the
// Data Home. It is intentionally not connected to any executable or startup
// path. Failures after this apply's journal publication run only its exact
// owned recovery; ordinary startup never invokes recovery automatically.
func ApplyOwned(ctx context.Context, root *runtimepaths.Root, backend keys.Backend,
	confirmPlan string) (ApplyResult, *Error) {
	if root == nil {
		return ApplyResult{}, fail(CodeOwnershipRequired)
	}
	return applyOwned(ctx, root, backend, secureApplyRandom, legacydpapi.UnprotectLegacy,
		runtime.GOOS == "windows", confirmPlan)
}

func applyOwned(ctx context.Context, store applyMutationStore, backend keys.Backend,
	random applyRandom, unprotect unprotectFunc, windows bool,
	confirmPlan string) (result ApplyResult, problem *Error) {
	if ctx == nil || store == nil || random == nil || store.RequireProcessOwnership() != nil {
		return result, fail(CodeOwnershipRequired)
	}
	plan, snapshot, sourceProblem := readSnapshotOwned(store, windows, unprotect)
	if sourceProblem != nil {
		return result, sourceProblem
	}
	defer snapshot.clear()
	if confirmPlan != plan.PlanID {
		return result, fail(CodePlanMismatch)
	}
	if _, managerProblem := keys.NewManager(snapshot.Channel, backend, keys.RandomBytes(random)); managerProblem != nil {
		return result, fail(CodeApplyFailed)
	}
	migrationID, idProblem := newApplyID(random, "")
	if idProblem != nil {
		return result, idProblem
	}
	journalAttempted := false
	defer func() {
		if problem == nil || !journalAttempted {
			return
		}
		owned, present, observationProblem := observeApplyJournal(store, migrationID)
		if observationProblem != nil || present && !owned {
			problem = fail(CodeRecoveryFailed)
			return
		}
		if !present {
			return
		}
		recovery, recoveryProblem := recoverOwned(context.WithoutCancel(ctx), store, backend)
		if recoveryProblem != nil || !recovery.Present || !recovery.Recovered && !recovery.Finalized {
			problem = fail(CodeRecoveryFailed)
		}
	}()
	initial, recoveryProblem := writeRecoveryCopy(ctx, store, migrationID, snapshot, plan)
	if recoveryProblem != nil {
		return result, recoveryProblem
	}
	journalAttempted = true
	outcome, publishProblem := publishJournal(store, initial)
	if publishProblem != nil || outcome != journalWritten {
		return result, fail(CodeApplyFailed)
	}
	dataHomeID, idProblem := newApplyID(random, "dh_")
	if idProblem != nil {
		return result, idProblem
	}
	keyResult, keyProblem := createJournaledKey(
		snapshot.Channel, backend, keys.RandomBytes(random), dataHomeID, initial,
		func(current, next Journal) (journalWriteOutcome, *Error) {
			return advanceJournal(store, current, next)
		},
	)
	if keyProblem != nil {
		return result, keyProblem
	}
	defer clear(keyResult.Master.Key)
	current := keyResult.LastProven
	if problem = writeVerifiedStage(ctx, store, snapshot, plan, current, keyResult.Master); problem != nil {
		return result, problem
	}
	current, problem = confirmStageSourceAndAdvance(
		ctx, store, windows, unprotect, snapshot, plan, current, keyResult.Master,
	)
	if problem != nil {
		return result, problem
	}
	current, problem = swapVerifiedStage(ctx, store, snapshot, plan, current, keyResult.Master)
	if problem != nil {
		return result, problem
	}
	current, problem = activateMigrationMarker(ctx, store, snapshot, plan, current, keyResult.Master)
	if problem != nil {
		return result, problem
	}
	if _, problem = completeActivatedMigration(ctx, store, snapshot, plan, current, keyResult.Master); problem != nil {
		return result, problem
	}
	return ApplyResult{Plan: plan}, nil
}

func newApplyID(random applyRandom, prefix string) (string, *Error) {
	data, err := random(16)
	defer clear(data)
	if err != nil || len(data) != 16 {
		return "", fail(CodeApplyFailed)
	}
	return prefix + hex.EncodeToString(data), nil
}

func secureApplyRandom(size int) ([]byte, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		clear(data)
		return nil, err
	}
	return data, nil
}

func observeApplyJournal(store applyMutationStore, migrationID string) (owned, present bool, problem *Error) {
	if store.RequireProcessOwnership() != nil {
		return false, false, fail(CodeRecoveryFailed)
	}
	raw, present, pathProblem := store.ReadSingleLinkFile(datahome.DocumentRecoveryPath, MaxJournalBytes)
	if pathProblem != nil || store.RequireProcessOwnership() != nil {
		return false, false, fail(CodeRecoveryFailed)
	}
	if !present {
		return false, false, nil
	}
	journal, journalProblem := ParseJournal(raw)
	if journalProblem != nil {
		return false, true, nil
	}
	return journal.MigrationID == migrationID, true, nil
}
