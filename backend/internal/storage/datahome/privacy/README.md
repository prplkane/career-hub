# Windows private DACL observation and explicit application — CH-34 / CH-36

`VerifyPrivateTree(ctx, explicitRoot)` observes the existing Windows bootstrap
policy from `src/data_home.py`; it does **not** set ACLs, initialize a Data Home,
read candidate file contents, open SQLite, or activate the Go backend. There are
no subprocesses or new dependencies in production. Non-Windows builds return
`ACL_PLATFORM_UNSUPPORTED`; POSIX mode/macOS extended ACL handling is separate.

## Policy and native boundary

The current **process token** supplies the SID, not environment variables or a
caller-supplied account. `GetSecurityInfo` reads only the DACL from each retained
file handle. The pinned x/sys wrapper frees the native allocation and returns a
Go-owned descriptor. A valid descriptor with a present non-null exactly-two-ACE
DACL is required before canonical SDDL conversion. Empty conversion output fails.

The canonical DACL-only string passes the source predicate:

- Root: protected DACL, exactly two ordinary allow/full-file-control (`FA`)
  entries, each with exactly OI+CI inheritance.
- Descendants: unprotected DACL; each ACE has ID or OI+CI+ID flags.
- Trustees: current process SID and SYSTEM, with source-compatible SY and LA
  aliases. When the process itself is SYSTEM, source set semantics still require
  two ACEs, both of which can identify SYSTEM. This is not generalized duplicate
  ACE acceptance for ordinary users.
- AI/AR controls are optional; ACE/control order follows source semantics.
  Owner/defaulted descriptor fields are not independent policy conditions.
  Missing/null/empty DACLs, deny/object/callback entries, extra trustees and
  non-FA rights fail. Native canonicalization can normalize input spellings;
  compare readback, not the original string passed to a descriptor constructor.

The internal predicate accepts ASCII SID syntax because Windows emits ASCII
numeric SIDs. Arbitrary Unicode decimal SID strings accepted by Python's `\d`
are not part of this native contract. The optional actual Python test compares
raw synthetic ASCII strings separately from native canonicalization.

## Traversal, budgets and limits

The root must be an explicit non-drive-root local drive path, not UNC/device/ADS,
relative/traversal or a reserved Win32 device component. The drive anchor and
each ancestor are opened and retained before the next component is resolved.
Only the drive anchor uses absolute `CreateFile(OPEN_EXISTING)`; subsequent opens
use `NtCreateFile` with the retained parent's `RootDirectory`, one validated
basename, `FILE_OPEN`, `OBJ_DONT_REPARSE`, and `FILE_OPEN_REPARSE_POINT`. The same
relative-open contract is used for second-pass enumeration. Native calls request
read access and read/write sharing **without delete sharing**. Initial root path
components use ordinary case-insensitive Win32 lookup; enumerated descendants use
the exact observed names, without forcing `OBJ_CASE_INSENSITIVE`. Reparse points,
non-disk objects and non-directory files with link count other than one fail.
This pins ordinary path identities during the inspection. It is not an ACL on
the ancestors and does not certify local-volume provenance (e.g. mapped drives).

There are at most 4,096 tree entries including the root, 64 root components and
64 descendant levels, 4,096 UTF-8 path bytes and 255 bytes per component. Native
Windows path/API restrictions can refuse an otherwise in-budget name. Directory
enumeration requests only the remaining entry budget plus one. Handles and
directory names are bounded by these limits; one temporary extra handle is used
for the second enumeration pass. No file bytes are read.

Each of two passes permits at most 1 MiB of canonical SDDL measured as UTF-16
bytes, and one native descriptor must not exceed 64 KiB. The source's 1 MiB
`icacls /save` cap also includes filenames/BOM/newlines, so this is explicitly a
different budget, not byte-for-byte snapshot-size parity. Descriptors originate
from the native API, not an arbitrary serialized user buffer. Checks before
string conversion constrain descriptor shape; conversion still allocates before
the cumulative output-size check.

The second pass rechecks held identity/type/link count, exact DACL and sorted
directory membership. A final metadata check also covers the retained ancestors
outside the Data Home. All handles are released on success/failure. Inspection
has a 15-second context budget and honors earlier cancellation **between OS
calls**. Synchronous Windows file/security calls are not preempted; no abandoned
goroutine pretends that cancellation has finished. Hard host shutdown still
requires process termination/reaping if an OS call stalls.

