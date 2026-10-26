//go:build windows

package runtimepaths

import (
	"context"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const backupReadSupported = true

// Caller holds Root's read lock and has checked process ownership. Tests may
// exercise this private acquisition without publishing an irreversible claim.
func openBackupRead(r *Root, ctx context.Context, relative string, limit int64) (_ *BackupRead, problem *Error) {
	p, problem := r.walk(relative)
	g := &BackupRead{root: r, path: p, ctx: ctx}
	keep := false
	defer func() {
		if !keep {
			if closeProblem := g.release(); closeProblem != nil {
				problem = closeProblem
			}
		}
	}()
	if problem != nil {
		return nil, problem
	}
	if !p.present {
		return nil, failure("FILE_MISSING")
	}
	if p.info.Size() < 0 || p.info.Size() > limit {
		return nil, failure("FILE_SIZE_LIMIT")
	}
	g.size = p.info.Size()
	for _, directory := range append(append([]*os.Root{}, r.handles...), p.handles...) {
		pin, problem := pinOwnershipRoot(directory)
		if problem != nil {
			return nil, problem
		}
		g.directories = append(g.directories, pin)
		before, err := directory.Stat(".")
		if err != nil {
			return nil, failure("PATH_CHANGED")
		}
		after, err := pin.Stat()
		if err != nil || isLinkLike(after) || !after.IsDir() || !os.SameFile(before, after) {
			return nil, failure("PATH_CHANGED")
		}
	}
	if problem := checkLocalVolume(g.directories[len(g.directories)-1]); problem != nil {
		return nil, failure("BACKUP_VOLUME_REJECTED")
	}
	g.file, problem = openBackupFile(p.parent, p.name)
	if problem != nil {
		return nil, problem
	}
	if problem = g.checkLocked(); problem != nil {
		return nil, problem
	}
	if ctx.Err() != nil {
		return nil, failure("BACKUP_READ_CANCELED")
	}
	keep = true
	return g, nil
}

// No DELETE/write access, inheritance, reparse traversal, absolute-path open or
// write/delete sharing. The kernel rejects a conflicting preexisting writer.
func openBackupFile(parent *os.Root, name string) (file *os.File, problem *Error) {
	directory, err := parent.Open(".")
	if err != nil {
		return nil, failure("FILE_OPEN")
	}
	defer func() {
		if err := directory.Close(); err != nil && problem == nil {
			if file != nil {
				_ = file.Close()
				file = nil
			}
			problem = failure("FILE_CLOSE")
		}
	}()
	nativeName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, failure("PATH_INVALID_COMPONENT")
	}
	attributes := windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: windows.Handle(directory.Fd()), ObjectName: nativeName, Attributes: windows.OBJ_DONT_REPARSE}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ, &attributes, &status, nil, 0,
		windows.FILE_SHARE_READ, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	runtime.KeepAlive(directory)
	if err != nil {
		return nil, failure("FILE_OPEN")
	}
	file = os.NewFile(uintptr(handle), name)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, failure("FILE_OPEN")
	}
	return file, nil
}
