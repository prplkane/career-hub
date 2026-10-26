package runtimepaths

import (
	"os"
	"sync"
)

// SQLiteFiles retains the checked main file and its directory chain while a
// trusted caller uses SQLite's native VFS. It is not a VFS sandbox. The caller
// must close every database/rows/connection before closing this guard and must
// keep the originating Root open. No raw handle or arbitrary SQL is exposed.
type SQLiteFiles struct {
	mu          sync.Mutex
	root        *Root
	relative    string
	path        *checkedPath
	main        *os.File
	directories []*os.File
	closed      bool
	closeResult *Error
}

// GuardSQLiteFiles requires this process's claimed Data Home. A missing main
// database returns (nil,nil) without creating parents/files. Existing main and
// companion paths must be ordinary single-link files. Auxiliary creation and
// deletion by native SQLite are allowed, not replaced by immutable/nolock mode.
func (r *Root) GuardSQLiteFiles(relative string) (*SQLiteFiles, *Error) {
	if !sqliteFilesSupported {
		return nil, failure("SQLITE_FILES_UNSUPPORTED")
	}
	if r == nil {
		return nil, failure("ROOT_CLOSED")
	}
	processOwnership.Lock()
	claim := processOwnership.held
	processOwnership.Unlock()
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed || r.base == nil {
		return nil, failure("ROOT_CLOSED")
	}
	if !processRootOwned(claim, r) {
		return nil, failure("OWNERSHIP_REQUIRED")
	}
	return guardSQLiteFiles(r, relative)
}

// Check is a point-in-time revalidation, not permission for a later arbitrary
// native open. Close checks again after SQLite cleanup before releasing pins.
func (g *SQLiteFiles) Check() *Error {
	if g == nil {
		return failure("SQLITE_FILES_CLOSED")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return failure("SQLITE_FILES_CLOSED")
	}
	return g.check()
}

// Close is idempotent and retains any failed final verification/cleanup result.
// Never release pins after an operation timeout while driver work is still live.
func (g *SQLiteFiles) Close() *Error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return g.closeResult
	}
	g.closeResult = g.check()
	if e := g.release(); e != nil {
		g.closeResult = e
	}
	g.closed = true
	return g.closeResult
}

func (g *SQLiteFiles) release() *Error {
	var problem *Error
	if g.main != nil {
		if g.main.Close() != nil {
			problem = failure("FILE_CLOSE")
		}
		g.main = nil
	}
	for i := len(g.directories) - 1; i >= 0; i-- {
		if g.directories[i].Close() != nil {
			problem = failure("ROOT_CLOSE")
		}
	}
	g.directories = nil
	if g.path != nil {
		for i := len(g.path.handles) - 1; i >= 0; i-- {
			if g.path.handles[i].Close() != nil {
				problem = failure("ROOT_CLOSE")
			}
		}
		g.path.handles = nil
	}
	return problem
}
