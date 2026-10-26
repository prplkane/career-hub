//go:build !windows && !linux && !darwin

package runtimepaths

import (
	"io/fs"
	"os"
)

func replacementLinkCheck(*os.File, fs.FileInfo) *Error {
	return failure("ROOT_PLATFORM_UNSUPPORTED")
}
