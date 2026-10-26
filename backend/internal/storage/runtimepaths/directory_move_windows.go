//go:build windows

package runtimepaths

import (
	"io/fs"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const directoryMoveSupported = true

type directoryMoveRenameInfo struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32
	FileName       [256]uint16
}

// openOwnedMoveDirectory binds DELETE authority to the already-validated
// source object. Omitting delete sharing prevents a second writer from moving
// the source while this handle remains open.
func openOwnedMoveDirectory(parent *os.Root, name string, expected fs.FileInfo) (owned *os.File, problem *Error) {
	if expected == nil || !expected.IsDir() || !creationBasename(name) {
		return nil, failure("DIRECTORY_OPEN")
	}
	directory, err := parent.Open(".")
	if err != nil {
		return nil, failure("PATH_DIRECTORY_UNAVAILABLE")
	}
	defer func() {
		if closeError := directory.Close(); closeError != nil && problem == nil {
			if owned != nil {
				_ = owned.Close()
				owned = nil
			}
			problem = failure("FILE_CLOSE")
		}
	}()
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, failure("PATH_INVALID_COMPONENT")
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: windows.Handle(directory.Fd()),
		ObjectName:    objectName,
		Attributes:    windows.OBJ_DONT_REPARSE,
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ|windows.DELETE, &attributes, &status, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_FOR_BACKUP_INTENT, 0, 0)
	runtime.KeepAlive(directory)
	if err != nil || handle == 0 || handle == windows.InvalidHandle {
		return nil, failure("DIRECTORY_OPEN")
	}
	owned = os.NewFile(uintptr(handle), name)
	if owned == nil {
		_ = windows.CloseHandle(handle)
		return nil, failure("DIRECTORY_OPEN")
	}
	info, err := owned.Stat()
	if err != nil || isLinkLike(info) || !info.IsDir() || !os.SameFile(expected, info) {
		_ = owned.Close()
		return nil, failure("PATH_CHANGED")
	}
	return owned, nil
}

// moveOwnedDirectory performs one handle-relative NT rename with replacement
// disabled. It never falls back to an absolute pathname or MoveFile.
func moveOwnedDirectory(source *os.File, targetParent *os.Root, target string) (problem error) {
	if source == nil || targetParent == nil || !creationBasename(target) {
		return windows.ERROR_INVALID_PARAMETER
	}
	directory, err := targetParent.Open(".")
	if err != nil {
		return err
	}
	defer func() {
		if closeError := directory.Close(); closeError != nil && problem == nil {
			problem = closeError
		}
	}()
	name, err := windows.UTF16FromString(target)
	if err != nil || len(name) > 256 {
		return windows.ERROR_INVALID_NAME
	}
	rename := directoryMoveRenameInfo{
		RootDirectory:  windows.Handle(directory.Fd()),
		FileNameLength: uint32((len(name) - 1) * 2),
	}
	copy(rename.FileName[:], name)
	var status windows.IO_STATUS_BLOCK
	err = windows.NtSetInformationFile(windows.Handle(source.Fd()), &status, (*byte)(unsafe.Pointer(&rename)),
		uint32(unsafe.Sizeof(rename)), windows.FileRenameInformation)
	runtime.KeepAlive(source)
	runtime.KeepAlive(directory)
	return err
}

func nativeDirectoryMoveCollision(err error) bool {
	return err == windows.STATUS_OBJECT_NAME_COLLISION || err == windows.ERROR_ALREADY_EXISTS || err == windows.ERROR_FILE_EXISTS
}
