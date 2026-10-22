# MIG-100: Backend language and migration boundary

Status: **Accepted direction; implementation and feasibility verification in progress**.

Reviewed: 2026-09-07. Decision task: CH-17; first implementation: CH-18.
Source baseline: `c06d417`.

The user authorized Go backend migration first, retaining and testing Electron
throughout. Electron/Wails evaluation follows the backend migration; an early
Wails comparison is not a prerequisite. User-interaction-only checks are recorded
for later testing and must not block independent automated implementation work.

## Recommendation

Use **Go for the application backend**, retaining **Electron, the existing
JavaScript UI, and the browser extension** during the backend migration. Preserve
the local HTTP and sidecar protocols. Do not introduce Wails, Tauri, React, a new
database format, or new product behavior in the same program of work.

This is a recommendation for this repository, not a claim that Go is universally
better than Rust or that Python cannot support a well-structured application.
The [technology selection](../reference/technology_selection.md) now authorizes this staged
backend replacement. Python remains the current production implementation until
the documented cutover gates pass; accepted direction is not a claim of parity.

## What the review actually found

At the pinned commit, `git ls-files src` contains 92 Python files with 61,271
physical lines, excluding tests. These are size measurements, not statements or
complexity scores. The largest modules include:

| Module | Physical lines | Migration significance |
| --- | ---: | --- |
| `src/run_intake_ui.py` | 7,787 | HTTP, composition and lifecycle have accumulated together |
| `src/application_history.py` | 4,433 | SQLite schema, migration and several domains share a module |
| `src/email_sync.py` | 2,716 | Network deadlines, receipts, leases and observability |
| `src/job_discovery.py` | 2,256 | Feed state, provenance and promotion semantics |
| `src/agent_workflow.py` | 2,042 | Existing machine-consumable commands and safety boundaries |

The system is not just a web server over a database:

- YAML files remain canonical Career Vault and catalog data; SQLite schema 27
  holds application/discovery/email state. Data Home format 2 is a separate
  compatibility boundary.
- `spaCy` is used for token-aware phrase matching. `python-docx` and ReportLab
  implement real intake/export behavior. Removing Python means replacing these
  capabilities, not only rewriting HTTP handlers.
- The encrypted document envelope, native credential identities, snapshot hashes,
  revision checks and backup/restore journals are compatibility requirements.
- The desktop host already uses Electron/Forge and a tested version-1 sidecar
  protocol. There is no existing Go or Rust backend to complete.

See the [architecture atlas](../reference/architecture_atlas.md),
[runtime requirements](../../requirements.in),
[desktop package](../../desktop/package.json), and
semantic compatibility register.

The practical problem is responsibility boundaries and verifiable behavior.
Translating the largest Python files into equally large Go files would not solve
it. Static types improve some checks; explicit transaction ownership, deadlines,
contracts and tests still do most of the work here.

## Alternatives

The effort assessments below are engineering judgments based on the current
workload. They are not benchmarks, hiring-market claims, or estimates of a
particular model's coding ability.

| Choice | Benefits for this product | Costs and limits | Verdict |
| --- | --- | --- | --- |
| Modularize Python only | Lowest compatibility risk; preserves document and matching ecosystem | Retains Python packaging/runtime and requires disciplined boundaries anyway | Valid fallback and comparison baseline |
| Go backend + current shell | Standard HTTP/I/O facilities, explicit errors, package boundaries and a compiled sidecar; good fit for a local I/O-heavy service | GC remains; document/tokenization parity and native keychain integration need proof | **Recommended** |
| Rust backend + current shell | Ownership/type system catches many memory and thread-safety errors; no tracing GC | More ownership/async integration work for this service; same storage, document and OS compatibility problems remain | Strong alternative, but no demonstrated requirement justifies the additional porting work here |
| Go + Wails or Rust + Tauri together | Potentially reduces shell footprint as well | Simultaneously changes renderer, native bindings, installers, updates and security boundary | Separate decision after backend parity |

