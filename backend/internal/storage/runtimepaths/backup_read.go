package runtimepaths

import (
	"context"
	"io"
	"os"
	"sync"
)

// These are separate from ordinary catalog/object read limits. The archive
// ceiling covers the v2 32 GiB payload plus bounded ZIP metadata overhead.
const MaxBackupReadBytes int64 = (32 << 30) + (128 << 20)
const MaxBackupReadChunk = 256 << 10

// BackupRead retains one immutable-by-sharing regular file for streaming reads.
// It is not a coherent Home snapshot or an extraction/writing capability.
// Keep its Root open and close this lease after every consumer has stopped.
// ReadAt/Check/Close serialize; after Close no retained caller can read again.
type BackupRead struct {
	mu          sync.Mutex
	root        *Root
	path        *checkedPath
	file        *os.File
	directories []*os.File
	ctx         context.Context
	size        int64
	closed      bool
	closeResult *Error
}

// OpenBackupRead requires existing process ownership and existing ordinary
// single-link content. It creates nothing. A preexisting writable file handle
// conflicts with the native read-only sharing lease; there is no weaker retry.
func (r *Root) OpenBackupRead(ctx context.Context, relative string, limit int64) (*BackupRead, *Error) {
	if !backupReadSupported {
		return nil, failure("BACKUP_READ_UNSUPPORTED")
	}
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("BACKUP_READ_CANCELED")
	}
	if limit < 0 || limit > MaxBackupReadBytes {
		return nil, failure("READ_LIMIT_INVALID")
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
	return openBackupRead(r, ctx, relative, limit)
}

func (g *BackupRead) Size() int64 {
	if g == nil {
		return 0
	}
	return g.size
}

// ReadAt has a fixed per-call ceiling and checks the opened identity before and
// after each read. Failure of those checks discards/clears the returned bytes.
// Cancellation is cooperative; the maintenance parent owns hard process limits.
func (g *BackupRead) ReadAt(p []byte, offset int64) (int, error) {
	if g == nil {
		return 0, failure("BACKUP_READ_CLOSED")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.ctx == nil {
		return 0, failure("BACKUP_READ_CLOSED")
	}
	if offset < 0 || len(p) > MaxBackupReadChunk {
		return 0, failure("READ_LIMIT_INVALID")
	}
	if g.ctx.Err() != nil {
		return 0, failure("BACKUP_READ_CANCELED")
	}
	if problem := g.check(); problem != nil {
		return 0, problem
	}
	if len(p) == 0 {
		return 0, nil
	}
	if offset >= g.size {
		return 0, io.EOF
	}
	n, err := g.file.ReadAt(p, offset)
	if problem := g.check(); problem != nil {
		clear(p[:n])
		return 0, problem
	}
	if g.ctx.Err() != nil {
		clear(p[:n])
		return 0, failure("BACKUP_READ_CANCELED")
	}
	if err != nil && err != io.EOF {
		clear(p[:n])
		return 0, failure("FILE_READ")
	}
	return n, err
}

func (g *BackupRead) Check() *Error {
	if g == nil {
		return failure("BACKUP_READ_CLOSED")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.ctx == nil {
		return failure("BACKUP_READ_CLOSED")
	}
	if g.ctx.Err() != nil {
		return failure("BACKUP_READ_CANCELED")
	}
	return g.check()
}

func (g *BackupRead) check() *Error {
	if g.root == nil || g.path == nil || g.file == nil {
		return failure("BACKUP_READ_CLOSED")
	}
	g.root.mu.RLock()
	defer g.root.mu.RUnlock()
	if g.root.closed {
		return failure("ROOT_CLOSED")
	}
	return g.checkLocked()
}

// Caller holds Root's read lock; acquisition must not reacquire it while a
// concurrent Root.Close is waiting for the write lock.
func (g *BackupRead) checkLocked() *Error {
	info, err := g.file.Stat()
	if err != nil {
		return failure("FILE_STAT")
	}
	if isLinkLike(info) || !info.Mode().IsRegular() {
		return failure("PATH_NOT_REGULAR")
	}
	if !os.SameFile(g.path.info, info) || info.Size() != g.size || !info.ModTime().Equal(g.path.info.ModTime()) {
		return failure("PATH_CHANGED")
	}
	if problem := replacementLinkCheck(g.file, info); problem != nil {
		return problem
	}
	if problem := g.path.recheckLeaf(info); problem != nil {
		return problem
	}
	return checkBindings(appendBindings(g.root.bindings, g.path.bindings))
}

// Close retains the first final-validation/cleanup failure on repeated calls.
// Cancellation does not prevent releasing handles. A failed Close invalidates
// the caller's verification/publication decision, even after successful reads.
func (g *BackupRead) Close() *Error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return g.closeResult
	}
	g.closeResult = g.check()
	if problem := g.release(); problem != nil {
		g.closeResult = problem
	}
	g.closed = true
	return g.closeResult
}

func (g *BackupRead) release() *Error {
	var problem *Error
	if g.file != nil {
		if g.file.Close() != nil {
			problem = failure("FILE_CLOSE")
		}
		g.file = nil
	}
	for i := len(g.directories) - 1; i >= 0; i-- {
		if g.directories[i].Close() != nil {
			problem = failure("ROOT_CLOSE")
		}
	}
	g.directories = nil
	if g.path != nil {
		if e := g.path.close(); e != nil {
			problem = e
		}
		g.path.handles = nil
	}
	return problem
}
