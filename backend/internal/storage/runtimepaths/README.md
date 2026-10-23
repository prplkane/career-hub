# Managed paths — CH26 reads, CH35 replacement, CH36 creation

This package implements `Open(root)`, `Root.Check(relative)`, bounded
`Root.ReadFile(relative, limit)`, and `Root.Close()`. It is an offline migration
component; it is not a product route, Data Home bootstrapper, ACL manager,
filesystem browser or secret store. The catalog store supplies four
fixed relative catalog paths and explicit host-resolved resource/data roots.
The separate Windows-only `ClaimForProcess` opt-in now excludes cooperating
backends; see [process ownership and its limits](README.ownership.md). Ordinary
Open/read/create/replace methods still require caller-owned writer coordination.
`GuardSQLiteFiles` now adds an owned Windows main/companion-file boundary;
see [its lifetime, auxiliary-file semantics and limits](README.sqlite.md).

CH-29 adds `Root.Exists(relative)` for metadata-only recovery-sentinel checks.
It accepts an ordinary file or directory leaf and never opens leaf content.
Links/reparse points, aliases, special files and unsafe parents still fail;
identity and parent bindings are rechecked. `Check` and `ReadFile` remain
regular-file-only. Presence is an observation, not a lock or reservation.

## Bounded object and metadata reads (CH40)

`ReadObjectFile(relative, limit)` shares `ReadFile`'s complete managed path,
opened-stream, size+1, type and pre/post identity checks, but has a separate
32 MiB absolute ceiling. A caller must use its tighter format bound; the
document reader passes the exact maximum envelope size (20 MiB plaintext plus
framing/header/nonce/tag). Ordinary `ReadFile` and all create/replace limits
remain 16 MiB. There is no truncation, unguarded `os.ReadFile` fallback,
automatic authentication or grant of writer access. Both read methods now
discard bytes when their stream or per-read directory-handle cleanup fails.

`ReadSingleLinkWindow(relative, offset, limit)` is the native diagnostic
journal's bounded polling primitive. It returns an owned window of at most
256 KiB plus the observed whole-file size; the file itself must remain at or
below the ordinary 16 MiB ceiling. Offset-at-EOF is an empty present window.
Opened-stream identity, size, mtime, retained parents and single-link count
are checked before and after reading, with no bytes returned on drift, invalid
range, close error or hardlink. It does not parse lines, authenticate requests,
coordinate another process or reserve the file against later replacement.

`ListFileNames()` returns owned sorted names only when every entry is an ordinary
regular file. It checks metadata identity without opening/reading file contents.
The existing 10,000-entry bound, normalization/collision checks and root binding
checks apply. It performs one directory scan plus bounded metadata checks, not
a fresh sibling scan through `Check` for every entry. `ListNames` keeps its
general file/directory behavior unchanged. Neither operation is a filesystem
transaction or protection from hostile same-user mutation.

The document reader uses `RequireProcessOwnership()` to observe the existing
claim for its exact native Data Home directory. This never claims, unlocks or
creates anything. The SQLite guard shares the same native identity predicate;
its ownership behavior is unchanged. Unsupported platforms remain explicit.

## Recovery archive read lease (CH-74.27)

`ReserveDirectory(relative)` creates one absent ordinary directory with native
`FILE_CREATE` below existing confined parents, requiring exact process ownership.
It retains a no-delete handle and ancestor pins through the reservation lifetime;
it never adopts a preexisting directory or changes its ACL. Outcomes distinguish
`not_created`, `created` and `unknown`; a later close/error cannot undo known
creation. Unknown preserves evidence and forbids blind retry. `Close` releases
handles without removing partial contents; it is idempotent and retains failures.
The reservation prevents namespace rename/deletion, not writes to child files,
and it is not a durability, recovery or whole-Home-coherence guarantee.

`ListDirectoryEntries(relative)` is a read-only bounded typed inventory of one
ordinary directory, with name, directory/file type, size and modification time.
It rejects links/reparse points, special files and portable normalized/casefold
collisions; metadata identity and retained directory bindings are rechecked.
For child directories, the enumerated identity is bound to a fresh rooted stat,
then a second stat checks metadata stability. Native deep-tree fixtures exposed
stale enumeration timestamps after creation; those timestamps are not treated as
live drift. Identity/type/link checks and regular-file metadata checks remain.
It does not read content, prove single-link files or provide a stable snapshot.
Later content use must obtain the corresponding source lease; whole-Home writer
coordination remains a caller responsibility. Existing listing APIs are unchanged.

