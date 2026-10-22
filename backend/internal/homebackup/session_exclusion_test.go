package homebackup

import "testing"

// The agent connection file (ADR SEC-250) holds a live capability token under
// the Data Home's session/ directory. Backups must never capture it.
func TestDefaultBackupRootsExcludeTheSessionDirectory(t *testing.T) {
	roots, problem := selectedRoots(nil)
	if problem != nil {
		t.Fatal(problem)
	}
	for _, root := range roots {
		if root == "session" {
			t.Fatal("backups must not include the session directory")
		}
	}
}
