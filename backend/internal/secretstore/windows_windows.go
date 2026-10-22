//go:build windows

package secretstore

import (
	"bytes"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	credentialGeneric     = 1
	persistLocalMachine   = 2
	errorNotFound         = 1168
	MaxWindowsTargetUnits = 32767 // Excludes the terminating UTF-16 NUL.
	MaxWindowsBlobBytes   = 2560
)

// CREDENTIAL_ATTRIBUTEW and CREDENTIALW use native pointer alignment. FILETIME
// remains two DWORDs on both Win32 and Win64; no Go int or bool crosses the ABI.
type credentialAttribute struct {
	Keyword   *uint16
	Flags     uint32
	ValueSize uint32
	Value     *byte
}

type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         *credentialAttribute
	TargetAlias        *uint16
	UserName           *uint16
}

type credentialAPI interface {
	Write(*credential, uint32) (bool, uint32)
	Read(*uint16, uint32, uint32) (*credential, bool, uint32)
	Delete(*uint16, uint32, uint32) (bool, uint32)
	Free(*credential)
}

type procedure interface {
	Find() error
	Call(...uintptr) (uintptr, uintptr, error)
}

type windowsAPI struct{ write, read, delete, free procedure }

// callProcedure preserves the escape/liveness contract of LazyProc.Call across
// our injectable interface. In particular Read's pointer-to-result must not
// remain on a movable Go stack after conversion to uintptr.
//
//go:uintptrescapes
func callProcedure(proc procedure, args ...uintptr) (uintptr, uintptr, error) {
	return proc.Call(args...)
}

func bindWindowsAPI(newProc func(string) procedure) (credentialAPI, error) {
	api := &windowsAPI{}
	for _, item := range []struct {
		name string
		dest *procedure
	}{
		{"CredWriteW", &api.write}, {"CredReadW", &api.read}, {"CredDeleteW", &api.delete}, {"CredFree", &api.free},
	} {
		proc := newProc(item.name)
		if proc == nil || proc.Find() != nil {
			return nil, &BackendError{Code: CodeBackendUnavailable, Operation: "initialize"}
		}
		*item.dest = proc
	}
	return api, nil
}

// NewWindowsBackend resolves only the approved System32 DLL/procedures. Finding
// functions does not read/write/list credentials. Lookup failure returns a safe
// error before any LazyProc.Call can take its panic-on-unresolved path.
func NewWindowsBackend() (Backend, error) {
	dll := windows.NewLazySystemDLL("advapi32.dll")
	api, err := bindWindowsAPI(func(name string) procedure { return dll.NewProc(name) })
	if err != nil {
		return nil, err
	}
	return &windowsBackend{api: api}, nil
}

// errnoCode examines the error returned by the syscall itself. Do not call
// GetLastError later: another syscall/OS thread could replace that value.
func errnoCode(err error) uint32 {
	if code, ok := err.(syscall.Errno); ok {
		return uint32(code)
	}
	return 0
}

func (a *windowsAPI) Write(value *credential, flags uint32) (bool, uint32) {
	result, _, lastError := callProcedure(a.write, uintptr(unsafe.Pointer(value)), uintptr(flags))
	code := errnoCode(lastError)
	runtime.KeepAlive(value)
	return result != 0, code
}
func (a *windowsAPI) Read(target *uint16, kind, flags uint32) (*credential, bool, uint32) {
	var value *credential
	result, _, lastError := callProcedure(a.read, uintptr(unsafe.Pointer(target)), uintptr(kind), uintptr(flags), uintptr(unsafe.Pointer(&value)))
	code := errnoCode(lastError)
	runtime.KeepAlive(target)
	return value, result != 0, code
}
func (a *windowsAPI) Delete(target *uint16, kind, flags uint32) (bool, uint32) {
	result, _, lastError := callProcedure(a.delete, uintptr(unsafe.Pointer(target)), uintptr(kind), uintptr(flags))
	code := errnoCode(lastError)
	runtime.KeepAlive(target)
	return result != 0, code
}
func (a *windowsAPI) Free(value *credential) {
	// CredFree returns void; its incidental return register/last-error are not status.
	_, _, _ = callProcedure(a.free, uintptr(unsafe.Pointer(value)))
	runtime.KeepAlive(value)
}

