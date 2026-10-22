# Data Home admission and Windows bootstrap — CH-29 / CH-36

`Inspect(explicitRoot, optionalExpectedChannel)` ports normal
`data_home.inspect_data_home` admission, not bootstrap or legacy migration. It
uses retained managed-root reads for the fixed format marker and metadata-only
existence checks for the two recovery sentinels. Journal contents are never read.
An ordinary file or directory sentinel blocks admission. Marker bytes, unknown
candidate content, native error text and absolute paths never enter errors.

Preserve source validation order: marker/path/read, JSON object, application and
integer-like version and channel, newer/older format, exact current fields and
home ID, document-recovery sentinel, restore sentinel, expected channel. Normal
admission requires format 2; no metadata or schema is migrated. Missing markers
are unavailable, not an invitation to create an empty replacement profile.

The marker is not required to use canonical JSON. Duplicates are last-wins;
Python's bool-as-int version behavior remains migration-required. Huge supported
integers retain newer/older ordering. NaN/Infinity are float-like invalid version
values, including when an earlier duplicate gets overwritten. Only fixed ASCII
identity/channel fields can survive validation; discarded surrogate strings do
not become retained user data.

Operational bounds differ explicitly from unbounded source marker reads: 64 KiB
marker bytes, JSON depth 900, and the pinned Python integer-digit limit 4300.
Nesting above the budget is refused before decoder grammar/depth errors; it is
not classified as a Python domain error, even for incomplete out-of-budget JSON.
The source raises unhandled errors for very deep nesting, overlong integers and an array/object
marker channel; this adapter reports `DATA_HOME_INPUT_UNSUPPORTED` with a fixed
reason. Such results are not source-error parity. Existing managed-root path,
Unicode, special-file and directory-count limits also apply.

This operation does not create directories, seed defaults, perform a write probe,
set ACLs/modes, open SQLite, recover journals, acquire a process lock or read an
OS credential. A valid marker proves neither native ownership nor exclusivity.
The caller must retain the real host/bootstrap/lifecycle authority before any
product cutover. In particular, Python initialized Windows homes skip ACL setup;
an owner-equals-current-token gate would be a separate policy change. POSIX and
macOS bootstrap permission changes are also outside this read-only operation.

Multiple metadata reads are not a cross-file snapshot or reservation: a writer
could create a recovery sentinel after the check. No concurrent writer/recovery
handoff is safe merely because `Inspect` returned successfully. Native Unix and
installed-app acceptance remain separate from the synthetic Windows evidence.

## Explicit Windows bootstrap

`Bootstrap(ctx, explicitRoot, channel)` ports `src/data_home.py`'s empty-home
startup use case. It is not wired into the installed sidecar and does not select
a user's profile. There is no skip-ACL production option. Unsupported platforms
fail before mutation; POSIX permissions and native macOS acceptance remain open.

The caller must stop other writers before the whole operation.

The dedicated sidecar now uses `BootstrapForProcess`, which inserts a Windows
[process ownership claim](../runtimepaths/README.ownership.md) after step 1 and
before private marker reads, permission enforcement or seed writes. Claim failure
does not undo any empty ancestor directories created in step 1. The claim survives
all returns until process exit; retry uses a fresh process. Plain `Bootstrap`
remains an unowned primitive for callers that already coordinate writers.

Ordering:

1. Validate channel/context and open or create the explicit ordinary root.
2. Admit an existing marker, or check recovery sentinels and the allowlisted
   partial root. Preflight the five default paths without reading opaque bytes.
3. For a markerless root only, apply and verify the private Windows tree DACL.
4. Ensure eleven top-level directories and taxonomy parents; create only missing
   defaults with native no-overwrite publication. Preserve existing file bytes.
5. For a new home, verify inherited permissions, generate a UUIDv4 identity and
   publish the canonical format-2 marker last.
6. Perform the owned empty-file write probe, then reread admission metadata.

The five YAML defaults are exact source bytes, including final LF. Their hashes
are an import-planning contract; do not regenerate them with the YAML encoder.
No candidate facts, document keys, SQLite database or credentials are created.
Existing initialized Windows homes retain their ACLs and identity, even if their
ACLs would fail a new-home privacy check. Missing defaults can be added; existing
opaque or invalid-YAML defaults are never silently reset. A separate explicit
repair workflow would be needed to change that source policy.

### Failures, retries and deliberate safety deltas

`BootstrapError` exposes fixed `Stage`, `Code`, `CauseCode` and `MayHaveChanged`,
not raw paths, document bytes, SIDs or OS prose. The change flag includes partial
directory creation, attempted ACL changes, staging/publication and the transient
probe. It is conservative, not a transaction result or a rollback promise.
If ACL application fails, permissions can be partly restricted; retry reapplies
them, never restores the previously broad policy.

An ordinary seed failure before the marker leaves completed defaults intact;
retry fills only missing ones. Probe failure occurs after marker publication:
retry keeps the identity and skips new-home ACL enforcement. Hard process death
can leave an owned stage. A top-level orphan is an unknown partial entry and is
refused for explicit recovery; no wildcard cleanup or universal crash-retry
guarantee exists. A nested orphan may remain under an allowlisted directory.

Three corrections are intentional, not hidden source-parity claims:

- Both markerless recovery sentinels win before permission/content writes.
  Python detects the nested document sentinel after defaults and marker writes;
  its root restore sentinel instead first trips the unknown-partial check.
- A directory occupying a default file path is refused before ACL setup rather
  than skipped as if a valid file existed.
- Native create-only publication refuses an intervening destination. Python's
  existence-check followed by replacing rename can overwrite that creator.

The managed-path bounds, Unicode/case-alias refusals and native ACL bounds also
apply. The 60-second bootstrap budget is cooperative between synchronous OS
calls, not preemption. Plain Bootstrap supplies no global writer lease; the
ForProcess variant excludes only cooperating Go backends. No coherent multi-file transaction,
directory/power-loss durability proof or hostile same-user namespace isolation
is claimed. ACL inheritance may propagate outside the enumerated snapshot if a
writer mutates it concurrently. See [permission limits](privacy/README.md) and
[creation guarantees](../runtimepaths/README.md).

### Verification

Native synthetic tests exercise missing/precreated homes, permission-before-seed
ordering, exact files/bytes, existing opaque data, initialized ACL preservation,
seed/marker/probe interruptions, late-creator conflicts, cancellation, entropy
failure and unsafe marker/recovery/partial/default paths. The actual pinned
Python comparison checks all defaults/directories/marker bytes and a
Python → Go → Python bootstrap sequence. Fresh Python layout uses its test-only
ACL bypass as a byte oracle; Go ACL effects are independently verified natively.
Python rereads the Go-created marked home through normal source bootstrap.

Run tests/vet for `./internal/datahome`, `./internal/datahome/privacy` and
`./internal/runtimepaths` from `backend`. All fixtures are
temporary and synthetic. Neither these tests nor cross-compilation authorizes
installed activation, real-profile import, secret recovery or Unix acceptance.
