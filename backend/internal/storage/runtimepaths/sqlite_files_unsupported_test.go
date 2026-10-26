//go:build !windows

package runtimepaths

import "testing"

func TestSQLiteFilesUnsupportedBeforeFilesystem(t *testing.T) {
	var root *Root
	g, e := root.GuardSQLiteFiles("not/a/valid/root")
	if g != nil {
		t.Fatal("unsupported platform acquired guard")
	}
	code(t, e, "SQLITE_FILES_UNSUPPORTED")
}
