//go:build windows

package runtimepaths

import (
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func reserveDirectory(r *Root, relative string, ops directoryReservationOperations) (_ *DirectoryReservation, outcome DirectoryCreationOutcome, problem *Error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	outcome = DirectoryNotCreated
	if r.closed {
		return nil, outcome, failure("ROOT_CLOSED")
	}
	parts, p := relativeComponents(relative)
	if p != nil {
		return nil, outcome, p
	}
	path, p := r.directoryMoveTarget(parts)
	g := &DirectoryReservation{root: r, path: path}
	keep := false
	defer func() {
		if !keep {
			if p := g.release(); p != nil {
				problem = p
			}
		}
	}()
	if p != nil {
		return nil, outcome, p
	}
	if path.present {
		return nil, outcome, failure("DIRECTORY_EXISTS")
	}
	// Retain the direct parent independently so release works after Root.Close.
	parent, err := path.parent.OpenRoot(".")
	if err != nil {
		return nil, outcome, failure("PATH_OPEN_DIRECTORY")
	}
	path.handles = append(path.handles, parent)
	path.parent = parent
	for _, directory := range append(append([]*os.Root{}, r.handles...), path.handles...) {
		pin, p := pinOwnershipRoot(directory)
		if p != nil {
			return nil, outcome, p
		}
		g.directories = append(g.directories, pin)
		before, err := directory.Stat(".")
		if err != nil {
			return nil, outcome, failure("PATH_CHANGED")
		}
		after, err := pin.Stat()
		if err != nil || isLinkLike(after) || !after.IsDir() || !os.SameFile(before, after) {
			return nil, outcome, failure("PATH_CHANGED")
		}
	}
	if p := checkLocalVolume(g.directories[len(g.directories)-1]); p != nil {
		return nil, outcome, failure("BACKUP_VOLUME_REJECTED")
	}
	if p := checkBindings(appendBindings(r.bindings, path.bindings)); p != nil {
		return nil, outcome, p
	}
	if ops.create == nil {
		ops.create = createReservedDirectory
	}
	if ops.beforeCreate != nil {
		ops.beforeCreate()
	}
	g.file, outcome, problem = ops.create(parent, path.name)
	if problem != nil {
		return nil, outcome, problem
	}
	if outcome != DirectoryCreated || g.file == nil {
		return nil, DirectoryCreationUnknown, failure("DIRECTORY_CREATE_UNCONFIRMED")
	}
	// Do not call Check under the Root write lock.
	held, err := g.file.Stat()
	if err != nil || isLinkLike(held) || !held.IsDir() {
		return nil, outcome, failure("PATH_CHANGED")
	}
	current, err := parent.Lstat(path.name)
	if err != nil || isLinkLike(current) || !current.IsDir() || !os.SameFile(held, current) {
		return nil, outcome, failure("PATH_CHANGED")
	}
	name, present, p := portableChild(parent, path.name)
	if p != nil || !present || name != path.name {
		return nil, outcome, failure("PATH_CHANGED")
	}
	if p := checkBindings(appendBindings(r.bindings, path.bindings)); p != nil {
		return nil, outcome, p
	}
	keep = true
	return g, outcome, nil
}

func createReservedDirectory(parent *os.Root, name string) (file *os.File, outcome DirectoryCreationOutcome, problem *Error) {
	outcome = DirectoryNotCreated
	if parent == nil || !creationBasename(name) {
		return nil, outcome, failure("PATH_INVALID_COMPONENT")
	}
	directory, err := parent.Open(".")
	if err != nil {
		return nil, outcome, failure("PATH_DIRECTORY_UNAVAILABLE")
	}
	defer func() {
		if err := directory.Close(); err != nil && problem == nil {
			problem = failure("FILE_CLOSE")
		}
	}()
	nativeName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, outcome, failure("PATH_INVALID_COMPONENT")
	}
	attributes := windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: windows.Handle(directory.Fd()), ObjectName: nativeName, Attributes: windows.OBJ_DONT_REPARSE}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ, &attributes, &status, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.FILE_CREATE, windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	runtime.KeepAlive(directory)
	if err != nil {
		if err == windows.STATUS_OBJECT_NAME_COLLISION {
			return nil, DirectoryNotCreated, failure("DIRECTORY_EXISTS")
		}
		return nil, DirectoryCreationUnknown, failure("DIRECTORY_CREATE_UNCONFIRMED")
	}
	outcome = DirectoryCreated
	if handle == 0 || handle == windows.InvalidHandle {
		return nil, DirectoryCreationUnknown, failure("DIRECTORY_CREATE_UNCONFIRMED")
	}
	file = os.NewFile(uintptr(handle), name)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, outcome, failure("DIRECTORY_CREATE_UNCONFIRMED")
	}
	return file, outcome, nil
}
