//go:build windows

package privacy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Every ACL write is confined to a newly created, exclusively owned fixture.
// The parent temporary directory retains its original ACL throughout each test.
type nativeFixture struct {
	t         *testing.T
	container string
	root      string
	sid       string
	paths     []string
}

func newNativeFixture(t *testing.T) *nativeFixture {
	t.Helper()
	return nativeFixtureIn(t, t.TempDir())
}

func nativeFixtureIn(t *testing.T, container string) *nativeFixture {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		t.Fatal("cannot obtain current process token SID")
	}
	f := &nativeFixture{t: t, container: container, root: filepath.Join(container, "tree"), sid: user.User.Sid.String()}
	if err := os.Mkdir(f.root, 0700); err != nil {
		t.Fatal("cannot create synthetic ACL root")
	}
	f.paths = append(f.paths, f.root)
	f.setDACL(f.root, f.privateRootDACL(), true)
	return f
}

func (f *nativeFixture) privateRootDACL() string {
	return "D:P(A;OICI;FA;;;" + f.sid + ")(A;OICI;FA;;;SY)"
}

func (f *nativeFixture) owned(path string) {
	f.t.Helper()
	rel, err := filepath.Rel(f.root, filepath.Clean(path))
	if err != nil || !filepath.IsAbs(path) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		f.t.Fatal("fixture attempted an ACL write outside its owned root")
	}
}

func (f *nativeFixture) setDACL(path, sddl string, protected bool) {
	f.t.Helper()
	f.owned(path)
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		f.t.Fatal("invalid synthetic security descriptor")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		f.t.Fatal("synthetic descriptor has no DACL")
	}
	flags := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
	if protected {
		flags = windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, flags, nil, nil, dacl, nil); err != nil {
		f.t.Fatal("cannot set synthetic DACL")
	}
}

// Restore access before TempDir removes the fixture, including when an assertion
// fails after installing a DACL which grants no file access to the current user.
func (f *nativeFixture) replaceDACL(path, sddl string, protected bool) {
	f.t.Helper()
	f.owned(path)
	before := nativeDACL(f.t, path)
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		f.t.Fatal("cannot capture synthetic DACL flags")
	}
	control, _, err := sd.Control()
	if err != nil {
		f.t.Fatal("cannot read synthetic DACL flags")
	}
	f.t.Cleanup(func() { f.setDACL(path, before, control&windows.SE_DACL_PROTECTED != 0) })
	f.setDACL(path, sddl, protected)
}

func (f *nativeFixture) mkdir(rel string) string {
	f.t.Helper()
	path := filepath.Join(f.root, rel)
	f.owned(path)
	if err := os.Mkdir(path, 0700); err != nil {
		f.t.Fatal("cannot create synthetic directory")
	}
	f.paths = append(f.paths, path)
	return path
}

func (f *nativeFixture) write(rel string, content []byte) string {
	f.t.Helper()
	path := filepath.Join(f.root, rel)
	f.owned(path)
	if err := os.WriteFile(path, content, 0600); err != nil {
		f.t.Fatal("cannot create synthetic file")
	}
	f.paths = append(f.paths, path)
	return path
}

func nativeDACL(t *testing.T, path string) string {
	t.Helper()
	return nativeDescriptor(t, path, windows.DACL_SECURITY_INFORMATION)
}

func nativeDescriptor(t *testing.T, path string, flags windows.SECURITY_INFORMATION) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, flags)
	if err != nil || sd == nil || !sd.IsValid() {
		t.Fatal("cannot read synthetic DACL")
	}
	encoded := sd.String()
	if encoded == "" {
		t.Fatal("synthetic security descriptor conversion failed")
	}
	return encoded
}

type nativeSnapshot struct {
	Security    string
	Content     string
	Names       []string
	NamesDenied bool
	Size        int64
	Mode        os.FileMode
	ModTime     time.Time
}

