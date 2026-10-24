//go:build windows

package runtimepaths

import (
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

type credentialFileID struct {
	VolumeSerial uint64
	FileID       [16]byte
}

type credentialScopeQueries struct {
	identity func(windows.Handle) (credentialFileID, error)
	path     func(windows.Handle, *uint16, uint32, uint32) (uint32, error)
}

func credentialScopeForClaim(claim *processClaim) (CredentialScope, *Error) {
	if claim == nil || len(claim.roots) == 0 || claim.token == 0 {
		return CredentialScope{}, failure("OWNERSHIP_REQUIRED")
	}
	file := claim.roots[len(claim.roots)-1]
	if e := checkLocalVolume(file); e != nil {
		return CredentialScope{}, failure("CREDENTIAL_SCOPE_UNAVAILABLE")
	}
	return queryCredentialScope(file, credentialScopeQueries{
		identity: func(handle windows.Handle) (credentialFileID, error) {
			var info credentialFileID
			err := windows.GetFileInformationByHandleEx(handle, windows.FileIdInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
			return info, err
		},
		path: windows.GetFinalPathNameByHandle,
	})
}

func queryCredentialScope(file *os.File, queries credentialScopeQueries) (CredentialScope, *Error) {
	if file == nil || queries.identity == nil || queries.path == nil {
		return CredentialScope{}, failure("CREDENTIAL_SCOPE_UNAVAILABLE")
	}
	defer runtime.KeepAlive(file)
	handle := windows.Handle(file.Fd())
	before, err := queries.identity(handle)
	if err != nil || before.FileID == [16]byte{} {
		return CredentialScope{}, failure("CREDENTIAL_SCOPE_UNAVAILABLE")
	}
	newBuffer := func(size int) []uint16 {
		buffer := make([]uint16, size)
		for i := range buffer {
			buffer[i] = 0xffff
		}
		return buffer
	}
	buffer := newBuffer(512)
	// FILE_NAME_NORMALIZED = 0, VOLUME_NAME_GUID = 1. Never reuse the volume
	// admission helper's FILE_NAME_OPENED flags or fall back to a DOS path.
	n, err := queries.path(handle, &buffer[0], uint32(len(buffer)), 1)
	if err == nil && n >= uint32(len(buffer)) && n <= maxCredentialPathUnits {
		buffer = newBuffer(int(n))
		n, err = queries.path(handle, &buffer[0], uint32(len(buffer)), 1)
	}
	if err != nil || n == 0 || n >= uint32(len(buffer)) || buffer[n] != 0 {
		return CredentialScope{}, failure("CREDENTIAL_SCOPE_UNAVAILABLE")
	}
	after, err := queries.identity(handle)
	if err != nil || before != after {
		return CredentialScope{}, failure("CREDENTIAL_SCOPE_UNAVAILABLE")
	}
	return credentialScopeDigest(before.VolumeSerial, before.FileID, buffer[:n])
}
