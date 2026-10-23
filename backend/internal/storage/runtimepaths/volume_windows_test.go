//go:build windows

package runtimepaths

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

const syntheticVolumeRoot = `\\?\Volume{12345678-1234-abCD-1234-123456789abc}\`

func TestLocalVolumeNativeReadOnlyAndHandleRelease(t *testing.T) {
	dir := testHome(t)
	put(t, dir, "applications/fixture", "synthetic bytes")
	root := opened(t, dir)
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	daclBefore, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal("fixture DACL inspection unavailable")
	}
	namesBefore, e := root.ListNames()
	if e != nil {
		t.Fatal(e)
	}
	for range 8 {
		if e := root.CheckLocalVolume(); e != nil {
			// Do not silently skip an unsupported native test environment.
			t.Fatal(e)
		}
	}
	namesAfter, e := root.ListNames()
	if e != nil || !reflect.DeepEqual(namesBefore, namesAfter) {
		t.Fatal("volume check changed root membership")
	}
	after, err := os.Stat(dir)
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("volume check changed directory identity or mtime")
	}
	daclAfter, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil || daclBefore.String() != daclAfter.String() {
		t.Fatal("volume check changed existing DACL")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "applications", "fixture"))
	if err != nil || string(raw) != "synthetic bytes" {
		t.Fatal("volume check changed fixture content")
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	code(t, root.CheckLocalVolume(), "ROOT_CLOSED")
	// A leaked directory handle without delete sharing would prevent this.
	moved := filepath.Join(testHome(t), "moved")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal("volume check retained a directory handle after Close")
	}
}

func TestLocalVolumeLifecycleAndFailureCleanup(t *testing.T) {
	var absent *Root
	code(t, absent.CheckLocalVolume(), "ROOT_CLOSED")
	code(t, (&Root{}).CheckLocalVolume(), "ROOT_CLOSED")
	root := opened(t, testHome(t))
	for _, want := range []string{"", "VOLUME_READ_ONLY"} {
		var inspected *os.File
		e := root.checkLocalVolumeWith(func(file *os.File) *Error {
			inspected = file
			if want != "" {
				return failure(want)
			}
			return nil
		})
		if want != "" {
			code(t, e, want)
		} else if e != nil {
			t.Fatal(e)
		}
		if inspected == nil {
			t.Fatal("native inspection was not invoked")
		}
		// Windows File.Stat calls the native handle API directly and need not
		// return os.ErrClosed. Close, unlike Stat, uses Go's closed-file state.
		if err := inspected.Close(); !errors.Is(err, os.ErrClosed) {
			t.Fatal("native directory handle leaked on return")
		}
	}
	for _, queryError := range []*Error{nil, failure("VOLUME_UNAVAILABLE")} {
		code(t, root.checkLocalVolumeWith(func(file *os.File) *Error {
			_ = file.Close()
			return queryError
		}), "PATH_CHANGED")
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	code(t, root.checkLocalVolumeWith(func(*os.File) *Error {
		t.Fatal("closed root invoked inspection")
		return nil
	}), "ROOT_CLOSED")
}

func TestLocalVolumeNativeInvalidHandleIsSanitized(t *testing.T) {
	file, err := os.Open(testHome(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	code(t, checkLocalVolume(file), "VOLUME_UNAVAILABLE")
}

func mockVolumeCalls(t *testing.T, file *os.File) volumeCalls {
	t.Helper()
	return volumeCalls{
		finalPath: func(h windows.Handle, buffer *uint16, size, flags uint32) (uint32, error) {
			if h != windows.Handle(file.Fd()) || size != maxVolumePathUnits || flags != volumeNameGUID|fileNameOpened {
				t.Fatal("native final-path contract differs")
			}
			value := windows.StringToUTF16(syntheticVolumeRoot + "fixture")
			copy(unsafe.Slice(buffer, int(size)), value)
			return uint32(len(value) - 1), nil
		},
		driveType: func(root *uint16) uint32 {
			if windows.UTF16PtrToString(root) != syntheticVolumeRoot {
				t.Fatal("drive query used a letter, full path or malformed volume root")
			}
			return windows.DRIVE_FIXED
		},
		information: func(h windows.Handle, label *uint16, labelSize uint32, serial, component, flags *uint32, name *uint16, size uint32) error {
			if h != windows.Handle(file.Fd()) || label != nil || labelSize != 0 || serial != nil || component != nil || size != windows.MAX_PATH+1 {
				t.Fatal("volume query requested unnecessary private metadata")
			}
			*flags = windows.FILE_PERSISTENT_ACLS
			copy(unsafe.Slice(name, int(size)), windows.StringToUTF16("NTFS"))
			return nil
		},
	}
}

func TestLocalVolumeQueryFailuresAndFilesystemPolicy(t *testing.T) {
	file, err := os.Open(testHome(t))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, test := range []struct {
		name, want string
		change     func(*volumeCalls)
	}{
		{"supported", "", func(*volumeCalls) {}},
		{"path query error", "VOLUME_UNAVAILABLE", func(c *volumeCalls) {
			c.finalPath = func(windows.Handle, *uint16, uint32, uint32) (uint32, error) {
				return 12, errors.New("private native path")
			}
		}},
		{"zero result", "VOLUME_UNAVAILABLE", func(c *volumeCalls) {
			c.finalPath = func(windows.Handle, *uint16, uint32, uint32) (uint32, error) { return 0, nil }
		}},
		{"path limit", "VOLUME_PATH_LIMIT", func(c *volumeCalls) {
			c.finalPath = func(_ windows.Handle, _ *uint16, size, _ uint32) (uint32, error) { return size, nil }
		}},
		{"overflow result", "VOLUME_PATH_LIMIT", func(c *volumeCalls) {
			c.finalPath = func(windows.Handle, *uint16, uint32, uint32) (uint32, error) { return ^uint32(0), nil }
		}},
		{"missing terminator", "VOLUME_UNAVAILABLE", func(c *volumeCalls) {
			c.finalPath = func(_ windows.Handle, buffer *uint16, size, _ uint32) (uint32, error) {
				unsafe.Slice(buffer, int(size))[2] = 'x'
				return 2, nil
			}
		}},
		{"unparseable root", "VOLUME_UNAVAILABLE", func(c *volumeCalls) {
			c.finalPath = func(_ windows.Handle, buffer *uint16, size, _ uint32) (uint32, error) {
				value := windows.StringToUTF16(`C:\fixture`)
				copy(unsafe.Slice(buffer, int(size)), value)
				return uint32(len(value) - 1), nil
			}
		}},
		{"volume query error", "VOLUME_UNAVAILABLE", func(c *volumeCalls) {
			c.information = func(windows.Handle, *uint16, uint32, *uint32, *uint32, *uint32, *uint16, uint32) error {
				return errors.New("private volume name and serial")
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := mockVolumeCalls(t, file)
			test.change(&calls)
			got := checkWindowsVolume(file, calls)
			if test.want == "" {
				if got != nil {
					t.Fatal(got)
				}
			} else {
				code(t, got, test.want)
				if got.Error() != "managed catalog path: "+test.want {
					t.Fatal("native metadata escaped fixed error")
				}
			}
		})
	}
	for _, drive := range []uint32{windows.DRIVE_UNKNOWN, windows.DRIVE_NO_ROOT_DIR, windows.DRIVE_REMOVABLE, windows.DRIVE_REMOTE, windows.DRIVE_CDROM, windows.DRIVE_RAMDISK, 12345} {
		calls := mockVolumeCalls(t, file)
		calls.driveType = func(*uint16) uint32 { return drive }
		calls.information = func(windows.Handle, *uint16, uint32, *uint32, *uint32, *uint32, *uint16, uint32) error {
			t.Fatal("unsupported drive queried filesystem details")
			return nil
		}
		want := "VOLUME_TYPE_UNSUPPORTED"
		if drive == windows.DRIVE_REMOTE {
			want = "VOLUME_REMOTE"
		} else if drive == windows.DRIVE_UNKNOWN || drive == windows.DRIVE_NO_ROOT_DIR {
			want = "VOLUME_UNAVAILABLE"
		}
		code(t, checkWindowsVolume(file, calls), want)
	}
	for _, test := range []struct {
		name  string
		flags uint32
		want  string
	}{
		{"ReFS", windows.FILE_PERSISTENT_ACLS, "VOLUME_FILESYSTEM_UNSUPPORTED"},
		{"exFAT", 0, "VOLUME_FILESYSTEM_UNSUPPORTED"},
		{"", 0, "VOLUME_FILESYSTEM_UNSUPPORTED"},
		{strings.Repeat("x", windows.MAX_PATH+1), 0, "VOLUME_UNAVAILABLE"},
		{"NTFS", 0, "VOLUME_ACLS_UNSUPPORTED"},
		{"NTFS", windows.FILE_PERSISTENT_ACLS | windows.FILE_READ_ONLY_VOLUME, "VOLUME_READ_ONLY"},
		{"ntfs", windows.FILE_PERSISTENT_ACLS | windows.FILE_SUPPORTS_HARD_LINKS, ""},
	} {
		calls := mockVolumeCalls(t, file)
		calls.information = func(_ windows.Handle, _ *uint16, _ uint32, _, _, flags *uint32, name *uint16, size uint32) error {
			*flags = test.flags
			copy(unsafe.Slice(name, int(size)), windows.StringToUTF16(test.name))
			return nil
		}
		got := checkWindowsVolume(file, calls)
		if test.want == "" {
			if got != nil {
				t.Fatal(got)
			}
		} else {
			code(t, got, test.want)
		}
	}
}

func TestLocalVolumeGUIDRootNeverFallsBackToDriveNames(t *testing.T) {
	for _, path := range []string{syntheticVolumeRoot, syntheticVolumeRoot + "directory", syntheticVolumeRoot + "路径"} {
		units := windows.StringToUTF16(path)
		root, e := volumeGUIDRoot(units[:len(units)-1])
		if e != nil || windows.UTF16ToString(root[:]) != syntheticVolumeRoot {
			t.Fatal("valid GUID root refused")
		}
	}
	for _, path := range []string{
		"", `C:\`, `\\server\share\`, `\\?\UNC\server\share\`, `\Device\HarddiskVolume1\`,
		syntheticVolumeRoot[:len(syntheticVolumeRoot)-1],
		strings.Replace(syntheticVolumeRoot, "Volume", "volume", 1),
		strings.Replace(syntheticVolumeRoot, "12345678", "1234567z", 1),
		strings.Replace(syntheticVolumeRoot, "-", "_", 1),
		strings.Replace(syntheticVolumeRoot, "}", "]", 1),
		syntheticVolumeRoot + "\x00tail",
	} {
		units := make([]uint16, len(path))
		for i := range path {
			units[i] = uint16(path[i])
		}
		root, e := volumeGUIDRoot(units)
		code(t, e, "VOLUME_UNAVAILABLE")
		if root != [volumeGUIDRootLen + 1]uint16{} {
			t.Fatal("failed volume parse returned a partial root")
		}
	}
}
