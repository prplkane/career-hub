package runtimepaths

import (
	"os"
)

// MaxWindowReadBytes bounds one polling read independently of the 16 MiB
// journal-segment limit. Callers must still bound their response and events.
const MaxWindowReadBytes int64 = 256 << 10

// Legacy diagnostics can predate segmented journals. The historical file is
// never loaded whole, but a finite ceiling prevents a hostile sparse file
// from becoming an unbounded cursor space.
const MaxLegacyLogBytes int64 = 1 << 30

type windowOperations struct {
	readAt func(*os.File, []byte, int64) (int, error)
}

// ReadSingleLinkWindow returns owned bytes from a fixed offset of one guarded
// regular file. It never truncates an oversized file to make it seem valid:
// the whole file must remain <= MaxReadBytes, even though at most 256 KiB is
// copied. An offset exactly at EOF returns an empty, present window. A changed
// path, size, mtime, identity or hardlink count returns no bytes. This is an
// observation, not a snapshot spanning separate calls or a write reservation.
func (r *Root) ReadSingleLinkWindow(relative string, offset, limit int64) ([]byte, int64, bool, *Error) {
	return r.readSingleLinkWindowWithMax(relative, offset, limit, MaxReadBytes, windowOperations{})
}

func (r *Root) readSingleLinkWindow(relative string, offset, limit int64, ops windowOperations) (content []byte, size int64, present bool, problem *Error) {
	return r.readSingleLinkWindowWithMax(relative, offset, limit, MaxReadBytes, ops)
}

// ReadSingleLinkLegacyLogWindow is reserved for the pre-migration diagnostics
// log. Its per-call byte bound and identity checks are identical to the native
// segment reader; only the total observed file-size ceiling differs.
func (r *Root) ReadSingleLinkLegacyLogWindow(relative string, offset, limit int64) ([]byte, int64, bool, *Error) {
	return r.readSingleLinkWindowWithMax(relative, offset, limit, MaxLegacyLogBytes, windowOperations{})
}

func (r *Root) readSingleLinkWindowWithMax(relative string, offset, limit, maxFileSize int64, ops windowOperations) (content []byte, size int64, present bool, problem *Error) {
	if r == nil {
		return nil, 0, false, failure("ROOT_CLOSED")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return nil, 0, false, failure("ROOT_CLOSED")
	}
	if offset < 0 || limit < 1 || limit > MaxWindowReadBytes {
		return nil, 0, false, failure("READ_LIMIT_INVALID")
	}
	checked, e := r.walk(relative)
	if checked != nil {
		defer func() {
			if closeError := checked.close(); closeError != nil {
				content, size, present, problem = nil, 0, false, closeError
			}
		}()
	}
	if e != nil {
		return nil, 0, false, e
	}
	bindings := appendBindings(r.bindings, checked.bindings)
	if !checked.present {
		if e := checkBindings(bindings); e != nil {
			return nil, 0, false, e
		}
		return nil, 0, false, nil
	}
	if checked.info.Size() > maxFileSize {
		return nil, 0, false, failure("FILE_SIZE_LIMIT")
	}
	f, err := checked.parent.OpenFile(checked.name, readOnlyFlags(), 0)
	if err != nil {
		return nil, 0, false, failure("FILE_OPEN")
	}
	defer func() {
		if f.Close() != nil {
			content, size, present, problem = nil, 0, false, failure("FILE_CLOSE")
		}
	}()
	before, err := f.Stat()
	if err != nil {
		return nil, 0, false, failure("FILE_STAT")
	}
	if isLinkLike(before) || !before.Mode().IsRegular() {
		return nil, 0, false, failure("PATH_NOT_REGULAR")
	}
	if !os.SameFile(checked.info, before) || checked.info.Size() != before.Size() ||
		!checked.info.ModTime().Equal(before.ModTime()) {
		return nil, 0, false, failure("PATH_CHANGED")
	}
	if e := replacementLinkCheck(f, before); e != nil {
		return nil, 0, false, e
	}
	if before.Size() > maxFileSize {
		return nil, 0, false, failure("FILE_SIZE_LIMIT")
	}
	if offset > before.Size() {
		return nil, 0, false, failure("READ_OFFSET_INVALID")
	}
	if e := checked.recheckLeaf(before); e != nil {
		return nil, 0, false, e
	}
	readLength := limit
	if remaining := before.Size() - offset; remaining < readLength {
		readLength = remaining
	}
	raw := make([]byte, int(readLength))
	if ops.readAt == nil {
		ops.readAt = (*os.File).ReadAt
	}
	if readLength > 0 {
		if n, err := ops.readAt(f, raw, offset); err != nil || n != len(raw) {
			return nil, 0, false, failure("FILE_READ")
		}
	}
	after, err := f.Stat()
	if err != nil {
		return nil, 0, false, failure("FILE_STAT")
	}
	if !after.Mode().IsRegular() || isLinkLike(after) || !os.SameFile(before, after) ||
		before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, 0, false, failure("PATH_CHANGED")
	}
	if e := replacementLinkCheck(f, after); e != nil {
		return nil, 0, false, e
	}
	if e := checked.recheckLeaf(after); e != nil {
		return nil, 0, false, e
	}
	if e := checkBindings(bindings); e != nil {
		return nil, 0, false, e
	}
	return raw, before.Size(), true, nil
}
