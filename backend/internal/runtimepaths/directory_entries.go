package runtimepaths

import (
	"errors"
	"io"
	"os"
	"sort"
	"time"

	"careerhub.local/backend/internal/textcompat"
)

// DirectoryEntry is an observation, not a retained file lease or a snapshot.
type DirectoryEntry struct {
	Name        string
	IsDirectory bool
	Size        int64
	Modified    time.Time
}

// ListDirectoryEntries observes ordinary files and directories in one bounded
// scan. Links, special files and portable name collisions fail closed. Callers
// must coordinate writers and retain/check content separately before publishing.
func (r *Root) ListDirectoryEntries(relative string) (entries []DirectoryEntry, present bool, problem *Error) {
	if r == nil {
		return nil, false, failure("ROOT_CLOSED")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return nil, false, failure("ROOT_CLOSED")
	}
	p, e := r.walkEntry(relative, true)
	if p != nil {
		defer func() {
			if err := p.close(); err != nil && problem == nil {
				entries, present, problem = nil, false, err
			}
		}()
	}
	if e != nil {
		return nil, false, e
	}
	if !p.present {
		return nil, false, nil
	}
	if !p.info.IsDir() {
		return nil, false, failure("PATH_NOT_DIRECTORY")
	}
	directory, err := p.parent.OpenRoot(p.name)
	if err != nil {
		return nil, false, failure("PATH_OPEN_DIRECTORY")
	}
	defer func() {
		if err := directory.Close(); err != nil && problem == nil {
			entries, present, problem = nil, false, failure("ROOT_CLOSE")
		}
	}()
	held, err := directory.Stat(".")
	if err != nil || isLinkLike(held) || !held.IsDir() || !os.SameFile(p.info, held) {
		return nil, false, failure("PATH_CHANGED")
	}
	bindings := appendBindings(r.bindings, p.bindings)
	bindings = append(bindings, binding{p.parent, p.name, held})
	file, err := directory.Open(".")
	if err != nil {
		return nil, false, failure("PATH_DIRECTORY_UNAVAILABLE")
	}
	defer func() {
		if err := file.Close(); err != nil && problem == nil {
			entries, present, problem = nil, false, failure("FILE_CLOSE")
		}
	}()
	entries = []DirectoryEntry{}
	seen := map[string]bool{}
	for {
		batch, readErr := file.ReadDir(256)
		for _, item := range batch {
			if len(entries) >= MaxDirectoryEntries {
				return nil, false, failure("PATH_DIRECTORY_LIMIT")
			}
			if e := validComponent(item.Name()); e != nil {
				return nil, false, e
			}
			normalized, e := normalizeName(item.Name())
			if e != nil {
				return nil, false, e
			}
			key := textcompat.Fold(normalized)
			if seen[key] {
				return nil, false, failure("PATH_COLLISION")
			}
			seen[key] = true
			before, err := item.Info()
			if err != nil {
				return nil, false, failure("PATH_CHANGED")
			}
			if isLinkLike(before) {
				return nil, false, failure("PATH_LINK")
			}
			if !before.IsDir() && !before.Mode().IsRegular() {
				return nil, false, failure("PATH_NOT_REGULAR")
			}
			if before.IsDir() {
				// Native enumeration can retain an older directory timestamp
				// after deep tree creation even when identity is unchanged. Bind
				// the listed identity, then compare two fresh rooted observations
				// rather than treating that stale index timestamp as live drift.
				fresh, err := directory.Lstat(item.Name())
				if err != nil || isLinkLike(fresh) || !fresh.IsDir() || !os.SameFile(before, fresh) {
					return nil, false, failure("PATH_CHANGED")
				}
				before = fresh
			}
			after, err := directory.Lstat(item.Name())
			if err != nil || isLinkLike(after) || before.Mode().Type() != after.Mode().Type() || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
				return nil, false, failure("PATH_CHANGED")
			}
			entries = append(entries, DirectoryEntry{Name: item.Name(), IsDirectory: after.IsDir(), Size: after.Size(), Modified: after.ModTime()})
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, false, failure("PATH_DIRECTORY_UNAVAILABLE")
		}
	}
	if e := checkBindings(bindings); e != nil {
		return nil, false, e
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, true, nil
}
