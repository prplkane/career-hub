//go:build !windows

package runtimepaths

import "testing"

func TestDirectoryMoveUnsupported(t *testing.T) {
	var root *Root
	outcome, problem := root.MoveDirectoryNoReplace("source", "target")
	if outcome != DirectoryMoveNotMoved || problem == nil || problem.Code != "DIRECTORY_MOVE_PLATFORM_UNSUPPORTED" {
		t.Fatalf("unexpected unsupported result: %q / %#v", outcome, problem)
	}
}
