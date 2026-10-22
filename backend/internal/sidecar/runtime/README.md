# Development sidecar lifecycle and explicit Windows bootstrap

CH-32 connects the protocol, initialized-workspace admission and registry reader
into `cmd/career-hub-sidecar`. This is a separately built migration executable,
not the selected or packaged Career Hub backend. Electron remains unchanged.

The default executable mode accepts `--sidecar`, followed by one v1 startup record on
stdin with explicit absolute resource/data roots, `port: 0` and a valid channel.
It emits readiness on stdout and stops on shutdown or stdin EOF. Stdout contains
bounded protocol records, never logs, paths or browser capabilities. Invalid CLI
arguments exit 2 without reading input or printing output. The process owns its
pipes; `Run` must be followed by process exit, not embedded as a reusable service.

The read-only HTTP surface includes:

| Route | Purpose |
| --- | --- |
| `GET /api/health` | Readiness/version, subject to local browser boundary |
| `GET /api/session` | Capabilities for a caller that already holds the host-issued token; returns no token (ADR SEC-250) |
| `GET /api/skills-registry` | Authenticated fresh core/custom registry snapshot |
| `GET /api/vault/{profile,eligibility,education,experiences,projects,skills,snapshot}` | Fixed validated Vault views |
| `GET /api/documents` | Permitted document metadata |

In default `--sidecar` mode the workspace must already be initialized. Resource files, data identity and
catalogs are admitted before readiness and revalidated for registry reads. See
the sibling `readonly` and `httpapi` READMEs for precise bounds and compatibility
deltas. No profile discovery, bootstrap, schema upgrade, recovery, SQLite access,
durable write, static UI, extension API or default-backend switch is added.

