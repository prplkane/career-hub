# Legacy Windows DPAPI byte adapter

`UnprotectLegacy([]byte) ([]byte, error)` is the CH-31 migration feasibility
boundary for `src/windows_dpapi.py::unprotect_legacy_document`. It accepts one
caller-supplied ciphertext and returns an independently owned plaintext. This
package is not connected to product startup or the migration coordinator.

It does not discover DataHome, read/write files, access credentials, generate
keys, protect new documents, select a fallback, or cache ciphertext/plaintext.
The existing versioned encrypted-document envelope is unchanged. There is no
public API for injecting an alternative native backend.

## Preserved behavior

- Non-Windows platforms return `WINDOWS_DPAPI_UNAVAILABLE` before input
  validation. Windows requires nonempty bytes and retains arbitrary binary
  content, including embedded NUL bytes.
- `CryptUnprotectData` is called with `CRYPTPROTECT_UI_FORBIDDEN` (`1`), NULL
  description output, NULL entropy, NULL reserved and NULL prompt. It does not
  introduce alternate identity or machine-scope options. The native BOOL result
  determines success; incidental stale LastError values are ignored.
- Input is copied before native resolution/calls. Output is copied before
  `LocalFree`, so callers cannot retain aliases to native buffers. A successful
  zero-length output remains an empty nonnil byte slice; document-level rules
  must reject an empty document where appropriate.
- Failed decryption returns the source's fixed account-focused error text. A
  FALSE result does not establish caller ownership of an output allocation;
  this adapter neither inspects nor frees an uncertain failure-result pointer.
  This matches the Python wrapper and avoids assuming that partially allocated
  native memory remains caller-owned after a failed call.

## Explicit safety deltas and limits

The raw Python wrapper has no size limits. This adapter limits ciphertext to
`20 MiB + 64 KiB` (the existing legacy migration consumer's input bound) and
plaintext to `20 MiB` (the document-size bound). These are stricter byte-adapter
contracts, not claims about all ciphertext accepted by DPAPI.

Oversized native output and a nonzero length with a NULL output pointer are
rejected before reading memory. Every successful-call output is passed once to
`LocalFree`, including malformed output; NULL is a documented valid argument.
If cleanup fails, no plaintext is returned, even if copying succeeded. Cleanup
failure takes precedence over malformed-output failure. The Python wrapper
ignores the `LocalFree` return value; this adapter deliberately fails closed.
It does not retry a failed/uncertain free or promise secure erasure of Go/native
allocator memory.

DLL resolution uses only `windows.NewLazySystemDLL` with fixed `crypt32.dll` and
`kernel32.dll` names. Package-level lazy binding objects reuse synchronized
resolution; they perform no eager native calls and hold no document data.
Procedure discovery failures are returned, not panicked. The pointer call shim
preserves Go's uintptr escape contract, with inline pointer conversions and
explicit liveness barriers.

Errors contain only fixed adapter codes/messages. The Go code attributes are
new metadata; Python's `WindowsDPAPIError` exposes message-only errors. No raw
Win32 error, underlying exception, path, pointer, input or output is retained.

## Integrity and remaining gates

Native success is **not** proof that arbitrary corruption was detected. Microsoft
documents varied tamper errors and possible success with corrupted output.
Caller validation of ledger structure, allowed sizes and recorded document
hashes must remain in the recovery pipeline. This package does not replace those
checks or port the complete recovery transaction.

Ordinary package tests use private fake native APIs/procedures only: they do not
resolve the native factory, inspect credentials, or decrypt user documents.
They cover ABI layout, exact arguments, bounds, binary copy ownership, failed
calls, successful malformed outputs, cleanup, stale errors and minimized errors.
Windows ABI and non-Windows branches can additionally be cross-compiled; that is
compiler evidence, not native acceptance. Separately gated synthetic Python/Go
interoperability and recovery-consumer integration remain parent-owned checks.

Primary references:

- [Microsoft CryptUnprotectData](https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptunprotectdata)
- [Microsoft LocalFree](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-localfree)
- Existing pinned `golang.org/x/sys/windows` v0.47.0 `DataBlob` and
  `NewLazySystemDLL` implementation; no dependency/version change.
