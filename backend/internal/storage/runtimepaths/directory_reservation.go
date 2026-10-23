package runtimepaths

import (
	"os"
	"sync"
)

// DirectoryCreationOutcome describes namespace creation, not completed backup
// or directory-entry durability. Created survives any later failure. Unknown
// must retain evidence and must never be retried as a definitely absent write.
type DirectoryCreationOutcome string

const (
	DirectoryNotCreated      DirectoryCreationOutcome = "not_created"
	DirectoryCreated         DirectoryCreationOutcome = "created"
	DirectoryCreationUnknown DirectoryCreationOutcome = "unknown"
)

// DirectoryReservation pins a newly created ordinary directory against rename
// and deletion until Close. It does not freeze its children or exclude writes
// to files, and Close never deletes the directory or its partial contents.
type DirectoryReservation struct {
	mu          sync.Mutex
	root        *Root
	path        *checkedPath
	directories []*os.File
	file        *os.File
	closed      bool
	closeResult *Error
}

// ReserveDirectory creates exactly one absent child below existing confined
// parents. No preexisting directory is adopted, no ACL is rewritten and no
// missing parent is created. Requires process ownership on supported Windows.
func (r *Root) ReserveDirectory(relative string) (*DirectoryReservation, DirectoryCreationOutcome, *Error) {
	if !creationSupported {
		return nil, DirectoryNotCreated, failure("DIRECTORY_RESERVATION_UNSUPPORTED")
	}
	if r == nil {
		return nil, DirectoryNotCreated, failure("ROOT_CLOSED")
	}
	if p := r.RequireProcessOwnership(); p != nil {
		return nil, DirectoryNotCreated, p
	}
	return reserveDirectory(r, relative, directoryReservationOperations{})
}

type directoryReservationOperations struct {
	create       func(*os.Root, string) (*os.File, DirectoryCreationOutcome, *Error)
	beforeCreate func()
}

func (g *DirectoryReservation) Check() *Error {
	if g == nil {
		return failure("DIRECTORY_RESERVATION_CLOSED")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.checkLocked()
}

func (g *DirectoryReservation) checkLocked() *Error {
	if g.closed || g.root == nil || g.file == nil || g.path == nil {
		return failure("DIRECTORY_RESERVATION_CLOSED")
	}
	g.root.mu.RLock()
	defer g.root.mu.RUnlock()
	if g.root.closed {
		return failure("ROOT_CLOSED")
	}
	if p := checkBindings(appendBindings(g.root.bindings, g.path.bindings)); p != nil {
		return p
	}
	name, present, p := portableChild(g.path.parent, g.path.name)
	if p != nil {
		return p
	}
	if !present || name != g.path.name {
		return failure("PATH_CHANGED")
	}
	held, err := g.file.Stat()
	if err != nil || isLinkLike(held) || !held.IsDir() {
		return failure("PATH_CHANGED")
	}
	current, err := g.path.parent.Lstat(name)
	if err != nil || isLinkLike(current) || !current.IsDir() || !os.SameFile(held, current) {
		return failure("PATH_CHANGED")
	}
	return nil
}

func (g *DirectoryReservation) Close() *Error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return g.closeResult
	}
	g.closeResult = g.checkLocked()
	if p := g.release(); p != nil {
		g.closeResult = p
	}
	g.closed = true
	return g.closeResult
}

func (g *DirectoryReservation) release() *Error {
	var problem *Error
	if g.file != nil {
		if err := g.file.Close(); err != nil {
			problem = failure("FILE_CLOSE")
		}
		g.file = nil
	}
	for i := len(g.directories) - 1; i >= 0; i-- {
		if err := g.directories[i].Close(); err != nil {
			problem = failure("FILE_CLOSE")
		}
	}
	g.directories = nil
	if g.path != nil {
		if p := g.path.close(); p != nil {
			problem = p
		}
		g.path = nil
	}
	return problem
}
