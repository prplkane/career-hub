//go:build windows

package runtimepaths

import (
	"os"
	"runtime"
	"strings"

	"golang.org/x/sys/windows"
)

const (
	volumeCheckSupported = true
	// GetFinalPathNameByHandleW: avoid DOS-letter/cwd inference. Network shares
	// have no volume GUID; unsupported mount-manager results are refused.
	volumeNameGUID = 0x1
	fileNameOpened = 0x8
	// GUID replacement is longer than a drive letter; never grow unboundedly.
	maxVolumePathUnits = MaxPathBytes + 64 + 1
	volumeGUIDPrefix   = `\\?\Volume{`
	volumeGUIDRootLen  = len(volumeGUIDPrefix) + 36 + 2
)

type volumeCalls struct {
	finalPath   func(windows.Handle, *uint16, uint32, uint32) (uint32, error)
	driveType   func(*uint16) uint32
	information func(windows.Handle, *uint16, uint32, *uint32, *uint32, *uint32, *uint16, uint32) error
}

func checkLocalVolume(file *os.File) *Error {
	return checkWindowsVolume(file, volumeCalls{
		finalPath: windows.GetFinalPathNameByHandle, driveType: windows.GetDriveType,
		information: windows.GetVolumeInformationByHandle,
	})
}

func checkWindowsVolume(file *os.File, calls volumeCalls) *Error {
	defer runtime.KeepAlive(file)
	var path [maxVolumePathUnits]uint16
	n, err := calls.finalPath(windows.Handle(file.Fd()), &path[0], uint32(len(path)), volumeNameGUID|fileNameOpened)
	if err != nil || n == 0 {
		return failure("VOLUME_UNAVAILABLE")
	}
	if n >= uint32(len(path)) {
		return failure("VOLUME_PATH_LIMIT")
	}
	// A successful Win32 length excludes the terminator. Do not interpret a
	// truncated/nonterminated result as a shorter path or fall back to a letter.
	if path[n] != 0 {
		return failure("VOLUME_UNAVAILABLE")
	}
	root, e := volumeGUIDRoot(path[:n])
	if e != nil {
		return e
	}
	switch calls.driveType(&root[0]) {
	case windows.DRIVE_FIXED:
	case windows.DRIVE_REMOTE:
		return failure("VOLUME_REMOTE")
	case windows.DRIVE_UNKNOWN, windows.DRIVE_NO_ROOT_DIR:
		return failure("VOLUME_UNAVAILABLE")
	default:
		return failure("VOLUME_TYPE_UNSUPPORTED")
	}
	var flags uint32
	var name [windows.MAX_PATH + 1]uint16
	// Do not request a volume label or serial number. The path/GUID is ephemeral
	// native input only; none of it is exposed in an Error or a diagnostic.
	if err := calls.information(windows.Handle(file.Fd()), nil, 0, nil, nil, &flags, &name[0], uint32(len(name))); err != nil {
		return failure("VOLUME_UNAVAILABLE")
	}
	terminator := -1
	for i, value := range name {
		if value == 0 {
			terminator = i
			break
		}
	}
	if terminator < 0 {
		return failure("VOLUME_UNAVAILABLE")
	}
	if !strings.EqualFold(windows.UTF16ToString(name[:terminator]), "NTFS") {
		return failure("VOLUME_FILESYSTEM_UNSUPPORTED")
	}
	if flags&windows.FILE_PERSISTENT_ACLS == 0 {
		return failure("VOLUME_ACLS_UNSUPPORTED")
	}
	if flags&windows.FILE_READ_ONLY_VOLUME != 0 {
		return failure("VOLUME_READ_ONLY")
	}
	return nil
}

// Return only a validated volume GUID root, with the required trailing slash
// and NUL. The remainder of the native path is never used to open another file.
func volumeGUIDRoot(path []uint16) ([volumeGUIDRootLen + 1]uint16, *Error) {
	var root [volumeGUIDRootLen + 1]uint16
	if len(path) < volumeGUIDRootLen {
		return root, failure("VOLUME_UNAVAILABLE")
	}
	for _, unit := range path {
		if unit == 0 {
			return root, failure("VOLUME_UNAVAILABLE")
		}
	}
	for i, want := range volumeGUIDPrefix {
		if path[i] != uint16(want) {
			return root, failure("VOLUME_UNAVAILABLE")
		}
	}
	for i := range 36 {
		unit := path[len(volumeGUIDPrefix)+i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if unit != '-' {
				return root, failure("VOLUME_UNAVAILABLE")
			}
		} else if !(unit >= '0' && unit <= '9' || unit >= 'a' && unit <= 'f' || unit >= 'A' && unit <= 'F') {
			return root, failure("VOLUME_UNAVAILABLE")
		}
	}
	if path[volumeGUIDRootLen-2] != '}' || path[volumeGUIDRootLen-1] != '\\' {
		return root, failure("VOLUME_UNAVAILABLE")
	}
	copy(root[:], path[:volumeGUIDRootLen])
	return root, nil
}
