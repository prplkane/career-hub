//go:build !windows

package runtimepaths

import (
	"io/fs"
	"os"
)

const creationSupported = false

func publishCreation(*os.Root, string, string, fs.FileInfo, []byte) (bool, *Error) {
	return false, failure("CREATE_PLATFORM_UNSUPPORTED")
}

func publishCreationBounded(*os.Root, string, string, fs.FileInfo, []byte, int64) (bool, *Error) {
	return false, failure("CREATE_PLATFORM_UNSUPPORTED")
}

func cleanupCreationStage(*os.Root, string, fs.FileInfo) *Error {
	return failure("CREATE_PLATFORM_UNSUPPORTED")
}
