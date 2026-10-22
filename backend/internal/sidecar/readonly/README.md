# Initialized read-only workspace — CH-32

## CH-42 full application and Resume Library services

`Applications`, `Application`, `ResumePlan`, `ResumeLibrary` and internal
`ResumeDigestIdentities` expose complete persistence models, separate from the
older `ApplicationSummaries` pilot. They require current prepared storage and
the process ownership claim; missing/old storage returns preparation-required,
not a misleading empty ledger. Data Home admission and fixed resource boundaries
are checked before and after synchronous database/guard cleanup. Fixed error
categories replace driver causes; failures never return partial data.

Domain reads use coherent read-only transactions and bounded batched queries.
The source's public picker/management and private digest projections remain
distinct. These methods do not yet replace the higher-level HTTP routes that
also add assessment/JD context or resume-digest status. No installed backend,
writer, UI contract or secret/document access policy changes.

The actual source corpus passes all 117 cases through native process-owned
services, plus eighteen path/admission/schema/cancellation/freshness scenarios.
The combined workspace regression passes at 79.8% statement coverage (41.821s);
startup/runtime regressions and targeted vet pass. See the
complete contract and release limitations.

`Open(protocol.StartRecord) (*Workspace, error)` composes existing managed root,
Data Home admission, catalog loading and payload packages before readiness.
`AppVersion()` returns the initial explicit resource version. `Registry()` loads
a fresh owned payload; it does not retain mutable catalog snapshots or handles.
There is no close operation or publicly injectable backend.

The dedicated executable uses `OpenForProcess`: after resource/path validation,
but before private marker/catalog admission, it acquires the Windows
[Data Home ownership claim](../../runtimepaths/README.ownership.md). The claim
survives Workspace disposal and startup failures until process exit. Later
request checks do not reacquire it. Plain Open remains an unowned read primitive;
this opt-in is Windows/fixed-NTFS only, not native Unix read-sidecar acceptance.

CH-37 also exposes `Preflight(protocol.StartRecord) error` for explicit bootstrap
composition. It permits a missing candidate under existing ordinary managed
ancestry, checks separation without creating it, and decodes shipped core model
inputs before any later mutation. It neither admits private data nor returns a
workspace. The ordinary `Open` order remains unchanged. See the
[startup contract](../startup/README.md) for the drive-root-only candidate
restriction, earlier malformed-core refusal and point-in-time ownership limits.

Only an explicit absolute resource root and a separate initialized Data Home
are accepted. Port must be zero and channel exactly `dev`, `beta`, or `stable`.
Lexical same/ancestor overlap is refused in either direction, including Windows
case aliases; after managed opens, native file identities are compared against
both roots' ancestor chains to catch additional OS aliases. No symlink/junction
resolution fallback is used. The existing managed reader rejects link/reparse
components, unsafe names, unsupported namespaces, and broad filesystem roots.
Identity observations are not reservations against root replacement or mounts.

Fixed resources are `VERSION`, `src/intake_ui_static`, and both core catalog
files defined by `catalogstore`. The static sentinel only needs to be an
ordinary file or directory, matching the source startup `exists()` check; this
package does not serve UI assets. Both core catalogs must be present regular
files, a stricter pilot requirement than the standalone loader's absent-file
defaults. Catalog syntax/model errors map to schema initialization failure.
Custom catalogs remain optional and are never created or repaired.

`VERSION` follows `src/app_version.py`'s ASCII requirement, exact version regex,
and one final newline after Python universal-newline conversion (LF, CRLF or CR).
In particular, the source regex accepts numeric prerelease identifiers with
leading zeroes; this is not replaced by stricter third-party SemVer rules.
The pilot adds a 1 KiB byte cap. Later reads validate the current resource set,
but the announced application version stays the initially read value, like the
reference's import-time version. Immutable deployment integrity is host-owned.

Every catalog read is preceded and followed by `datahome.Inspect` with the
expected channel. The initial `data_home_id` is retained; changes refuse data
with `DATA_HOME_IDENTITY_CHANGED`. Channel, format and recovery failures retain
the existing admission codes. No cached successful payload is returned after a
failed observation. These individual reads are **not** a cross-file transaction,
lock, ownership/ACL check, or proof against an identity-restoring concurrent
writer. An external writer must be coordinated by the host. Methods retain no
mutable per-read state; serialization in HTTP does not create a filesystem lock.
There is no cancellation in native filesystem operations: a startup process
deadline can terminate the subprocess, but an ordinary HTTP deadline does not
interrupt an in-progress OS read.

