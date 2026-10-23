//go:build !windows

package runtimepaths

import (
	"os"
	"testing"
)

func TestLocalVolumeUnsupportedBeforeFilesystem(t *testing.T) {
	var root *Root
	code(t, root.CheckLocalVolume(), "VOLUME_PLATFORM_UNSUPPORTED")
	code(t, (&Root{}).checkLocalVolumeWith(func(*os.File) *Error {
		t.Fatal("unsupported platform invoked native inspection")
		return nil
	}), "VOLUME_PLATFORM_UNSUPPORTED")
	code(t, checkLocalVolume(nil), "VOLUME_PLATFORM_UNSUPPORTED")
}
