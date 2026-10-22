package runtimepaths

import "os"

// ListDirectoryNames returns bounded sorted names from one ordinary relative
// directory and reports whether that directory exists. The directory and all
// ancestors stay bound to the receiver's retained root handles. It reads no
// file content and creates nothing.
func (r *Root) ListDirectoryNames(relative string) ([]string, bool, *Error) {
	return r.listDirectoryNames(relative, false)
}

// ListDirectoryFileNames additionally requires every child to be an ordinary
// regular file, matching ListFileNames at a confined relative directory.
func (r *Root) ListDirectoryFileNames(relative string) ([]string, bool, *Error) {
	return r.listDirectoryNames(relative, true)
}

// ListDirectoryFileMetadata is the metadata-preserving counterpart of
// ListDirectoryFileNames. The fixed relative directory and every ancestor are
// retained through the receiver's root handles; every child must be an
// ordinary single-name regular file.
func (r *Root) ListDirectoryFileMetadata(relative string) ([]FileMetadata, bool, *Error) {
	return r.listDirectoryMetadata(relative, true)
}

func (r *Root) listDirectoryNames(relative string, regularFilesOnly bool) (names []string, present bool, problem *Error) {
	entries, present, problem := r.listDirectoryMetadata(relative, regularFilesOnly)
	if problem != nil || !present {
		return nil, present, problem
	}
	names = make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names, true, nil
}

func (r *Root) listDirectoryMetadata(relative string, regularFilesOnly bool) (entries []FileMetadata, present bool, problem *Error) {
	if r == nil {
		return nil, false, failure("ROOT_CLOSED")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return nil, false, failure("ROOT_CLOSED")
	}
	checked, e := r.walkEntry(relative, true)
	if checked != nil {
		defer func() {
			if closeError := checked.close(); closeError != nil && problem == nil {
				entries, present, problem = nil, false, closeError
			}
		}()
	}
	if e != nil {
		return nil, false, e
	}
	if !checked.present {
		return nil, false, nil
	}
	if !checked.info.IsDir() {
		return nil, false, failure("PATH_NOT_DIRECTORY")
	}
	child, err := checked.parent.OpenRoot(checked.name)
	if err != nil {
		return nil, false, failure("PATH_OPEN_DIRECTORY")
	}
	defer func() {
		if err := child.Close(); err != nil && problem == nil {
			entries, present, problem = nil, false, failure("ROOT_CLOSE")
		}
	}()
	held, err := child.Stat(".")
	if err != nil || isLinkLike(held) || !held.IsDir() || !os.SameFile(checked.info, held) {
		return nil, false, failure("PATH_CHANGED")
	}
	bindings := appendBindings(r.bindings, checked.bindings)
	bindings = append(bindings, binding{checked.parent, checked.name, held})
	nested := &Root{base: child, bindings: bindings}
	entries, e = nested.listEntries(regularFilesOnly)
	if e != nil {
		return nil, false, e
	}
	return entries, true, nil
}