This is a point-in-time observation, **not a transaction, writer lease or future
access guarantee**. A same-user process can change ACLs/content/membership during
or after a pass; a change can occur after its final observation or change back
between observations. No-share-delete also does not lock file contents or DAC
changes. A native review reproduced in-place junction conversion of an **empty**
held directory while write sharing was enabled, including through a handle with
only `FILE_WRITE_ATTRIBUTES`; removing write sharing is not a complete fix.
No-share-delete is not a universal no-reparse lock. The implementation therefore
uses handle-relative child opens instead of reopening absolute paths, and checks
parent/child metadata as well. Persistent conversion is refused by retained-handle
checks. A populated directory could not be converted in that native test, and
known children are retained against ordinary deletion. These observations do not
establish a general adversarial filesystem sandbox or protect against privileged
filesystem/driver manipulation. File reads can affect OS-managed last-access metadata. This verifier
requests no content/permission writes, but cannot promise all filesystem metadata
is unchanged. Initial-root permissions, ancestor replacement authority, durable
bootstrap, local-volume qualification, cross-process ownership and SQLite's own
DB/journal/WAL/SHM pathname opens remain production gates.

**Do not wire this as an unconditional existing-home admission rule.** The Python
bootstrap skips recursive ACL enforcement once a valid marker exists. Refusing
an older initialized profile under this new check would be a separate product
policy change needing an explicit migration/repair workflow.

## Errors and verification

Errors retain only `ACL_SETUP_FAILED`, `ACL_TREE_LIMIT`,
`ACL_CONTEXT_CANCELED` or `ACL_PLATFORM_UNSUPPORTED`. Paths, SIDs, descriptor
bytes and OS diagnostics never enter these errors. These codes are pilot-local;
HTTP/Diagnostics/UI integration is not implemented.

Run `go test -buildvcs=false -mod=readonly ./internal/storage/datahome/privacy` and
`go vet -buildvcs=false -mod=readonly ./internal/storage/datahome/privacy` from `backend`.
Set `CAREERHUB_ACL_PYTHON` to pinned Python 3.12.14 for actual source comparisons.
All tests keep native mutation under owned synthetic temporary directories;
no real Data Home or installed permission policy is changed. See the migration progress log for
measured results and unsupported checks; cross-compilation is not native evidence.

Primary API references: [GetSecurityInfo](https://learn.microsoft.com/en-us/windows/win32/api/aclapi/nf-aclapi-getsecurityinfo),
[security descriptor strings](https://learn.microsoft.com/en-us/windows/win32/secauthz/security-descriptor-string-format),
[CreateFile sharing](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew).
The native relative-open contract is documented under
[NtCreateFile](https://learn.microsoft.com/en-us/windows/win32/api/winternl/nf-winternl-ntcreatefile)
and [OBJECT_ATTRIBUTES](https://learn.microsoft.com/en-us/windows/win32/api/ntdef/ns-ntdef-_object_attributes).

## Explicit new-home permission application

CH-36 adds `ApplyPrivateTree(ctx, explicitRoot) (mayHaveChanged, *Error)`, called
only after markerless bootstrap admission and before any new private content.
The read-only verifier above stays separate. This is a Windows-only provider,
not an existing-home repair command or a product endpoint.

It retains and checks the entire bounded tree before the first ACL dispatch.
Drive/ancestor handles remain read-only; the target root and descendants request
WRITE_DAC. Ordinary files request metadata, READ_CONTROL and synchronization
rights, not FILE_READ_DATA. This allows repair of a protected file whose content
read is denied while its metadata/DACL remains accessible. Directories require
listing rights too; a denied tree that cannot be enumerated is safely refused,
not blindly repaired.

The root receives a protected exactly-two-ACE DACL for the current process SID
and SYSTEM, each with full control and OI/CI inheritance. Descendants receive an
**empty non-null unprotected DACL**, causing Windows to rebuild inherited ACEs.
A null DACL would grant everyone access and is never passed. Breadth-first
application repairs protected children as well as ordinary inherited children.
Owner and SACL fields are not changed. Native readback verifies the exact policy,
then retained metadata and membership are checked again.

The change flag becomes true before native mutation dispatch and stays true
after partial failures or close errors. No rollback broadens old permissions.
Context checks use a 45-second cooperative budget, not interruption of stalled
kernel calls. `SetSecurityInfo` can itself propagate inheritable ACEs to children:
that OS action is not an atomic bounded snapshot of this traversal. Other
writers must be excluded by the caller; neither retained handles nor pre/post
checks establish malicious-same-user isolation or a global Data Home lease.

Native tests cover broad roots/protected children, opaque content/mtime and
outside-parent preservation, later child inheritance, read-denied file repair,
partial setter failure/retry, unsafe hardlinks and cancellation before writes.
The composed bootstrap adds another privacy readback before publishing its
marker. Real installed and other-OS acceptance remain separate.

Primary mutation reference:
[SetSecurityInfo and inheritance behavior](https://learn.microsoft.com/en-us/windows/win32/api/aclapi/nf-aclapi-setsecurityinfo).
