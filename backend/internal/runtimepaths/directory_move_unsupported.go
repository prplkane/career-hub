//go:build !windows

package runtimepaths

import (
	"io/fs"
	"os"
)

const directoryMoveSupported = false

func openOwnedMoveDirectory(*os.Root, string, fs.FileInfo) (*os.File, *Error) {
	return nil, failure("DIRECTORY_MOVE_PLATFORM_UNSUPPORTED")
}

func moveOwnedDirectory(*os.File, *os.Root, string) error { return fs.ErrInvalid }

func nativeDirectoryMoveCollision(error) bool { return false }
