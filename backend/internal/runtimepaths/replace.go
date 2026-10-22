package runtimepaths

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
)

// ReplaceFile replaces one existing regular, single-link file through retained
// parents. It never creates missing parents or bootstraps a missing target.
// Both byte slices must remain unchanged until return and be <= MaxReadBytes.
// Expected bytes are compared twice, not atomically with a cross-process writer.
// A true committed result must not be retried as an uncommitted failure: rename
// succeeded (or its effect was observed), even if later checks/cleanup failed.
// File Sync is not directory durability or a Windows atomic-rename guarantee.
func (r *Root) ReplaceFile(relative string, expected, replacement []byte) (committed bool, problem *Error) {
	return r.replaceFile(relative, expected, replacement, replaceOperations{})
}

// Per-call fault seams keep production and concurrent Root instances independent.
// A close override must close the real file even when reporting a synthetic error.
type replaceOperations struct {
	write  func(*os.File, []byte) (int, error)
	sync   func(*os.File) error
	close  func(*os.File) error
	rename func(*os.Root, string, string) error
}

func (r *Root) replaceFile(relative string, expected, replacement []byte, ops replaceOperations) (committed bool, problem *Error) {
	if r == nil {
		return false, failure("ROOT_CLOSED")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false, failure("ROOT_CLOSED")
	}
	if int64(len(expected)) > MaxReadBytes || int64(len(replacement)) > MaxReadBytes {
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
	original, problem := readReplacementBytes(checked, checked.info, expected)
	if problem != nil {
		return false, problem
	}
	if problem = replacementBindings(checked, bindings); problem != nil {
		return false, problem
	}
	if ops.write == nil {
		ops.write = (*os.File).Write
	}
	if ops.sync == nil {
		ops.sync = (*os.File).Sync
	}
	if ops.close == nil {
		ops.close = (*os.File).Close
	}
	if ops.rename == nil {
		ops.rename = (*os.Root).Rename
	}

	var stage *os.File
	var stageName string
	for attempt := 0; attempt < 8; attempt++ {
		stageName = ".careerhub-replace-" + rand.Text() + ".tmp"
		var err error
		stage, err = checked.parent.OpenFile(stageName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return false, failure("FILE_STAGE")
		}
	}
	if stage == nil {
		return false, failure("FILE_STAGE")
	}
	closed := false
	var stageIdentity fs.FileInfo
	defer func() {
		if !closed {
			if err := ops.close(stage); err != nil && problem == nil {
				problem = failure("FILE_CLOSE")
			}
		}
		if e := removeOwnedStage(checked.parent, stageName, stageIdentity); e != nil && problem == nil {
			problem = e
		}
	}()
	var err error
	stageIdentity, err = stage.Stat()
	if err != nil {
		return false, failure("FILE_STAT")
	}
	if isLinkLike(stageIdentity) || !stageIdentity.Mode().IsRegular() {
		return false, failure("PATH_NOT_REGULAR")
	}
	if problem = replacementLinkCheck(stage, stageIdentity); problem != nil {
		return false, problem
	}
	if n, err := ops.write(stage, replacement); err != nil || n != len(replacement) {
		return false, failure("FILE_WRITE")
	}
	if err := ops.sync(stage); err != nil {
		return false, failure("FILE_SYNC")
	}
	closed = true // Close consumes the file even when it reports an OS error.
	if err := ops.close(stage); err != nil {
		return false, failure("FILE_CLOSE")
	}
	staged := &checkedPath{parent: checked.parent, name: stageName, info: stageIdentity, present: true}
	if _, problem = readReplacementBytes(staged, stageIdentity, replacement); problem != nil {
		if problem.Code == "FILE_CONFLICT" {
			problem = failure("FILE_STAGE")
		}
		return false, problem
	}
	if _, problem = readReplacementBytes(checked, original, expected); problem != nil {
		return false, problem
	}
	if problem = replacementBindings(checked, bindings); problem != nil {
		return false, problem
	}
	if problem = staged.recheckLeaf(stageIdentity); problem != nil {
		return false, problem
	}
	if err := ops.rename(checked.parent, stageName, checked.name); err != nil {
		// Preserve an observed commit even on a non-Unix/ambiguous rename error.
		if now, statErr := checked.parent.Lstat(checked.name); statErr == nil && os.SameFile(stageIdentity, now) {
			committed = true
		}
		return committed, failure("FILE_RENAME")
	}
	committed = true
	if _, problem = readReplacementBytes(checked, stageIdentity, replacement); problem != nil {
		return true, problem
	}
	if problem = replacementBindings(checked, bindings); problem != nil {
		return true, problem
	}
	return true, nil
}

// All leaf opens are read-only. Only the exclusively created stage receives
// writes; the destination name is replaced, never followed by a write open.
func readReplacementBytes(path *checkedPath, identity fs.FileInfo, expected []byte) (observed fs.FileInfo, problem *Error) {
	file, err := path.parent.OpenFile(path.name, readOnlyFlags(), 0)
	if err != nil {
		return nil, failure("FILE_OPEN")
	}
	defer func() {
		if err := file.Close(); err != nil && problem == nil {
			problem = failure("FILE_CLOSE")
		}
	}()
	before, err := file.Stat()
	if err != nil {
		return nil, failure("FILE_STAT")
	}
	if isLinkLike(before) || !before.Mode().IsRegular() {
		return nil, failure("PATH_NOT_REGULAR")
	}
	if !os.SameFile(identity, before) {
		return nil, failure("PATH_CHANGED")
	}
	if problem := replacementLinkCheck(file, before); problem != nil {
		return nil, problem
	}
	if before.Size() > MaxReadBytes {
		return nil, failure("FILE_SIZE_LIMIT")
	}
	if problem := path.recheckLeaf(before); problem != nil {
		return nil, problem
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxReadBytes+1))
	if err != nil {
		return nil, failure("FILE_READ")
	}
	if int64(len(raw)) > MaxReadBytes {
		return nil, failure("FILE_SIZE_LIMIT")
	}
	after, err := file.Stat()
	if err != nil {
		return nil, failure("FILE_STAT")
	}
	if isLinkLike(after) || !after.Mode().IsRegular() || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, failure("PATH_CHANGED")
	}
	if problem := replacementLinkCheck(file, after); problem != nil {
		return nil, problem
	}
	if problem := path.recheckLeaf(after); problem != nil {
		return nil, problem
	}
	if !bytes.Equal(raw, expected) {
		return nil, failure("FILE_CONFLICT")
	}
	return after, nil
}

func replacementBindings(path *checkedPath, bindings []binding) *Error {
	if problem := checkBindings(bindings); problem != nil {
		return problem
	}
	normalized, problem := normalizeName(path.name)
	if problem != nil {
		return problem
	}
	name, present, problem := portableChild(path.parent, normalized)
	if problem != nil {
		return problem
	}
	if !present || name != path.name {
		return failure("PATH_CHANGED")
	}
	return nil
}

func removeOwnedStage(parent *os.Root, name string, identity fs.FileInfo) *Error {
	current, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	// If identity is unavailable or the name changed ownership, do not remove an
	// unproven entry. A failed operation may then leave its bounded stage behind.
	if err != nil || identity == nil || isLinkLike(current) || !current.Mode().IsRegular() || !os.SameFile(identity, current) {
		return failure("FILE_CLEANUP")
	}
	if err := parent.Remove(name); err != nil {
		return failure("FILE_CLEANUP")
	}
	return nil
}
