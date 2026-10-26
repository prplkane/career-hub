//go:build windows

package privacy

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

const inspectionTimeout = 15 * time.Second

type heldEntry struct {
	file     *os.File
	path     string
	identity windows.ByHandleFileInformation
	sddl     string
	names    []string
	depth    int
	parent   *heldEntry
	name     string
	foldName bool
}

// VerifyPrivateTree reads the source Windows bootstrap ACL policy on an explicit
// existing local tree. All inspected handles deny delete sharing until return.
// A second metadata/ACL/membership pass detects observed changes, not every race.
// Cancellation is checked between synchronous OS calls; it cannot preempt them.
// No file content, ACL, credential, marker, or database is written/read as content.
func VerifyPrivateTree(ctx context.Context, root string) (failure *Error) {
	if ctx == nil {
		return fail("ACL_CONTEXT_CANCELED")
	}
	ctx, cancel := context.WithTimeout(ctx, inspectionTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return fail("ACL_CONTEXT_CANCELED")
	}
	anchor, parts, ok := rootParts(root)
	if !ok {
		return fail("ACL_SETUP_FAILED")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return fail("ACL_SETUP_FAILED")
	}
	principal := user.User.Sid.String()
	if !sidPattern.MatchString(principal) {
		return fail("ACL_SETUP_FAILED")
	}
	held := []*heldEntry{}
	defer func() {
		for i := len(held) - 1; i >= 0; i-- {
			if err := held[i].file.Close(); err != nil && failure == nil {
				failure = fail("ACL_SETUP_FAILED")
			}
		}
	}()
	// Resolve one component relative to the held parent, not its pathname. A
	// no-share-delete handle alone cannot prevent in-place reparse conversion.
	var rootEntry *heldEntry
	for i := 0; i <= len(parts); i++ {
		if ctx.Err() != nil {
			return fail("ACL_CONTEXT_CANCELED")
		}
		var entry *heldEntry
		var e *Error
		if i == 0 {
			entry, e = openEntry(anchor)
		} else {
			entry, e = openChildMode(rootEntry, parts[i-1], true)
		}
		if e != nil {
			return e
		}
		held = append(held, entry)
		if !isDirectory(entry.identity) {
			return fail("ACL_SETUP_FAILED")
		}
		rootEntry = entry
	}
	entries := []*heldEntry{rootEntry}
	budget := 0
	for i := 0; i < len(entries); i++ {
		if ctx.Err() != nil {
			return fail("ACL_CONTEXT_CANCELED")
		}
		entry := entries[i]
		sddl, e := readPrivateDACL(entry.file, principal, i != 0, &budget)
		if e != nil {
			return e
		}
		entry.sddl = sddl
		if !isDirectory(entry.identity) {
			continue
		}
		names, e := readNames(entry.file, MaxEntries-len(entries))
		if e != nil {
			return e
		}
		entry.names = names
		for _, name := range names {
			if ctx.Err() != nil {
				return fail("ACL_CONTEXT_CANCELED")
			}
			if !validName(name) || len(entry.path)+1+len(name) > MaxPathBytes || entry.depth >= MaxDepth {
				return fail("ACL_TREE_LIMIT")
			}
			child, e := openChild(entry, name)
			if e != nil {
				return e
			}
			child.depth = entry.depth + 1
			held, entries = append(held, child), append(entries, child)
		}
	}
	// Re-observe the same retained objects, including link count and DACL. A
	// directory is reopened only for enumeration; its original handle is retained.
	budget = 0
	for i, entry := range entries {
		if ctx.Err() != nil {
			return fail("ACL_CONTEXT_CANCELED")
		}
		metadata, e := inspectHandle(windows.Handle(entry.file.Fd()))
		if e != nil || !sameEntry(entry.identity, metadata) {
			return fail("ACL_SETUP_FAILED")
		}
		sddl, e := readPrivateDACL(entry.file, principal, i != 0, &budget)
		if e != nil {
			return e
		}
		if sddl != entry.sddl {
			return fail("ACL_SETUP_FAILED")
		}
		if isDirectory(entry.identity) {
			if e = checkNames(entry); e != nil {
				return e
			}
		}
	}
	// Ancestors are not checked against the private DACL policy, but their held
	// type/identity must still be ordinary. Delete sharing does not prevent every
	// in-place metadata change (notably an empty-directory reparse conversion).
	for _, entry := range held {
		if ctx.Err() != nil {
			return fail("ACL_CONTEXT_CANCELED")
		}
		metadata, e := inspectHandle(windows.Handle(entry.file.Fd()))
		if e != nil || !sameEntry(entry.identity, metadata) {
			return fail("ACL_SETUP_FAILED")
		}
	}
	if ctx.Err() != nil {
		return fail("ACL_CONTEXT_CANCELED")
	}
	return nil
}

