# Career Hub Data Home and Recovery Contract

> **Status: needs rewrite.** The intent below is still accurate, but its `python -m src.*` commands belong to the retired Python runtime and no longer run.
> Until this document is rewritten, use the desktop's Settings (backup, restore, recovery, active path) and [private data](private_data.md). See the
> [documentation index](../README.md); historical comparisons use the
> pinned reference export.

Career Hub separates immutable application resources from mutable candidate
data. The code package supplies `src/data/` and UI assets; a data home holds
Career Vault records, saved JDs, application history, resumes, templates, and
local operational artifacts.

## Native Windows Paths and Channels

The supported Go desktop uses `%LOCALAPPDATA%\CareerHub\dev` or `stable`,
according to its package channel. Go Preview uses the separate
`dev-go-preview` Home. Settings shows the active path. The native launcher does
not read the retired Python launcher pointer or use the repository as a Home.
Existing Python Homes require explicit supported import or restore; they are
never silently moved or selected. Shared resources remain immutable.

## Historical Python Paths and Channels

The Python commands used the repository as their development data root when
`--data-root` was omitted. That transition mode was retired with the Python
runtime; the Go app and `career-hub-agent` always use a Data Home.

The launcher keeps only a machine-local pointer. Candidate content never enters
that file. Platform locations are:

| Platform | Launcher pointer | Default profile root |
| --- | --- | --- |
| Windows | `%LOCALAPPDATA%\CareerHub\launcher.json` | `%USERPROFILE%\CareerHubData\<profile>` |
| macOS | `~/Library/Application Support/CareerHub/launcher.json` | `~/CareerHubData/<profile>` |

The default user-visible profiles are:

```text
%USERPROFILE%\CareerHubData\main\
%USERPROFILE%\CareerHubData\sandbox\
  applications\
  career_vault\
  documents\
  jobs\
  outputs\
  resume_versions\
  templates\
  logs\
  security-reports\
  backups\
  secrets\
```

`main` is the one authoritative personal data home. `sandbox` is disposable
development/test state. A release channel may still be recorded in the data
home marker for compatibility checks, but a launcher must not silently choose
a different personal dataset merely because application code was updated.

The `secrets` directory is reserved. Provider keys are stored through
`src.secret_store` in Windows Credential Manager or the macOS data-protection
Keychain, never as plaintext files. Raw scanner reports belong in
`security-reports`; they are local operational evidence and are neither tracked
nor included in normal recovery ZIPs.

On an uninitialized Windows data home (no valid data-home marker), Career Hub
applies a private ACL to the root for the current process-token SID and
`SYSTEM` before writing candidate content, then reads back the protected DACL
and requires exactly those two inheritable full-control principals. For an
allowlisted markerless partial bootstrap, Career Hub first rejects links and
reparse points, resets existing descendants to inherit that same private ACL,
and verifies every read-back entry before continuing. This makes interrupted
ACL setup retryable without preserving a broad protected child DACL. Once the
marker exists, bootstrap does not recursively rewrite ACLs on the initialized
home. On POSIX development hosts, each bootstrap verifies that
the current user owns the managed directories and enforces owner-only mode
`0700`. macOS additionally replaces each managed directory's extended ACL with
a verified zero-entry ACL through a no-follow directory descriptor, preventing
an inherited allow entry from bypassing the BSD mode bits.

## Initialize and Start

Create and remember the main home explicitly before importing real data:

```powershell
python -m src.data_home --data-root "$env:USERPROFILE\CareerHubData\main" --channel stable
python -m src.data_home_selection set --profile main --data-root "$env:USERPROFILE\CareerHubData\main" --channel stable
.\scripts\start_career_hub.ps1
```

The macOS direct-development equivalent is:

```bash
python -m src.data_home --data-root "$HOME/CareerHubData/main" --channel stable
python -m src.run_intake_ui --data-root "$HOME/CareerHubData/main" --data-profile main --channel stable --port 8766 --open
```

Bootstrap creates only allowlisted application-owned directories, empty custom
Skills Registry group and skill overlays under `career_vault/taxonomy/`, and a
version marker. Core groups and skills remain immutable application resources
under `src/data/`. Bootstrap does not invent candidate facts, overwrite a user
file, or move any repository data. An empty home is valid for onboarding/import;
a complete Career Vault is still required before Vault-based assessment or
editing workflows can run.

## Explicit Development-Data Import

Do not copy or move private files manually. First review a dry-run plan, then
repeat its exact plan ID as confirmation:

