package homebackup

import "careerhub.local/backend/internal/runtimepaths"

type restorePosition int

const (
	restoreActive restorePosition = iota
	restoreStaged
	restoreOriginal
	restoreCandidate
)

type restorePlacement [4]bool
type restoreLayout map[string]restorePlacement

type restoreRootMove struct {
	Name string
	From restorePosition
	To   restorePosition
}

func restoreRootPath(j restoreJournal, name string, position restorePosition) string {
	switch position {
	case restoreActive:
		return name
	case restoreStaged:
		return j.stagePath() + "/" + name
	case restoreOriginal:
		return j.recoveryPath() + "/" + name
	case restoreCandidate:
		return j.recoveryPath() + "/interrupted_candidate/" + name
	default:
		return ""
	}
}

// Validate all placement states before returning any action. These are observed
// names, not identity/content proof: the effect owner must verify the appropriate
// before/after tree and re-observe at each boundary. Never infer that an absent
// original is harmless, or overwrite an existing candidate to make room.
func planRestoreRootMoves(j restoreJournal, layout restoreLayout, rollback bool) ([]restoreRootMove, *Error) {
	if !validRestoreJournal(j) || len(layout) != len(j.Roots) {
		return nil, failure("BACKUP_RESTORE_LAYOUT_INVALID")
	}
	for _, root := range j.Roots {
		state, present := layout[root.Name]
		if !present || !validRestorePlacement(root.HadActive, state) {
			return nil, failure("BACKUP_RESTORE_LAYOUT_INVALID")
		}
		if !rollback && state != (restorePlacement{root.HadActive, true, false, false}) {
			return nil, failure("BACKUP_RESTORE_ROLLBACK_REQUIRED")
		}
	}
	result := []restoreRootMove{}
	if !rollback {
		for _, root := range j.Roots {
			if root.HadActive {
				result = append(result, restoreRootMove{root.Name, restoreActive, restoreOriginal})
			}
			result = append(result, restoreRootMove{root.Name, restoreStaged, restoreActive})
		}
		return result, nil
	}
	for n := len(j.Roots) - 1; n >= 0; n-- {
		root := j.Roots[n]
		state := layout[root.Name]
		if state[restoreActive] && (state[restoreOriginal] || !root.HadActive) {
			result = append(result, restoreRootMove{root.Name, restoreActive, restoreCandidate})
		}
		if state[restoreOriginal] {
			result = append(result, restoreRootMove{root.Name, restoreOriginal, restoreActive})
		}
	}
	return result, nil
}

func validRestorePlacement(hadActive bool, s restorePlacement) bool {
	if hadActive {
		return s == (restorePlacement{true, true, false, false}) ||
			s == (restorePlacement{false, true, true, false}) ||
			s == (restorePlacement{true, false, true, false}) ||
			s == (restorePlacement{false, false, true, true}) ||
			s == (restorePlacement{true, false, false, true})
	}
	return s == (restorePlacement{false, true, false, false}) ||
		s == (restorePlacement{true, false, false, false}) ||
		s == (restorePlacement{false, false, false, true})
}

func restorePositionIsBefore(root restoreRoot, state restorePlacement, position restorePosition) bool {
	return position == restoreOriginal || position == restoreActive && root.HadActive && !state[restoreOriginal]
}

func observeRestoreLayout(root *runtimepaths.Root, j restoreJournal) (restoreLayout, *Error) {
	if root == nil || root.RequireProcessOwnership() != nil {
		return nil, failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	if !validRestoreJournal(j) {
		return nil, failure("BACKUP_RESTORE_JOURNAL_INVALID")
	}
	layout := restoreLayout{}
	for _, item := range j.Roots {
		var state restorePlacement
		for position := restoreActive; position <= restoreCandidate; position++ {
			_, present, p := root.ListDirectoryEntries(restoreRootPath(j, item.Name, position))
			if p != nil {
				return nil, failure("BACKUP_RESTORE_LAYOUT_INVALID")
			}
			state[position] = present
		}
		layout[item.Name] = state
	}
	return layout, nil
}
