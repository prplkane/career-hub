package runtimepaths

import (
	"errors"
	"io/fs"
	"os"
)

// DirectoryMoveOutcome records whether this call moved the owned source
// directory. Unknown is deliberately not retryable: recovery must inspect the
// journal and both names instead of repeating a possibly committed move.
type DirectoryMoveOutcome string

const (
	DirectoryMoveNotMoved DirectoryMoveOutcome = "not_moved"
	DirectoryMoveMoved    DirectoryMoveOutcome = "moved"
	DirectoryMoveUnknown  DirectoryMoveOutcome = "unknown"
)

type directoryMoveNameState uint8

const (
	directoryMoveNameUnknown directoryMoveNameState = iota
	directoryMoveNameAbsent
	directoryMoveNameForeign
	directoryMoveNameOwned
)

type directoryMoveOperations struct {
	open       func(*os.Root, string, fs.FileInfo) (*os.File, *Error)
	move       func(*os.File, *os.Root, string) error
	close      func(*os.File) error
	beforeMove func()
}

// MoveDirectoryNoReplace moves one ordinary directory between existing
// confined parents. The destination must not exist and is never replaced or
// removed. A process ownership claim for this exact Root is mandatory.
//
// A Moved result remains committed even when problem is non-nil. Unknown means
// the native call was attempted but neither retained name proved the outcome;
// callers must stop and enter explicit recovery rather than retrying.
func (r *Root) MoveDirectoryNoReplace(source, target string) (DirectoryMoveOutcome, *Error) {
	return r.moveDirectoryNoReplace(source, target, directoryMoveOperations{})
}

func (r *Root) moveDirectoryNoReplace(source, target string, ops directoryMoveOperations) (outcome DirectoryMoveOutcome, problem *Error) {
	if !directoryMoveSupported {
		return DirectoryMoveNotMoved, failure("DIRECTORY_MOVE_PLATFORM_UNSUPPORTED")
	}
	if r == nil {
		return DirectoryMoveNotMoved, failure("ROOT_CLOSED")
	}
	// Keep lock order consistent with ClaimForProcess: ownership, then Root.
	// A published claim cannot be released or replaced before process exit.
	if e := r.RequireProcessOwnership(); e != nil {
		return DirectoryMoveNotMoved, e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return DirectoryMoveNotMoved, failure("ROOT_CLOSED")
	}

	sourceParts, e := relativeComponents(source)
	if e != nil {
		return DirectoryMoveNotMoved, e
	}
	targetParts, e := relativeComponents(target)
	if e != nil {
		return DirectoryMoveNotMoved, e
	}
	if componentPrefix(sourceParts, targetParts) {
		return DirectoryMoveNotMoved, failure("DIRECTORY_MOVE_INVALID")
	}

	sourcePath, e := r.walkEntry(source, true)
	if sourcePath != nil {
		defer func() {
			if closeProblem := sourcePath.close(); closeProblem != nil && problem == nil {
				problem = closeProblem
			}
		}()
	}
	if e != nil {
		return DirectoryMoveNotMoved, e
	}
	if !sourcePath.present {
		return DirectoryMoveNotMoved, failure("DIRECTORY_MISSING")
	}
	if !sourcePath.info.IsDir() {
		return DirectoryMoveNotMoved, failure("PATH_NOT_DIRECTORY")
	}

	targetPath, e := r.directoryMoveTarget(targetParts)
	if targetPath != nil {
		defer func() {
			if closeProblem := targetPath.close(); closeProblem != nil && problem == nil {
				problem = closeProblem
			}
		}()
	}
	if e != nil {
		return DirectoryMoveNotMoved, e
	}
	if targetPath.present {
		return DirectoryMoveNotMoved, failure("DIRECTORY_EXISTS")
	}

	bindings := appendBindings(r.bindings, sourcePath.bindings)
	bindings = appendBindings(bindings, targetPath.bindings)
	if e = checkBindings(bindings); e != nil {
		return DirectoryMoveNotMoved, e
	}
	if _, present, checkProblem := portableChild(targetPath.parent, targetPath.name); checkProblem != nil {
		return DirectoryMoveNotMoved, checkProblem
	} else if present {
		return DirectoryMoveNotMoved, failure("DIRECTORY_EXISTS")
	}

	if ops.open == nil {
		ops.open = openOwnedMoveDirectory
	}
	if ops.move == nil {
		ops.move = moveOwnedDirectory
	}
	if ops.close == nil {
		ops.close = (*os.File).Close
	}
	owned, openProblem := ops.open(sourcePath.parent, sourcePath.name, sourcePath.info)
	if openProblem != nil {
		return DirectoryMoveNotMoved, openProblem
	}
	defer func() {
		if closeError := ops.close(owned); closeError != nil && problem == nil {
			problem = failure("FILE_CLOSE")
		}
	}()
	if ops.beforeMove != nil {
		ops.beforeMove()
	}
	if e = checkBindings(bindings); e != nil {
		return DirectoryMoveNotMoved, e
	}

	nativeError := ops.move(owned, targetPath.parent, targetPath.name)
	identity, statError := owned.Stat()
	if statError != nil || isLinkLike(identity) || !identity.IsDir() || !os.SameFile(sourcePath.info, identity) {
		identity = nil
	}
	sourceState, sourceObservation := observeMovedDirectoryName(sourcePath.parent, sourcePath.name, identity)
	targetState, targetObservation := observeMovedDirectoryName(targetPath.parent, targetPath.name, identity)
	outcome = classifyDirectoryMove(sourceState, targetState)

	if e = checkBindings(bindings); e != nil {
		return outcome, e
	}
	if nativeError != nil {
		if outcome == DirectoryMoveNotMoved && directoryMoveCollision(nativeError) {
			return outcome, failure("DIRECTORY_EXISTS")
		}
		return outcome, failure("DIRECTORY_MOVE")
	}
	if identity == nil || sourceObservation != nil || targetObservation != nil {
		return outcome, failure("DIRECTORY_MOVE_VERIFY")
	}
	if outcome != DirectoryMoveMoved || sourceState != directoryMoveNameAbsent || targetState != directoryMoveNameOwned {
		return outcome, failure("DIRECTORY_MOVE_VERIFY")
	}
	return DirectoryMoveMoved, nil
}