func openEntry(path string) (*heldEntry, *Error) {
	encoded, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, fail("ACL_SETUP_FAILED")
	}
	handle, err := windows.CreateFile(encoded, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, fail("ACL_SETUP_FAILED")
	}
	return adoptHandle(handle, path)
}

// openChild never resolves intermediate pathnames and never requests creation.
// OBJ_DONT_REPARSE refuses namespace reparses, including a changed parent; the
// terminal OPEN_REPARSE_POINT plus metadata check also refuses the link itself.
func openChild(parent *heldEntry, name string) (*heldEntry, *Error) {
	return openChildMode(parent, name, false)
}

func openChildMode(parent *heldEntry, name string, foldName bool) (*heldEntry, *Error) {
	return openChildAccess(parent, name, foldName, windows.FILE_GENERIC_READ)
}

func openChildAccess(parent *heldEntry, name string, foldName bool, access uint32) (*heldEntry, *Error) {
	if parent == nil || !validName(name) || len(parent.path)+1+len(name) > MaxPathBytes {
		return nil, fail("ACL_SETUP_FAILED")
	}
	metadata, e := inspectHandle(windows.Handle(parent.file.Fd()))
	if e != nil || !sameEntry(parent.identity, metadata) || !isDirectory(metadata) {
		return nil, fail("ACL_SETUP_FAILED")
	}
	encoded, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, fail("ACL_SETUP_FAILED")
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(parent.file.Fd()),
		ObjectName:    encoded,
		Attributes:    windows.OBJ_DONT_REPARSE,
	}
	// Initial user-selected root components retain normal Win32 lookup semantics;
	// enumerated descendants use their exact observed names, not forced folding.
	if foldName {
		attributes.Attributes |= windows.OBJ_CASE_INSENSITIVE
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var handle windows.Handle
	err = windows.NtCreateFile(&handle, access, &attributes,
		&windows.IO_STATUS_BLOCK{}, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.FILE_OPEN, windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT,
		0, 0)
	runtime.KeepAlive(parent.file)
	runtime.KeepAlive(encoded)
	if err != nil {
		return nil, fail("ACL_SETUP_FAILED")
	}
	entry, e := adoptHandle(handle, filepath.Join(parent.path, name))
	if e != nil {
		return nil, e
	}
	entry.parent, entry.name, entry.foldName = parent, name, foldName
	return entry, nil
}

func adoptHandle(handle windows.Handle, path string) (*heldEntry, *Error) {
	metadata, e := inspectHandle(handle)
	if e != nil {
		_ = windows.CloseHandle(handle)
		return nil, e
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, fail("ACL_SETUP_FAILED")
	}
	return &heldEntry{file: file, path: path, identity: metadata}, nil
}

func inspectHandle(handle windows.Handle) (windows.ByHandleFileInformation, *Error) {
	var info windows.ByHandleFileInformation
	kind, err := windows.GetFileType(handle)
	if err != nil || kind != windows.FILE_TYPE_DISK {
		return info, fail("ACL_SETUP_FAILED")
	}
	if err = windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || !isDirectory(info) && info.NumberOfLinks != 1 {
		return info, fail("ACL_SETUP_FAILED")
	}
	return info, nil
}

