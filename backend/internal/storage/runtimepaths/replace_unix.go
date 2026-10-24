//go:build linux || darwin

package runtimepaths

import (
	"io/fs"
	"os"
	"syscall"
)

func replacementLinkCheck(_ *os.File, info fs.FileInfo) *Error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return failure("FILE_STAT")
	}
	if stat.Nlink != 1 {
		return failure("PATH_HARDLINK")
	}
	return nil
}
