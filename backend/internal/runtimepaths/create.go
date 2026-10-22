package runtimepaths

import (
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"careerhub.local/backend/internal/textcompat"
)

// OpenOrCreate opens an explicit local root and creates missing ordinary
// directories. The boolean conservatively records that directory creation may
// have occurred, including on failure. It never removes partial directories or
// applies permissions. The bootstrap caller must harden before private content.
// This creation pilot refuses non-Windows platforms before filesystem writes.
func OpenOrCreate(root string) (*Root, bool, *Error) { return open(root, true) }

func ensureChildDirectory(parent *os.Root, component string) (string, bool, *Error) {
	normalized, e := normalizeName(component)
	if e != nil {
		return "", false, e
	}
	name, present, e := portableChild(parent, normalized)
	if e != nil {
		return "", false, e
	}
	changed := false
	if !present {
		name = component
		// Windows ignores these Unix mode bits and uses ordinary DACL inheritance.
		// Do not supply a custom descriptor or change an existing directory's ACL.
		changed = true
		if err := parent.Mkdir(name, 0777); err != nil && !errors.Is(err, fs.ErrExist) {
			return "", changed, failure("DIRECTORY_CREATE")
		}
		observed, found, e := portableChild(parent, normalized)
		if e != nil {
			return "", changed, e
		}
		if !found || observed != name {
			return "", changed, failure("PATH_CHANGED")
		}
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return "", changed, failure("PATH_STAT")
	}
	if isLinkLike(info) {
		return "", changed, failure("PATH_LINK")
	}
	if !info.IsDir() {
		return "", changed, failure("PATH_NOT_DIRECTORY")
	}
	return name, changed, nil
}

// EnsureDirectory creates only missing ordinary descendants. Existing directory
// ACLs are retained. A true boolean on failure means partial creation may remain.
func (r *Root) EnsureDirectory(relative string) (changed bool, problem *Error) {
	if !creationSupported {
		return false, failure("CREATE_PLATFORM_UNSUPPORTED")
	}
	if r == nil {
		return false, failure("ROOT_CLOSED")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false, failure("ROOT_CLOSED")
	}
	parts, e := relativeComponents(relative)
	if e != nil {
		return false, e
	}
	if e = checkBindings(r.bindings); e != nil {
		return false, e
	}
	p := &checkedPath{parent: r.base}
	defer func() {
		if e := closeCreationParents(p); e != nil && problem == nil {
			problem = e
		}
	}()
	for _, component := range parts {
		name, created, e := ensureChildDirectory(p.parent, component)
		changed = changed || created
		if e != nil {
			return changed, e
		}
		if e = descendCreation(p, name); e != nil {
			return changed, e
		}
		if e = checkBindings(appendBindings(r.bindings, p.bindings)); e != nil {
			return changed, e
		}
	}
	return changed, nil
}

func descendCreation(p *checkedPath, name string) *Error {
	before, err := p.parent.Lstat(name)
	if err != nil {
		return failure("PATH_STAT")
	}
	if isLinkLike(before) {
		return failure("PATH_LINK")
	}
	if !before.IsDir() {
		return failure("PATH_NOT_DIRECTORY")
	}
	child, err := p.parent.OpenRoot(name)
	if err != nil {
		return failure("PATH_OPEN_DIRECTORY")
	}
	p.handles = append(p.handles, child)
	held, err := child.Stat(".")
	if err != nil {
		return failure("PATH_STAT")
	}
	if isLinkLike(held) || !held.IsDir() {
		return failure("PATH_LINK")
	}
	if !os.SameFile(before, held) {
		return failure("PATH_CHANGED")
	}
	p.bindings = append(p.bindings, binding{p.parent, name, held})
	p.parent = child
	return nil
}

func closeCreationParents(p *checkedPath) *Error {
	var e *Error
	for i := len(p.handles) - 1; i >= 0; i-- {
		if err := p.handles[i].Close(); err != nil {
			e = failure("FILE_CLOSE")
		}
	}
	return e
}

// ListNames returns bounded sorted top-level names, not file content. Names are
// not rewritten. Portable Unicode/case collisions and unreadable scans fail.
func (r *Root) ListNames() (names []string, problem *Error) {
	return r.listNames(false)
}

// ListFileNames additionally requires each entry to be an ordinary regular
// file and rechecks its identity without opening/reading its contents. One
// bounded scan avoids calling Check for each name (which rescans siblings).
// This is not a filesystem snapshot; cooperating writers must be coordinated.
func (r *Root) ListFileNames() ([]string, *Error) {
	return r.listNames(true)
}