```powershell
python -m src.data_migration plan `
  --source-root "C:\path\to\career_hub" `
  --data-root "$env:USERPROFILE\CareerHubData\main"

python -m src.data_migration apply `
  --source-root "C:\path\to\career_hub" `
  --data-root "$env:USERPROFILE\CareerHubData\main" `
  --confirm-plan "sha256:<plan-id-from-dry-run>"
```

The importer copies into private staging, verifies checksums, validates Vault,
JDs, SQLite, and Resume Library references, then applies the staged copy. It
never deletes, moves, or modifies the source workspace. A source or destination
change invalidates the confirmation. Existing destination files are conflicts,
except for an unchanged application-owned bootstrap default. The durable import
receipt contains only counts, hashes, and a plan ID—never absolute paths,
resume text, JD content, Vault facts, or secrets.

## Recovery Backups and Restore

Create a local recovery ZIP:

```powershell
python -m src.data_backup create `
  --data-root "$env:USERPROFILE\CareerHubData\main"
```

`create --archive` accepts only a relative filename below that same private
`backups` directory. It cannot write a normal recovery ZIP to an arbitrary
external location.

Restore only after reviewing the archive source:

```powershell
python -m src.data_backup restore `
  --data-root "$env:USERPROFILE\CareerHubData\main" `
  --archive "C:\safe-location\career_hub_backup.zip"
```

Each archive has a versioned manifest of relative paths, sizes, SHA-256
checksums, data-home format, and SQLite schema version. Creation writes a
temporary archive, reopens it, and re-hashes every stored payload against the
pre-write manifest before atomically installing or returning the ZIP. A source
change during archive writing therefore fails without leaving a named recovery
archive. Restore rejects
absolute/UNC/drive-qualified/traversal paths, symlink-like entries, duplicate
or case-colliding Windows paths, malformed manifests, corrupted checksums, and
newer incompatible formats before changing active data. It validates staging
before replacement and retains a named pre-restore recovery copy in
`backups\restore-recovery`. Manifest, file-count, per-entry, and aggregate
extraction sizes are bounded before staging. Retention never deletes the only
recovery copy.

Before the first active directory moves, restore publishes a durable,
content-minimized journal. A crash or lost power blocks normal Data Home access
instead of exposing a mixture of old and new roots. Recover that exact
transaction explicitly, then review the retained candidate and recovery copy:

```powershell
python -m src.data_backup recover `
  --data-root "$env:USERPROFILE\CareerHubData\main"
```

Starting another restore never rolls an existing journal back implicitly. The
same rule applies to legacy DPAPI document migration: plan, apply the exact
confirmed plan, and use `python -m src.document_migration recover --data-root
<path>` if an interruption is reported.

The installed desktop host performs that same contract through its protected
startup page. When a v1 home is detected, **Review update** runs a read-only
plan and shows only format versions, document count, and encrypted byte count.
**Update workspace** then applies exactly that in-memory plan, retains a
verified recovery copy under `backups/document-encryption-migration`, and starts
the normal sidecar only after verification succeeds. The renderer never chooses
the Data Home path or supplies a plan id. A legacy home that predates the
Documents feature and therefore has no `documents` directory is treated as a
valid empty document store, not as damaged data.

Normal recovery ZIPs are local, unencrypted recovery artifacts protected by the
data-home ACL. They deliberately exclude `secrets`, scanner reports, prior
backups, and staging directories. A portable export is a different action: it
must use a user-provided password and a vetted AES-encrypted ZIP provider; the
password must never be stored, logged, or reused for automatic backups. It is
not implemented by the local recovery command. Career Hub does not
automatically encrypt general YAML or SQLite files. The `documents` root is the
narrow exception: its ledger metadata and every stored PDF/image blob use a
common AES-256-GCM envelope whose random master key is held in Windows
Credential Manager or the macOS data-protection Keychain. Recovery ZIPs thus
contain document ciphertext, not plaintext. Those files can be restored only
where the original account/device key remains available; cross-device portable
document recovery is intentionally not implemented. Legacy Windows DPAPI
documents require the explicit, recoverable `SEC-240` migration under their
original Windows account and device.

OS-backed document encryption is an at-rest boundary, not a sandbox from other
code already running as the same user. Career Hub therefore defines a separate
future-model projection that returns only active document titles and excludes ids,
filenames, categories, dates, notes, hashes, and bytes. A future model runtime
must receive only that allowlisted projection and must not receive the document
read capability or the local browser capability token. Until such a restricted
runtime is integrated, no claim is made that an arbitrary local model process
is technically unable to decrypt files as the user.

## Schema Migration

Before an existing application SQLite schema is migrated, Career Hub creates a
named SQLite recovery copy under `backups\sqlite-schema-recovery` and records
the source/target schema versions in a content-minimized recovery record. An
application refuses a data-home or SQLite schema newer than it understands.