func componentPrefix(parent, candidate []string) bool {
	if len(candidate) <= len(parent) {
		return false
	}
	for i := range parent {
		if parent[i] != candidate[i] {
			return false
		}
	}
	return true
}

func (r *Root) directoryMoveTarget(parts []string) (*checkedPath, *Error) {
	if e := checkBindings(r.bindings); e != nil {
		return nil, e
	}
	p := &checkedPath{parent: r.base}
	for _, component := range parts[:len(parts)-1] {
		name, present, e := portableChild(p.parent, component)
		if e != nil {
			return p, e
		}
		if !present {
			return p, failure("PATH_PARENT_MISSING")
		}
		if e := descendCreation(p, name); e != nil {
			return p, e
		}
	}
	p.name = parts[len(parts)-1]
	name, present, e := portableChild(p.parent, p.name)
	if e != nil {
		return p, e
	}
	if !present {
		return p, checkBindings(appendBindings(r.bindings, p.bindings))
	}
	info, err := p.parent.Lstat(name)
	if err != nil {
		return p, failure("PATH_STAT")
	}
	if isLinkLike(info) {
		return p, failure("PATH_LINK")
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return p, failure("PATH_NOT_REGULAR")
	}
	p.name, p.info, p.present = name, info, true
	return p, checkBindings(appendBindings(r.bindings, p.bindings))
}

func observeMovedDirectoryName(parent *os.Root, name string, identity fs.FileInfo) (directoryMoveNameState, *Error) {
	observed, present, e := portableChild(parent, name)
	if e != nil {
		return directoryMoveNameUnknown, e
	}
	if !present {
		return directoryMoveNameAbsent, nil
	}
	info, err := parent.Lstat(observed)
	if err != nil {
		return directoryMoveNameUnknown, failure("PATH_CHANGED")
	}
	if identity != nil && !isLinkLike(info) && info.IsDir() && os.SameFile(identity, info) {
		return directoryMoveNameOwned, nil
	}
	return directoryMoveNameForeign, nil
}

func classifyDirectoryMove(source, target directoryMoveNameState) DirectoryMoveOutcome {
	if target == directoryMoveNameOwned {
		return DirectoryMoveMoved
	}
	if source == directoryMoveNameOwned {
		return DirectoryMoveNotMoved
	}
	return DirectoryMoveUnknown
}

func directoryMoveCollision(err error) bool {
	return errors.Is(err, fs.ErrExist) || nativeDirectoryMoveCollision(err)
}
