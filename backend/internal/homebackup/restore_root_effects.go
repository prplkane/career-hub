package homebackup

import (
	"bytes"
	"context"
	"maps"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

type restoreRootResult struct {
	CompletedMoves                  int
	LastSource, LastTarget          string
	LastMove                        runtimepaths.DirectoryMoveOutcome
	RecoveryDirectoryMayHaveChanged bool
	RootPhaseComplete               bool
}

type restoreRootOperations struct {
	beforeMove func(int, restoreRootMove) *Error
	move       func(*runtimepaths.Root, string, string) (runtimepaths.DirectoryMoveOutcome, *runtimepaths.Error)
	afterMove  func(int, restoreRootMove) *Error
}

// moveRestoreRoots is internal, journal-admitted root movement, not a complete
// restore command. The caller must quiesce all writers and own the maintenance
// process. It never changes the marker, records success, removes the journal or
// cleans any tree. Ordinary startup therefore stays blocked even after this
// phase completes. Logical/schema validation remains a separate prerequisite.
//
// Activation accepts only pristine prepared placement. Interrupted activation
// is not resumed automatically; explicit rollback preserves the candidate and
// restores originals. A failed/unknown native result stops immediately. A new
// explicit invocation must inspect evidence and placement afresh, never retry a
// stored rename merely because its previous call reported an error.
func moveRestoreRoots(ctx context.Context, root *runtimepaths.Root, channel string, confirmWritersStopped, rollback bool, ops restoreRootOperations) (result restoreRootResult, problem *Error) {
	result.LastMove = runtimepaths.DirectoryMoveNotMoved
	if ctx == nil || ctx.Err() != nil {
		return result, failure("BACKUP_CANCELED")
	}
	if !confirmWritersStopped {
		return result, failure("BACKUP_WRITER_CONFIRMATION_REQUIRED")
	}
	j, evidence, p := readRestoreJournal(ctx, root, channel)
	if p != nil {
		return result, p
	}
	boundJournal, p := encodeRestoreJournal(j, j.RootFingerprint)
	if p != nil {
		return result, p
	}
	var before, after Manifest
	if !restoreCanonicalJSON(evidence.beforeManifest, &before) || !restoreCanonicalJSON(evidence.afterManifest, &after) {
		return result, failure("BACKUP_RESTORE_EVIDENCE_INVALID")
	}
	checkJournal := func() *Error {
		current, _, p := readRestoreJournal(ctx, root, channel)
		if p != nil {
			return p
		}
		raw, p := encodeRestoreJournal(current, j.RootFingerprint)
		if p != nil || !bytes.Equal(raw, boundJournal) {
			return failure("BACKUP_RESTORE_EVIDENCE_CHANGED")
		}
		if !rollback {
			marker, present, q := root.ReadObjectFile(datahome.MarkerPath, datahome.MaxMarkerBytes)
			if q != nil || !present || !bytes.Equal(marker, evidence.beforeMarker) {
				return failure("BACKUP_RESTORE_HOME_CHANGED")
			}
		}
		return nil
	}
	if p := checkJournal(); p != nil {
		return result, p
	}
	layout, p := observeRestoreLayout(root, j)
	if p != nil {
		return result, p
	}
	actions, p := planRestoreRootMoves(j, layout, rollback)
	if p != nil {
		return result, p
	}
	treeTargets := func(name string, position restorePosition, before bool) []string {
		if !rollback {
			if before {
				return []string{restoreRootPath(j, name, restoreActive), restoreRootPath(j, name, restoreOriginal)}
			}
			return []string{restoreRootPath(j, name, restoreActive), restoreRootPath(j, name, restoreStaged), restoreRootPath(j, name, restoreCandidate)}
		}
		// Rollback does not need room for a fresh forward activation. In
		// particular, preparation rejected for long recovery paths can still
		// be rolled back without moving its unchanged active/staged trees.
		targets := []string{restoreRootPath(j, name, position)}
		for _, action := range actions {
			if action.Name == name && action.From == position {
				targets = append(targets, restoreRootPath(j, name, action.To))
			}
		}
		return targets
	}
	roots := map[string]restoreRoot{}
	for _, item := range j.Roots {
		roots[item.Name] = item
		state := layout[item.Name]
		for position := restoreActive; position <= restoreCandidate; position++ {
			if !state[position] {
				continue
			}
			files := after.Files
			if restorePositionIsBefore(item, state, position) {
				files = before.Files
			}
			if p := verifyRestoreTree(ctx, root, restoreRootPath(j, item.Name, position), item.Name, files, treeTargets(item.Name, position, restorePositionIsBefore(item, state, position))); p != nil {
				return result, p
			}
		}
	}
	checkLayout := func() *Error {
		if p := checkJournal(); p != nil {
			return p
		}
		current, p := observeRestoreLayout(root, j)
		if p != nil || !maps.Equal(current, layout) {
			return failure("BACKUP_RESTORE_LAYOUT_CHANGED")
		}
		return nil
	}
	if ops.move == nil {
		ops.move = (*runtimepaths.Root).MoveDirectoryNoReplace
	}
	for index, action := range actions {
		if ops.beforeMove != nil {
			if p := ops.beforeMove(index, action); p != nil {
				return result, p
			}
		}
		if p := checkLayout(); p != nil {
			return result, p
		}
		state, item := layout[action.Name], roots[action.Name]
		files := after.Files
		if restorePositionIsBefore(item, state, action.From) {
			files = before.Files
		}
		source, target := restoreRootPath(j, action.Name, action.From), restoreRootPath(j, action.Name, action.To)
		targets := treeTargets(action.Name, action.From, restorePositionIsBefore(item, state, action.From))
		if p := verifyRestoreTree(ctx, root, source, action.Name, files, targets); p != nil {
			return result, p
		}
		if action.To == restoreCandidate {
			changed, q := root.EnsureDirectory(j.recoveryPath() + "/interrupted_candidate")
			result.RecoveryDirectoryMayHaveChanged = result.RecoveryDirectoryMayHaveChanged || changed
			if q != nil {
				return result, failure("BACKUP_RESTORE_DESTINATION_UNAVAILABLE")
			}
		}
		if ctx.Err() != nil {
			return result, failure("BACKUP_CANCELED")
		}
		result.LastSource, result.LastTarget = source, target
		var moveProblem *runtimepaths.Error
		result.LastMove, moveProblem = ops.move(root, source, target)
		if result.LastMove == runtimepaths.DirectoryMoveMoved {
			result.CompletedMoves++
		}
		if moveProblem != nil || result.LastMove != runtimepaths.DirectoryMoveMoved {
			return result, failure("BACKUP_RESTORE_MOVE_UNCONFIRMED")
		}
		state[action.From], state[action.To] = false, true
		layout[action.Name] = state
		if ops.afterMove != nil {
			if p := ops.afterMove(index, action); p != nil {
				return result, p
			}
		}
		if p := checkLayout(); p != nil {
			return result, p
		}
		if p := verifyRestoreTree(ctx, root, target, action.Name, files, targets); p != nil {
			return result, p
		}
	}
	if p := checkLayout(); p != nil {
		return result, p
	}
	result.RootPhaseComplete = true
	return result, nil
}