Go's module conventions support private implementation packages under `internal`
and thin executables under `cmd`; that is useful structure, not an architectural
framework. [Go module layout](https://go.dev/doc/modules/layout).

Go has garbage collection, so it is not a route to a no-GC runtime. Rust's
ownership model provides memory management without GC, and its types help reject
many concurrency errors. Neither establishes the correctness of an application
status transition or a SQLite transaction.
[Go GC guide](https://go.dev/doc/gc-guide),
[Rust ownership](https://doc.rust-lang.org/book/ch04-01-what-is-ownership.html),
[Rust concurrency](https://doc.rust-lang.org/book/ch16-00-concurrency.html).

Reconsider Rust if a measured requirement needs tighter memory/latency control,
an essential native integration proves materially better there, or the maintainer
chooses Rust as the long-term ownership language. Do not choose it solely because
the app is packaged for desktop. Conversely, stop or reduce the Go migration if
its feasibility pilots fail; do not weaken compatibility to justify this ADR.

## Proposed stack

| Area | Initial choice | Dependency/acceptance gate |
| --- | --- | --- |
| Language | Supported stable Go toolchain, pinned when MIG-120 starts | Native Windows build; record minimum OS/arch and all build inputs |
| HTTP and lifecycle | `net/http`, `context`, explicit timeouts, existing NDJSON protocol | Exact routes/auth/status/body/lifecycle conformance; no framework required |
| Persistence | `database/sql`; evaluate `modernc.org/sqlite` first | Existing schema/SQL/PRAGMA/backup behavior and native builds pass before adoption |
| YAML | A maintained YAML implementation selected by fixture tests | Scalar typing, duplicate handling, ordering and revision semantics; not a JSON-only rewrite |
| Mail | Evaluate `emersion/go-imap/v2` behind a narrow transport interface | Version/API maturity, cancellation, read-only access, TLS, fake-server fault tests |
| Unicode/matching | Explicit normalization/tokenization adapter; evaluate `golang.org/x/text` | Python case-folding, punctuation, matching and offset parity |
| Encryption | Standard AES-GCM primitives + exact Career Hub envelope codec | Bidirectional interoperability and tamper/identity rejection |
| OS secrets | Small platform adapters preserving existing identities and policies | Native Credential Manager/Keychain tests; Linux support remains a separate gate |
| Documents | Select implementation only after representative import/render pilots | No feature removal; Python worker allowed only as an explicit temporary stage |
| UI/desktop | Existing UI + Electron/Forge + existing extension | Same contracts and isolation; change sidecar resource/spawn packaging only when ready |

`modernc.org/sqlite` is a CGO-free SQLite implementation, but that does not make
every dependency or OS integration CGO-free. Treat it as a candidate, not a
complete portability proof. [Driver documentation](https://pkg.go.dev/modernc.org/sqlite).

The reviewed go-imap v2 documentation explicitly describes the major version as
in development. Do not install an unpinned `latest` version or describe it as a
settled stable choice; record the selected release and risks during the pilot.
[go-imap documentation](https://pkg.go.dev/github.com/emersion/go-imap/v2).

The Go database handle is a connection pool. Connection limits and transaction
use must be designed together; a goroutine per request is not an IMAP/SQLite
deadlock fix. [Managing Go database connections](https://go.dev/doc/database/manage-connections).

## Intended code organization

One backend module, not microservices or one module per entity. This is an
outline to grow as slices land, not a requirement to scaffold empty directories:

```text
backend/
  go.mod, go.sum
  cmd/career-hub/          # composition; sidecar, browser and agent entry points
  internal/
    runtime/              # paths, process protocol, resource identity
    vault/                # profile/evidence/custom catalog use cases
    skills/               # catalog lookup, tokenization, evidence associations
    discovery/            # feed, capture, promotion and provenance
    applications/         # ledger, statuses, contacts and material lineage
    mail/                 # parser, sender policy and sync orchestration
    documents/            # document registry, envelope and controlled access
    materials/            # validated drafts and render adapters
    storage/              # explicit SQLite/YAML/filesystem implementations
    platform/             # credentials and platform-specific integration
    httpapi/              # HTTP translation, auth and error serialization
    agentapi/             # existing command/packet compatibility
  testdata/               # synthetic, versioned compatibility fixtures
```

Services own use cases and transaction boundaries. HTTP handlers and desktop IPC
translate requests; they do not contain matching or application business rules.
Use concrete types, small interfaces at real I/O boundaries, explicit clocks and
ID generators for deterministic tests. Avoid an all-purpose repository layer,
dependency-injection framework, untyped event bus or a giant `utils` package.
Create domain interfaces when a slice needs them, not one interface per struct.

One backend owns a Data Home at a time. The initial strategy is staged development
and comparison against isolated fixture copies, followed by a whole-backend
cutover. It does **not** require a production Python/Go routing proxy or two
servers writing the user's database.

## Shell and performance boundary

Keep Electron during this program. A Go backend alone does not remove Chromium
or guarantee a tiny installer. Wails uses platform WebViews and has native
platform prerequisites; even reused HTML/CSS needs rendering, accessibility,
extension and installer regression checks. [Wails prerequisites](https://wails.io/docs/gettingstarted/installation/).

The existing DESK-000 baseline
measured a Linux source Python process at an earlier commit, not today's installed
Windows Electron app. It cannot substantiate claims such as 20 MB total RAM,
20 ms startup, or a particular installer shrink ratio.

Re-measure both implementations on the same host and fixture. Keep the existing
[desktop budgets](../product/desktop_delivery_roadmap.md): cold usable p50 <= 3 seconds,
warm <= 2 seconds, zero duplicate sidecars, no app-owned work after clean close,
and offline core functionality. Record sidecar and whole-process-tree memory
separately, packed/unpacked bytes, and raw timing samples. Do not revise failing
budgets inside a migration ticket. Any additional size or speed target is a
reviewed target before measurement, never a promised outcome of the language.

## Decision gates

1. User agreement recorded: Go backend / retained Electron / compatibility-first
   scope. Remaining items are implementation evidence, not further scope approval.
2. MIG-110 pins a current baseline and enumerates all observable contracts.
3. MIG-120 proves the risky adapters: SQLite, envelope/credentials, matching,
   document support and process lifecycle. Any unproved adapter stays explicit.
4. Every slice satisfies its tests and state-diff checks; known source defects are
   separate approved behavior changes, not blindly preserved or silently fixed.
5. Packaged Windows acceptance precedes replacing the user's runtime. A fully
   Python-free release is a separate exit gate from an interim Go-backed build.
6. macOS and Linux require native evidence; a successful cross-build is not it.

The execution plan and
tool specification define the work and
evidence. This ADR does not authorize releases, external uploads, or publishing
private fixtures.
