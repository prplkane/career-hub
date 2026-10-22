package homebackup

import (
	"strings"
	"testing"
)

func TestRestorePlacementExhaustive(t *testing.T) {
	for _, hadActive := range []bool{false, true} {
		accepted := 0
		for mask := 0; mask < 16; mask++ {
			j, _ := journalFixture(t)
			j.Roots[0].HadActive = hadActive
			var state restorePlacement
			for n := range state {
				state[n] = mask&(1<<n) != 0
			}
			layout := restoreLayout{"jobs": state}
			moves, p := planRestoreRootMoves(j, layout, true)
			if p != nil {
				if moves != nil {
					t.Fatal("partial plan on invalid layout")
				}
				continue
			}
			accepted++
			for _, move := range moves {
				if !state[move.From] || state[move.To] {
					t.Fatal("plan would overwrite or move missing directory", hadActive, mask)
				}
				state[move.From], state[move.To] = false, true
				if !validRestorePlacement(hadActive, state) {
					t.Fatal("intermediate state cannot recover")
				}
			}
			if state[restoreActive] != hadActive || state[restoreOriginal] || state[restoreStaged] == state[restoreCandidate] {
				t.Fatal("rollback lost original or candidate", hadActive, mask)
			}
			again, p := planRestoreRootMoves(j, restoreLayout{"jobs": state}, true)
			if p != nil || len(again) != 0 {
				t.Fatal("completed rollback is not idempotent", p)
			}
		}
		want := 3
		if hadActive {
			want = 5
		}
		if accepted != want {
			t.Fatal("unexpected admitted layouts", hadActive, accepted)
		}
	}
}

func TestRestoreActivationAndRollbackPlans(t *testing.T) {
	j, _ := journalFixture(t)
	j.Roots = []restoreRoot{{"jobs", true}, {"logs", false}}
	layout := restoreLayout{"jobs": {true, true, false, false}, "logs": {false, true, false, false}}
	moves, p := planRestoreRootMoves(j, layout, false)
	if p != nil || len(moves) != 3 {
		t.Fatal(moves, p)
	}
	for _, move := range moves {
		state := layout[move.Name]
		if !state[move.From] || state[move.To] {
			t.Fatal("unsafe activation")
		}
		state[move.From], state[move.To] = false, true
		layout[move.Name] = state
		// Every interrupted forward prefix is recoverable but not re-activatable.
		if _, p := planRestoreRootMoves(j, layout, true); p != nil {
			t.Fatal("interrupted prefix not recoverable", p)
		}
		if _, p := planRestoreRootMoves(j, layout, false); p == nil || p.Code != "BACKUP_RESTORE_ROLLBACK_REQUIRED" {
			t.Fatal("interrupted activation resumed", p)
		}
	}
	rollback, p := planRestoreRootMoves(j, layout, true)
	if p != nil || len(rollback) != 3 || rollback[0].Name != "logs" || rollback[1].To != restoreCandidate || rollback[2].From != restoreOriginal {
		t.Fatal("reverse recovery order", rollback, p)
	}
	for _, bad := range []restoreLayout{{}, {"jobs": {true, true, false, false}, "logs": {}, "extra": {}}, {"jobs": {true, true, false, false}, "logs": {true, true, false, false}}} {
		if plan, p := planRestoreRootMoves(j, bad, true); p == nil || plan != nil {
			t.Fatal("invalid late root returned partial plan")
		}
	}
	for _, position := range []restorePosition{restoreActive, restoreStaged, restoreOriginal, restoreCandidate} {
		path := restoreRootPath(j, "jobs", position)
		if path == "" || strings.Contains(path, "..") || !strings.HasSuffix(path, "jobs") {
			t.Fatal("invalid derived path")
		}
	}
}