## Public diagnostics

`*Error.Code` is one of:

- `SIDECAR_START_INVALID_PORT`, `SIDECAR_START_INVALID_CHANNEL`,
  `SIDECAR_START_INVALID_PATH`, `SIDECAR_PATH_BOUNDARY_INVALID`,
  `SIDECAR_RESOURCE_INVALID`, `SIDECAR_SCHEMA_INITIALIZATION_FAILED`.
- `DATA_HOME_UNAVAILABLE`, `INVALID_DATA_HOME`, `DATA_HOME_NEWER_THAN_APP`,
  `DATA_HOME_MIGRATION_REQUIRED`, `DATA_HOME_RECOVERY_REQUIRED`,
  `DATA_HOME_CHANNEL_MISMATCH`, `DATA_HOME_INPUT_UNSUPPORTED`,
  `DATA_HOME_IDENTITY_CHANGED`.
- The process-owned entry point adds `DATA_HOME_IN_USE`,
  `DATA_HOME_OWNERSHIP_UNAVAILABLE` and `DATA_HOME_OWNERSHIP_UNSUPPORTED`;
  native causes remain private and unknown ownership codes become unavailable.

All formatting emits one fixed message, never paths, catalog labels, identities,
native error prose, or nested causes. Unknown admission codes become unavailable.
Input bounds/unsupported admission cases and the identity-drift code are explicit
pilot policy, not newly claimed Python error parity.

## Scope and verification

This is source-mode explicit resource selection only. It does not implement
Python's implicit `package_resource_root`, legacy same-root bridge, `~` or cwd
discovery, frozen executable-parent / `_MEIPASS` identity, deployment provenance,
bootstrap, default-custom-group writes, SQLite writes/job registration, NLP warmup,
recovery, credentials, profile selection, arbitrary file routes, or mutations.
Initialized metadata alone is not authorization to use private production data.

Synthetic `t.TempDir` tests cover payload equivalence/copies and fresh catalogs,
all channels, version syntax/newlines/bounds, roots and overlap, missing and
invalid resources, initialized marker failures, later identity/channel/recovery
drift, post-read rejection, fixed error formatting, and unchanged tree bytes,
file sets, modes and regular-file mtimes. OS access times and directory mtimes
are intentionally not asserted: Windows directory-enumeration timestamps can
lag synthetic fixture creation until later handle queries refresh them.
Native Windows metadata tests may need normal host permissions when the sandbox
cannot inspect configured ancestors; fixtures remain synthetic in either mode.

From `backend`, with pinned Go 1.27.1 and offline module/cache configuration:

```text
go test -count=1 ./internal/sidecar/readonly
go vet ./internal/sidecar/readonly
```

Python source references: `runtime_paths.resolve_resource_root`,
`resolve_sidecar_resource_root`, `RuntimePaths.resource_path`,
`run_intake_ui._sidecar_paths_overlap`, `run_sidecar`,
`data_home.inspect_data_home`, and `app_version.read_application_version`.
Existing package contracts remain applicable; real lifecycle/HTTP/Electron
integration and native platform acceptance are separate parent-owned gates.

## CH-38 Profile read

`Workspace.Profile(context.Context) (json.RawMessage, error)` validates current
resource/root boundaries, checks the admitted initialized Data Home and expected
channel, and invokes the fixed eight-file `vaultread.ReadProfile` service. It
rechecks identity/channel/format/recovery after the read, including a failed read,
before returning either owned raw JSON or the original privacy-limited
`*vaultread.Error`. Post-read admission failure takes precedence over stale Vault
diagnostics. Registry's existing behavior is unchanged.

The profile is fresh and unnormalized; same-read revision bytes, scalar-key
collisions and output limits remain the Vault service's responsibility. Missing
Vault files are errors, not default profile creation. Cancellation is checked
between resource/admission/load operations and returns `VAULT_READ_CANCELED`;
synchronous native calls still require host lifecycle bounds. No cache, global
lease, writes or installed endpoint selection is added. These admission checks
are point-in-time observations, not exclusion of identity-restoring writers.