func (f *nativeFixture) snapshot() map[string]nativeSnapshot {
	f.t.Helper()
	snapshots := make(map[string]nativeSnapshot, len(f.paths)+1)
	// Include the unchanged container: verification must not repair ancestors.
	for _, path := range append(append([]string{}, f.paths...), f.container) {
		info, err := os.Lstat(path)
		if err != nil {
			f.t.Fatal("cannot inspect synthetic entry")
		}
		item := nativeSnapshot{
			Security: nativeDescriptor(f.t, path, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION),
			Size:     info.Size(), Mode: info.Mode(), ModTime: info.ModTime(),
		}
		if info.Mode().IsRegular() {
			content, err := os.ReadFile(path)
			if err != nil {
				f.t.Fatal("cannot read synthetic content")
			}
			item.Content = string(content)
		}
		if info.IsDir() {
			children, err := os.ReadDir(path)
			if errors.Is(err, os.ErrPermission) {
				// Denied enumeration is expected for deliberately missing-user or
				// empty-DACL fixtures. Do not credit unknown names as observed empty.
				item.NamesDenied = true
			} else if err != nil {
				f.t.Fatal("cannot enumerate synthetic snapshot directory")
			}
			for _, child := range children {
				item.Names = append(item.Names, child.Name())
			}
		}
		snapshots[path] = item
	}
	return snapshots
}

func assertNativeCode(t *testing.T, err *Error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("VerifyPrivateTree failed: %s", err.Code)
		}
		return
	}
	if err == nil || err.Code != want {
		t.Fatalf("VerifyPrivateTree returned %v, want %s", err, want)
	}
	if err.Error() != "data home privacy: "+want || *err != (Error{Code: want}) {
		t.Fatal("error contains data beyond the fixed error code")
	}
}

func (f *nativeFixture) verifyUnchanged(want string) {
	f.t.Helper()
	before := f.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assertNativeCode(f.t, VerifyPrivateTree(ctx, f.root), want)
	if !reflect.DeepEqual(before, f.snapshot()) {
		f.t.Fatal("verification changed synthetic contents, metadata, or permissions")
	}
}

// A DELETE-access reopen fails if the verifier retains a no-share-delete handle.
// Sharing ordinary readers avoids false failures from host antivirus/indexers.
// This checks directories as well as files without relying on garbage collection.
func (f *nativeFixture) assertReleased() {
	f.t.Helper()
	for _, path := range append(append([]string{}, f.paths...), f.container) {
		encoded, err := windows.UTF16PtrFromString(path)
		if err != nil {
			f.t.Fatal("cannot encode synthetic path")
		}
		handle, err := windows.CreateFile(encoded, windows.DELETE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err != nil {
			f.t.Fatalf("synthetic entry denies delete-access reopen after verification (path bytes=%d, native error=%v)", len(path), err)
		}
		if err := windows.CloseHandle(handle); err != nil {
			f.t.Fatal("cannot close synthetic delete-access reopen handle")
		}
	}
}

func TestNativePrivateTreeAndReadOnlyInvariance(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested-%t", nested), func(t *testing.T) {
			f := newNativeFixture(t)
			if nested {
				f.mkdir("messages")
				f.mkdir(filepath.Join("messages", "nested"))
				f.write("metadata.json", []byte("synthetic metadata\x00\xff"))
				f.write(filepath.Join("messages", "nested", "sample.eml"), []byte("Subject: synthetic\r\n\r\nbody\r\n"))
			}
			f.verifyUnchanged("")
			f.verifyUnchanged("")
			f.assertReleased()
			// DELETE-access reopening above is the exact retained-handle check.
			// Renaming an entire populated tree additionally races unrelated host
			// scanners opening descendants after that check; it is not a leak oracle.
		})
	}
}

