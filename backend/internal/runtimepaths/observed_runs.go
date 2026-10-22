package runtimepaths

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"sort"
	"sync"
	"time"

	"careerhub.local/backend/internal/textcompat"
)

// ObservedRuns is bound to its parent Root. It never exposes a child root or
// an absolute path, and becomes unusable when that parent closes.
type ObservedRuns struct {
	mu          sync.RWMutex
	leavesMu    sync.Mutex
	root        *Root
	names       []string
	children    map[string]fs.FileInfo
	leaves      map[runLeaf]leafWitness
	parentScans int // counted full parent scans; child reads never increment it
	closeChild  func(*os.Root) error
}
type runLeaf struct{ child, leaf string }
type leafWitness struct {
	present  bool
	info     fs.FileInfo
	size     int64
	modified time.Time
}

// ObservedFile carries the bytes and mtime from one guarded leaf observation.
// Missing leaves have zero-value metadata. A failed read returns no observation.
type ObservedFile struct {
	Content  []byte
	Present  bool
	Modified time.Time
}

var runLeaves = map[string]bool{"extracted.yaml": true, "review.yaml": true, "approval_result.yaml": true}

type ObservedMetadata struct {
	Present  bool
	Size     int64
	Modified time.Time
}

// ChildMetadata observes a fixed leaf without reading its contents. A later
// ReadChildFile and final Recheck must agree with this pagination witness.
func (o *ObservedRuns) ChildMetadata(name, leaf string) (result ObservedMetadata, problem *Error) {
	if o == nil || o.root == nil {
		return result, failure("ROOT_CLOSED")
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	r := o.root
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return result, failure("ROOT_CLOSED")
	}
	if !runLeaves[leaf] {
		return result, failure("PATH_INVALID")
	}
	if e := checkBindings(r.bindings); e != nil {
		return result, e
	}
	child, e := o.child(name)
	if e != nil {
		return result, e
	}
	defer func() {
		if o.closeChild(child) != nil {
			result, problem = ObservedMetadata{}, failure("ROOT_CLOSE")
		}
	}()
	first, e := observeLeaf(child, leaf)
	if e != nil {
		return result, e
	}
	last, e := observeLeaf(child, leaf)
	if e != nil {
		return result, e
	}
	if !sameLeaf(first, last) {
		return result, failure("PATH_CHANGED")
	}
	if e := checkBindings(appendBindings(r.bindings, []binding{{parent: r.base, name: name, identity: o.children[name]}})); e != nil {
		return result, e
	}
	o.leavesMu.Lock()
	defer o.leavesMu.Unlock()
	key := runLeaf{name, leaf}
	if old, found := o.leaves[key]; found && !sameLeaf(old, last) {
		return result, failure("PATH_CHANGED")
	}
	o.leaves[key] = last
	return ObservedMetadata{last.present, last.size, last.modified}, nil
}

// Intake runs contain a small fixed artifact set. Capping each child scan
// prevents a 10,000-run request from multiplying 10,000-entry child scans.
const MaxRunArtifactEntries = 128

