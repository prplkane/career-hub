//go:build windows

package runtimepaths

import (
	"io/fs"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func (r *Root) newBackupStage(relative string) (_ *backupStage, attempted bool, problem *Error) {
	if r == nil {
		return nil, false, failure("ROOT_CLOSED")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, false, failure("ROOT_CLOSED")
	}
	p, problem := r.creationTarget(relative)
	s := &backupStage{path: p}
	keep := false
	defer func() {
		if !keep {
			if e := s.closeParents(); e != nil {
				problem = e
			}
		}
	}()
	if problem != nil {
		return nil, false, problem
	}
	if p.present {
		return nil, false, failure("FILE_EXISTS")
	}
	// A separately retained parent remains usable for safe cleanup if a callback
	// closes the originating Root. No Root lock is held across consumer callbacks.
	parent, err := p.parent.OpenRoot(".")
	if err != nil {
		return nil, false, failure("PATH_OPEN_DIRECTORY")
	}
	p.handles = append(p.handles, parent)
	p.parent = parent
	for _, directory := range append(append([]*os.Root{}, r.handles...), p.handles...) {
		pin, e := pinOwnershipRoot(directory)
		if e != nil {
			return nil, false, e
		}
		s.directories = append(s.directories, pin)
		before, err := directory.Stat(".")
		if err != nil {
			return nil, false, failure("PATH_CHANGED")
		}
		after, err := pin.Stat()
		if err != nil || isLinkLike(after) || !after.IsDir() || !os.SameFile(before, after) {
			return nil, false, failure("PATH_CHANGED")
		}
	}
	if e := checkLocalVolume(s.directories[len(s.directories)-1]); e != nil {
		return nil, false, failure("BACKUP_VOLUME_REJECTED")
	}
	if e := checkBindings(appendBindings(r.bindings, p.bindings)); e != nil {
		return nil, false, e
	}
	attempted = true
	s.file, s.name, s.identity, problem = newCreationStage(p.parent)
	if problem != nil {
		return nil, true, problem
	}
	keep = true
	return s, true, nil
}

func openBackupPublication(parent *os.Root, name string, identity fs.FileInfo) (*os.File, *Error) {
	return openOwnedCreation(parent, name, identity)
}

func renameBackupFile(source *os.File, parent *os.Root, target string) (problem error) {
	if source == nil || parent == nil || !creationBasename(target) {
		return windows.ERROR_INVALID_PARAMETER
	}
	directory, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer func() {
		if err := directory.Close(); err != nil && problem == nil {
			problem = err
		}
	}()
	name, err := windows.UTF16FromString(target)
	if err != nil || len(name) > 256 {
		return windows.ERROR_INVALID_NAME
	}
	rename := creationRenameInfo{RootDirectory: windows.Handle(directory.Fd()), FileNameLength: uint32((len(name) - 1) * 2)}
	copy(rename.FileName[:], name)
	var status windows.IO_STATUS_BLOCK
	err = windows.NtSetInformationFile(windows.Handle(source.Fd()), &status, (*byte)(unsafe.Pointer(&rename)), uint32(unsafe.Sizeof(rename)), windows.FileRenameInformation)
	runtime.KeepAlive(source)
	runtime.KeepAlive(directory)
	return err
}
