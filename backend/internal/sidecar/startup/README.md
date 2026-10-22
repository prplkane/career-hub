# Explicit Windows bootstrap workspace — CH-37

`Open(context.Context, protocol.StartRecord) (*readonly.Workspace, error)` is an
explicit composition for a maintainer-selected bootstrap mode. It never selects
itself, discovers a profile, consults launcher preferences, or changes the
default read-only workspace. The installed Python/Electron startup is unchanged.

Order: cancellation check; read-only resource/boundary preflight; cancellation
check; CH36 `datahome.Bootstrap`; cancellation check; `readonly.Open`; final
cancellation check. Resources require the existing static sentinel, regular core
files, and valid ASCII `VERSION`. Core YAML/model decoding uses existing fixed
input limits **before mutation**. Combined core/custom graph validation follows
bootstrap; private custom files are not decoded, rewritten or repaired in
preflight. Earlier malformed-core refusal is an explicit maintainer safety delta
from Python's bootstrap-before-schema order, not exact error precedence parity.

The dedicated executable selects `OpenForProcess`, using
`datahome.BootstrapForProcess` at the bootstrap step. CH-41 then explicitly runs
`schema.PrepareOwned` after `readonly.Open` admits the initialized home and full
effective registry. `Workspace.CheckAdmission` rechecks the bound identity,
channel, recovery sentinels and resource boundary before preparation and after
all its native cleanup, including when preparation reports an error. An observed
cancellation never returns a workspace. This preparation library adds no flag or
request-time write. The CLI separately uses the admitted workspace to compose
CH-8's authenticated material operations in explicit bootstrap mode only.
The ordinary `Open` primitive still bootstraps without SQLite preparation.

`OpenNewJobRecoveryForProcess` is a separate, unconnected maintenance
composition. It runs the same resource/path-boundary preflight, claims the exact
existing Data Home, and admits only the new-job sentinel through
`InspectOwnedForNewJobRecovery`. It never bootstraps, prepares SQLite, opens an
ordinary workspace or repairs/replays a phase. Its returned workspace pins the
startup Home and resource root and exposes only the read-only recovery plan and
the expected-plan one-step owner. There is no CLI flag, stdio protocol,
Electron handler, startup retry loop or installed activation for it yet.

The Windows Data Home
claim precedes private reads/ACL writes/seeding and is retained until OS process
exit, including failed/canceled startup. Subsequent readonly.Open does not claim
again. The ordinary Open primitive and its external-writer precondition remain;
neither variant coordinates unmodified legacy Python or arbitrary local writers.

Missing candidate roots are not created to establish separation. Managed root
opens validate full path syntax and existing ancestry; retained managed readers
validate missing descendants. Native identities catch aliases into the resource
root. A new sibling sharing a parent with resources is allowed. Candidates whose
only existing ancestor is the drive/filesystem root are refused as
`DATA_HOME_UNAVAILABLE` in this composition; this is a narrower maintainer pilot
restriction, **not** a restriction of CH36 Bootstrap. Shipped Electron channel
paths have an existing ordinary platform/user ancestor. This compatibility gate
must be revisited before adopting broader user-selected startup paths.

Preflight releases its handles before bootstrap, which independently repeats its
checks. This is not a namespace reservation, exclusive writer lease, hostile
same-user reparse/mount defense, or deployment-manifest attestation. The host
must keep other writers quiescent and retain exact-child ownership until exit.
Native synchronous calls only observe cancellation between operations. The
supervisor owns its outer startup/termination deadlines; canceled or killed
startup may leave initialized defaults, a published marker, or an admitted
partial tree, according to CH36. No error promises rollback or deletes partials.

`*Error.Code` forwards only allowlisted readonly/bootstrap/preparation categories;
unexpected causes become `SIDECAR_STARTUP_FAILED`. All formatting is fixed and
retains no path, nested cause, descriptor, catalog text or bootstrap stage.
`DATA_HOME_STARTUP_CANCELLED` is returned on observed context cancellation.
No success/workspace is returned after an observed stop. Lifecycle decides which
stdio exit record to emit; this library opens no listener and emits no output.

This does **not** fully replace Python `_initialize_ui_runtime`: default-group
maintenance, saved-job registration, NLP warmup, credentials, document/restore
recovery and other-OS bootstrap providers remain separate. SQLite schema
preparation is now explicit in process-owned bootstrap only. Default `--sidecar`
HTTP remains read-only; the explicit bootstrap CLI also enables owned material
editor/review/export/download routes with per-operation admission rechecks.
Preparation failure may follow an attempted or completed
commit; no startup error promises rollback or deletes a recovery copy. The
protocol intentionally exposes only a fixed failure code, not internal reports
or filenames. See the preparation contract.

The unchanged runtime's 10-second initialization bound (and host's 15-second
outer bound) shortens the service's nominal 60-second budget. This checkpoint
does not relax timeouts to make a slow migration appear healthy. Large/slow-home
acceptance and crash/disk qualification must precede an installed cutover.

Focused tests use synthetic temporary homes only. They cover preflight refusal
before bootstrap dispatch, missing-root separation and native aliases, malformed
core inputs/bounds, cancellation, sanitized errors, final admission, native
missing/precreated initialization, retained IDs/opaque bytes/ACLs, and recovery/
channel/version/private-schema refusals. Symlink/8.3 cases explicitly skip when
the host cannot create/provide those primitives. Native Windows host permission
may be required when sandbox ancestor inspection fails. Cross-compilation is
not native acceptance. Parent-owned process tests cover listener/pipe/timeout
ordering separately.

From `backend`, using pinned Go 1.27.1 and offline caches:

```text
go test -buildvcs=false -count=1 ./internal/sidecar/readonly ./internal/sidecar/startup
go vet -buildvcs=false ./internal/sidecar/readonly ./internal/sidecar/startup
```
