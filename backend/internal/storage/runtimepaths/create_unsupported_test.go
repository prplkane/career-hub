//go:build !windows

package runtimepaths

import "testing"

func TestCreationUnsupportedBeforeAnyWrite(t *testing.T) {
	_, changed, e := OpenOrCreate("invalid")
	assertReplaceError(t, changed, e, false, "CREATE_PLATFORM_UNSUPPORTED")
	var r *Root
	changed, e = r.EnsureDirectory("invalid")
	assertReplaceError(t, changed, e, false, "CREATE_PLATFORM_UNSUPPORTED")
	changed, e = r.CreateFile("invalid", nil)
	assertReplaceError(t, changed, e, false, "CREATE_PLATFORM_UNSUPPORTED")
	code(t, r.ProbeWritable(), "CREATE_PLATFORM_UNSUPPORTED")
}