// ObserveRunDirectories scans an already-open intake_runs root once. All
// entries must be ordinary directories with collision-free portable names.
func (r *Root) ObserveRunDirectories() (*ObservedRuns, *Error) {
	if r == nil {
		return nil, failure("ROOT_CLOSED")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return nil, failure("ROOT_CLOSED")
	}
	entries, e := scanRunDirectories(r)
	if e != nil {
		return nil, e
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return &ObservedRuns{root: r, names: names, children: entries, leaves: map[runLeaf]leafWitness{}, parentScans: 1, closeChild: (*os.Root).Close}, nil
}

// Names returns a copy of the exact observed child names in sorted order.
func (o *ObservedRuns) Names() []string {
	if o == nil {
		return nil
	}
	return append([]string(nil), o.names...)
}

func scanRunDirectories(r *Root) (result map[string]fs.FileInfo, problem *Error) {
	if e := checkBindings(r.bindings); e != nil {
		return nil, e
	}
	directory, err := r.base.Open(".")
	if err != nil {
		return nil, failure("PATH_DIRECTORY_UNAVAILABLE")
	}
	defer func() {
		if directory.Close() != nil {
			result = nil
			problem = failure("FILE_CLOSE")
		}
	}()
	result = map[string]fs.FileInfo{}
	folded := map[string]bool{}
	for {
		entries, err := directory.ReadDir(256)
		for _, entry := range entries {
			name := entry.Name()
			if isPlaceholderFile(entry) {
				continue
			}
			normalized, e := observedRunName(name)
			if e != nil {
				return nil, e
			}
			key := textcompat.Fold(normalized)
			if folded[key] {
				return nil, failure("PATH_COLLISION")
			}
			folded[key] = true
			before, e2 := entry.Info()
			if e2 != nil {
				return nil, failure("PATH_CHANGED")
			}
			if isLinkLike(before) {
				return nil, failure("PATH_LINK")
			}
			if !before.IsDir() {
				return nil, failure("PATH_NOT_DIRECTORY")
			}
			after, e2 := r.base.Lstat(name)
			if e2 != nil || isLinkLike(after) || !after.IsDir() || !os.SameFile(before, after) {
				return nil, failure("PATH_CHANGED")
			}
			result[name] = after
		}
		if len(result) > MaxDirectoryEntries {
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
	return result, nil
}

// placeholderFileNames are inert files that Python-era Homes inherited from the
// repository template (.gitkeep) or that the operating system adds to folders
// (desktop.ini, Thumbs.db, .DS_Store). They are never runs.
var placeholderFileNames = map[string]bool{".gitkeep": true, "desktop.ini": true, "Thumbs.db": true, ".DS_Store": true}

// isPlaceholderFile reports whether a run-parent entry is one of those inert
// files. Only a regular file qualifies: a directory, symlink, or other
// reparse point with the same name still fails the strict scan.
func isPlaceholderFile(entry fs.DirEntry) bool {
	return placeholderFileNames[entry.Name()] && entry.Type().IsRegular()
}

func observedRunName(name string) (string, *Error) {
	if e := validComponent(name); e != nil {
		return "", e
	}
	normalized, e := normalizeName(name)
	if e != nil {
		return "", e
	}
	if e := validComponent(normalized); e != nil {
		return "", e
	}
	return normalized, nil
}

func (o *ObservedRuns) child(name string) (*os.Root, *Error) {
	expected, ok := o.children[name]
	if !ok {
		return nil, failure("PATH_INVALID")
	}
	before, err := o.root.base.Lstat(name)
	if err != nil || isLinkLike(before) || !before.IsDir() || !os.SameFile(expected, before) {
		return nil, failure("PATH_CHANGED")
	}
	child, err := o.root.base.OpenRoot(name)
	if err != nil {
		return nil, failure("PATH_OPEN_DIRECTORY")
	}
	held, err := child.Stat(".")
	if err != nil || isLinkLike(held) || !held.IsDir() || !os.SameFile(expected, held) {
		_ = child.Close()
		return nil, failure("PATH_CHANGED")
	}
	return child, nil
}

func observedRunLeaf(child *os.Root, leaf string) (name string, found bool, problem *Error) {
	directory, err := child.Open(".")
	if err != nil {
		return "", false, failure("PATH_DIRECTORY_UNAVAILABLE")
	}
	defer func() {
		if directory.Close() != nil {
			name, found, problem = "", false, failure("FILE_CLOSE")
		}
	}()
	entries, err := directory.ReadDir(MaxRunArtifactEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, failure("PATH_DIRECTORY_UNAVAILABLE")
	}
	if len(entries) > MaxRunArtifactEntries {
		return "", false, failure("PATH_DIRECTORY_LIMIT")
	}
	target := textcompat.Fold(leaf)
	for _, entry := range entries {
		normalized, e := normalizeName(entry.Name())
		if e != nil {
			return "", false, e
		}
		if textcompat.Fold(normalized) != target {
			continue
		}
		if found {
			return "", false, failure("PATH_COLLISION")
		}
		name, found = entry.Name(), true
	}
	if found && name != leaf {
		return "", false, failure("PATH_CASE_COLLISION")
	}
	return name, found, nil
}

func observeLeaf(child *os.Root, leaf string) (leafWitness, *Error) {
	name, found, e := observedRunLeaf(child, leaf)
	if e != nil {
		return leafWitness{}, e
	}
	if !found {
		return leafWitness{}, nil
	}
	if name != leaf {
		return leafWitness{}, failure("PATH_CHANGED")
	}
	info, err := child.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return leafWitness{}, nil
	}
	if err != nil {
		return leafWitness{}, failure("PATH_CHANGED")
	}
	if isLinkLike(info) {
		return leafWitness{}, failure("PATH_LINK")
	}
	if !info.Mode().IsRegular() {
		return leafWitness{}, failure("PATH_NOT_REGULAR")
	}
	return leafWitness{present: true, info: info, size: info.Size(), modified: info.ModTime()}, nil
}
func sameLeaf(a, b leafWitness) bool {
	if a.present != b.present {
		return false
	}
	if !a.present {
		return true
	}
	return os.SameFile(a.info, b.info) && a.size == b.size && a.modified.Equal(b.modified)
}

// ReadChildFile opens only one observed child and one fixed report leaf. It
// records both present and absent witnesses for a final Recheck.
func (o *ObservedRuns) ReadChildFile(name, leaf string, limit int64) (result ObservedFile, problem *Error) {
	if o == nil || o.root == nil {
		return ObservedFile{}, failure("ROOT_CLOSED")
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	r := o.root
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return ObservedFile{}, failure("ROOT_CLOSED")
	}
	if !runLeaves[leaf] {
		return ObservedFile{}, failure("PATH_INVALID")
	}
	if limit < 0 || limit > MaxReadBytes {
		return ObservedFile{}, failure("READ_LIMIT_INVALID")
	}
	if e := checkBindings(r.bindings); e != nil {
		return ObservedFile{}, e
	}
	child, e := o.child(name)
	if e != nil {
		return ObservedFile{}, e
	}
	defer func() {
		if o.closeChild(child) != nil {
			result = ObservedFile{}
			problem = failure("ROOT_CLOSE")
		}
	}()
	first, e := observeLeaf(child, leaf)
	if e != nil {
		return ObservedFile{}, e
	}
	if first.present && first.size > limit {
		return ObservedFile{}, failure("FILE_SIZE_LIMIT")
	}
	if first.present {
		file, err := child.OpenFile(leaf, readOnlyFlags(), 0)
		if err != nil {
			return ObservedFile{}, failure("FILE_OPEN")
		}
		defer func() {
			if file.Close() != nil {
				result = ObservedFile{}
				problem = failure("FILE_CLOSE")
			}
		}()
		held, err := file.Stat()
		if err != nil || isLinkLike(held) || !held.Mode().IsRegular() || !os.SameFile(first.info, held) {
			return ObservedFile{}, failure("PATH_CHANGED")
		}
		content, err := io.ReadAll(io.LimitReader(file, limit+1))
		if err != nil {
			return ObservedFile{}, failure("FILE_READ")
		}
		if int64(len(content)) > limit {
			return ObservedFile{}, failure("FILE_SIZE_LIMIT")
		}
		after, err := file.Stat()
		if err != nil || isLinkLike(after) || !after.Mode().IsRegular() || !os.SameFile(first.info, after) || first.size != after.Size() || !first.modified.Equal(after.ModTime()) {
			return ObservedFile{}, failure("PATH_CHANGED")
		}
		result.Content = content
	}
	last, e := observeLeaf(child, leaf)
	if e != nil || !sameLeaf(first, last) {
		return ObservedFile{}, failure("PATH_CHANGED")
	}
	if e := checkBindings(appendBindings(r.bindings, []binding{{parent: r.base, name: name, identity: o.children[name]}})); e != nil {
		return ObservedFile{}, e
	}
	key := runLeaf{name, leaf}
	o.leavesMu.Lock()
	defer o.leavesMu.Unlock()
	if previous, ok := o.leaves[key]; ok && !sameLeaf(previous, last) {
		return ObservedFile{}, failure("PATH_CHANGED")
	}
	o.leaves[key] = last
	result.Present = first.present
	result.Modified = first.modified
	return result, nil
}

// Recheck reobserves membership and all recorded positive/negative leaves.
// A failure discards the entire result; it never returns partial witnesses.
func (o *ObservedRuns) Recheck() *Error {
	if o == nil || o.root == nil {
		return failure("ROOT_CLOSED")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	r := o.root
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return failure("ROOT_CLOSED")
	}
	current, e := scanRunDirectories(r)
	if e != nil {
		return e
	}
	o.parentScans++
	if len(current) != len(o.children) {
		return failure("PATH_CHANGED")
	}
	for name, old := range o.children {
		now, ok := current[name]
		if !ok || !os.SameFile(old, now) {
			return failure("PATH_CHANGED")
		}
	}
	for key, old := range o.leaves {
		child, e := o.child(key.child)
		if e != nil {
			return e
		}
		now, leafError := observeLeaf(child, key.leaf)
		closeError := o.closeChild(child)
		if closeError != nil {
			return failure("ROOT_CLOSE")
		}
		if leafError != nil {
			return leafError
		}
		if !sameLeaf(old, now) {
			return failure("PATH_CHANGED")
		}
	}
	return checkBindings(r.bindings)
}