func TestNativeRejectsNonPrivateRootDACL(t *testing.T) {
	for _, test := range []struct {
		name string
		acl  func(*nativeFixture) string
	}{
		{"broad-instead-of-system", func(f *nativeFixture) string { return "D:P(A;OICI;FA;;;" + f.sid + ")(A;OICI;FA;;;WD)" }},
		{"extra-trustee", func(f *nativeFixture) string { return f.privateRootDACL() + "(A;OICI;FR;;;BU)" }},
		{"missing-system", func(f *nativeFixture) string { return "D:P(A;OICI;FA;;;" + f.sid + ")" }},
		{"missing-current-user", func(f *nativeFixture) string { return "D:P(A;OICI;FA;;;SY)" }},
		{"duplicate-current-user", func(f *nativeFixture) string { return "D:P(A;OICI;FA;;;" + f.sid + ")(A;OICI;FA;;;" + f.sid + ")" }},
		{"deny-ace", func(f *nativeFixture) string { return "D:P(D;OICI;FA;;;SY)(A;OICI;FA;;;" + f.sid + ")" }},
		{"not-full-control", func(f *nativeFixture) string { return "D:P(A;OICI;FA;;;" + f.sid + ")(A;OICI;FR;;;SY)" }},
		{"missing-object-inherit", func(f *nativeFixture) string { return "D:P(A;CI;FA;;;" + f.sid + ")(A;CI;FA;;;SY)" }},
		{"empty-dacl", func(*nativeFixture) string { return "D:P" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newNativeFixture(t)
			if f.sid == "S-1-5-18" {
				t.Skip("distinct current-user/SYSTEM adversarial fixture requires a non-SYSTEM token")
			}
			f.replaceDACL(f.root, test.acl(f), true)
			f.verifyUnchanged("ACL_SETUP_FAILED")
			// Restore explicitly so access-based cleanup assertions are meaningful.
			f.setDACL(f.root, f.privateRootDACL(), true)
			f.assertReleased()
		})
	}
}

func TestNativeRejectsNullDACL(t *testing.T) {
	f := newNativeFixture(t)
	f.t.Cleanup(func() { f.setDACL(f.root, f.privateRootDACL(), true) })
	if err := windows.SetNamedSecurityInfo(f.root, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, nil, nil); err != nil {
		t.Fatal("cannot install synthetic null DACL")
	}
	sd, err := windows.GetNamedSecurityInfo(f.root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal("cannot inspect synthetic null DACL")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl != nil {
		t.Fatal("null-DACL fixture did not produce a present null DACL")
	}
	f.verifyUnchanged("ACL_SETUP_FAILED")
	f.assertReleased()
}

func TestNativeRejectsUnprotectedRootAndProtectedChildren(t *testing.T) {
	t.Run("unprotected-root", func(t *testing.T) {
		f := newNativeFixture(t)
		f.replaceDACL(f.root, strings.Replace(f.privateRootDACL(), "D:P", "D:", 1), false)
		f.verifyUnchanged("ACL_SETUP_FAILED")
		f.assertReleased()
	})
	for _, directory := range []bool{false, true} {
		t.Run(fmt.Sprintf("protected-child-directory-%t", directory), func(t *testing.T) {
			f := newNativeFixture(t)
			var child string
			if directory {
				child = f.mkdir("child")
				f.write(filepath.Join("child", "content"), []byte("unchanged"))
			} else {
				child = f.write("child", []byte("unchanged"))
			}
			f.replaceDACL(child, f.privateRootDACL(), true)
			f.verifyUnchanged("ACL_SETUP_FAILED")
			f.assertReleased()
		})
	}
	t.Run("extra-explicit-child-ace", func(t *testing.T) {
		f := newNativeFixture(t)
		child := f.write("child", []byte("unchanged"))
		f.replaceDACL(child, "D:(A;;FR;;;BU)", false)
		f.verifyUnchanged("ACL_SETUP_FAILED")
		f.assertReleased()
	})
}

func TestNativeRejectsHardLink(t *testing.T) {
	f := newNativeFixture(t)
	file := f.write("source", []byte("synthetic hardlink target"))
	alias := filepath.Join(f.root, "alias")
	if err := os.Link(file, alias); err != nil {
		t.Fatal("cannot create synthetic hardlink")
	}
	f.paths = append(f.paths, alias)
	f.verifyUnchanged("ACL_SETUP_FAILED")
	f.assertReleased()
}

func TestNativeRejectsReparsePointsWithoutFollowing(t *testing.T) {
	for _, kind := range []string{"file-symlink", "directory-symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			f := newNativeFixture(t)
			target := f.mkdir("target")
			content := f.write(filepath.Join("target", "content"), []byte("synthetic link target"))
			link := filepath.Join(f.root, "link")
			var err error
			switch kind {
			case "file-symlink":
				err = os.Symlink(content, link)
			case "directory-symlink":
				err = os.Symlink(target, link)
			case "junction":
				err = nativeJunction(link, target)
			}
			if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) && kind != "junction" {
				t.Skip("Windows symlink creation requires the host's privilege or developer mode")
			}
			if err != nil {
				t.Fatal("cannot create synthetic reparse point")
			}
			before := f.snapshot() // Deliberately excludes the link, so this never follows it.
			assertNativeCode(t, VerifyPrivateTree(context.Background(), f.root), "ACL_SETUP_FAILED")
			if !reflect.DeepEqual(before, f.snapshot()) {
				t.Fatal("verification changed the synthetic reparse target or its containing tree")
			}
			f.assertReleased()
			// Remove only the link itself; never recursively traverse its target.
			if err := os.Remove(link); err != nil {
				t.Fatal("synthetic reparse point could not be removed after verification")
			}
			f.verifyUnchanged("")
		})
	}
}

