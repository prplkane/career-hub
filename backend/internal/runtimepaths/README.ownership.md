# Windows cooperating-backend ownership — CH27

`Root.ClaimForProcess() *Error` is an irreversible opt-in for one Data Home in
one dedicated Go sidecar process. Independent checked root-chain handles and a
noninheritable Windows named-object handle remain strongly referenced until OS
process exit. Closing the caller's Root, returning from startup, cancellation,
GC or a failed later initialization must not release the claim: native callbacks
can still be running. There is no public unlock, TTL/PID lock file or stale-file
cleanup. Private acquisition cleanup closes only handles it actually acquired.

`Root.RequireProcessOwnership()` (CH40) verifies that this exact opened directory
is already the process-owned home, with current root-binding checks. It returns
`OWNERSHIP_REQUIRED` rather than acquiring a missing claim; unsupported platforms
return `OWNERSHIP_UNSUPPORTED`. Closing and reopening a caller Root does not lose
the process claim. A different native directory cannot borrow it. The document
reader and SQLite guard share the native identity predicate; neither a matching
path string nor a copied home-ID marker constitutes ownership.

The claim requires the existing managed-root checks plus fixed NTFS/persistent
ACL/non-read-only-volume qualification. It hashes the native directory's volume
serial and 64-bit file index, not its path spelling, channel, account, port or
session. A `Global\CareerHub.DataHomeOwner.v1.` namespace gives cooperating
desktop processes the same token key across Windows sessions. The protected
object DACL grants READ_CONTROL and SYNCHRONIZE only to the current token SID
and SYSTEM; the name/identity, SIDs, descriptor and raw native errors are private.
See Microsoft's [native file identity](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getfileinformationbyhandle)
and [kernel namespaces](https://learn.microsoft.com/en-us/windows/win32/termserv/kernel-object-namespaces).

`CreateMutexEx` uses flags=0 as an **existence token**, not thread-owned locking.
It never waits or calls ReleaseMutex; a Go goroutine need not stay on one native
thread. ERROR_ALREADY_EXISTS returns a handle that the contender must close.
Access denial, wrong-object-type collision and unavailable namespace/native
results fail closed, with no Local/random-name fallback. Windows disposes of
the object after its last handle closes, including process termination.
See [CreateMutexEx](https://learn.microsoft.com/en-us/windows/win32/api/synchapi/nf-synchapi-createmutexexw).

Directory pinning is a separate requirement. Pinned Go1.27.1's ordinary os.Root
opens allow FILE_SHARE_DELETE; native regression proved that retaining them
alone did not prevent rename. Each root-chain object is therefore reopened
relative to its own handle with an empty NT name, FILE_OPEN, OBJ_DONT_REPARSE
and no delete sharing. Identity/type/ancestor bindings are rechecked, with no
absolute-path or ReOpenFile fallback. No directory/file is created by the claim.
See [native open/share semantics](https://learn.microsoft.com/en-us/windows/win32/api/winternl/nf-winternl-ntcreatefile).

Errors are fixed `OWNERSHIP_IN_USE`, `OWNERSHIP_UNAVAILABLE` or existing root/path
codes; non-Windows ClaimForProcess returns `OWNERSHIP_UNSUPPORTED` before native
or filesystem access. The startup boundary maps them to `DATA_HOME_IN_USE`,
`DATA_HOME_OWNERSHIP_UNAVAILABLE` and `DATA_HOME_OWNERSHIP_UNSUPPORTED`.
Unmodified non-Windows bootstrap retains its earlier unsupported-platform code.

## Limits and evidence

- This excludes **cooperating Go backends only**. Python/CLI tools, external
  editors, restore/migration tools and older app versions do not yet cooperate.
  Keep them stopped during cutover; no real profile is approved by this pilot.
- Predictable object names permit deliberate local precreation/handle retention
  denial of service. A cloned volume identity can cause false contention. Neither
  case permits ignoring the refusal; this is not hostile-local-user isolation.
- No-delete sharing pins rename/deletion, not same-user in-place reparse/content
  mutation, future SQLite VFS pathname/auxiliary opens, cloud-sync activity,
  drive remapping, a coherent snapshot or power-loss recovery. Existing-home
  permissions are not repaired or silently made stricter.
- Native Windows tests verify actual token DACL/noninheritance, case-identity
  exclusion, atomic contenders, independent homes, wrong-object collision,
  caller-close/GC retention, blocked rename, normal/abrupt-process restart and
  unchanged opaque fixtures. Both sidecar modes additionally run through the
  unchanged Electron supervisor. Cross-session/multi-account, installed-app,
  macOS/Linux and non-NTFS acceptance remain separate.
