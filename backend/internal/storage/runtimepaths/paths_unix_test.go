//go:build linux || darwin

package runtimepaths

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestNativeFIFORejected(t *testing.T) {
	dir := testHome(t)
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0600); err != nil {
		t.Skipf("native FIFO unavailable: %v", err)
	}
	r := opened(t, dir)
	code(t, r.Check("pipe"), "PATH_NOT_REGULAR")
	_, _, e := r.ReadFile("pipe", 20)
	code(t, e, "PATH_NOT_REGULAR")
}