`Root.OpenBackupRead(ctx, relative, limit)` requires the exact process-owned
Windows Home and opens one existing managed regular single-link file. It retains
native no-delete directory pins and a read-only file handle with READ sharing
only. A preexisting writable handle causes refusal; no write-sharing fallback,
absolute path open or raw handle is exposed. Ordinary read/object/window limits
are unchanged. The independent whole-file ceiling is 32 GiB + 128 MiB, with a
256 KiB maximum per `ReadAt` call; callers supply tighter format limits.

`BackupRead` implements `io.ReaderAt` and exposes an immutable observed `Size`.
Every read rechecks identity, size, mtime, hardlink count, leaf and ancestors
before and after native I/O. Failed postchecks clear copied bytes. `Check` and
idempotent `Close` retain fixed errors; consumers must reject their result on
failed final validation/cleanup. Cancellation is cooperative between native
calls; it never bypasses handle release. Keep the Root open and all consumers
stopped before closing the lease. No goroutine is abandoned on a timeout.

This is one retained-file boundary, not a coherent multi-file/Home snapshot,
SQLite WAL backup, writer scheduler or protection from a hostile same-user
process/preexisting writable memory mapping. The maintenance owner must still
quiesce application/legacy writers and validate the inventory. Unsupported
platforms return `BACKUP_READ_UNSUPPORTED` before filesystem access; native
macOS/Linux qualification has not been performed.

Focused Windows tests cover actual read/write/delete sharing, file/parent
rename pins, an existing writer, hardlinks, empty/missing/oversized requests,
cancellation, closed roots, sticky final-validation failure and released handles.
A synthetic 16 MiB + 1 sparse fixture is read at its tail with a one-byte buffer
to check the separate ceiling, not to claim measured memory use. A subprocess
claims a disposable root and consumes a real ZIP through this ReaderAt;
unowned acquisition is refused. `homebackup.VerifyLocal` uses the same native
lease for full archive metadata and CRC/SHA-256 checks without extracting data.

## Recovery archive streaming publication (CH-74.27)

`Root.CreateBackupStream(ctx, relative, limit, produce, verify)` is a separate
Windows process-owned file-creation primitive. It streams into an exclusively
created random sibling, bounds total bytes and individual native writes, checks
the producer's digest against a streamed re-read after Sync/Close, and invokes
the format verifier while holding the exact stage identity with native
read/DELETE access and no write/delete sharing. Only then does one native
handle-relative no-replace rename publish the destination. Ordinary creation
limits/APIs are unchanged; this API does not create missing parents or ACLs.

Both callback interfaces become unusable after return. Callbacks run without
Root's mutex, so a producer can read a source lease on that same Root; final
publication rechecks Root and parents under the lock without a product callback.
Independent native parent pins and a retained parent handle survive an erroneous
Root.Close long enough for identity-checked stage cleanup. Closing the Root in a
callback refuses publication. Callback/native error text is never returned.

`StreamPublication.Outcome` is `not_published`, `published`, or `unknown`.
Failure after a known rename retains `published`; an ambiguous native error is
resolved only by observing the owned file identity at the source/target, otherwise
it remains `unknown`. Unknown skips automatic stage deletion. The separate
`CleanupConfirmed` describes owned temporary-name cleanup, not the validity of
the archive, release of every native resource, or permission to automatically
retry. Any error still requires the maintenance parent's owned-child lifecycle.
Existing/competing destination files are never overwritten or deleted.

Native tests cover producer/verifier failure, ignored size errors, short/changed
writes, Sync/Close failure, cancellation, Root.Close, callback invalidation,
an actual intervening destination creator, known committed failure and retained
unknown evidence. A child with a real ownership claim publishes a synthetic file.
These are process/native-operation tests, not a power-loss durability guarantee:
file Sync does not establish directory-entry durability. A coherent snapshot and
the restore journal/recovery owner remain separate work.

## Windows volume observation (CH27; not product admission)

`Root.CheckLocalVolume()` inspects an opened directory handle without reading
application files, creating probe files, or changing permissions. The initial
database storage policy requires a fixed NTFS volume with persistent ACL support
and without the read-only-volume flag. Removable/RAM/CD-ROM/unknown media, other
filesystems (including unverified ReFS), and unavailable native results fail
explicitly. This does not assert that other filesystems cannot support SQLite.
Non-Windows calls return `VOLUME_PLATFORM_UNSUPPORTED` before filesystem access.
Existing `Open`, standalone catalog reads, unowned `Bootstrap` and initialized-home
ACL policy are unchanged. The explicit process-ownership gate also uses this
observation on the Data Home; that is not qualification of a database descendant.

