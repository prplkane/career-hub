package runtimepaths

import (
	"errors"
	"io/fs"
	"os"
)

// RemoveFile removes one existing regular single-link file only after matching
// its exact bytes through retained parents. No recursive removal or directory
// creation occurs. removed stays true after an observed deletion even if later
// checks/cleanup fail. Callers still need ownership/writer coordination; the
// comparison and removal are not a cross-process atomic compare-and-delete.
func (r *Root) RemoveFile(relative string, expected []byte) (removed bool, problem *Error) {
	return r.removeFile(relative, expected, (*os.Root).Remove)
}

func (r *Root) removeFile(relative string, expected []byte, remove func(*os.Root, string) error) (removed bool, problem *Error) {
	if r == nil {
		return false, failure("ROOT_CLOSED")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false, failure("ROOT_CLOSED")
	}
	if int64(len(expected)) > MaxReadBytes || remove == nil {
		return false, failure("WRITE_LIMIT_INVALID")
	}
	checked, problem := r.walk(relative)
	if checked != nil {
		defer func() {
			for i := len(checked.handles) - 1; i >= 0; i-- {
				if err := checked.handles[i].Close(); err != nil && problem == nil {
					problem = failure("FILE_CLOSE")
				}
			}
		}()
	}
	if problem != nil {
		return false, problem
	}
	if !checked.present {
		return false, failure("FILE_MISSING")
	}
	bindings := appendBindings(r.bindings, checked.bindings)
	identity, e := readReplacementBytes(checked, checked.info, expected)
	if e != nil {
		return false, e
	}
	if e = replacementBindings(checked, bindings); e != nil {
		return false, e
	}
	if _, e = readReplacementBytes(checked, identity, expected); e != nil {
		return false, e
	}
	if e = replacementBindings(checked, bindings); e != nil {
		return false, e
	}
	if err := remove(checked.parent, checked.name); err != nil {
		if _, statErr := checked.parent.Lstat(checked.name); errors.Is(statErr, fs.ErrNotExist) {
			removed = true
		}
		return removed, failure("FILE_REMOVE")
	}
	removed = true
	if e = checkBindings(bindings); e != nil {
		return true, e
	}
	name, e := normalizeName(checked.name)
	if e != nil {
		return true, e
	}
	_, present, e := portableChild(checked.parent, name)
	if e != nil {
		return true, e
	}
	if present {
		return true, failure("PATH_CHANGED")
	}
	return true, nil
}