type windowsBackend struct{ api credentialAPI }

func windowsTarget(target string) ([]uint16, error) {
	if !utf8.ValidString(target) || strings.ContainsRune(target, 0) {
		return nil, &BackendError{Code: CodeTargetInvalid}
	}
	units := 0
	for _, r := range target {
		units++
		if r > 0xffff {
			units++
		}
		if units > MaxWindowsTargetUnits {
			return nil, &BackendError{Code: CodeTargetInvalid}
		}
	}
	encoded, err := windows.UTF16FromString(target)
	if err != nil {
		return nil, &BackendError{Code: CodeTargetInvalid}
	}
	return encoded, nil
}

func windowsFailure(operation string, code uint32) error {
	return &BackendError{Code: CodeWindowsFailure, Operation: operation, Win32Code: code}
}

func (b *windowsBackend) Write(target string, secret []byte) error {
	name, err := windowsTarget(target)
	if err != nil {
		return err
	}
	if len(secret) > MaxWindowsBlobBytes {
		return &BackendError{Code: CodeBlobTooLarge}
	}
	// Snapshot caller bytes and match ctypes' trailing zero allocation; that
	// extra byte is not part of CredentialBlobSize, including an empty secret.
	blob := make([]byte, len(secret)+1)
	copy(blob, secret)
	username := []uint16{'C', 'a', 'r', 'e', 'e', 'r', 'H', 'u', 'b', 0}
	value := credential{Type: credentialGeneric, TargetName: &name[0], CredentialBlobSize: uint32(len(secret)), CredentialBlob: &blob[0], Persist: persistLocalMachine, UserName: &username[0]}
	ok, code := b.api.Write(&value, 0)
	runtime.KeepAlive(name)
	runtime.KeepAlive(blob)
	runtime.KeepAlive(username)
	runtime.KeepAlive(value)
	if !ok {
		return windowsFailure("write", code)
	}
	return nil
}

func (b *windowsBackend) Read(target string) ([]byte, bool, error) {
	name, err := windowsTarget(target)
	if err != nil {
		return nil, false, err
	}
	value, ok, code := b.api.Read(&name[0], credentialGeneric, 0)
	runtime.KeepAlive(name)
	if !ok {
		if code == errorNotFound {
			return nil, false, nil
		}
		return nil, false, windowsFailure("read", code)
	}
	if value == nil {
		return nil, false, &BackendError{Code: CodeInvalidResult}
	}
	// Every successful nonnil native result is freed, including malformed ones.
	// A nil result has no allocation to free and is rejected without dereference.
	defer b.api.Free(value)
	if value.Type != credentialGeneric || value.CredentialBlobSize > MaxWindowsBlobBytes || value.CredentialBlobSize > 0 && value.CredentialBlob == nil {
		return nil, false, &BackendError{Code: CodeInvalidResult}
	}
	if value.CredentialBlobSize == 0 {
		return []byte{}, true, nil
	}
	result := bytes.Clone(unsafe.Slice(value.CredentialBlob, int(value.CredentialBlobSize)))
	runtime.KeepAlive(value)
	return result, true, nil
}

func (b *windowsBackend) Delete(target string) (bool, error) {
	name, err := windowsTarget(target)
	if err != nil {
		return false, err
	}
	ok, code := b.api.Delete(&name[0], credentialGeneric, 0)
	runtime.KeepAlive(name)
	if ok {
		return true, nil
	}
	if code == errorNotFound {
		return false, nil
	}
	return false, windowsFailure("delete", code)
}