The observation uses a handle-relative `Open(".")` from the retained root,
checks its identity/type and ancestor bindings, and repeats these checks even
when native volume inspection fails. It closes the extra handle on all returns.
The Windows adapter obtains a bounded GUID-form final path from that handle,
validates only its volume-root prefix, then queries drive type for that root
and filesystem flags/name by the same handle. There is no DOS-letter, current
directory or NT-device-path fallback. It requests no volume label or serial;
neither the temporary GUID/path nor native error prose leaves the adapter.
See Microsoft's [handle-derived path contract](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getfinalpathnamebyhandlew),
[drive types](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getdrivetypew),
and [volume information](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getvolumeinformationbyhandlew).

Errors distinguish `VOLUME_UNAVAILABLE`, `VOLUME_PATH_LIMIT`, `VOLUME_REMOTE`,
`VOLUME_TYPE_UNSUPPORTED`, `VOLUME_FILESYSTEM_UNSUPPORTED`,
`VOLUME_ACLS_UNSUPPORTED` and `VOLUME_READ_ONLY`, in addition to existing
root/path/close codes. A network handle may fail GUID lookup with
`VOLUME_UNAVAILABLE` before a drive type can be observed.

This checks **only this root**, not every descendant or future pathname open.
Call it on the actual database parent. A successful observation is not an ACL
audit, writable-directory test, free-space check, cloud-sync detector, exclusive
writer lease, power-loss guarantee or SQLite VFS sandbox. A fixed NTFS volume
can still be virtual/USB-backed or contain ordinary cloud-synced directories.
Native calls are bounded in number and buffer size (4,161 UTF-16 path units);
they are synchronous, without a promised hard wall-clock bound. The host still
owns process deadlines, competing-writer policy and supported storage selection.

Native tests require supported Windows storage and do not skip an unexpected
volume refusal. They check actual API success, unchanged fixture content,
directory identity/mtime/DACL/membership and handle release. Injected native
results cover unavailable/truncated/malformed paths, drive classes, filesystem
flags and private-error redaction; these are not real SMB/removable/ReFS tests.

## Windows creation primitives (unactivated CH36 component)

- `OpenOrCreate(root) (*Root, bool, *Error)` accepts an explicit absolute local
  directory and creates missing ordinary components. Unlike `Open`, its scans
  reject portable case/Unicode aliases on the configured chain too. Names are
  used as stored, not rewritten. The boolean conservatively means creation may
  have happened, including when a later check fails; partial directories stay.
- `Root.EnsureDirectory(relative) (bool, *Error)` creates missing ordinary
  descendants with the same portable checks and conservative creation flag.
  Existing ACLs are untouched; new Windows directories use normal inheritance.
- `Root.ListNames() ([]string, *Error)` returns owned, sorted, bounded top-level
  names, never content. It refuses portable collisions and incomplete reads.
- `Root.CreateFile(relative, content) (created bool, *Error)` requires existing
  parents, bounds opaque content to 16 MiB, and never overwrites an existing name.
  Existing regular leaves return `FILE_EXISTS`; directories/links fail separately.
- `Root.ProbeWritable() *Error` creates/closes/removes one owned random empty
  file. It does not inspect or remove preexisting temporary files.

All mutation APIs above refuse non-Windows platforms before writes with
`CREATE_PLATFORM_UNSUPPORTED`. ListNames remains a read-only operation.
Missing parents for CreateFile return `PATH_PARENT_MISSING`; directory creation
failures use `DIRECTORY_CREATE`; failed publication uses `FILE_PUBLISH`. Other
bounded path/read/write/cleanup codes are reused; no raw paths or OS text escape.

CreateFile exclusively creates a random sibling stage, writes, Syncs and closes
it, then reopens that owned identity with native DELETE/read access and no
write/delete sharing. It verifies the bytes and performs one handle-relative
`NtSetInformationFile(FileRenameInformation)` with `ReplaceIfExists=FALSE`.
No replacing rename fallback exists. An intervening exact-name creator is
refused by that native operation, not merely by the preceding existence check.
This corrects Python `_atomic_write(overwrite=False)`'s check-then-os.replace
race. See Microsoft's [FILE_RENAME_INFORMATION contract](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntifs/ns-ntifs-_file_rename_information).

