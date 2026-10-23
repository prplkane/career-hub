//go:build windows

package runtimepaths

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const ownershipSupported = true
const ownershipAccess = windows.READ_CONTROL | windows.SYNCHRONIZE

type processClaim struct {
	roots []*os.File
	token windows.Handle
}

func processRootOwned(claim *processClaim, r *Root) bool {
	if claim == nil || len(claim.roots) == 0 || claim.token == 0 {
		return false
	}
	owned, err := claim.roots[len(claim.roots)-1].Stat()
	if err != nil {
		return false
	}
	current, err := r.base.Stat(".")
	return err == nil && !isLinkLike(current) && current.IsDir() && os.SameFile(owned, current)
}

// Go's handle-relative OpenRoot permits delete sharing. Reopen the same native
// object without that sharing, rather than resolving its absolute pathname.
func pinOwnershipRoot(original *os.Root) (pinned *os.File, problem *Error) {
	borrowed, err := original.Open(".")
	if err != nil {
		return nil, failure("OWNERSHIP_UNAVAILABLE")
	}
	defer func() {
		if err := borrowed.Close(); err != nil && problem == nil {
			if pinned != nil {
				_ = pinned.Close()
				pinned = nil
			}
			problem = failure("OWNERSHIP_UNAVAILABLE")
		}
	}()
	// An empty NT name reopens RootDirectory itself. Pinned Go's Openat uses
	// this same representation for "."; do not resolve a DOS or final path.
	name, err := windows.NewNTUnicodeString("")
	if err != nil {
		return nil, failure("OWNERSHIP_UNAVAILABLE")
	}
	attributes := windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: windows.Handle(borrowed.Fd()), ObjectName: name, Attributes: windows.OBJ_DONT_REPARSE}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ, &attributes, &status, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_FOR_BACKUP_INTENT, 0, 0)
	runtime.KeepAlive(borrowed)
	if err != nil || handle == windows.InvalidHandle || handle == 0 {
		return nil, failure("OWNERSHIP_UNAVAILABLE")
	}
	pinned = os.NewFile(uintptr(handle), ".")
	if pinned == nil {
		_ = windows.CloseHandle(handle)
		return nil, failure("OWNERSHIP_UNAVAILABLE")
	}
	return pinned, nil
}

// close is for failed acquisition and isolated tests only. Published claims
// remain strongly referenced until OS process exit, including startup failures.
func (c *processClaim) close() *Error {
	var problem *Error
	for i := len(c.roots) - 1; i >= 0; i-- {
		if c.roots[i].Close() != nil {
			problem = failure("OWNERSHIP_UNAVAILABLE")
		}
	}
	c.roots = nil
	if c.token != 0 {
		if windows.CloseHandle(c.token) != nil {
			problem = failure("OWNERSHIP_UNAVAILABLE")
		}
		c.token = 0
	}
	return problem
}

// Caller holds Root's read lock and the process claim lock.
func acquireProcessClaim(r *Root) (_ *processClaim, problem *Error) {
	if e := checkBindings(r.bindings); e != nil {
		return nil, e
	}
	c := &processClaim{}
	keep := false
	defer func() {
		if !keep {
			if e := c.close(); e != nil {
				problem = e
			}
		}
	}()
	for _, original := range r.handles {
		clone, e := pinOwnershipRoot(original)
		if e != nil {
			return nil, e
		}
		c.roots = append(c.roots, clone)
		before, err := original.Stat(".")
		if err != nil {
			return nil, failure("PATH_CHANGED")
		}
		after, err := clone.Stat()
		if err != nil || isLinkLike(after) || !after.IsDir() || !os.SameFile(before, after) {
			return nil, failure("PATH_CHANGED")
		}
	}
	directory := c.roots[len(c.roots)-1]
	before, err := directory.Stat()
	if err != nil || isLinkLike(before) || !before.IsDir() || !os.SameFile(before, r.bindings[len(r.bindings)-1].identity) {
		return nil, failure("PATH_CHANGED")
	}
	// NTFS is the qualified initial target; the 64-bit native file identity
	// below is not claimed to be sufficient for every other filesystem.
	if e := checkLocalVolume(directory); e != nil {
		return nil, failure("OWNERSHIP_UNAVAILABLE")
	}
	var e *Error
	c.token, e = acquireOwnershipToken(directory)
	if e != nil {
		return nil, e
	}
	after, err := directory.Stat()
	if err != nil || isLinkLike(after) || !after.IsDir() || !os.SameFile(before, after) {
		return nil, failure("PATH_CHANGED")
	}
	if e := checkBindings(r.bindings); e != nil {
		return nil, e
	}
	keep = true
	return c, nil
}

func ownershipName(file *os.File) (string, *Error) {
	defer runtime.KeepAlive(file)
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info) != nil ||
		info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 ||
		info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		info.FileIndexHigh == 0 && info.FileIndexLow == 0 {
		return "", failure("OWNERSHIP_UNAVAILABLE")
	}
	var identity [12]byte
	binary.LittleEndian.PutUint32(identity[0:4], info.VolumeSerialNumber)
	binary.LittleEndian.PutUint32(identity[4:8], info.FileIndexHigh)
	binary.LittleEndian.PutUint32(identity[8:12], info.FileIndexLow)
	digest := sha256.Sum256(identity[:])
	// No account/session/channel/port/path spelling in the key: those must not
	// split ownership for the same directory. A cloned volume identity can
	// cause conservative false contention, never permission to ignore a claim.
	return `Global\CareerHub.DataHomeOwner.v1.` + hex.EncodeToString(digest[:]), nil
}

func acquireOwnershipToken(file *os.File) (windows.Handle, *Error) {
	name, e := ownershipName(file)
	if e != nil {
		return 0, e
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil || !user.User.Sid.IsValid() {
		return 0, failure("OWNERSHIP_UNAVAILABLE")
	}
	sid := user.User.Sid.String()
	sddl := "D:P(A;;0x00120000;;;" + sid + ")"
	if sid != "S-1-5-18" {
		sddl += "(A;;0x00120000;;;SY)"
	}
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return 0, failure("OWNERSHIP_UNAVAILABLE")
	}
	attributes := windows.SecurityAttributes{
		Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor,
		InheritHandle: 0,
	}
	nativeName, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, failure("OWNERSHIP_UNAVAILABLE")
	}
	// This is an existence token, not thread-owned synchronization. Never set
	// INITIAL_OWNER or use Wait/ReleaseMutex (Go goroutines can change threads).
	token, err := windows.CreateMutexEx(&attributes, nativeName, 0, ownershipAccess)
	runtime.KeepAlive(descriptor)
	if err != nil {
		if token != 0 {
			if windows.CloseHandle(token) != nil {
				return 0, failure("OWNERSHIP_UNAVAILABLE")
			}
		}
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return 0, failure("OWNERSHIP_IN_USE")
		}
		return 0, failure("OWNERSHIP_UNAVAILABLE")
	}
	if token == 0 {
		return 0, failure("OWNERSHIP_UNAVAILABLE")
	}
	return token, nil
}
