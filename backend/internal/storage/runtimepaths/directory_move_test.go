package runtimepaths

import "testing"

func TestClassifyDirectoryMoveFacts(t *testing.T) {
	tests := []struct {
		name           string
		source, target directoryMoveNameState
		want           DirectoryMoveOutcome
	}{
		{"target proves commit", directoryMoveNameAbsent, directoryMoveNameOwned, DirectoryMoveMoved},
		{"target proof wins over recreated source", directoryMoveNameForeign, directoryMoveNameOwned, DirectoryMoveMoved},
		{"source proves no commit", directoryMoveNameOwned, directoryMoveNameAbsent, DirectoryMoveNotMoved},
		{"source proof wins over collision", directoryMoveNameOwned, directoryMoveNameForeign, DirectoryMoveNotMoved},
		{"both absent is unknown", directoryMoveNameAbsent, directoryMoveNameAbsent, DirectoryMoveUnknown},
		{"foreign names are unknown", directoryMoveNameForeign, directoryMoveNameForeign, DirectoryMoveUnknown},
		{"failed observations are unknown", directoryMoveNameUnknown, directoryMoveNameUnknown, DirectoryMoveUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyDirectoryMove(test.source, test.target); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestDirectoryMoveDescendantComparison(t *testing.T) {
	if !componentPrefix([]string{"documents"}, []string{"documents", "nested"}) {
		t.Fatal("descendant target was not detected")
	}
	for _, candidate := range [][]string{{"documents"}, {"document"}, {"backups", "documents"}} {
		if componentPrefix([]string{"documents"}, candidate) {
			t.Fatalf("unrelated target was rejected: %#v", candidate)
		}
	}
}
