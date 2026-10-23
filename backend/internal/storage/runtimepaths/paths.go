// Package runtimepaths provides explicitly rooted reads and bounded managed-file
// mutation primitives. It does not discover or bootstrap a data home.
package runtimepaths

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

const (
	MaxPathBytes              = 4096
	MaxComponents             = 64
	MaxComponentBytes         = 255
	MaxDirectoryEntries       = 10000
	MaxReadBytes        int64 = 16 << 20
	// Explicit larger-object reads have their own ceiling; catalog reads and
	// all existing mutation limits remain at MaxReadBytes.
	MaxObjectReadBytes int64 = 32 << 20
)

// Error never retains an operating-system error, path, or directory entry name.
type Error struct {
	Code string `json:"code"`
}

func (e *Error) Error() string   { return "managed catalog path: " + e.Code }
func failure(code string) *Error { return &Error{Code: code} }

type binding struct {
	parent   *os.Root
	name     string
	identity fs.FileInfo
}

// Root owns native directory handles. It permits concurrent readers; mutations
// and Close serialize with all calls. Its ancestor chain is retained and rechecked,
// rather than re-opening an absolute path after a preliminary Lstat check.
type Root struct {
	mu       sync.RWMutex
	base     *os.Root
	handles  []*os.Root
	bindings []binding
	closed   bool
}

// Open requires an existing explicit absolute directory, never a filesystem
// root. All configured components must be ordinary directories, not links or
// Windows reparse points. UNC/device namespaces are outside this local boundary.
func Open(root string) (*Root, *Error) {
	r, _, e := open(root, false)
	return r, e
}

func open(root string, create bool) (_ *Root, changed bool, problem *Error) {
	if create && !creationSupported {
		return nil, false, failure("CREATE_PLATFORM_UNSUPPORTED")
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil, false, failure("ROOT_PLATFORM_UNSUPPORTED")
	}
	anchor, components, e := absoluteComponents(root)
	if e != nil {
		return nil, false, e
	}
	h, err := os.OpenRoot(anchor)
	if err != nil {
		return nil, false, failure("ROOT_UNAVAILABLE")
	}
	r := &Root{base: h, handles: []*os.Root{h}}
	ok := false
	defer func() {
		if !ok {
			_ = r.Close()
		}
	}()
	for _, component := range components {
		if create {
			name, created, e := ensureChildDirectory(r.base, component)
			changed = changed || created
			if e != nil {
				return nil, changed, e
			}
			component = name
		}
		before, err := r.base.Lstat(component)
		if err != nil {
			return nil, changed, failure("ROOT_UNAVAILABLE")
		}
		if isLinkLike(before) {
			return nil, changed, failure("ROOT_LINK")
		}
		if !before.IsDir() {
			return nil, changed, failure("ROOT_NOT_DIRECTORY")
		}
		child, err := r.base.OpenRoot(component)
		if err != nil {
			return nil, changed, failure("ROOT_UNAVAILABLE")
		}
		r.handles = append(r.handles, child)
		held, err := child.Stat(".")
		if err != nil {
			return nil, changed, failure("ROOT_UNAVAILABLE")
		}
		if isLinkLike(held) || !held.IsDir() {
			return nil, changed, failure("ROOT_LINK")
		}
		if !os.SameFile(before, held) {
			return nil, changed, failure("PATH_CHANGED")
		}
		r.bindings = append(r.bindings, binding{r.base, component, held})
		r.base = child
	}
	if e = checkBindings(r.bindings); e != nil {
		return nil, changed, e
	}
	ok = true
	return r, changed, nil
}

