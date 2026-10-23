// Package secretstore defines the explicitly selected credential-store boundary.
// It provides no listing, plaintext fallback, or automatic key regeneration.
package secretstore

import (
	"fmt"
	"io"
	"reflect"
	"strconv"
)

// Backend exchanges bytes without assuming a text encoding. Read's found flag
// distinguishes missing entries from existing empty values. Errors take
// precedence over accompanying values; callers must never treat them as absence.
type Backend interface {
	Write(target string, secret []byte) error
	Read(target string) (secret []byte, found bool, err error)
	Delete(target string) (deleted bool, err error)
}

// IsNilBackend detects nil interfaces and interfaces containing typed nil values.
func IsNilBackend(backend Backend) bool {
	if backend == nil {
		return true
	}
	value := reflect.ValueOf(backend)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	}
	return false
}

const (
	CodeBackendUnavailable = "SECRET_STORE_UNAVAILABLE"
	CodeWindowsFailure     = "SECRET_STORE_WINDOWS_ERROR"
	CodeTargetInvalid      = "SECRET_TARGET_INVALID"
	CodeBlobTooLarge       = "SECRET_BLOB_TOO_LARGE"
	CodeInvalidResult      = "SECRET_BACKEND_INVALID_RESULT"
)

// BackendError retains only a fixed category/operation and numeric Win32 code.
// It never retains target names, credential bytes, underlying errors or DLL paths.
// Codes are Go adapter categories, not invented Python exception attributes.
type BackendError struct {
	Code      string
	Operation string
	Win32Code uint32
}

func (e *BackendError) Error() string {
	if e != nil {
		switch e.Code {
		case CodeBackendUnavailable:
			if e.Operation == "initialize" {
				return "Windows Credential Manager could not be initialized"
			}
			return "Windows Credential Manager is unavailable on this platform"
		case CodeWindowsFailure:
			op := e.Operation
			if op != "write" && op != "read" && op != "delete" {
				op = "operation"
			}
			return "Windows Credential Manager " + op + " failed (Win32 error " + strconv.FormatUint(uint64(e.Win32Code), 10) + ")"
		case CodeTargetInvalid:
			return "Windows Credential Manager target is invalid"
		case CodeBlobTooLarge:
			return "Windows Credential Manager value exceeds the supported native size"
		case CodeInvalidResult:
			return "Windows Credential Manager returned an invalid value"
		}
	}
	return "Windows Credential Manager operation failed"
}

// Format keeps even verbose diagnostic formatting content-minimized.
func (e *BackendError) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
