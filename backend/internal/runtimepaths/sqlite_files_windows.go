//go:build windows

package runtimepaths

import (
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const sqliteFilesSupported = true

// Caller retains Root's read lock. Tests can exercise this acquisition without
// publishing an irreversible process claim; the exported API never skips it.
func guardSQLiteFiles(r *Root, relative string) (_ *SQLiteFiles, problem *Error) {
	p, e := r.walk(relative)
	g := &SQLiteFiles{root: r, relative: relative, path: p}
	keep := false
	defer func() {
		if !keep {
			if e := g.release(); e != nil {
				problem = e
			}
		}
	}()
	if e != nil {
		return nil, e
	}
	if !p.present {
		return nil, checkBindings(appendBindings(r.bindings, p.bindings))
	}
	// Retain every configured and descendant directory independently. Native
	// SQLite still opens pathnames; do not rely on Go's delete-sharing roots.
	for _, directory := range append(append([]*os.Root{}, r.handles...), p.handles...) {
		pin, e := pinOwnershipRoot(directory)
		if e != nil {
			return nil, e
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
	g.main, e = openSQLiteFile(p.parent, p.name, false)
	if e != nil {
		return nil, e
	}
	// This function already holds root.mu; checkLocked must not reacquire it
	// while a concurrent Close is waiting for the writer lock.
	if e := g.checkLocked(); e != nil {
		return nil, e
	}
	keep = true
	return g, nil
}

func (g *SQLiteFiles) check() *Error {
	if g.root == nil || g.main == nil || g.path == nil || len(g.directories) == 0 {
		return failure("SQLITE_FILES_CLOSED")
	}
	g.root.mu.RLock()
	defer g.root.mu.RUnlock()
	if g.root.closed {
		return failure("ROOT_CLOSED")
	}
	return g.checkLocked()
}

func (g *SQLiteFiles) checkLocked() *Error {
	info, err := g.main.Stat()
	if err != nil {
		return failure("FILE_STAT")
	}
	if isLinkLike(info) || !info.Mode().IsRegular() {
		return failure("PATH_NOT_REGULAR")
	}
	if !os.SameFile(info, g.path.info) {
		return failure("PATH_CHANGED")
	}
	if e := replacementLinkCheck(g.main, info); e != nil {
		return e
	}
	if e := g.path.recheckLeaf(info); e != nil {
		return e
	}
	if e := checkBindings(appendBindings(g.root.bindings, g.path.bindings)); e != nil {
		return e
	}
	if e := checkLocalVolume(g.directories[len(g.directories)-1]); e != nil {
		return failure("SQLITE_VOLUME_REJECTED")
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if e := g.checkAuxiliary(suffix); e != nil {
			return e
		}
	}
	return checkBindings(appendBindings(g.root.bindings, g.path.bindings))
}

func (g *SQLiteFiles) checkAuxiliary(suffix string) (problem *Error) {
	p, e := g.root.walk(g.relative + suffix)
	if p != nil {
		defer func() {
			for i := len(p.handles) - 1; i >= 0; i-- {
				if p.handles[i].Close() != nil {
					problem = failure("ROOT_CLOSE")
				}
			}
		}()
	}
	if e != nil {
		return e
	}
	if !p.present {
		return checkBindings(appendBindings(g.root.bindings, p.bindings))
	}
	// Auxiliary observations deliberately do not reserve the name or prohibit
	// native SQLite's later unlink/recreation. Never retain a no-delete pin here.
	file, e := openSQLiteFile(p.parent, p.name, true)
	if e != nil {
		return e
	}
	defer func() {
		if file.Close() != nil {
			problem = failure("FILE_CLOSE")
		}
	}()
	info, err := file.Stat()
	if err != nil || isLinkLike(info) || !info.Mode().IsRegular() {
		return failure("PATH_NOT_REGULAR")
	}
	if !os.SameFile(p.info, info) {
		return failure("PATH_CHANGED")
	}
	if e := replacementLinkCheck(file, info); e != nil {
		return e
	}
	if e := p.recheckLeaf(info); e != nil {
		return e
	}
	return checkBindings(appendBindings(g.root.bindings, p.bindings))
}

func openSQLiteFile(parent *os.Root, name string, allowDelete bool) (file *os.File, problem *Error) {
	return openSQLiteFileAccess(parent, name, allowDelete, false)
}

func openSQLiteFileAccess(parent *os.Root, name string, allowDelete, write bool) (file *os.File, problem *Error) {
	borrowed, err := parent.Open(".")
	if err != nil {
		return nil, failure("FILE_OPEN")
	}
	defer func() {
		if borrowed.Close() != nil && problem == nil {
			if file != nil {
				_ = file.Close()
				file = nil
			}
			problem = failure("FILE_CLOSE")
		}
	}()
	nativeName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, failure("PATH_INVALID")
	}
	attributes := windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: windows.Handle(borrowed.Fd()), ObjectName: nativeName, Attributes: windows.OBJ_DONT_REPARSE}
	sharing := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE)
	if allowDelete {
		sharing |= windows.FILE_SHARE_DELETE
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	access := uint32(windows.FILE_GENERIC_READ)
	if write {
		access |= windows.FILE_GENERIC_WRITE
	}
	err = windows.NtCreateFile(&handle, access, &attributes, &status, nil, 0,
		sharing, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT, 0, 0)
	runtime.KeepAlive(borrowed)
	if err != nil || handle == 0 || handle == windows.InvalidHandle {
		return nil, failure("FILE_OPEN")
	}
	file = os.NewFile(uintptr(handle), ".")
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, failure("FILE_OPEN")
	}
	return file, nil
}

func (g *SQLiteFiles) syncMain() (problem *Error) {
	file, e := openSQLiteFileAccess(g.path.parent, g.path.name, false, true)
	if e != nil {
		return e
	}
	defer func() {
		if file.Close() != nil {
			problem = failure("FILE_CLOSE")
		}
	}()
	info, err := file.Stat()
	if err != nil || !os.SameFile(info, g.path.info) {
		return failure("PATH_CHANGED")
	}
	if e := replacementLinkCheck(file, info); e != nil {
		return e
	}
	if file.Sync() != nil {
		return failure("FILE_SYNC")
	}
	return nil
}
