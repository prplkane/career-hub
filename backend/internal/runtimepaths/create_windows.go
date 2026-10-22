//go:build windows

package runtimepaths

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const creationSupported = true

// FILE_RENAME_INFORMATION: the leading union is four bytes on current SDKs;
// RootDirectory and FileName are naturally aligned on both 32/64-bit Windows.
// Only FileRenameInformation is used: Flags=0 means ReplaceIfExists=FALSE.
type creationRenameInfo struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32
	FileName       [256]uint16
}

// openOwnedCreation opens a single basename relative to a retained directory,
// rejects reparses and checks the owned identity before returning a DELETE-capable
// handle. No write/delete sharing permits stage replacement after this check.
func openOwnedCreation(parent *os.Root, name string, expected fs.FileInfo) (owned *os.File, problem *Error) {
	if expected == nil || !creationBasename(name) {
		return nil, failure("FILE_CLEANUP")
	}
	directory, err := parent.Open(".")
	if err != nil {
		return nil, failure("PATH_DIRECTORY_UNAVAILABLE")
	}
	defer func() {
		if err := directory.Close(); err != nil && problem == nil {
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
	attributes := windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: windows.Handle(directory.Fd()), ObjectName: objectName, Attributes: windows.OBJ_DONT_REPARSE}
	var h windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, windows.FILE_GENERIC_READ|windows.DELETE, &attributes, &status, nil, 0,
		windows.FILE_SHARE_READ, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	runtime.KeepAlive(directory)
	if err != nil {
		return nil, failure("FILE_OPEN")
	}
	f := os.NewFile(uintptr(h), name)
	if f == nil {
		_ = windows.CloseHandle(h)
		return nil, failure("FILE_OPEN")
	}
	info, err := f.Stat()
	if err != nil || isLinkLike(info) || !info.Mode().IsRegular() || !os.SameFile(expected, info) {
		_ = f.Close()
		return nil, failure("PATH_CHANGED")
	}
	if e := replacementLinkCheck(f, info); e != nil {
		_ = f.Close()
		return nil, e
	}
	return f, nil
}

// publishCreation uses one NT rename operation with replacement disabled. There
// is no MoveFile/os.Rename fallback and no deletion of the destination on error.
func publishCreation(parent *os.Root, stageName, target string, identity fs.FileInfo, content []byte) (created bool, problem *Error) {
	return publishCreationBounded(parent, stageName, target, identity, content, MaxReadBytes)
}

func publishCreationBounded(parent *os.Root, stageName, target string, identity fs.FileInfo, content []byte, limit int64) (created bool, problem *Error) {
	return publishCreationWithLimit(parent, stageName, target, identity, content, limit, nil)
}

// beforeRename is a per-call native test seam after all preflight, proving an
// intervening destination is refused by the actual NT call, not by an Lstat.
func publishCreationWith(parent *os.Root, stageName, target string, identity fs.FileInfo, content []byte, beforeRename func()) (created bool, problem *Error) {
	return publishCreationWithLimit(parent, stageName, target, identity, content, MaxReadBytes, beforeRename)
}

func publishCreationWithLimit(parent *os.Root, stageName, target string, identity fs.FileInfo, content []byte, limit int64, beforeRename func()) (created bool, problem *Error) {
	if !creationBasename(target) {
		return false, failure("PATH_INVALID_COMPONENT")
	}
	file, e := openOwnedCreation(parent, stageName, identity)
	if e != nil {
		return false, e
	}
	defer func() {
		if err := file.Close(); err != nil && problem == nil {
			problem = failure("FILE_CLOSE")
		}
	}()
	before, err := file.Stat()
	if err != nil {
		return false, failure("FILE_STAT")
	}
	if limit != MaxReadBytes && limit != MaxObjectReadBytes || before.Size() > limit {
		return false, failure("FILE_SIZE_LIMIT")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return false, failure("FILE_READ")
	}
	if !bytes.Equal(raw, content) {
		return false, failure("FILE_STAGE")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return false, failure("PATH_CHANGED")
	}
	directory, err := parent.Open(".")
	if err != nil {
		return false, failure("PATH_DIRECTORY_UNAVAILABLE")
	}
	defer func() {
		if err := directory.Close(); err != nil && problem == nil {
			problem = failure("FILE_CLOSE")
		}
	}()
	name, err := windows.UTF16FromString(target)
	if err != nil || len(name) > 256 {
		return false, failure("PATH_COMPONENT_LIMIT")
	}
	rename := creationRenameInfo{RootDirectory: windows.Handle(directory.Fd()), FileNameLength: uint32((len(name) - 1) * 2)}
	copy(rename.FileName[:], name)
	if beforeRename != nil {
		beforeRename()
	}
	var status windows.IO_STATUS_BLOCK
	err = windows.NtSetInformationFile(windows.Handle(file.Fd()), &status, (*byte)(unsafe.Pointer(&rename)),
		uint32(unsafe.Sizeof(rename)), windows.FileRenameInformation)
	runtime.KeepAlive(file)
	runtime.KeepAlive(directory)
	if err != nil {
		if current, statErr := parent.Lstat(target); statErr == nil && os.SameFile(identity, current) {
			created = true
		}
		if !created && (errors.Is(err, windows.STATUS_OBJECT_NAME_COLLISION) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS)) {
			return false, failure("FILE_EXISTS")
		}
		return created, failure("FILE_PUBLISH")
	}
	created = true
	current, err := parent.Lstat(target)
	if err != nil || isLinkLike(current) || !current.Mode().IsRegular() || !os.SameFile(identity, current) {
		return true, failure("PATH_CHANGED")
	}
	if e := replacementLinkCheck(file, current); e != nil {
		return true, e
	}
	observed, present, e := portableChild(parent, target)
	if e != nil {
		return true, e
	}
	if !present || observed != target {
		return true, failure("PATH_CHANGED")
	}
	return true, nil
}

// Cleanup never passes a pathname to DeleteFile/Remove. Even if the random name
// is swapped after Lstat, openOwnedCreation verifies identity before setting
// delete disposition on that same handle. Foreign/reparse/hardlinked files stay.
func cleanupCreationStage(parent *os.Root, name string, identity fs.FileInfo) (problem *Error) {
	info, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil || identity == nil || isLinkLike(info) || !info.Mode().IsRegular() || !os.SameFile(identity, info) {
		return failure("FILE_CLEANUP")
	}
	file, e := openOwnedCreation(parent, name, identity)
	if e != nil {
		return failure("FILE_CLEANUP")
	}
	var status windows.IO_STATUS_BLOCK
	deleteFile := byte(1) // FILE_DISPOSITION_INFORMATION.DeleteFile (BOOLEAN)
	err = windows.NtSetInformationFile(windows.Handle(file.Fd()), &status, &deleteFile, 1, windows.FileDispositionInformation)
	runtime.KeepAlive(file)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return failure("FILE_CLEANUP")
	}
	// A different process can retain a delete-sharing reader. Do not report a
	// completed probe/cleanup while the name is still pending deletion.
	if _, err := parent.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
		return failure("FILE_CLEANUP")
	}
	return nil
}