Cleanup opens and verifies the created identity before applying native delete
disposition to that same handle; it never deletes an unverified path. Missing
identity, changed/reparse/hardlinked stage or unresolved cleanup leaves the
entry for explicit attention. It never scans for leftovers. A true CreateFile
result persists after post-publication verification/close failures (and when
the stage identity is observed at the target after an ambiguous native error).
Such a result must not be treated as an uncommitted automatic retry.

The bootstrap caller owns permission setup **before any stage/private content**,
marker-last ordering and the quiescent-writer precondition. These APIs do not
apply ACLs, acquire a global lease, discover a Data Home or activate the product.
Same-instance calls serialize, not separate Roots/processes. Mkdir and initial
stage creation use pinned Go's handle-relative os.Root operations plus ancestry
pre/post checks; unlike the explicit publication/cleanup opens they do not add
OBJ_DONT_REPARSE. Do not claim resistance to a hostile same-user in-place reparse
mutation, coherent cross-file snapshots, mount/drive remapping or power-loss
durability. File Sync is not directory fsync or a broad Windows atomicity promise.

Focused native synthetic tests cover nested creation, preservation, binary/empty
publication, a late intervening creator after native preflight, concurrent Roots,
injected write/sync/close/post-publication failures, foreign/hardlinked stage
cleanup refusal, ordinary junctions, exact 10,000-entry scan bounds and ABI.
Symlink-only cases explicitly skip without Windows privilege; they do not replace
the privilege-free native junction cases. Full bootstrap/Python/default-byte/ACL
integration is verified separately by the caller, not by these primitive tests.

## Owned directory move (unactivated DMR2 primitive)

`Root.MoveDirectoryNoReplace(source, target)` is Windows-only and additionally
requires the exact Root's irreversible process ownership claim. Both parents
must already exist under retained, rechecked handles; the source must be an
ordinary directory and the target must be absent. A target below the source is
refused. The operation opens the source relative to its held parent with
`DELETE` access, no delete sharing and explicit reparse refusal, then supplies
the held destination-parent handle to one native
`NtSetInformationFile(FileRenameInformation)` call with replacement disabled.
It has no absolute-path/MoveFile fallback and no remove or recursive operation.
See Microsoft's [relative rename structure and no-replace rule](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntifs/ns-ntifs-_file_rename_information).

The result is `not_moved`, `moved`, or `unknown`. The retained source identity
at the destination proves `moved`; the same identity at the source proves
`not_moved`; neither proof is `unknown`. A moved result remains committed even
when a native/post-check/close error is also returned. Unknown is an explicit
recovery boundary and must never be retried automatically. Tests cover a real
cross-parent move, an intervening target, an injected error after commit,
ownership refusal, descendant refusal and the native ABI. This primitive does
not copy or verify tree contents, publish a journal, decide rollback, create or
discard credentials, or complete document migration.

## Existing-file replacement (unactivated CH35 pilot)

`Root.ReplaceFile(relative, expected, replacement)` returns `(committed, *Error)`.
It serializes with all calls on that same Root; separate Roots/processes still
require caller coordination. It requires an existing ordinary single-link file,
the existing portable path policy, and both byte slices bounded to 16 MiB. The
caller must keep those slices unchanged until return. Missing files or parents
are refused; nothing bootstraps them.

The operation compares the raw expected bytes, exclusively creates an
unpredictably named sibling, writes and syncs that stage, then closes it. It
re-reads the stage and original target, rechecks identities/types/link counts and
parent/path bindings, and renames through the retained parent. Target write
opens are never used. Stage cleanup removes only an entry whose identity still
matches the created file; uncertain ownership leaves the bounded stage alone.
Success and ordinary failures remove the stage. An interrupted process can leave
a stage; this API does not scan for or delete leftovers from other operations.

`FILE_CONFLICT` means observed bytes differ from `expected`; `FILE_MISSING` and
`PATH_HARDLINK` refuse missing and multiply linked targets. Existing path/read
error codes are retained. Additional operational errors are
`WRITE_LIMIT_INVALID`, `FILE_STAGE`, `FILE_WRITE`, `FILE_SYNC`, `FILE_CLOSE`,
`FILE_RENAME`, and `FILE_CLEANUP`. Errors contain no raw paths or OS prose.
`committed=true` remains true if post-rename verification or cleanup fails, or
the stage's identity is observed at the target after a rename error. Callers must
not treat that result as a safe uncommitted retry or claim the new bytes are still
current after another writer intervened.