func TestNativeRejectsJunctionRootAndAncestor(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		t.Run(fmt.Sprintf("ancestor-%t", ancestor), func(t *testing.T) {
			f := newNativeFixture(t)
			target := f.mkdir("target")
			actualRoot := target
			if ancestor {
				actualRoot = f.mkdir(filepath.Join("target", "inner"))
			}
			f.setDACL(actualRoot, f.privateRootDACL(), true)
			f.write(filepath.Join(strings.TrimPrefix(actualRoot, f.root+string(filepath.Separator)), "content"), []byte("unchanged target"))
			assertNativeCode(t, VerifyPrivateTree(context.Background(), actualRoot), "")
			link := filepath.Join(f.root, "junction")
			if err := nativeJunction(link, target); err != nil {
				t.Fatal("cannot create synthetic ancestor junction")
			}
			viaLink := link
			if ancestor {
				viaLink = filepath.Join(link, "inner")
			}
			before := f.snapshot()
			assertNativeCode(t, VerifyPrivateTree(context.Background(), viaLink), "ACL_SETUP_FAILED")
			if !reflect.DeepEqual(before, f.snapshot()) {
				t.Fatal("rejected junction root changed its synthetic target")
			}
			f.assertReleased()
			if err := os.Remove(link); err != nil {
				t.Fatal("synthetic ancestor junction could not be removed after return")
			}
		})
	}
}

// Construct the documented mount-point reparse buffer directly, following the
// native fixture pattern in Go's os_windows_test.go. No shell command is run.
func nativeJunction(link, target string) error {
	if err := os.Mkdir(link, 0700); err != nil {
		return err
	}
	return nativeSetJunction(link, target)
}