func (r *Root) listNames(regularFilesOnly bool) (names []string, problem *Error) {
	entries, problem := r.listEntries(regularFilesOnly)
	if problem != nil {
		return nil, problem
	}
	names = make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names, nil
}

// FileMetadata is an observation, not a file handle or a content revision.
type FileMetadata struct {
	Name     string
	Size     int64
	Modified time.Time
}

// ListFileMetadata retains ListFileNames' confinement and regular-file policy.
// A single bounded scan supplies ordering metadata without rescanning siblings
// once per file. Callers must coordinate writers and recheck before publication.
func (r *Root) ListFileMetadata() ([]FileMetadata, *Error) {
	return r.listEntries(true)
}

func (r *Root) listEntries(regularFilesOnly bool) (entriesOut []FileMetadata, problem *Error) {
	if r == nil {
		return nil, failure("ROOT_CLOSED")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return nil, failure("ROOT_CLOSED")
	}
	if e := checkBindings(r.bindings); e != nil {
		return nil, e
	}
	f, err := r.base.Open(".")
	if err != nil {
		return nil, failure("PATH_DIRECTORY_UNAVAILABLE")
	}
	defer func() {
		if err := f.Close(); err != nil && problem == nil {
			entriesOut = nil
			problem = failure("FILE_CLOSE")
		}
	}()
	entriesOut = []FileMetadata{}
	seen := map[string]bool{}
	for {
		entries, err := f.ReadDir(256)
		for _, entry := range entries {
			normalized, e := normalizeName(entry.Name())
			if e != nil {
				return nil, e
			}
			key := textcompat.Fold(normalized)
			if seen[key] {
				return nil, failure("PATH_COLLISION")
			}
			metadata := FileMetadata{Name: entry.Name()}
			if regularFilesOnly {
				before, err := entry.Info()
				if err != nil {
					return nil, failure("PATH_CHANGED")
				}
				if isLinkLike(before) {
					return nil, failure("PATH_LINK")
				}
				if !before.Mode().IsRegular() {
					return nil, failure("PATH_NOT_REGULAR")
				}
				after, err := r.base.Lstat(entry.Name())
				if err != nil || isLinkLike(after) || !after.Mode().IsRegular() || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
					return nil, failure("PATH_CHANGED")
				}
				metadata.Size, metadata.Modified = after.Size(), after.ModTime()
			}
			seen[key] = true
			entriesOut = append(entriesOut, metadata)
		}
		if len(entriesOut) > MaxDirectoryEntries {
			return nil, failure("PATH_DIRECTORY_LIMIT")
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, failure("PATH_DIRECTORY_UNAVAILABLE")
		}
	}
	if e := checkBindings(r.bindings); e != nil {
		return nil, e
	}
	sort.Slice(entriesOut, func(i, j int) bool { return entriesOut[i].Name < entriesOut[j].Name })
	return entriesOut, nil
}

// CreateFile publishes one new regular file without ever replacing an existing
// name. Parents must exist. Content is opaque and must not be mutated by callers
// until return. A true boolean means publication occurred even if a later check
// or close failed; do not automatically retry such a result as uncommitted.
func (r *Root) CreateFile(relative string, content []byte) (bool, *Error) {
	return r.createFile(relative, content, createOperations{})
}

// CreateObjectFile preserves CreateFile's no-replace and confinement contract
// with the separate object ceiling used by encrypted documents. It does not
// weaken CreateFile's smaller metadata limit.
func (r *Root) CreateObjectFile(relative string, content []byte) (bool, *Error) {
	return r.createObjectFile(relative, content, createOperations{})
}

type createOperations struct {
	write         func(*os.File, []byte) (int, error)
	sync          func(*os.File) error
	close         func(*os.File) error // must consume the real file even when failing
	beforePublish func(*os.Root, string, string)
	afterPublish  func() *Error
}

func (r *Root) createFile(relative string, content []byte, ops createOperations) (created bool, problem *Error) {
	return r.createFileWithLimit(relative, content, MaxReadBytes, ops)
}

func (r *Root) createObjectFile(relative string, content []byte, ops createOperations) (created bool, problem *Error) {
	return r.createFileWithLimit(relative, content, MaxObjectReadBytes, ops)
}

