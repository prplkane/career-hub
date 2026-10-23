// Package legacydpapi preserves the byte-only legacy Windows document decryption
// boundary. It does not read files, access credentials, or coordinate migration.
// Native success does not replace the caller's ledger/size/hash integrity checks.
package legacydpapi

import (
	"fmt"
	"io"
)

const (
	MaxCiphertextBytes = 20*1024*1024 + 64*1024
	MaxPlaintextBytes  = 20 * 1024 * 1024

	CodeUnavailable          = "WINDOWS_DPAPI_UNAVAILABLE"
	CodeInitializationFailed = "WINDOWS_DPAPI_INITIALIZATION_FAILED"
	CodeInputInvalid         = "WINDOWS_DPAPI_INPUT_INVALID"
	CodeInputTooLarge        = "WINDOWS_DPAPI_INPUT_TOO_LARGE"
	CodeDecryptFailed        = "WINDOWS_DPAPI_DECRYPTION_FAILED"
	CodeOutputInvalid        = "WINDOWS_DPAPI_OUTPUT_INVALID"
	CodeCleanupFailed        = "WINDOWS_DPAPI_CLEANUP_FAILED"
)

// Error retains only a stable adapter category. No native pointer, input/output
// bytes, identity, system path, error text or underlying exception is retained.
// Python exposes fixed messages but does not have these Go code attributes.
type Error struct{ Code string }

func (e *Error) Error() string {
	if e != nil {
		switch e.Code {
		case CodeUnavailable:
			return "Windows DPAPI is unavailable on this platform."
		case CodeInitializationFailed:
			return "Windows DPAPI could not be initialized."
		case CodeInputInvalid:
			return "Windows DPAPI input is invalid."
		case CodeInputTooLarge:
			return "Windows DPAPI input exceeds the supported legacy size."
		case CodeDecryptFailed:
			return "Windows DPAPI decryption failed for this account."
		case CodeOutputInvalid:
			return "Windows DPAPI returned invalid legacy output."
		case CodeCleanupFailed:
			return "Windows DPAPI output cleanup failed."
		}
	}
	return "Windows DPAPI operation failed."
}

func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }

func fail(code string) error { return &Error{Code: code} }