func absoluteComponents(raw string) (string, []string, *Error) {
	if !utf8.ValidString(raw) || len(raw) > MaxPathBytes {
		return "", nil, failure("ROOT_INVALID")
	}
	if !filepath.IsAbs(raw) {
		return "", nil, failure("ROOT_ABSOLUTE_REQUIRED")
	}
	var anchor, rest string
	if runtime.GOOS == "windows" {
		volume := filepath.VolumeName(raw)
		if len(volume) != 2 || volume[1] != ':' || !((volume[0] >= 'a' && volume[0] <= 'z') || (volume[0] >= 'A' && volume[0] <= 'Z')) {
			return "", nil, failure("ROOT_NAMESPACE_UNSUPPORTED")
		}
		anchor = volume + string(filepath.Separator)
		rest = strings.ReplaceAll(raw[len(volume):], "\\", "/")
	} else {
		if strings.Contains(raw, "\\") {
			return "", nil, failure("ROOT_INVALID")
		}
		anchor = "/"
		rest = raw
	}
	rest = strings.TrimSuffix(strings.TrimPrefix(rest, "/"), "/")
	if rest == "" {
		return "", nil, failure("ROOT_TOO_BROAD")
	}
	parts := strings.Split(rest, "/")
	if len(parts) > MaxComponents {
		return "", nil, failure("PATH_DEPTH_LIMIT")
	}
	for _, part := range parts {
		if e := validComponent(part); e != nil {
			return "", nil, e
		}
	}
	return anchor, parts, nil
}

func relativeComponents(raw string) ([]string, *Error) {
	if !utf8.ValidString(raw) {
		return nil, failure("PATH_UNICODE_UNSUPPORTED")
	}
	if len(raw) == 0 || len(raw) > MaxPathBytes {
		return nil, failure("PATH_INVALID")
	}
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "\\") || filepath.IsAbs(raw) || filepath.VolumeName(raw) != "" {
		return nil, failure("PATH_RELATIVE_REQUIRED")
	}
	parts := strings.Split(strings.ReplaceAll(raw, "\\", "/"), "/")
	if len(parts) > MaxComponents {
		return nil, failure("PATH_DEPTH_LIMIT")
	}
	for i, part := range parts {
		if e := validComponent(part); e != nil {
			return nil, e
		}
		normalized, e := normalizeName(part)
		if e != nil {
			return nil, e
		}
		if len(normalized) > MaxComponentBytes {
			return nil, failure("PATH_COMPONENT_LIMIT")
		}
		parts[i] = normalized
	}
	return parts, nil
}

func validComponent(name string) *Error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `<>:"/\|?*`) || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return failure("PATH_INVALID_COMPONENT")
	}
	if !utf8.ValidString(name) {
		return failure("PATH_UNICODE_UNSUPPORTED")
	}
	if len(name) > MaxComponentBytes {
		return failure("PATH_COMPONENT_LIMIT")
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return failure("PATH_INVALID_COMPONENT")
		}
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$", "CLOCK$":
		return failure("PATH_DEVICE_NAME")
	}
	if strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT") {
		suffix := base[3:]
		if len(suffix) == 1 && suffix[0] >= '1' && suffix[0] <= '9' || suffix == "¹" || suffix == "²" || suffix == "³" {
			return failure("PATH_DEVICE_NAME")
		}
	}
	return nil
}

func (r *Root) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	var result error
	for i := len(r.handles) - 1; i >= 0; i-- {
		if err := r.handles[i].Close(); err != nil {
			result = failure("ROOT_CLOSE")
		}
	}
	return result
}

// Check validates an optional regular-file path without reading file bytes.
// A missing descendant is valid; an existing directory/nonregular leaf is not.
// This is not a reservation: ReadFile independently repeats all checks.
func (r *Root) Check(relative string) *Error {
	if r == nil {
		return failure("ROOT_CLOSED")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return failure("ROOT_CLOSED")
	}
	checked, e := r.walk(relative)
	if checked != nil {
		defer checked.close()
	}
	if e != nil {
		return e
	}
	return checkBindings(appendBindings(r.bindings, checked.bindings))
}

// Exists checks for an ordinary file or directory without opening its content.
// This supports recovery sentinels whose mere presence must block admission.
// Links, aliases and special files are refused. Missing descendants are absent;
// this is an observation, not a reservation against later creation/replacement.
func (r *Root) Exists(relative string) (bool, *Error) {
	if r == nil {
		return false, failure("ROOT_CLOSED")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return false, failure("ROOT_CLOSED")
	}
	checked, e := r.walkEntry(relative, true)
	if checked != nil {
		defer checked.close()
	}
	if e != nil {
		return false, e
	}
	if checked.present {
		now, err := checked.parent.Lstat(checked.name)
		if err != nil || isLinkLike(now) || now.Mode().Type() != checked.info.Mode().Type() || !os.SameFile(now, checked.info) {
			return false, failure("PATH_CHANGED")
		}
	}
	if e := checkBindings(appendBindings(r.bindings, checked.bindings)); e != nil {
		return false, e
	}
	return checked.present, nil
}

