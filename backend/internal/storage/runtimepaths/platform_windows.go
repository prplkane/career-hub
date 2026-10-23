//go:build windows

package runtimepaths

import (
	"io/fs"
	"os"
	"syscall"
)

func isLinkLike(info fs.FileInfo) bool {
	if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return true
	}
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return !ok || attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func readOnlyFlags() int { return os.O_RDONLY }