func (r *Root) createFileWithLimit(relative string, content []byte, limit int64, ops createOperations) (created bool, problem *Error) {
	if !creationSupported {
		return false, failure("CREATE_PLATFORM_UNSUPPORTED")
	}
	if r == nil {
		return false, failure("ROOT_CLOSED")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false, failure("ROOT_CLOSED")
	}
	if limit != MaxReadBytes && limit != MaxObjectReadBytes || int64(len(content)) > limit {
		return false, failure("WRITE_LIMIT_INVALID")
	}
	p, e := r.creationTarget(relative)
	if p != nil {
		defer func() {
			if e := closeCreationParents(p); e != nil && problem == nil {
				problem = e
			}
		}()
	}
	if e != nil {
		return false, e
	}
	if p.present {
		return false, failure("FILE_EXISTS")
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
	stage, name, identity, e := newCreationStage(p.parent)
	if e != nil {
		return false, e
	}
	closed := false
	defer func() {
		if !closed {
			if err := ops.close(stage); err != nil && problem == nil {
				problem = failure("FILE_CLOSE")
			}
		}
		if e := cleanupCreationStage(p.parent, name, identity); e != nil && problem == nil {
			problem = e
		}
	}()
	if n, err := ops.write(stage, content); err != nil || n != len(content) {
		return false, failure("FILE_WRITE")
	}
	if err := ops.sync(stage); err != nil {
		return false, failure("FILE_SYNC")
	}
	closed = true
	if err := ops.close(stage); err != nil {
		return false, failure("FILE_CLOSE")
	}
	if ops.beforePublish != nil {
		ops.beforePublish(p.parent, name, p.name)
	}
	if e := checkBindings(appendBindings(r.bindings, p.bindings)); e != nil {
		return false, e
	}
	// Repeat the portable collision check; native no-replace handles a later
	// exact-name intervening creator, without weakening this into check+replace.
	if _, present, e := portableChild(p.parent, p.name); e != nil {
		return false, e
	} else if present {
		return false, failure("FILE_EXISTS")
	}
	created, problem = publishCreationBounded(p.parent, name, p.name, identity, content, limit)
	if problem != nil {
		return created, problem
	}
	if ops.afterPublish != nil {
		if e := ops.afterPublish(); e != nil {
			return true, e
		}
	}
	if e := checkBindings(appendBindings(r.bindings, p.bindings)); e != nil {
		return true, e
	}
	return true, nil
}

func (r *Root) creationTarget(relative string) (*checkedPath, *Error) {
	parts, e := relativeComponents(relative)
	if e != nil {
		return nil, e
	}
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
	if present {
		p.name, p.present = name, true
		p.info, _ = p.parent.Lstat(name)
		if p.info == nil {
			return p, failure("PATH_STAT")
		}
		if isLinkLike(p.info) {
			return p, failure("PATH_LINK")
		}
		if !p.info.Mode().IsRegular() {
			return p, failure("PATH_NOT_REGULAR")
		}
	}
	return p, checkBindings(appendBindings(r.bindings, p.bindings))
}

func newCreationStage(parent *os.Root) (*os.File, string, fs.FileInfo, *Error) {
	for attempt := 0; attempt < 8; attempt++ {
		name := ".careerhub-create-" + rand.Text() + ".tmp"
		f, err := parent.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", nil, failure("FILE_STAGE")
		}
		info, err := f.Stat()
		if err != nil || isLinkLike(info) || !info.Mode().IsRegular() {
			_ = f.Close() // No proven identity: retain rather than delete a name.
			return nil, "", nil, failure("FILE_STAT")
		}
		if e := replacementLinkCheck(f, info); e != nil {
			_ = f.Close()
			return nil, "", nil, e
		}
		return f, name, info, nil
	}
	return nil, "", nil, failure("FILE_STAGE")
}

// ProbeWritable creates and closes one random empty stage, then removes only
// the verified owned file by native handle. No foreign path is deleted.
func (r *Root) ProbeWritable() (problem *Error) {
	if !creationSupported {
		return failure("CREATE_PLATFORM_UNSUPPORTED")
	}
	if r == nil {
		return failure("ROOT_CLOSED")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return failure("ROOT_CLOSED")
	}
	if e := checkBindings(r.bindings); e != nil {
		return e
	}
	f, name, identity, e := newCreationStage(r.base)
	if e != nil {
		return e
	}
	if err := f.Close(); err != nil {
		problem = failure("FILE_CLOSE")
	}
	if e := cleanupCreationStage(r.base, name, identity); e != nil && problem == nil {
		problem = e
	}
	if e := checkBindings(r.bindings); e != nil && problem == nil {
		problem = e
	}
	return problem
}

// The name validation is shared with paths.go; native helpers accept only one
// component, never an absolute path or a path to follow outside the held parent.
func creationBasename(name string) bool {
	return validComponent(name) == nil && !strings.ContainsAny(name, `/\`)
}