Private-seam tests cover denial-before-load, post-read drift even on error,
safe error preservation and cancellation boundaries. Synthetic native tests
cover eight valid files, fresh revisions, caller-owned JSON, missing-profile
non-creation and admission/resource changes without fixture writes by reads.
The composed Windows package check passed in 4.254s with 93.0% coverage using
Go1.27.1; targeted vet passed. Pure cancellation/admission checks also run without
filesystem access. This is not other-platform or installed-application acceptance.

## CH-39 all fixed Vault views

`Workspace.Vault(ctx, vaultread.View)` now applies that same admission to Profile,
Eligibility, Education, Experiences, Projects, legacy Skills and Snapshot.
`Workspace.Profile` remains a convenience wrapper. Invalid enum values return
`VAULT_VIEW_INVALID` before inspecting either root; a caller's path/string is
never a projection selector. The common private admission seam is tested for
post-read drift on both successful and failed loads.

Native tests compare each new view with its service, then alter only synthetic
source comments and require fresh revisions. Caller-owned payloads, unchanged
file trees and identity-drift refusal are checked. Snapshot allows contact:null
under common validation; standalone Profile still rejects it. The package
passes in 6.636 s at 93.1% coverage, with vet passing. The real Windows process
suite also verifies all seven routes through the unchanged supervisor.
No writer ownership, installed activation or other-OS native acceptance follows.

## CH-27 Application-summary service

`Workspace.ApplicationSummaries(ctx, jobIDs)` composes the existing exact
application-summary query model and read-only connector with the claimed-root
SQLite guard. It opens only `applications/career_hub.sqlite3` under the admitted
Data Home. There is no arbitrary filename/SQL selector, new HTTP endpoint,
schema migration, installed runtime switch or implicit application creation.
Python's general list/detail calls are deliberately not replaced by this method.

Input bounds are shared with `applications.ValidateSummaryRequest` and checked
before filesystem admission. The request IDs are cloned, not normalized.
An empty request returns an owned empty map even for a nil context/workspace,
matching the source's early return. Other requests validate resources, recheck
the originally admitted home identity/channel/format/recovery state, and require
the exact process ownership claim before observing the database. Plain `Open`
does not silently acquire a claim or make an unowned application read succeed.
Missing main database/parent returns an empty map without creation only after
that ownership check. Schema below 27 returns empty; newer schema is refused.
An existing directory/link/unsafe companion is a path refusal, not an empty
database fallback; this is an explicit stricter target admission policy.

For an existing database, cleanup is synchronous and ordered: rows/connection,
database, guard final verification/pins, originating root. Then home admission
is checked again, including after empty or failed reads. A failed final check,
cleanup or cancellation discards the result; no stale success or native-error
cause escapes. The private admission seam tests drift without filesystem races.
Resource files are revalidated before each operation, not held as a cross-file
transaction or deployment-integrity lease.

The service emits only fixed `*readonly.Error` categories in addition to the
existing resource/admission errors:

- `APPLICATION_INPUT_LIMIT`, `APPLICATION_INPUT_UNSUPPORTED`,
  `APPLICATION_READ_CANCELED`.
- `DATA_HOME_OWNERSHIP_REQUIRED`, `APPLICATION_STORAGE_UNSUPPORTED`,
  `APPLICATION_PATH_REJECTED`.
- `APPLICATION_DATABASE_UNAVAILABLE`, `APPLICATION_SCHEMA_TOO_NEW`,
  `APPLICATION_READ_FAILED`, `APPLICATION_VALUE_UNSUPPORTED`,
  `APPLICATION_RESULT_LIMIT`, `APPLICATION_CLEANUP_FAILED`.

Unknown model/path categories collapse to a fixed failure. Raw SQL, paths,
request IDs, native messages and wrapped causes are not retained. These service
categories are explicit diagnostic policy, not Python exception-class parity.

The current service is Windows/fixed-NTFS only; unsupported native storage has
no unguarded fallback, even for a missing database. Contexts remain cooperative,
not a hard native-operation timeout; callers own bounded process/request
lifecycle and must not abandon driver goroutines before cleanup. The guard
preserves legitimate SQLite WAL/SHM effects and independent SELECTs, not a
snapshot or hostile-same-user VFS sandbox. Existing-home permissions and
unmodified legacy writers remain under the documented storage/cutover policy.

Tests cover source early returns/shared bounds, before/after admission drift,
cancellation and fixed error privacy. Twenty disposable Windows process cases
exercise the real service with missing/current/older/newer/malformed databases,
owned/unowned access, main/companion hardlinks, typed enrichment, fresh reads and
native handle release before child exit. File membership, bytes, file mtimes
and modes are compared around each closed-database read. Explicit test-fixture
writes are completed separately. Non-Windows refusal has its own test; a
cross-build alone does not count as native execution.