// ReadFile returns owned bytes from one opened file stream, with a limit+1 read
// bound and pre/post identity/type checks. Missing optional descendants return
// (nil,false,nil); every failure returns no bytes. It never writes or creates.
func (r *Root) ReadFile(relative string, limit int64) ([]byte, bool, *Error) {
	return r.readFile(relative, limit, MaxReadBytes)
}

// ReadObjectFile reuses ReadFile's managed-path and stream checks with a
// separate 32 MiB ceiling. Callers must supply the tighter actual format limit
// (for example the document envelope limit), never truncate at this boundary.
// This grants no authentication, decryption, ownership claim or write access.
func (r *Root) ReadObjectFile(relative string, limit int64) ([]byte, bool, *Error) {
	return r.readFile(relative, limit, MaxObjectReadBytes)
}

func (r *Root) readFile(relative string, limit, ceiling int64) (content []byte, present bool, problem *Error) {
	return r.readFileChecked(relative, limit, ceiling, false)
}

// ReadSingleLinkFile adds mutation-preflight semantics to the ordinary read
// port. Coordinators can reject a hardlinked intent/source before changing any
// other file. It does not reserve the path; writes must still recheck it.
// General ReadFile callers retain their existing read-only hardlink policy.
func (r *Root) ReadSingleLinkFile(relative string, limit int64) ([]byte, bool, *Error) {
	return r.readFileChecked(relative, limit, MaxReadBytes, true)
}

func (r *Root) readFileChecked(relative string, limit, ceiling int64, singleLink bool) (content []byte, present bool, problem *Error) {
	if r == nil {
		return nil, false, failure("ROOT_CLOSED")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return nil, false, failure("ROOT_CLOSED")
	}
	if limit < 0 || limit > ceiling {
		return nil, false, failure("READ_LIMIT_INVALID")
	}
	checked, e := r.walk(relative)
	if checked != nil {
		defer func() {
			if e := checked.close(); e != nil {
				content, present, problem = nil, false, e
			}
		}()
	}
	if e != nil {
		return nil, false, e
	}
	bindings := appendBindings(r.bindings, checked.bindings)
	if !checked.present {
		if e := checkBindings(bindings); e != nil {
			return nil, false, e
		}
		return nil, false, nil
	}
	if checked.info.Size() > limit {
		return nil, false, failure("FILE_SIZE_LIMIT")
	}
	f, err := checked.parent.OpenFile(checked.name, readOnlyFlags(), 0)
	if err != nil {
		return nil, false, failure("FILE_OPEN")
	}
	defer func() {
		if f.Close() != nil {
			content, present, problem = nil, false, failure("FILE_CLOSE")
		}
	}()
	before, err := f.Stat()
	if err != nil {
		return nil, false, failure("FILE_STAT")
	}
	if isLinkLike(before) || !before.Mode().IsRegular() {
		return nil, false, failure("PATH_NOT_REGULAR")
	}
	if !os.SameFile(checked.info, before) {
		return nil, false, failure("PATH_CHANGED")
	}
	if singleLink {
		if e := replacementLinkCheck(f, before); e != nil {
			return nil, false, e
		}
	}
	if before.Size() > limit {
		return nil, false, failure("FILE_SIZE_LIMIT")
	}
	if e := checked.recheckLeaf(before); e != nil {
		return nil, false, e
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, false, failure("FILE_READ")
	}
	if int64(len(raw)) > limit {
		return nil, false, failure("FILE_SIZE_LIMIT")
	}
	after, err := f.Stat()
	if err != nil {
		return nil, false, failure("FILE_STAT")
	}
	if !after.Mode().IsRegular() || isLinkLike(after) || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, false, failure("PATH_CHANGED")
	}
	if singleLink {
		if e := replacementLinkCheck(f, after); e != nil {
			return nil, false, e
		}
	}
	if e := checked.recheckLeaf(after); e != nil {
		return nil, false, e
	}
	if e := checkBindings(bindings); e != nil {
		return nil, false, e
	}
	return raw, true, nil
}

type checkedPath struct {
	parent   *os.Root
	name     string
	info     fs.FileInfo
	present  bool
	handles  []*os.Root
	bindings []binding
}

