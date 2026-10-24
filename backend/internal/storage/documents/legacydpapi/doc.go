// Package legacydpapi preserves the byte-only legacy Windows document
// decryption boundary. It does not read files, access credentials, generate
// keys, protect new documents or coordinate migration, and native success
// does not replace the caller's ledger, size and hash integrity checks.
//
// UnprotectLegacy(ciphertext) returns an independently owned plaintext.
// Non-Windows builds return WINDOWS_DPAPI_UNAVAILABLE before input
// validation. On Windows the input must be nonempty and may contain
// arbitrary bytes, including NUL. CryptUnprotectData is called with
// CRYPTPROTECT_UI_FORBIDDEN and null description, entropy, reserved and
// prompt arguments; no alternate identity or machine-scope option is
// introduced. The native BOOL result decides success and stale last-error
// values are ignored.
//
// Bounds: ciphertext at most 20 MiB + 64 KiB and plaintext at most 20 MiB.
// Oversized output and a nonzero length with a null pointer are rejected
// before memory is read. Input is copied before the native call and output
// is copied before LocalFree, so callers never alias native buffers; an
// empty successful output is an empty non-nil slice. Every successful-call
// output is passed exactly once to LocalFree; a failed free discards the
// plaintext and takes precedence over a malformed-output failure. A failed
// decryption does not touch the output pointer.
//
// Only crypt32.dll and kernel32.dll are bound, through lazy system DLL
// resolution; procedure lookup failures are returned, not panicked. Errors
// carry fixed codes and messages with no Win32 error, path, pointer, input
// or output.
package legacydpapi