## CH-40 Authenticated document metadata

`Workspace.Documents(ctx, includeArchived)` returns the complete permitted
metadata listing. `Workspace.DocumentTitles(ctx)` returns only active titles for
model context. Both use the same fresh read: initialized-home/resource admission,
exact Windows process ownership, legacy refusal, state and active-key checks,
authenticated ledger decryption with a retained-key lookup, strict ledger parse,
exact active **and archived** blob file membership, synchronous cleanup and
post-read home admission. Nothing is cached, generated, migrated or written.
The existing metadata GET route is wired through the authenticated Go HTTP
adapter. The title-only method is internal, not a new HTTP route. The installed
Python runtime is unchanged.

Only the validated header key ID selects a stored retained key. The expected
home, purpose and ledger object ID are independent trusted context. The active
key must still be available first, even for a missing ledger or a retained-key
envelope. No active-key fallback, credential discovery or regeneration occurs.
An empty store does not instantiate the native credential adapter. Nonempty
production reads select the existing Windows adapter; test injection is private,
not an app setting or publicly configurable backend. Keys and temporary
plaintext owned by this reader are cleared when no longer needed; this is
best effort, not guaranteed runtime-wide memory zeroization.

Only `documents/key_state.json` and `documents/ledger.chdoc` have content reads.
The file directory is scanned for ordinary names/types, never blob bytes.
The larger managed read uses the exact envelope bound without changing catalog
limits. All metadata records retain their stored values and ordering; revision
hashes ciphertext. The model projection never includes notes, IDs, dates,
categories, filenames or blob contents. This is an application boundary, not
a restriction on a separately authorized OS-level agent.

Fixed public error categories cover `DOCUMENT_READ_CANCELED`,
`DOCUMENT_STORAGE_INVALID`, `DOCUMENT_STORAGE_UNSUPPORTED`,
`DOCUMENT_STORAGE_LIMIT`, `DOCUMENT_FILE_INVALID`,
`DOCUMENT_MIGRATION_REQUIRED`, `DOCUMENT_KEY_STATE_MISSING`,
`DOCUMENT_KEY_UNAVAILABLE`, `DOCUMENT_ENCRYPTION_INVALID`,
`DOCUMENT_ENCRYPTION_UNAVAILABLE`, `DOCUMENT_ENCRYPTION_FAILED`,
`DOCUMENT_LEDGER_INVALID` and `DOCUMENT_LEDGER_INPUT_UNSUPPORTED`,
plus existing resource/admission/ownership failures. Errors retain no native
causes, paths, target names, key bytes or partial document output.

Native synthetic tests exercise 33 cases, including two stricter target-policy
unowned refusals. Thirty-one encrypted-read outcomes match actual Python source,
including records, ciphertext revisions and hashes of exact credential-read
targets/order. The source uses only an injected fake credential backend; its
filesystem, admission, key manager, decryption and ledger reader are real.
Both readers leave fixture bytes/file mtimes/modes/membership unchanged.
Actual state/ledger/file-directory rename before child exit verifies per-read
handle cleanup, separately from process-owned root lifetime.
Three additional cases prove beta/stable namespaces and refusal when a dev home
has only a stable-namespace key: 36 native cases and 34 source matches overall.
The independent channel run's request hash is
`9524c39360ec662d989a079a9c897da7ffd9a4cb7739bf00d87f5aaed23a7549`
(fixture locations vary; it is not a portable corpus hash).

The content-denial fixture uses exclusive byte-range locks and first verifies
that an ordinary `os.ReadFile` really fails. Metadata reading then succeeds.
A sharing-mode denial was not an appropriate byte-access test: it also blocked
native metadata opens, so that attempted fixture was replaced without weakening
production guards. The [Windows lock contract](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-lockfileex)
does not constrain memory-mapped access; this test does not claim otherwise.
Locks are explicitly unlocked and handles closed before fixture cleanup.

Seven affected native regression packages passed, as did targeted vet.
Linux/amd64 and macOS/arm64 read-only test binaries cross-build, including an
explicit unsupported-document-storage test, but were not executed natively.
Real original-account credentials, installed identity and other-OS adapters
remain manual/native gates. See the continuing read contract.
