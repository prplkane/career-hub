# Windows credential adapter pilot

`NewWindowsBackend` implements the byte-oriented backend used by
`src/secret_store.py` (`WindowsCredentialManagerBackend`) at source checkpoint
`0d9337bc`. It is not selected or activated by the product. The separate provider
facade and document-key manager supply their own exact target identities. This
adapter has no listing/enumeration, fallback, key-generation, recovery or retry API.

## Native boundary

Only the already-pinned `golang.org/x/sys/windows v0.47.0` is used. The factory
uses `NewLazySystemDLL("advapi32.dll")`, not the ambient DLL search path. It
explicitly resolves all four functions with `Find` before exposing an adapter;
DLL/procedure failure becomes a fixed unavailable error, not `mustFind` panic or
an error containing library paths. The lazy system DLL remains loaded for the
process lifetime. Finding functions is not a credential access.

Writes call `CredWriteW` with Generic type 1, Local Machine persistence 2, username
`CareerHub`, zero flags, and zeroed unused fields. A copied byte buffer has one
extra terminator byte, matching ctypes; the blob size excludes that byte.
Embedded NULs and arbitrary binary bytes in the **blob** are preserved, including
an empty value. Read/delete use Generic type 1 and zero flags. Only Win32 error
1168 means missing; other errors, including 5 and 1312, remain failures. A
successful BOOL return ignores incidental stale last-error values.

Generic credential targets are natively case-insensitive; the Go adapter passes
the supplied target spelling unchanged and does not invent its own normalization.
Persistence is for the same user's subsequent logon sessions on the same computer,
not enterprise roaming. See Microsoft's [CREDENTIALW contract](https://learn.microsoft.com/en-us/windows/win32/api/wincred/ns-wincred-credentialw).

`CREDENTIALW` uses DWORD fields, FILETIME's two DWORDs and native pointer
alignment. Host ABI tests check every field offset: on amd64 the size is 80,
blob-size offset 32, blob-pointer offset 40, persistence offset 48, attributes
offset 56 and username offset 72. Win32 expected size is 52. Cross-builds alone
do not establish target-host ABI behavior.

The syscall's returned errno is captured immediately; no later `GetLastError`
call is made. Explicit `runtime.KeepAlive` guards pointed-to buffers. The injectable
procedure forwarding shim carries `//go:uintptrescapes`, preserving the
`LazyProc.Call` escape contract: in particular Read's pointer-to-result is heap
allocated instead of remaining on a movable Go stack. The pinned compiler's
escape diagnostics verify this. Uintptr values are not retained for later calls.

Reads return independent byte copies before freeing native storage. Every
successful **nonnil** returned credential allocation is passed exactly once to
`CredFree`, including malformed results and existing-empty values with a nil blob.
Success with a nil credential pointer is rejected without dereference or a call
to free a nonexistent allocation. The adapter never returns a native-backed slice.
It trusts the documented native allocation contract; it cannot validate arbitrary
forged process addresses. See [CredReadW](https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credreadw)
and [CredWriteW](https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credwritew).

## Bounds, error privacy and explicit safety deltas

Before native calls, targets must be Unicode-scalar UTF-8, contain no embedded
NUL, and contain at most 32,767 UTF-16 code units excluding the terminator.
Supplementary characters count as two units. Target strings are not trimmed,
case-folded or filesystem-normalized. Empty target strings are forwarded to the
native API, as in the low-level Python source.

Native blobs are bounded to 2,560 bytes for both write and read. This is distinct
from the provider facade's 512-byte **Set** policy; the raw backend must still
support existing binary document keys and pre-existing values above 512 bytes.

Compared with the direct ctypes implementation, these are recorded stricter
refusals, not silently claimed error parity:

- NUL/invalid-scalar/overlong targets fail before OS conversion or truncation.
- Oversized writes fail before native invocation.
- Successful reads with a nongeneric type, oversized blob, nil credential, or
  nonempty blob with nil data pointer fail safely instead of unbounded copying.

`BackendError` contains only `Code`, a fixed `Operation`, and a numeric Win32 code.
No target, secret, native pointer, underlying exception, DLL path, or key is
retained. Error formatting, including `%+v` and `%#v`, is content-minimized.
Win32 operation messages match the Python form exactly. The five Go adapter
categories are `SECRET_STORE_UNAVAILABLE`, `SECRET_STORE_WINDOWS_ERROR`,
`SECRET_TARGET_INVALID`, `SECRET_BLOB_TOO_LARGE`, and
`SECRET_BACKEND_INVALID_RESULT`; Python does not expose these code attributes.

The package emits no logs. Callers must not log provided secrets or returned bytes.
Snapshotting input/output prevents aliasing after calls; concurrent mutation of
an input during a call remains a caller data race. Garbage collection does not
guarantee secret-memory zeroization.

## Verification and gates

All ordinary Windows tests inject a fake API/procedure resolver. They **do not
call `NewWindowsBackend` or access any native credential**. They cover exact
fields/UTF-16, binary/empty/maximum payloads, mutable-buffer ownership, stale errno,
errors 0/5/1168/1312, malformed-result cleanup, failed procedure discovery and ABI.
The unsupported-platform constructor is a separate `!windows` stub; it refuses
instead of selecting a different store.

From `backend` with the pinned toolchain, `GOTOOLCHAIN=local`, existing module
cache and no downloads:

```text
go test -count=1 -cover ./internal/secretstore
go vet ./internal/secretstore
go build -mod=readonly -gcflags=-m ./internal/secretstore
```

Parent-owned opt-in interoperability tests must create randomly named isolated
synthetic targets and clean up only those exact targets. Required native evidence
includes Python↔Go binary exchange, overwrite, empty/missing/delete, case aliases,
and cleanup; unavailable native access is not a pass. These tests must never list
credentials, use existing target names, inspect private Data Homes, or pass secrets
in process arguments. Native errors/targets must remain minimized in diagnostics.

Windows packaged application identity, locked/denied/logon-session behavior,
rotation/crash recovery and legacy DPAPI remain separate gates. macOS Security
framework policy needs a separate adapter and review; Linux support is not
implemented. Neither cross-compilation nor fake API success permits activation
against existing user credentials.