func isDirectory(info windows.ByHandleFileInformation) bool {
	return info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
}

func sameEntry(a, b windows.ByHandleFileInformation) bool {
	return a.VolumeSerialNumber == b.VolumeSerialNumber && a.FileIndexHigh == b.FileIndexHigh && a.FileIndexLow == b.FileIndexLow && isDirectory(a) == isDirectory(b)
}

func readPrivateDACL(file *os.File, principal string, inherited bool, budget *int) (string, *Error) {
	sd, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil || sd == nil || !sd.IsValid() {
		return "", fail("ACL_SETUP_FAILED")
	}
	// Refuse oversized/unexpected descriptors before converting to SDDL. The
	// native ACL size is uint16; only a two-ACE DACL can reach string conversion.
	if sd.Length() > 64<<10 {
		return "", fail("ACL_TREE_LIMIT")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 2 {
		return "", fail("ACL_SETUP_FAILED")
	}
	sddl := sd.String()
	*budget += len(utf16.Encode([]rune(sddl))) * 2
	if *budget > MaxSnapshotBytes {
		return "", fail("ACL_TREE_LIMIT")
	}
	if !privateDACL(sddl, principal, inherited) {
		return "", fail("ACL_SETUP_FAILED")
	}
	return sddl, nil
}

func readNames(file *os.File, remaining int) ([]string, *Error) {
	names, err := file.Readdirnames(remaining + 1)
	if len(names) > remaining {
		return nil, fail("ACL_TREE_LIMIT")
	}
	if err != nil && err != io.EOF {
		return nil, fail("ACL_SETUP_FAILED")
	}
	slices.Sort(names)
	return names, nil
}

func checkNames(entry *heldEntry) (failure *Error) {
	// All production tree entries have a retained parent (drive roots are refused
	// at admission). Tests may construct an isolated helper entry without one.
	if entry.parent == nil {
		return fail("ACL_SETUP_FAILED")
	}
	current, e := openChildMode(entry.parent, entry.name, entry.foldName)
	if e != nil {
		return e
	}
	defer func() {
		if err := current.file.Close(); err != nil && failure == nil {
			failure = fail("ACL_SETUP_FAILED")
		}
	}()
	if !sameEntry(entry.identity, current.identity) {
		return fail("ACL_SETUP_FAILED")
	}
	names, e := readNames(current.file, len(entry.names))
	if e != nil {
		return e
	}
	if !slices.Equal(entry.names, names) {
		return fail("ACL_SETUP_FAILED")
	}
	return nil
}

func rootParts(root string) (string, []string, bool) {
	if !utf8.ValidString(root) || len(root) > MaxPathBytes || !filepath.IsAbs(root) {
		return "", nil, false
	}
	volume := filepath.VolumeName(root)
	if len(volume) != 2 || volume[1] != ':' || !((volume[0] >= 'a' && volume[0] <= 'z') || (volume[0] >= 'A' && volume[0] <= 'Z')) {
		return "", nil, false
	}
	rest := strings.ReplaceAll(root[2:], "\\", "/")
	rest = strings.TrimSuffix(strings.TrimPrefix(rest, "/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) > MaxDepth {
		return "", nil, false
	}
	for _, name := range parts {
		if !validName(name) {
			return "", nil, false
		}
	}
	return volume + "\\", parts, true
}

func validName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 255 || !utf8.ValidString(name) || strings.ContainsAny(name, `<>:"/\|?*`) || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return false
		}
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$", "CLOCK$":
		return false
	}
	if strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT") {
		suffix := base[3:]
		if len(suffix) == 1 && suffix[0] >= '1' && suffix[0] <= '9' || suffix == "¹" || suffix == "²" || suffix == "³" {
			return false
		}
	}
	return true
}