func (p *checkedPath) close() *Error {
	var result *Error
	for i := len(p.handles) - 1; i >= 0; i-- {
		if p.handles[i].Close() != nil {
			result = failure("ROOT_CLOSE")
		}
	}
	return result
}
func (p *checkedPath) recheckLeaf(expected fs.FileInfo) *Error {
	info, err := p.parent.Lstat(p.name)
	if err != nil {
		return failure("PATH_CHANGED")
	}
	if isLinkLike(info) {
		return failure("PATH_LINK")
	}
	if !info.Mode().IsRegular() {
		return failure("PATH_NOT_REGULAR")
	}
	if !os.SameFile(expected, info) {
		return failure("PATH_CHANGED")
	}
	return nil
}

func (r *Root) walk(relative string) (*checkedPath, *Error) {
	return r.walkEntry(relative, false)
}

func (r *Root) walkEntry(relative string, allowDirectory bool) (*checkedPath, *Error) {
	parts, e := relativeComponents(relative)
	if e != nil {
		return nil, e
	}
	if e = checkBindings(r.bindings); e != nil {
		return nil, e
	}
	p := &checkedPath{parent: r.base}
	for i, component := range parts {
		name, found, e := portableChild(p.parent, component)
		if e != nil {
			return p, e
		}
		if !found {
			return p, nil
		}
		info, err := p.parent.Lstat(name)
		if err != nil {
			return p, failure("PATH_CHANGED")
		}
		if isLinkLike(info) {
			return p, failure("PATH_LINK")
		}
		if i == len(parts)-1 {
			if !info.Mode().IsRegular() && !(allowDirectory && info.IsDir()) {
				return p, failure("PATH_NOT_REGULAR")
			}
			p.name, p.info, p.present = name, info, true
			return p, nil
		}
		if !info.IsDir() {
			return p, failure("PATH_NOT_DIRECTORY")
		}
		child, err := p.parent.OpenRoot(name)
		if err != nil {
			return p, failure("PATH_OPEN_DIRECTORY")
		}
		p.handles = append(p.handles, child)
		held, err := child.Stat(".")
		if err != nil {
			return p, failure("PATH_STAT")
		}
		if isLinkLike(held) || !held.IsDir() {
			return p, failure("PATH_LINK")
		}
		if !os.SameFile(info, held) {
			return p, failure("PATH_CHANGED")
		}
		p.bindings = append(p.bindings, binding{p.parent, name, held})
		p.parent = child
	}
	return p, failure("PATH_INVALID")
}

func checkBindings(bindings []binding) *Error {
	for _, b := range bindings {
		info, err := b.parent.Lstat(b.name)
		if err != nil {
			return failure("PATH_CHANGED")
		}
		if isLinkLike(info) {
			return failure("PATH_LINK")
		}
		if !info.IsDir() || !os.SameFile(b.identity, info) {
			return failure("PATH_CHANGED")
		}
	}
	return nil
}
func appendBindings(a, b []binding) []binding {
	out := make([]binding, 0, len(a)+len(b))
	out = append(out, a...)
	return append(out, b...)
}

func portableChild(parent *os.Root, component string) (string, bool, *Error) {
	f, err := parent.Open(".")
	if err != nil {
		return "", false, failure("PATH_DIRECTORY_UNAVAILABLE")
	}
	defer f.Close()
	var names []string
	for {
		entries, err := f.ReadDir(256)
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		if len(names) > MaxDirectoryEntries {
			return "", false, failure("PATH_DIRECTORY_LIMIT")
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", false, failure("PATH_DIRECTORY_UNAVAILABLE")
		}
	}
	sort.Strings(names)
	targetKey := textcompat.Fold(component)
	var match string
	count := 0
	for _, name := range names {
		normalized, e := normalizeName(name)
		if e != nil {
			return "", false, e
		}
		if textcompat.Fold(normalized) != targetKey {
			continue
		}
		count++
		match = name
	}
	if count > 1 {
		return "", false, failure("PATH_COLLISION")
	}
	if count == 0 {
		return "", false, nil
	}
	normalized, e := normalizeName(match)
	if e != nil {
		return "", false, e
	}
	if normalized != component {
		return "", false, failure("PATH_CASE_COLLISION")
	}
	return match, true, nil
}