This is not cross-process compare-and-swap. A writer can change the target
between the last comparison and rename, including making it absent; the native
rename may then create that final name. This API guarantees that it does not
*intentionally* create an initially missing target, not an atomic existing-only
rename. Callers need a real writer lease before activation. Go1.27.1 documents
that even same-directory rename is not guaranteed atomic on non-Unix platforms.
Its Windows retained-parent implementation uses native rename information with
replace/POSIX flags and a compatibility fallback; filesystem/share restrictions
can still refuse replacement. A successful file Sync does not supply directory
fsync, Windows atomicity, or power-loss durability.
The stage-identity check and named removal are likewise separate filesystem
operations, not an atomic conditional unlink against a hostile namespace writer.

The new inode inherits the parent's Windows ACL; target ACL/owner/mode metadata
is not copied, matching the source temporary-file replacement model. On POSIX,
the stage requests mode 0600 rather than Python's ordinary creation umask. No
native Unix/macOS acceptance is claimed by Windows tests or cross-compilation.

## Path and read contract

- For the read-only `Open` entry point, configuration must be absolute, existing and more specific than a
  filesystem root. No current-directory discovery, environment lookup, `~`
  expansion, fallback, mkdir, or automatic root creation occurs.
- Local Windows drive paths and absolute Linux/macOS paths are supported.
  Windows UNC, device namespaces and drive-relative paths are explicitly
  refused; other OS targets fail before file access.
- Configured ancestors are opened one component at a time through `os.Root`.
  Lstat/type/reparse checks and native identity comparisons bind each reviewed
  child to a retained parent handle. They are rechecked before/after reads.
  Configured root names are not NFC-rewritten or case-folded into another root.
- Relative components reject traversal, dot/empty segments, alternate data
  streams, control characters, trailing dots/spaces, and reserved Windows
  device names on every supported OS. Both slash forms are portable separators.
- A directory scan selects descendants by `NFC(name).casefold()` using the
  existing Python15 full-fold helper. Multiple equivalent children fail; a
  single different-case spelling fails. A single canonically equivalent NFD
  filename is used as stored, without renaming it. Scans are bounded and sorted
  before collision decisions, avoiding OS enumeration-order-dependent results.
- Missing descendants are valid optional paths. Missing reads return
  `(nil, false, nil)`. Existing directories/nonregular leaves and dangling
  symlink/reparse entries are failures, never equivalent to absent catalogs.
- `Check` reads directory metadata, not leaf bytes. It is not a reservation or
  cached authorization. `ReadFile` repeats validation independently, opens one
  regular file, checks its identity/type, reads at most `limit+1`, and rechecks
  identity/size/mtime and bound parents before returning owned bytes. All failure
  returns discard bytes. Raw revision and decoding must reuse these same bytes
  in the caller, rather than reopen the file.

## Unicode compatibility without a second normalizer

Use fixed `golang.org/x/text v0.42.0` (see `licenses/golang-x-text/README.md`),
not the advisored older candidate. Native
NFC runs in `unicode/norm`; this package does not implement Unicode composition.
The dependency selects Unicode17 on the pinned Go1.27.1 toolchain. To preserve
Python3.12's Unicode15 behavior:

1. Reject invalid UTF-8 before the library sees it.
2. Reject 66 scalars in 22 small ranges whose canonical combining class or NFD
   decomposition differs from Python Unicode15. Check both input and normalized
   output (new characters can compose from individually unchanged inputs).
3. Reject stream-safe transformations that insert CGJ, instead of silently
   changing a long combining-character name. Existing legitimate CGJ remains.

`TestNFCCompatibilityInventory` derives the guard independently for all 1,112,064
Unicode scalars using the pinned Python runtime and x/text CCC/NFD properties.
Its result must equal the 66-scalar guard exactly. This is a refusal set, not a
normalization table or custom Unicode engine. Newly assigned characters with
unchanged CCC=0 and NFD remain eligible. Normalization stability covers already
assigned compatible scalars; the actual complete catalog-path comparison still
belongs to the parent migration gate. Any dependency/toolchain change requires
rerunning this evidence; a different normalization version fails closed.

## Deliberate safety deltas from Python