func nativeSetJunction(link, target string) error {
	encoded, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(encoded, windows.FILE_WRITE_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	substitute := utf16.Encode([]rune(`\??\` + target))
	printName := utf16.Encode([]rune(target))
	pathBuffer := append(append(append(append([]uint16{}, substitute...), 0), printName...), 0)
	data := make([]byte, 16+2*len(pathBuffer))
	binary.LittleEndian.PutUint32(data[0:4], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(data[4:6], uint16(len(data)-8))
	binary.LittleEndian.PutUint16(data[10:12], uint16(2*len(substitute)))
	binary.LittleEndian.PutUint16(data[12:14], uint16(2*(len(substitute)+1)))
	binary.LittleEndian.PutUint16(data[14:16], uint16(2*len(printName)))
	for i, value := range pathBuffer {
		binary.LittleEndian.PutUint16(data[16+2*i:], value)
	}
	var returned uint32
	return windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT, &data[0], uint32(len(data)), nil, 0, &returned, nil)
}

// Exercise the namespace primitive without openChild's preceding metadata
// inspection, characterizing a conversion in the gap between those operations.
func nativeRelativeOpen(parent *heldEntry, name string) (windows.Handle, error) {
	encoded, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(parent.file.Fd()),
		ObjectName:    encoded, Attributes: windows.OBJ_DONT_REPARSE,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var handle windows.Handle
	err = windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ, &attributes,
		&windows.IO_STATUS_BLOCK{}, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.FILE_OPEN, windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	runtime.KeepAlive(parent.file)
	runtime.KeepAlive(encoded)
	return handle, err
}

func TestNativeHeldEmptyDirectoryCanBecomeJunction(t *testing.T) {
	f := newNativeFixture(t)
	empty := f.mkdir("empty")
	target := f.mkdir("target")
	f.write(filepath.Join("target", "content"), []byte("outside the held empty directory, inside the owned fixture"))
	targetEntry, failure := openEntry(target)
	assertNativeCode(t, failure, "")
	defer targetEntry.file.Close()
	control, err := nativeRelativeOpen(targetEntry, "content")
	if err != nil {
		t.Fatal("positive-control native relative child open failed")
	}
	if err := windows.CloseHandle(control); err != nil {
		t.Fatal("cannot close positive-control relative child")
	}
	entry, failure := openEntry(empty)
	assertNativeCode(t, failure, "")
	defer entry.file.Close()
	// Denying FILE_SHARE_DELETE pins ordinary rename/replacement, but does not
	// prevent in-place reparse conversion of an empty directory when writes share.
	if err := nativeSetJunction(empty, target); err != nil {
		t.Fatalf("synthetic held-directory reparse conversion failed: %v", err)
	}
	_, failure = inspectHandle(windows.Handle(entry.file.Fd()))
	assertNativeCode(t, failure, "ACL_SETUP_FAILED")
	child, failure := openChild(entry, "content")
	if child != nil {
		_ = child.file.Close()
		t.Fatal("relative child open followed the converted parent into its junction target")
	}
	assertNativeCode(t, failure, "ACL_SETUP_FAILED")
	if childHandle, err := nativeRelativeOpen(entry, "content"); err == nil {
		_ = windows.CloseHandle(childHandle)
		t.Fatal("native no-reparse relative open followed the converted directory to its target child")
	}
	if err := entry.file.Close(); err != nil {
		t.Fatal("cannot close retained synthetic directory handle")
	}
	if err := os.Remove(empty); err != nil {
		t.Fatal("cannot remove the synthetic junction itself after releasing its handle")
	}
	if err := os.Mkdir(empty, 0700); err != nil {
		t.Fatal("cannot restore empty owned fixture directory")
	}
	if err := targetEntry.file.Close(); err != nil {
		t.Fatal("cannot close positive-control target directory")
	}
	f.verifyUnchanged("")
	f.assertReleased()
}

func TestNativeRootRefusalsAndCancellation(t *testing.T) {
	f := newNativeFixture(t)
	file := f.write("ordinary-file", []byte("unchanged"))
	before := f.snapshot()
	for _, root := range []string{"", ".", "C:relative", f.root + "\x00", filepath.Join(f.root, "missing"), file} {
		assertNativeCode(t, VerifyPrivateTree(context.Background(), root), "ACL_SETUP_FAILED")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assertNativeCode(t, VerifyPrivateTree(ctx, f.root), "ACL_CONTEXT_CANCELED")
	assertNativeCode(t, VerifyPrivateTree(nil, f.root), "ACL_CONTEXT_CANCELED")
	if !reflect.DeepEqual(before, f.snapshot()) {
		t.Fatal("rejected/canceled verification changed the fixture")
	}
	f.assertReleased()
}

func TestNativeTreeEntryLimit(t *testing.T) {
	if MaxEntries != 4096 {
		t.Fatal("entry limit changed; review the bounded native fixture before resizing it")
	}
	f := newNativeFixture(t)
	// The root is counted, so MaxEntries children exceed the root-inclusive cap.
	for i := 0; i < MaxEntries; i++ {
		f.write(fmt.Sprintf("f%04d", i), nil)
	}
	before := nativeDACL(t, f.root)
	assertNativeCode(t, VerifyPrivateTree(context.Background(), f.root), "ACL_TREE_LIMIT")
	if nativeDACL(t, f.root) != before {
		t.Fatal("tree limit failure changed the root DACL")
	}
	f.assertReleased()
}

func TestNativeTreeDepthLimit(t *testing.T) {
	if MaxDepth != 64 {
		t.Fatal("depth limit changed; review the bounded native fixture before resizing it")
	}
	f := newNativeFixture(t)
	rel := ""
	for i := 0; i <= MaxDepth; i++ {
		rel = filepath.Join(rel, "d")
		f.mkdir(rel)
	}
	f.verifyUnchanged("ACL_TREE_LIMIT")
	f.assertReleased()
}
