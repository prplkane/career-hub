//go:build windows

package runtimepaths

import (
	"io/fs"
	"os"
	"runtime"
	"syscall"
)

func replacementLinkCheck(file *os.File, _ fs.FileInfo) *Error {
	var info syscall.ByHandleFileInformation
	err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info)
	runtime.KeepAlive(file)
	if err != nil {
		return failure("FILE_STAT")
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return failure("PATH_LINK")
	}
	if info.NumberOfLinks != 1 {
		return failure("PATH_HARDLINK")
	}
	return nil
}