CH-37 adds the separate **maintainer-only** `--sidecar-bootstrap` flag. It uses
the identical v1 stdin record and read-only HTTP routes, but explicitly initializes
a Windows Data Home before readiness. It validates resources and non-overlap,
calls CH-36 bootstrap, then admits the initialized workspace and registry.
CH-41 additionally prepares the application schema with a verified recovery
copy for older databases and rechecks the same admitted home after cleanup,
before readiness. Current databases are not migrated or backed up again.
CH-8 now also connects the owned material editor/review/export/download routes
in this explicit mode. See the [HTTP contract](../httpapi/README.md#owned-material-workflow)
for source-compatible URLs, authenticated request bounds and failure-state rules.
No installed Electron command, packaging selection or default `--sidecar`
behavior switches to this mode. Never point this incomplete executable at a
real profile; use owned synthetic homes with other writers stopped.

This is not full Python `_initialize_ui_runtime` parity: saved-job registration,
other services, static UI and general mutations remain absent.
An error after bootstrap can leave defaults or a marker already published; no
error implies an empty or rolled-back directory. Normal bootstrap retry rules
apply and no Data Home/document restore journal is automatically repaired.
SQLite's own hot rollback-journal handling is a distinct preparation concern.

Data selection stays with the host. Electron uses its platform/channel root and
does not read the browser launcher's machine-local `launcher.json` profile pointer.
The Go stdio mode neither merges those choices nor invents another default path.
Electron single-instance ownership is channel/userData-scoped; the supervisor
owns one exact child. Neither is a global lock against a CLI/another backend
opening the same Data Home. Quiescent-writer ownership remains a cutover gate.

Both Go executable modes now select their `ForProcess` workspace entry points.
They acquire a Windows/fixed-NTFS [cooperating-backend claim](../../runtimepaths/README.ownership.md)
before private admission or bootstrap writes. A duplicate owner reports
`DATA_HOME_IN_USE`; unavailable/unsupported claims retain fixed startup codes.
The claim belongs to the OS process, not Run or an HTTP request. It deliberately
has no deferred unlock on Run return: only actual process exit releases it.
This does not retrofit the Python backend or alter installed Electron selection.
Other-platform ownership is explicitly unsupported pending native providers.

## Lifecycle bounds and ownership

- First startup record: 10 seconds. Initialization: a separate 10 seconds.
- Each protocol output: 2 seconds; a blocked output attempts pipe closure once
  and does not trigger a second write through that failed output path.
- Each owned pipe's close attempt: a maximum 250 ms wait, shared by repeat and
  concurrent calls (up to 500 ms for both pipes). Its result is sticky even if
  native cancellation finishes later. Then return directly to process exit;
  this is not proof that pending native I/O or the OS process has already exited.
- Graceful HTTP shutdown: 2 seconds, then connection close; a final Serve join
  allows 100 ms. The unchanged desktop supervisor retains its outer process
  startup/stop limits (15 seconds / 3 seconds) and final child termination.
- HTTP header/read/write/idle limits: 2/5/10/15 seconds; configured header limit
  16 KiB. Admitted material operations alone use the owner's 20-second context
  and a per-request 25-second write deadline; the 5-second body bound remains.
  The HTTP adapter also bounds request targets and response transport.
- Bind only IPv4 `127.0.0.1:0`. Windows explicitly sets `SO_EXCLUSIVEADDRUSE`
  before bind; merely omitting `SO_REUSEADDR` is not the equivalent guarantee.

Cancellation is cooperative: context cancellation and HTTP write deadlines do
not preempt every synchronous filesystem call or handler computation. Process
exit and the parent-owned termination bound are the final stop mechanism. This
default read-only HTTP layer has no writes to drain. Explicit bootstrap-mode
material operations can publish while serving. They retain operation/source/SQL
ownership through synchronous cleanup; shutdown can interrupt a publication,
requiring exact retry/recovery after the child actually exits. No response error
promises rollback. The optional bootstrap performs synchronous
startup writes with cancellation checkpoints and explicit partial-state semantics.
Its nominal 60-second context and ACL's 45-second context are shortened by this
runtime's 10-second preparation deadline and the unchanged host's outer bound.
The dedicated process must exit after `Run`; an uncooperative callback/native call
can remain live until that exit. Windows pipe Close itself can wait for native
I/O references after requesting cancellation, so it too has a bounded wait here.
The wrapper preserves optional protocol Flush behavior. No in-process callback-quiescence guarantee is
claimed. Exact-child close/reaping must precede fixture cleanup or restart.

CH27's native supervisor suite now separately withholds the first start-record
write, without changing the actual supervisor, executable or pipe destruction.
It verifies the fixed Go timeout, native input destruction and exact child exit
before startup rejection, then pipe close and successful restart. The final
suite passes 14/14. An earlier standalone silent-parent natural-exit deadline
failure remains recorded in migration progress; its assertion is not removed or
relaxed. A bounded diagnostic observed both pipe closes and Run returning before
a later parent-observed process exit, but did not establish the earlier failure's
cause. Keep clean-host/native exit measurements separate from in-process tests.

The next control record is now read throughout preparation and serving, not only
after readiness. EOF or valid shutdown cancels preparation and exits 0 without
dispatching readiness once the stop is observed. Invalid control exits 3 with
the same sanitized protocol category. This is an explicit safety improvement
over Python's post-ready control reader. A readiness write already dispatched
cannot be retracted if stop delivery races it; do not infer a pipe transaction.
One bounded reader owns that record; closure attempts cancellation and process
exit finally reaps outstanding work. Parent-observed exit can include OS latency.

Read-only admission is not an OS-wide Data Home ownership lock, an atomic snapshot
across hostile concurrent writers, or a packaged-resource signature check.
Those are outstanding migration gates, including CH-27's SQLite pathname/WAL/SHM
ownership questions. Native macOS/Linux and installed app acceptance are separate.

## Validation

From `backend`, use the pinned offline toolchain/environment:

```text
go test -buildvcs=false -mod=readonly -count=1 -timeout=60s -cover ./internal/sidecar/protocol ./internal/sidecar/runtime ./internal/sidecar/httpapi ./internal/sidecar/readonly ./internal/sidecar/startup ./cmd/career-hub-sidecar
go vet -buildvcs=false -mod=readonly ./internal/sidecar/runtime ./internal/sidecar/httpapi ./internal/sidecar/readonly ./internal/sidecar/startup ./cmd/career-hub-sidecar
go build -trimpath -buildvcs=false -mod=readonly -o ../tmp/ch37/career-hub-sidecar.exe ./cmd/career-hub-sidecar
```

The opt-in `desktop/tests/go-sidecar.integration.test.js` uses the actual unchanged
`SidecarSupervisor` module and a real Go child, not the Electron GUI. Supply
per-process `CAREERHUB_TEST_GO_SIDECAR=1`, absolute `CAREERHUB_GO_SIDECAR`, reviewed
and `CAREERHUB_GO_SIDECAR_SHA256`.
Run it with the pinned Node 24.19.0, from the repository root:

```text
node --test desktop/tests/go-sidecar.integration.test.js
```

It creates only owned temporary synthetic roots. Read-only mode compares the Go
registry with the real Python source, checks denied requests, restart/new token,
EOF, malformed input, silent-parent timeout and cancellation. Root file hashes,
file modification times, modes and membership remain unchanged; atime and delayed
Windows directory timestamps are intentionally not an immutability oracle.

Cleanup must await each exact child's `close`, not merely a rejected operation
promise or a sent kill request. A failed reap retains the fixture. Protocol test
decoding rejects invalid UTF-8, BOM, missing final LF and oversized records.
Neither credentials nor an existing Data Home is used. Native tests skip without
opt-in; the three in-memory harness regressions still execute.

Windows bootstrap integration uses a test-only spawn adapter to select
`--sidecar-bootstrap`; the actual supervisor's health, deadlines, protocol and
exact-child logic are unchanged. It checks fresh default/marker state, source
registry/byte agreement, complete source-equivalent fresh SQLite schema, restart preservation, capability
rotation, duplicate-start refusal and failed-resource/overlap/recovery admission.
An actual post-bootstrap private-schema failure must preserve the published
marker and invalid private input; correcting only that fixture's catalog then
restarting retains identity, defaults and opaque files without a reset.
CH-41 adds populated version17 source/target upgrades with full pre-upgrade
recovery-image comparison, current-schema restart without another backup,
newer-schema refusal and recovery-copy failure without original-state changes.
The 18-test native run passed on Windows with no skips; the installed app and
supervisor implementation remain unchanged. This is process integration, not
Electron GUI/installer or power-loss acceptance.
Native permission effects are independently tested by the composed startup and
Data Home packages. Deterministic runtime tests cover EOF/shutdown/invalid control
while preparation or handler construction is blocked, and preparation timeout.
Injected stalled-close tests prove that both silent-input cleanup and timed-out
output cleanup return to process exit; concurrent/repeated close calls share one
attempt. These are not a simulation of the cause of every native timing outlier.
Content-free test diagnostics distinguish spawn/first-output/exit timing and byte
counts without retaining or exposing stderr text or capabilities.

Evidence, hashes and independent-review limits are recorded in
`docs/history/migration/progress.md`. No result here authorizes production cutover.
