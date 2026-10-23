//go:build windows

package legacydpapi

import (
	"bytes"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const cryptprotectUIForbidden = 1

// Lazy binding objects do not load DLLs at initialization. Reuse their
// synchronized procedure resolution without retaining caller bytes or results.
var (
	crypt32DLL         = windows.NewLazySystemDLL("crypt32.dll")
	kernel32DLL        = windows.NewLazySystemDLL("kernel32.dll")
	unprotectProcedure = crypt32DLL.NewProc("CryptUnprotectData")
	freeProcedure      = kernel32DLL.NewProc("LocalFree")
)

type nativeAPI interface {
	Unprotect(source, output *windows.DataBlob) bool
	Free(pointer *byte) bool
}

type procedure interface {
	Find() error
	Call(...uintptr) (uintptr, uintptr, error)
}

type windowsAPI struct{ unprotect, free procedure }

// Preserve the LazyProc.Call pointer-escape contract across the injected
// interface: uintptr conversions must not leave the source/output on a movable
// Go stack. All conversions occur directly in this call's argument expressions.
//
//go:uintptrescapes
func callProcedure(proc procedure, args ...uintptr) (uintptr, uintptr, error) {
	return proc.Call(args...)
}

func bindWindowsAPI(unprotect, free procedure) (nativeAPI, error) {
	if unprotect == nil || unprotect.Find() != nil || free == nil || free.Find() != nil {
		return nil, fail(CodeInitializationFailed)
	}
	return &windowsAPI{unprotect: unprotect, free: free}, nil
}

func loadWindowsAPI() (nativeAPI, error) {
	return bindWindowsAPI(unprotectProcedure, freeProcedure)
}

func (a *windowsAPI) Unprotect(source, output *windows.DataBlob) bool {
	// Description output, entropy, reserved and prompt are all NULL. Do not
	// change user/machine scope, request interactive UI, or add recovery entropy.
	result, _, _ := callProcedure(a.unprotect,
		uintptr(unsafe.Pointer(source)), 0, 0, 0, 0, cryptprotectUIForbidden, uintptr(unsafe.Pointer(output)))
	runtime.KeepAlive(source)
	runtime.KeepAlive(output)
	// The BOOL result defines success. Incidental stale LastError is irrelevant;
	// all actual failures use the source's fixed account-focused error message.
	return result != 0
}

func (a *windowsAPI) Free(pointer *byte) bool {
	result, _, _ := callProcedure(a.free, uintptr(unsafe.Pointer(pointer)))
	runtime.KeepAlive(pointer)
	// LocalFree succeeds with NULL, including an input NULL. A returned handle
	// means failure; never retry an uncertain free or inspect the freed memory.
	return result == 0
}

// UnprotectLegacy decrypts one caller-supplied legacy ciphertext. It performs
// no filesystem/credential operations and exposes no production protect helper.
// Callers must still validate decrypted ledger structure and document size/hash.
func UnprotectLegacy(content []byte) ([]byte, error) {
	return unprotectWith(content, loadWindowsAPI)
}

// unprotectWith is deliberately private: ordinary tests inject the native API
// instead of invoking any native factory or transforming existing user data.
func unprotectWith(content []byte, resolve func() (nativeAPI, error)) (plaintext []byte, err error) {
	if len(content) == 0 {
		return nil, fail(CodeInputInvalid)
	}
	if len(content) > MaxCiphertextBytes {
		return nil, fail(CodeInputTooLarge)
	}
	// Match the ctypes input buffer's trailing NUL without including it in Size.
	// Snapshot before the injected resolver/native call can mutate caller bytes.
	buffer := make([]byte, len(content)+1)
	copy(buffer, content)
	source := windows.DataBlob{Size: uint32(len(content)), Data: &buffer[0]}
	api, resolveErr := resolve()
	if resolveErr != nil || api == nil {
		return nil, fail(CodeInitializationFailed)
	}
	var output windows.DataBlob
	ok := api.Unprotect(&source, &output)
	runtime.KeepAlive(buffer)
	runtime.KeepAlive(source)
	if !ok {
		// Failure does not establish ownership of an output allocation. Do not
		// copy or free an uncertain pointer left in the failed-call result.
		return nil, fail(CodeDecryptFailed)
	}
	// Every successful-call output (including NULL and malformed metadata) is
	// passed once to LocalFree. Its failure suppresses even a copied plaintext.
	defer func() {
		if !api.Free(output.Data) {
			clear(plaintext)
			plaintext = nil
			err = fail(CodeCleanupFailed)
		}
	}()
	if output.Size > MaxPlaintextBytes || output.Size > 0 && output.Data == nil {
		return nil, fail(CodeOutputInvalid)
	}
	if output.Size == 0 {
		return []byte{}, nil
	}
	plaintext = bytes.Clone(unsafe.Slice(output.Data, int(output.Size)))
	runtime.KeepAlive(output)
	return plaintext, nil
}