The source is `src/runtime_paths.py`, especially `_safe_relative_path`,
`_portable_child`, and `managed_data_path`. Unlike some Python paths, this
boundary requires explicit absolute existing roots, refuses UNC/device roots,
does not collapse dot/empty segments, applies portable reserved-name rules, and
rejects leaf directories, dangling links and nonregular files. These are stated
read-safety deltas, not claims of source-value parity. Unsupported Unicode names
and directory/path/read limits are distinct failures, never missing catalogs.
An unsupported directory entry also fails that parent scan rather than being
silently omitted from collision detection.

| Bound | Limit |
| --- | ---: |
| Root/relative path bytes | 4,096 |
| Components per path | 64 |
| Relative component bytes before/after NFC | 255 |
| Entries scanned in one directory | 10,000 |
| Caller-specified single-file read cap | 16 MiB |

The catalog byte decoder normally supplies its smaller 2 MiB budget. Limits
are fixed error codes, not partial successful results.

## Containment guarantees and limits

### Bounded backup cleanup primitives (Windows)

`RemoveBackupFile(ctx, relative, size, sha256)` compares one ordinary single-link
file through a retained DELETE-capable handle using 64 KiB reads. It accepts
only a bounded size and lowercase SHA-256; ordinary `RemoveFile` read limits are
unchanged. `RemoveEmptyBackupDirectory(ctx, relative)` removes only an empty
ordinary directory, never children. Neither operation follows reparse points,
creates paths, removes the managed Root, or falls back to pathname deletion.
Both require the existing process-owned Root and pin/check ancestor identities.

Results are `not_removed`, `removed`, or `unknown`. Pre-dispatch refusal is known
not removed. After native dispatch, absence is checked through the retained
parent; a pending deletion, replacement, or unavailable result is not reported
as success. Observed removal survives a late error/cancellation. Do not retry an
unknown result or interpret removed-with-error as no effect. Name removal does
not promise immediate disk-space reclamation or secure erasure.

The implementation reuses the existing confined native opens and sets
[FileDispositionInformation](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntifs/nf-ntifs-ntsetinformationfile),
then only closes that handle. The kernel also rejects nonempty directories;
an independent delete-sharing reader can defer completion. Native tests cover
both situations, actual junction refusal, hardlinks, busy files, stale hashes,
parent/leaf rename exclusion, size bounds, cancellation and late/unknown results.

These primitives are not a cleanup policy or product route. The `homebackup`
owner must independently authorize exact service-generated targets, preserve
archives/active data/pending recovery/retained rollback sources, and quiesce
writers before using them. No recursive deletion or automatic retention is
enabled here. Non-Windows calls refuse before filesystem access; cross-compiling
their refusal tests is not native macOS/Linux qualification.

Go1.27 `os.Root` supplies handle-relative containment against symlink-swap
escapes on the supported OSes. The additional policy rejects even links pointing
inside the managed root and rejects all Windows reparse attributes, not only
symlink mode bits. Unix leaf opens add NONBLOCK and NOFOLLOW to avoid a raced
FIFO blocking the read and tighten link refusal.

This is **not** a snapshot/transaction spanning multiple files, an ACL guarantee,
a defense against a malicious same-identity in-place writer restoring metadata,
or proof that every in-root race is detectable. A file can still change during
an ordinary read without observable metadata changes. No exact memory/wall-time
guarantee exists for a hostile filesystem; the maintainer process must remain
bounded externally. `os.Root` does not prohibit mount boundaries/bind mounts,
and configured Windows drive mapping is a host concern. The bounded replacement
pilot adds no product activation; writer coordination and native durability
acceptance remain separate requirements.

## Verification

Native Windows tests cover optional reads without creation, ownership of bytes,
limits, regular-file type enforcement, full-fold/NFC collisions, replacement of
an opened root, and actual ordinary/dangling directory junction refusal. The
symlink test explicitly skips if Windows denies symlink creation; Unix FIFO and
symlink behavior require a native Unix run, not just cross-compilation. No test
uses installed/private Data Home: all filesystem fixtures use `t.TempDir`.

The Windows sandbox denied ancestor metadata access before tests reached their
fixtures. Native verification therefore ran with normal host permissions, still
with synthetic temporary roots in the isolated workspace. This is a test-host
limitation, not a bypass in the adapter or a production capability claim.

```powershell
$env:CAREERHUB_PATH_PYTHON = 'C:\path\to\pinned\python.exe'
go test -count=1 ./internal/storage/runtimepaths
go vet ./internal/storage/runtimepaths
```

Every `Error` contains only a fixed `Code`; underlying error prose and raw paths
are discarded. The store owns source attribution and its public error envelope.
The current boundary does not complete CH26, a native Unix gate, or sidecar cutover.
