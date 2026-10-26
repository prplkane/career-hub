//go:build linux || darwin

package runtimepaths

import (
	"io/fs"
	"os"
	"syscall"
)

func isLinkLike(info fs.FileInfo) bool { return info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 }

// NONBLOCK prevents a regular-file-to-FIFO swap from hanging the reader;
// NOFOLLOW also tightens Root's own containment policy for a raced leaf link.
func readOnlyFlags() int { return os.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOFOLLOW }
