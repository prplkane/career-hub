# Desktop Delivery Roadmap

This document is the ordered delivery plan for turning the current local browser
workflow into installable Windows and macOS applications. Desktop-host
construction was explicitly authorized on 2026-08-27. Browser mode remains
supported while native target and distribution gates are completed.

The [Capability Acceptance Registry](../process/acceptance_registry.md) remains the source
of truth for implementation status. Numbers below express dependency order
only. A phase is not "implemented" merely because an earlier historical phase
used the same number.

## Current Delivery Position

The development foundation now includes:

- the explicit resource/data-root boundary is implemented;
- the wrapper-neutral Python sidecar protocol in
  [`sidecar_protocol.md`](../reference/sidecar_protocol.md) is implemented and tested;
- a reproducible PyInstaller `onedir` sidecar and a locked-down Electron 44 host
  are packaged and lifecycle-smoked on exploratory Linux x86_64;
- the existing local UI runs in Electron with default-deny permissions,
  navigation, popups, IPC, and reviewed fuse settings;
- Windows Credential Manager and macOS Keychain-backed AES-GCM document
  protection are implemented in source with migration/recovery fixtures;
- explicit offline and connection-available UX preserves local workflows and
  never automatically replays an internet action;
- browser mode remains the normal development and dogfood runtime.

Windows x64 and macOS arm64 native builds, real platform-key/ACL evidence,
installers, signing/notarization, updates, and clean-machine upgrade/recovery
remain release gates. The Linux package is development evidence only and must
not be distributed as a supported product. See
[`desktop_release_gates.md`](../reference/desktop_release_gates.md).

## Intended Outcome

A user can eventually install Career Hub on a supported Windows or macOS
computer, open it without a terminal or development server, keep private data
locally, receive signed application updates, and export privacy-safe diagnostics
for debugging.

```text
Electron desktop host
  -> resolves OS paths
  -> starts and supervises one packaged Python sidecar
  -> opens the existing local UI in a locked-down bundled Chromium window
  -> owns lifecycle and single-instance behavior

Python sidecar
  -> owns the existing HTTP API and deterministic product logic
  -> reads packaged, read-only resources
  -> writes private data only below one explicit app-data root

Manifest V3 extension (current browser work)
  -> captures a visible JD after an explicit user action
  -> calls the same local intake API
  -> owns no assessment or ledger logic
```

## Fixed Architecture Decisions

Implementation tickets must not reopen these decisions without new measured
evidence and a separate architecture review.

1. Use Electron as a thin Windows/macOS desktop host when desktop-host work is
   explicitly authorized. The prior Tauri plan is superseded; no Tauri runtime
   was implemented and no Tauri files need migration.
2. Keep Career Vault, JD intake, assessment, ledger, and resume-plan logic in
   Python. Do not rewrite the product core in Node or Electron.
3. Package Python as a sidecar executable. Measure PyInstaller `onedir` against
   `onefile`; prefer `onedir` when it starts materially faster and bundles
   reliably.
4. Reuse the current HTML, CSS, and vanilla JavaScript UI for the first desktop
   version. React is not a prerequisite.
5. Use Electron's pinned Chromium runtime for consistent Windows/macOS rendering.
   This does not create or validate a Safari extension.
6. Keep the core offline and local-first. Do not add AWS, Firebase, hosted
   databases, authentication services, or AI APIs in this roadmap.
7. Use SQLite for mutable workflow/history, YAML for reviewable canonical facts,
   and ordinary files for source documents and resume artifacts.
8. GitHub stores source code and release artifacts, never private user data.
9. Implement bounded local diagnostics before considering remote telemetry.
10. Require platform signing, macOS notarization, and cryptographically verified
    updates before public desktop distribution.
11. Treat multi-device synchronization as a separate product capability. A
    desktop wrapper does not define identity, synchronization, conflicts, or key
    recovery.

Official references to use during implementation:

- [Electron security](https://www.electronjs.org/docs/latest/tutorial/security)
- [Electron application lifecycle](https://www.electronjs.org/docs/latest/api/app)
- [Electron ASAR archives](https://www.electronjs.org/docs/latest/tutorial/asar-archives)
- [Electron autoUpdater](https://www.electronjs.org/docs/latest/api/auto-updater)
- [Electron Forge configuration](https://www.electronforge.io/config/configuration)
- [Electron Forge macOS signing](https://www.electronforge.io/guides/code-signing/code-signing-macos)
- [PyInstaller](https://pyinstaller.org/en/stable/)
- [Chrome Manifest V3](https://developer.chrome.com/docs/extensions/develop/migrate/what-is-mv3)
- [Chrome extension permissions](https://developer.chrome.com/docs/extensions/develop/concepts/declare-permissions)
- [GitHub Actions artifacts](https://docs.github.com/en/actions/concepts/workflows-and-actions/workflow-artifacts)

## Desktop Capability Status

These names now exist in `docs/process/acceptance_registry.md`. That registry remains
authoritative; the summary here cannot raise a status.

| Capability | Current status | Completion outcome |
| --- | --- | --- |
| `CAP-APP-DATA-ROOT` | Implemented | Installed resources and mutable private data use separate explicit roots |
| `CAP-DESKTOP-FOUNDATION` | Foundation | Sidecar, encryption, locked Electron host, existing UI, offline UX, and lifecycle are implemented in source and Linux development packages; native target exit evidence remains |
| `CAP-DESKTOP-DIAGNOSTICS` | Planned | Bounded logs, health checks, incident correlation, and diagnostic export exist |
| `CAP-DESKTOP-DISTRIBUTION` | Planned | Per-platform CI produces signed/notarized installers and verified updates |
| `CAP-EXTENSION` | Implemented | The unpacked Chrome MV3 client is tested; installed-app discovery and store delivery remain bounded limitations |

## Contract For Smaller Models

This roadmap describes dependency order. It does not by itself authorize file
changes, dependency installation, network access, secret handling, or changes to
GitHub settings.

Assign exactly one ticket id per goal. Never assign a complete phase, a group of
adjacent tickets, or the entire roadmap to one autonomous run. A ticket is ready
to assign only when its prompt replaces every placeholder below and provides:

- one ticket id, one observable result, and the prerequisite evidence already
  satisfied;
- exact files allowed for inspection and a separate exact list of files or
  directories allowed for modification;
- the ticket's acceptance criteria and exact commands to run, including any
  command the ticket leaves to the task author;
- explicit non-goals and the required final-response format;
- explicit `yes` or `no` authorization for dependency changes, downloads or
  other network access, external GitHub settings, real private data, and secret
  handling;
- a stop condition for missing prerequisites, ambiguous ownership, or any
  architectural decision not fixed here.

If any required field is absent, still contains a placeholder, or conflicts with
the selected ticket, stop before editing and report that the assignment is not
ready.

Required reading for each ticket:

- `AGENTS.md`;
- `START_HERE.md`;
- `docs/process/engineering_rules.md`;
- `docs/process/workflow_rules.md`;
- `docs/process/small_model_playbook.md`;
- the global gates below;
- only the selected phase and its task-specific documents.

Execution rules:

1. Inspect `git status --short` before editing. Treat every pre-existing change,
   tracked or untracked, as user-owned. Do not overwrite, delete, stage, or
   "clean up" unrelated work. Stop if an allowed file already has overlapping
   changes whose ownership or intended result is unclear.
2. State a short plan naming only the allowed files and one pass/fail outcome.
3. Make the smallest reversible change that satisfies the selected ticket. Do
   not opportunistically refactor or begin the next ticket.
4. Run exactly the required checks. Do not silently substitute an easier check,
   claim an unrun check passed, or fix unrelated failures.
5. Review the final diff for scope, private data, generated files, absolute
   developer paths, and accidental dependency or lockfile changes.
6. Do not stage, commit, push, create a release, or change repository settings
   unless the prompt explicitly authorizes that exact action.

Hard stop conditions:

- a required change falls outside the allowed modification paths;
- a prerequisite has no recorded acceptance evidence;
- a new dependency, download, network call, external setting, or secret is
  needed but not explicitly authorized;
- a command would read, copy, overwrite, delete, or publish real private data
  beyond the ticket's explicit authorization;
- an acceptance criterion requires changing a fixed architecture decision or
  relaxing a global gate or performance budget;
- a required check is missing, unavailable, or fails for a reason outside the
  ticket, after collecting enough evidence to report it precisely.

Special handling:

- For `DATA-120`, implement and test with synthetic fixtures by default. Run an
  import against real ignored workspace data only after the user reviews the
  dry-run and explicitly confirms the exact source and destination in the
  current task. Never move or delete the source.
- For `REL-630`, use documented placeholders or disposable test keys in
  automation. Generating, backing up, or entering a production signing key is a
  human-controlled step and requires explicit authorization. Never print a
  private key or password in a prompt, command, log, or final response.

Copyable prompt:

```text
Read AGENTS.md, START_HERE.md, docs/process/engineering_rules.md,
docs/process/workflow_rules.md, docs/process/small_model_playbook.md, the Global Gates, and only
ticket <TICKET-ID> plus its phase context in docs/product/desktop_delivery_roadmap.md.

Goal: <one observable result for TICKET-ID>.
Prerequisite evidence: <exact registry rows, reports, or checks already met>.
Files allowed to inspect: <exact paths>.
Files allowed to modify: <exact paths; no implicit additions>.
Acceptance criteria: <copy the selected ticket's criteria>.
Required checks: <exact commands; do not leave this blank>.
Authorization: dependencies=<yes/no>; network/downloads=<yes/no>;
external settings=<yes/no>; real private data=<yes/no>; secrets=<yes/no>.
Out of scope: all other roadmap tickets, all product feature work, and <specific
non-goals>.
Stop and report before editing if a prerequisite is absent, an allowed file has
ambiguous overlapping work, a required field is missing, or completion requires
an unapproved path, dependency, external action, secret, destructive operation,
or architectural decision.
Do not stage, commit, push, release, or modify private ignored data unless that
exact action is explicitly authorized above.

Final response: status (completed or blocked), summary, files changed, commands
and exact results, acceptance evidence, notes/limitations, and no more than one
suggested next ticket. Never claim the capability or phase is complete unless
its exit evidence and registry update are both present.
```

## Global Gates

Every ticket must preserve all of the following:

- Existing browser mode remains supported until a later explicit retirement
  decision.
- CI, packaging, migration, and lifecycle tests use synthetic fixtures only.
  Real private data is read-only unless the selected ticket and current prompt
  explicitly authorize a specific operation.
- Private `career_vault`, `applications`, `jobs`, `outputs`, `resume_versions`,
  templates, and logs never enter Git or release artifacts.
- Business logic remains outside Electron main/preload code and HTTP route
  handlers.
- Every mutable write resolves below an explicit data root. Containment checks
  use normalized, canonical filesystem paths rather than string-prefix tests and
  reject `..`, sibling-prefix, symlink, junction, or reparse-point escapes.
- Packaged resources are read-only at runtime.
- The sidecar binds only to `127.0.0.1`, never `0.0.0.0`.
- Browser capability tokens remain sidecar-generated and memory-only. The local
  UI obtains its token through the existing `/api/session` contract; readiness
  output, process arguments, URLs, persistent storage, and diagnostics contain
  no token.
- Secrets and personal content never appear in logs, process arguments,
  environment dumps, build artifacts, or update metadata.
- A failed migration/update leaves a usable prior state or a documented,
  verified recovery backup. Compatibility and rollback claims must follow the
  approved app-version/data-format/schema policy.
- Dependencies require justification, lock/pin policy, license review, and a
  size/startup measurement when relevant.
- A capability becomes **Implemented** only after its registry row and automated
  or manual acceptance evidence are updated.

## Performance Budgets

Treat measurements as reproducible evidence, not informal impressions. Record
the operating system/architecture, CPU/RAM, power mode, Electron/Chromium and
Python versions, app version, commit, build type, fixture, measurement tool, and
exact start/stop boundary.

Use at least three recorded samples for an exploratory comparison. Use at least
ten recorded samples, after one untimed warm-up when appropriate, for a phase
exit or release gate. Preserve the raw samples and report the median/p50; report
p95 only when the sample count makes it meaningful.

A cold start begins with no app-owned process running. A warm start is the next
launch after a clean close on the same machine and build. These labels describe
application state, not a guarantee that the operating-system disk cache is cold.
Keep the fixture, machine, power mode, and measurement method unchanged when
comparing builds.

"Usable Job Library" means the main window is visible, sidecar readiness and
health have succeeded, the initial Job Library request/render has completed, no
loading state remains, and the UI accepts input. Prefer monotonic timestamps from
explicit lifecycle events; if the baseline uses a stopwatch, document who starts
and stops it.

For idle CPU, record the sampling tool/interval plus average and maximum over the
60-second window. A passing result has no recurring application-originated work
at a fixed polling cadence.

| Metric | Initial target | Boundary |
| --- | --- | --- |
| Cold start to usable Job Library | p50 <= 3 seconds | Installed release; sidecar not running |
| Warm start | p50 <= 2 seconds | Second launch after clean close |
| Idle CPU | no sustained polling; approximately 0% | Job Library open for 60 seconds |
| App-owned processes | one Electron main owner plus one Python sidecar | Report renderer/GPU/helper processes separately; re-baseline before setting a count budget |
| Duplicate sidecars | zero | Repeated desktop launch |
| Application log storage | <= 25 MB by default | Rotated operational logs |
| Background use after close | zero app-owned processes | Tray/autostart disabled |
| Offline core | fully usable | Network disabled after installation |

Do not relax a failed budget inside the same ticket. Record the cause and let a
separate review approve a new threshold or optimization.

---

## Phase 0: Governance And Baseline

Purpose: make later work measurable and keep capability names authoritative.
No runtime behavior is changed.

### `ROAD-000` Register Desktop Capabilities

Status: completed for the current foundation.

Goal: ensure every proposed desktop capability appears exactly once in the
acceptance registry with evidence-bounded status. Preserve the existing
`CAP-EXTENSION` implementation evidence while separating extension store/app
discovery work from the current unpacked client.

Allowed files:

- `docs/process/acceptance_registry.md`;
- `tests/test_acceptance_registry.py`;
- `README.md` and `START_HERE.md` only for links to this roadmap.

Acceptance:

- All proposed names appear exactly once in the registry.
- Registry states that roadmap numbers are dependency order only.
- `CAP-DESKTOP-FOUNDATION` remains **Foundation** until a packaged Electron host
  passes Phase 3; diagnostics and distribution remain **Planned**.
- Documentation contract tests reject duplicate capability ids and pass.

Checks:

```powershell
python -m unittest tests.test_acceptance_registry -v
git diff --check
```

### `DESK-000` Record The Current Baseline

Goal: create one content-minimized baseline using synthetic or empty data.

Record:

- operating system/architecture, CPU/RAM, and power mode;
- Python version for the browser baseline and Electron/Chromium version once a
  desktop host exists;
- current startup command and process layout;
- named synthetic or empty fixture and all raw timing/resource samples;
- cold/warm start time and idle CPU/memory using the global measurement protocol;
- relevant test and validator results;
- commit SHA, build type, measurement tool, and exact timing boundaries.

Allowed files:

- one report under `docs/history/benchmarks/`;
- one read-only measurement helper under `scripts/` only if manual measurement
  is not repeatable.

Acceptance:

- No private content or user-specific path is copied into the report.
- The report names its fixture and includes the global measurement metadata and
  raw samples.
- Browser-mode behavior is unchanged.
- Another agent can repeat the measurement without access to private data.

Checks:

```powershell
python -m unittest discover -s tests -v
python -m src.validate_vault
python -m src.validate_jobs
git status --short
```

### `DESK-010` Record Remaining Delivery Decisions

Status: implemented as the six evidence-bounded records under `docs/adr/`:
`DESK-010a` through `DESK-010f`. Deferred production choices remain owned by
their named evidence tickets; these records do not authorize Electron early.

Goal: create concise ADRs only for choices still left to measurement.

Required ADRs:

- PyInstaller `onedir` versus `onefile` decision method;
- app-data naming and `dev`/`beta`/`stable` separation;
- the single application-version source of truth;
- release-channel policy;
- supported-platform policy: Windows x64 and macOS arm64 are required future
  build targets; macOS x64 is a recorded decision gate before packaging;
- minimum OS versions derived from the supported pinned Electron major when
  `ELEC-300` begins, rather than a stale promise made before host work;
- app-version, data-home-format, and SQLite-schema compatibility and rollback
  policy, including how an older binary detects unsupported newer data.

Each ADR states status, context, decision or decision method, alternatives,
consequences, reversal conditions, and the ticket that will record evidence. The
compatibility ADR distinguishes binary rollback from restoring a pre-migration
backup and does not promise rollback across an irreversible migration. No ADR
may introduce cloud storage or a backend rewrite. Electron is already selected,
but an ADR cannot authorize scaffolding it ahead of the staged roadmap.

### Phase 0 Exit

- Every desktop capability appears exactly once in the registry with its
  evidence-bounded status.
- Baseline is reproducible.
- Remaining choices have measurable decision rules.

---

## Phase 1: Separate Resources From Private Data

Capability: `CAP-APP-DATA-ROOT`.

The repository root currently acts as both resource root and private workspace.
An installed application cannot write under its installation directory, so this
separation must precede packaging.

### Target Path Boundary

Proposed stable data root:

```text
%LOCALAPPDATA%\CareerHub\stable\
  applications\
  career_vault\
  jobs\
  outputs\
  resume_versions\
  templates\
  logs\
  backups\
```

Use separate roots for development/beta only if `DESK-010` approves them. Keep
existing relative private paths when that materially reduces migration risk.

Packaged read-only resources include:

- `src/intake_ui_static/`;
- `src/data/job_technologies.yaml`;
- `src/data/requirement_concepts.yaml`;
- version metadata;
- explicitly reviewed first-run bootstrap resources.

The user taxonomy overlay under `career_vault/taxonomy/` remains mutable private
data; it is not the shared packaged catalog.

### `DATA-100` Add An Explicit Runtime Path Contract

Goal: represent resource root and data root without a repository-wide rewrite.

Work:

1. Add one small immutable path/config object at the application boundary.
2. Preserve current CLI behavior by defaulting development data to the
   repository root during transition.
3. Split only call sites that currently confuse read-only `src/data` resources
   with private files below `project_root`.
4. Do not move real user files.

Acceptance:

- Tests can provide independent temporary resource/data roots.
- Existing repository-root commands still work.
- Core paths do not rediscover mutable storage from `Path.cwd()` after the
  configured boundary is entered.
- One shared containment rule protects mutable writes and does not use a raw
  string-prefix comparison.
- Tests reject `..` traversal, a sibling-prefix path such as `DataBackup` for
  root `Data`, and symlink/junction/reparse-point escape where the platform
  supports creating the fixture.
- No user-specific absolute path is committed or written to test output.

Required checks:

```powershell
python -m unittest discover -s tests -v
python -m src.validate_vault
python -m src.validate_jobs
git diff --check
```

### `DATA-110` Bootstrap A New Data Home

Goal: initialize an empty, writable app-data directory safely.

Work:

1. Create required directories idempotently.
2. Seed only application-owned defaults named in an allowlisted packaged
   bootstrap manifest.
3. Never overwrite or merge into an existing user file implicitly.
4. Store a data-home format marker/version.
5. Return stable errors for unwritable, partial, or incompatible homes.

Acceptance:

- First run creates a valid empty fixture home from only allowlisted defaults.
- Second run preserves the bytes and metadata of existing user files.
- Partial initialization resumes safely or fails without destructive cleanup.
- An unsupported newer data-home format fails before any mutable write.
- Tests cover empty, existing, read-only, partial, and incompatible homes.

### `DATA-120` Import Existing Development Data

Goal: explicitly copy the current ignored workspace into the new data root.

Work:

1. Dry-run and report the exact source/destination, conflicts, file count, size,
   and a content-minimized plan identifier.
2. Require explicit confirmation of that plan outside tests; reject confirmation
   if the source or destination has materially changed since the dry-run.
3. Copy into staging; never move, modify, or delete workspace originals.
4. Validate the staged Vault, jobs, SQLite, and referenced artifacts.
5. Apply only a validated staged import across a documented rollback boundary.
6. Write a content-minimized import receipt with no JD, resume, Vault fact, token,
   or user-specific absolute path.

Acceptance:

- Tests use synthetic fixtures; no test or CI path reads the real ignored
  workspace.
- Existing destination files are not overwritten silently.
- Failure leaves originals and the pre-import destination unchanged/recoverable.
- A stale or mismatched confirmation cannot apply a different import plan.
- A repeated completed import is idempotent or reports a clear conflict.
- `git status --short --untracked-files=all` shows no private data staged or
  newly trackable.

### `DATA-130` Backup, Restore, And Migration Safety

Status: implemented in source with bounded archives, preflight validation,
durable restore/document-migration journals, explicit idempotent recovery, and
synthetic power-loss fixtures. Target-host filesystem interruption rehearsal
remains part of installer/release acceptance.

Goal: make binary updates independent from data safety.

Work:

1. Define a versioned export manifest containing relative paths, sizes,
   checksums, data-home format, and SQLite schema version.
2. Export selected data to a ZIP without recursively including the destination,
   staging area, or prior backup archives.
3. Validate every archive entry's normalized destination and file type before
   extraction; reject absolute, drive-qualified, UNC, traversal, link, and
   Windows case-collision entries.
4. Extract and validate checksums, Vault, jobs, SQLite, and referenced artifacts
   fully in staging before replacement.
5. Back up SQLite before schema migration and name the backup in recovery state.
6. Enforce the compatibility policy from `DESK-010`; refuse a data-home format
   or database schema newer than the app supports before mutable work begins.
7. Replace active data only across a documented recoverable boundary.
8. Bound backup retention without deleting the only known-good recovery copy.

Acceptance:

- Fixture export/import round trip is lossless and manifest checksums match.
- Corrupt ZIP, duplicate/colliding destinations, unsupported entry types, and
  every listed path-escape form are rejected before active data changes.
- Failed restore leaves active data unchanged.
- Migration interruption has a tested recovery path naming the backup used.
- Newer unsupported data-home and SQLite versions fail closed without mutation.

### Phase 1 Exit

- All mutable writes can target a temporary data root.
- Clean and imported homes validate.
- Browser mode still passes its existing checks.
- Registry evidence marks `CAP-APP-DATA-ROOT` Implemented.

---

## Phase 2: Python Sidecar, Platform Readiness, And Packaging

Capability contribution: foundation for `CAP-DESKTOP-FOUNDATION`.

### `SIDE-200` Define A Machine-Readable Sidecar Protocol

Status: implemented as a wrapper-neutral foundation. This status does not
complete Phase 2 or make `CAP-DESKTOP-FOUNDATION` Implemented.

Goal: make desktop startup deterministic.

Required contract:

- `--sidecar` mode reads exactly one 8 KiB-bounded, versioned startup record from
  the inherited stdin pipe within 10 seconds;
- the startup record supplies port `0` and explicit resource/data roots without
  exposing user-specific paths in process arguments;
- bind only to `127.0.0.1` and report the OS-assigned port;
- reserve stdout for size-bounded protocol records and emit exactly one
  readiness record only after path, resource, schema, and listener validation;
- send sanitized free-form operational output to stderr, never stdout;
- expose a content-minimized health endpoint;
- keep the existing sidecar-owned, in-memory `/api/session` token bootstrap;
- accept a versioned graceful-shutdown record on the same owned stdin pipe and
  stop when the pipe closes because the parent exited;
- exit non-zero on startup or protocol failure;
- never open the system browser in desktop mode.

Example records, with placeholders rather than real paths:

```json
{"command":"start","protocol_version":1,"port":0,"resource_root":"<resource-root>","data_root":"<data-root>","channel":"stable"}
{"app_version":"0.1.0","channel":"stable","event":"ready","port":49152,"protocol_version":1}
{"command":"shutdown","protocol_version":1}
```

The exact implemented contract, exit categories, and explicit limitations live
in [`sidecar_protocol.md`](../reference/sidecar_protocol.md). The repository-root `VERSION`
file is now the single application-version source and is embedded in packaged
sidecars.

No startup, readiness, shutdown, health, or log record contains a capability
token. Startup configuration containing paths is never echoed or logged.

Acceptance:

- Test sends a valid startup record, receives one readiness record on an
  OS-assigned port, and completes a health request.
- Missing, oversized, malformed, duplicate, or unsupported protocol records;
  invalid roots; schema failure; timeout; and early exit are tested.
- Stderr noise cannot be parsed as readiness, and stdout contains no free-form
  logs before or after readiness.
- Graceful shutdown and simulated parent-pipe closure leave no sidecar process.
- Health and every protocol channel reveal no private content, token, or
  filesystem path.
- Existing browser-mode token, Origin, CSP, and pagination tests still pass.

### `PLAT-230` Add Windows/macOS Local-Data Parity

Status: implemented in source for wrapper-neutral platform defaults, portable
path collisions, platform-derived UI claims, deterministic PDF fonts, POSIX
privacy modes, and macOS zero-entry extended ACL enforcement. Native macOS ACL
acceptance remains a target-host release gate under `SEC-240`.

Goal: keep explicit host-provided paths while removing Windows-only defaults and
privacy assumptions from shared Python runtime code.

Work:

1. Preserve `RuntimePaths` as the wrapper-neutral boundary; the desktop host
   supplies installed resource/data roots explicitly.
2. Add macOS browser/development defaults only where a direct Python launcher
   still needs them.
3. Enforce private directory ownership and mode `0700` on macOS/POSIX while
   preserving the reviewed Windows ACL behavior.
4. Test symlink/reparse escapes, permissions, spaces/non-ASCII paths, case
   behavior, and Unicode-normalization collisions with synthetic fixtures.
5. Replace Windows-specific UI claims with platform-derived wording.
6. Select and license a deterministic bundled font before claiming equivalent
   document output across platforms.

This ticket does not add cloud sync, platform scheduling, or an Electron host.

### `SEC-240` Design Platform Secret And Document Protection

Status: architecture and source implementation complete in
`docs/adr/SEC-240_platform_secret_and_document_protection.md`, with synthetic
migration/recovery fixtures and independent security review. Real Credential
Manager, Keychain, DPAPI migration, and macOS ACL evidence remain target-host
gates before existing ciphertext changes outside disposable fixtures.

Goal: make macOS support possible without weakening existing Windows recovery.

Work:

1. Keep the existing Python secret-store interface and add a reviewed macOS
   Keychain backend so browser mode and Python email sync retain access.
2. Define a versioned authenticated-encryption envelope for private documents,
   with a master key protected by Windows Credential Manager/DPAPI or macOS
   Keychain.
3. Specify explicit migration, backup, rollback, and recovery behavior for
   existing DPAPI-bound document blobs. Never silently rewrite or strand them.
4. Treat portable/cross-device encrypted data and key recovery as a separate
   synchronization design.

Acceptance requires an independent security review and synthetic migration
fixtures before any existing ciphertext is changed.

### `SIDE-210` Create A Reproducible PyInstaller Build

Status: implemented and protocol-smoked on exploratory Linux x86_64 with an
exact hashed build lock, explicit resource allowlist, byte-verified resources,
relative SHA-256 manifests, and source/frozen version parity. Clean Windows x64
and macOS arm64 builds remain Phase 2 exit evidence.

Goal: run the Python application without Python installed on the target.

Work:

1. Add a reviewed spec and per-platform build entry points. PyInstaller builds
   run on the target operating system; do not treat it as a cross-compiler.
2. Include UI/static catalogs explicitly.
3. Exclude private ignored paths and development files explicitly.
4. Pin build tooling separately from runtime requirements.
5. Produce a file manifest and SHA-256 checksum.

Planned files:

```text
packaging/pyinstaller/
packaging/scripts/build_sidecar.ps1
packaging/scripts/build_sidecar.sh
```

Acceptance:

- Clean checkout + synthetic data builds successfully.
- Executable starts without the project virtualenv active.
- Manifest has no private file or absolute developer path.
- A missing packaged resource fails before release.

### `SIDE-220` Select `onedir` Or `onefile`

Status: `onedir` selected as the sole Electron integration layout under the ADR
default rule. The exploratory Linux comparison found a greater than five-second
`onefile` startup penalty plus temporary extraction. Full target-host
confirmation remains required before release.

Measure both candidates for:

- clean build time;
- artifact/install size;
- cold/warm readiness;
- antivirus/temp-extraction behavior;
- Electron `extraResource` layout and spawn behavior;
- traceback/diagnostic quality.

Default rule: choose `onedir` unless `onefile` has no material startup penalty
and meaningfully improves reliable installation. Record one production choice;
do not maintain both indefinitely.

### Phase 2 Exit

- Packaged sidecar passes protocol smoke tests.
- Build contents are auditable and private-data-free.
- Packaging choice is measurement-backed.
- Required Windows/macOS data, secret, and document-protection fixtures pass.
- The phase remains wrapper-neutral; Electron product logic belongs only to
  Phase 3.

---

## Phase 3: Electron Desktop Foundation

Capability: `CAP-DESKTOP-FOUNDATION`.

Do not begin this phase until current email, Job Feed, and extension work has a
separate handoff and the user explicitly authorizes desktop-host construction.
Complete the tickets separately.

Historical ticket ids `TAURI-300` through `TAURI-350` are superseded by the
`ELEC-*` tickets below. They produced no runtime files, so there is no Tauri code
or data format to migrate or delete.

### Required Future Build Targets

| Target | Foundation decision |
| --- | --- |
| Windows 10+ x64 | Required; Windows SDK `10.0.26100.8876` baseline |
| macOS 13+ arm64 | Required; Xcode `26.6`, macOS SDK `26.5`, deployment target `13.0` |
| macOS x64 | Decide before packaging from user/device evidence and dependency build support |
| Linux, mobile, Safari extension | Out of this desktop-host phase |

Electron `44.0.0`, its Chromium `152.0.7977.54` runtime, build Node `24.19.0`,
and Forge `7.11.2` are exact foundation pins. Revalidate the primary platform
sources and all native dependencies before each release candidate. A support
matrix change requires its own review; details live in
[`desktop_release_gates.md`](../reference/desktop_release_gates.md).

### Planned Layout

```text
desktop/
  package.json
  package-lock.json
  forge.config.js
  ui/loading.html
  src/
    main.js
    app_paths.js
    sidecar.js
    navigation.js
    lifecycle.js
```

Keep this package separate from the root maintainer-only browser-smoke package.
Installed users do not need Node or Python.

### `ELEC-300` Scaffold A Minimal Locked-Down Host

Status: implemented in source and Linux development packaging; native target
package acceptance remains pending.

Goal: open one stock Electron window before connecting Python.

Work:

1. Record the pinned supported Electron, Node, Forge, platform SDK, architecture,
   and minimum-OS matrix.
2. Scaffold only below `desktop/`; do not migrate the existing frontend.
3. Generate and review one desktop lockfile.
4. Use a minimal local loading page.
5. Set `nodeIntegration: false`, `contextIsolation: true`, `sandbox: true`,
   `webviewTag: false`, and `webSecurity: true`.
6. Configure reviewed Electron fuses to disable RunAsNode, `NODE_OPTIONS`, and
   command-line debugging in release builds.

Acceptance:

- The development command opens one placeholder window on a supported platform.
- Static/type/lint checks selected by the ticket pass.
- No Python process starts.
- No renderer API can spawn a process, read arbitrary files, or invoke a generic
  main-process command.
- No React or application UI rewrite is introduced.

### `ELEC-310` Resolve Platform Paths

Status: implemented with channel-specific identities, normalized path checks,
and a reviewed out-of-ASAR sidecar layout; target filesystem acceptance remains.

Goal: provide explicit resource/data roots to the sidecar.

Work:

1. Resolve packaged resources from `process.resourcesPath` and mutable data from
   reviewed Electron/OS APIs, not hardcoded environment strings.
2. Keep the executable and its support files outside `app.asar` using a reviewed
   Forge `extraResource` layout.
3. Apply the channel policy from `DESK-010`.
4. Validate resources and data-root writability and show a sanitized startup
   error on failure.

Acceptance:

- Spaces and non-ASCII paths work on Windows and macOS fixtures.
- Release never writes below installation/resource directories.
- Tests never use a developer's real application data.

### `ELEC-320` Spawn And Supervise One Sidecar

Status: implemented with an exact retained child, bounded protocol/health
checks, graceful shutdown, fail-closed exact-child termination, and explicit
retry after confirmed ownership release.

Goal: make Electron main the sole owner of the packaged Python process.

Work:

1. Spawn only the resolved bundled executable through `child_process.spawn`
   with `shell: false`, `detached: false`, hidden console on Windows, piped
   stdio, and a minimal explicit environment.
2. Expose no renderer IPC that accepts an executable, argument list, shell
   command, environment map, or arbitrary spawn request.
3. Send the `SIDE-200` start record through owned stdin; do not place roots or
   tokens in arguments or environment variables.
4. Parse only bounded stdout protocol records with a bounded timeout; drain and
   sanitize stderr independently.
5. Retain the exact child handle/stdin until that child emits `exit`; a bounded
   termination timeout remains owned in `stopping` and cannot be replaced by a
   retry.
6. Show retryable error UI instead of a blank window.

Acceptance:

- Success creates one Electron owner and one sidecar.
- Startup timeout, early exit, oversized/malformed readiness, protocol mismatch,
  and stop timeout have distinct stable error codes. A stop timeout cannot lose
  ownership or permit a second child; confirmed exit is required before retry.
- Navigation and retry cannot launch a second sidecar while one is owned.
- Process inspection shows no resource root, data root, or token in arguments.
- A sentinel unrelated environment secret is absent inside the sidecar.

### `ELEC-330` Open The Existing UI Safely

Status: implemented with a narrow custom loading protocol, exact loopback
origin, default-deny navigation/permissions, controlled document previews, and
`DESK-020` offline UX. Native target rendered acceptance remains pending.

Goal: navigate from the loading page to the owned loopback server.

Work:

1. Construct exactly `http://127.0.0.1:<validated-port>` from the owned child's
   readiness port after health succeeds. Never accept a URL, hostname, redirect,
   or origin from sidecar output.
2. Preserve `/api/session`, Origin, CSP, and pagination behavior.
3. Restrict navigation and `setWindowOpenHandler` for every created
   `webContents`; deny permissions by default.
4. Open only validated user-initiated HTTP(S) posting URLs with the OS browser.
5. Preserve controlled document previews and downloads without granting a
   general popup or filesystem capability.
6. Implement `DESK-020`: expose one accessible offline indicator from the
   browser/Chromium network state, keep local workflows enabled, disable only
   known network actions, and never add a reachability beacon or automatic
   retry.

Acceptance:

- Existing product workflow smoke checks pass in the pinned Electron renderer.
- The browser token is absent from readiness, URLs, storage, and screenshots.
- Untrusted links, redirects, popup attempts, credentials, and non-HTTP(S)
  schemes cannot replace the application page or invoke a local handler.
- Production DevTools are disabled unless an explicit diagnostic mode allows
  them.
- A network cut shows **Offline — local features remain available**, preserves
  local state, and distinguishes a sidecar failure from internet unavailability.
- A returning interface says **Connection available** without claiming that a
  provider is reachable; failed operations retry only after explicit user
  action.

### `ELEC-340` Implement Lifecycle And Single Instance

Status: implemented in source with pure controller/supervisor tests and Linux
package smoke. The accepted native behavior is recorded in
[`ELEC-340_lifecycle_and_process_ownership.md`](../adr/ELEC-340_lifecycle_and_process_ownership.md).

Goal: avoid orphan processes and duplicate SQLite writers on Windows and macOS.

Work:

1. Acquire `app.requestSingleInstanceLock()` before spawning a sidecar or opening
   product state.
2. Map each installed channel's application identity to one canonical data root.
3. Treat second-launch arguments and working directory as untrusted; only focus
   and restore the existing window.
4. Normal close sends the versioned shutdown record, waits a short bound, then
   terminates and reaps only the retained child when required. If exact-child
   termination exceeds the bound, keep Electron and the handle alive, report a
   sanitized error, and fail closed until confirmed exit.
5. Handle macOS close/activate behavior explicitly without starting a duplicate
   child.
6. Sidecar crash produces a recoverable state; retry waits for confirmed exit.
7. Keep tray and autostart disabled.

Acceptance:

- Clean close leaves no Career Hub sidecar.
- Simultaneous launches create one window owner, sidecar, and SQLite writer.
- A second launch cannot inject a path, URL, or command.
- Distinct channels can coexist only with distinct identities and data roots.
- No process-name kill can terminate unrelated Python programs.
- Forced host close, sidecar crash, retry, macOS window reactivation, and
  restart-after-crash are tested without orphans. Synthetic non-termination
  proves that ownership is retained and duplicate restart is refused.

### `ELEC-350` Add Desktop Lifecycle Smoke Tests

Status: controller, supervisor, renderer-offline, and Linux package paths are
automated in
`elec350_linux_lifecycle_smoke.md`;
the Windows x64 and macOS arm64 matrix below remains required.

Run the applicable matrix on Windows x64 and macOS arm64, plus macOS x64 if that
target is approved:

- first launch with an empty synthetic home;
- simultaneous launch race and later focus-only second launch;
- startup failure, timeout, oversized output, and malformed readiness;
- ordinary close, forced host close, and parent-pipe closure;
- sidecar crash followed by one controlled retry;
- data persistence across restart;
- offline startup;
- cutoff during Gmail sync and public-JD resolution, followed by explicit
  recovery without automatic replay;
- accessible offline/connection-available transitions while local editing,
  export, backup, documents, and manual `.eml` import remain usable;
- paths with spaces, non-ASCII, and macOS Unicode-normalization collisions.

Keep visual workflow checks in `docs/guides/manual_testing.md`; automate lifecycle
checks wherever feasible.

### Phase 3 Exit

Current result: **not exited**. Source implementation and Linux development
evidence are complete, but the required native Windows/macOS rows below have
not run on this Linux host.

- Release-like Windows and macOS builds start without a terminal or installed
  Python.
- Existing product smoke checks pass in the pinned Electron renderer.
- Phase 0 performance/process measurements are repeated per target.
- Close leaves no app-owned process.
- Registry evidence marks `CAP-DESKTOP-FOUNDATION` Implemented.

---

## Phase 4: Diagnostics And Supportability

Capability: `CAP-DESKTOP-DIAGNOSTICS`.

### Structured Event Contract

Include these fields when relevant:

```text
timestamp_utc, level, component, event, incident_id, correlation_id,
app_version, commit_sha, schema_version, duration_ms, error_code,
exception_type
```

Never log raw JD/resume content, Career Vault facts, contacts, tokens, request
bodies, full paths, or environment dumps.

### `DIAG-400` Add Bounded Python Logs

Goal: unify operational logs without replacing valid intake-event contracts.

Work:

1. Use Python `logging` with stable JSONL formatting.
2. Use `RotatingFileHandler`; keep default aggregate size <= 25 MB.
3. Capture uncaught exceptions and API-boundary failures.
4. Return stable error and incident ids.
5. Test redaction.

Acceptance:

- Rotation is tested using a small test limit.
- Exceptions remain valid structured records or linked sanitized artifacts.
- An unwritable log directory produces one visible error, not a crash loop.

### `DIAG-410` Correlate Electron, Python, And Frontend Failures

Work:

1. Use a bounded Electron-main logger for lifecycle/update events; do not add a
   remote transport.
2. Map sidecar stdout/stderr to component-aware events.
3. Capture sanitized frontend `error` and `unhandledrejection`.
4. Propagate one correlation id through local requests.

Acceptance:

- A controlled request failure is traceable across layers.
- App version/commit SHA agree everywhere.
- Logging failure cannot recursively flood logs.

### `DIAG-420` Add Health And Diagnostic Export

Health checks:

- sidecar protocol/version;
- writable data root;
- packaged resource manifest;
- bounded SQLite integrity/schema compatibility;
- required directories;
- updater configuration;
- latest incident ids.

Default diagnostic ZIP:

- bounded recent logs;
- app/commit/OS/Electron/Chromium versions;
- schema/migration status;
- health results;
- packaging manifest/checksums.

Acceptance:

- ZIP excludes database, JD, Vault, resumes, templates, tokens, and full paths.
- A privacy fixture proves representative secrets/PII are absent.
- Export works offline.

### Phase 4 Exit

- Controlled errors produce stable incident ids.
- Privacy tests cover logs and diagnostic ZIP.
- An agent can identify failing layer/release from the bundle.
- Registry evidence marks `CAP-DESKTOP-DIAGNOSTICS` Implemented.

---

## Phase 5: Per-Platform Installers And Local Upgrade Rehearsal

Capability contribution: foundation for `CAP-DESKTOP-DISTRIBUTION`.

### `DIST-500` Build One Installer Per Required Platform

Goal: install, launch, repair/reinstall, and uninstall a release build.

Work:

1. Build the selected sidecar format first.
2. Build each sidecar on its target operating system and copy it outside ASAR
   through the reviewed Forge resource layout.
3. Bundle every required sidecar file/resource and reject unexpected files.
4. Support one installer format per platform first; do not add parallel makers
   without a distribution need.
5. Keep user data outside the install directory.

Acceptance:

- Clean install works offline without Python or Node installed.
- Repair/reinstall preserves user data.
- Uninstall never silently deletes private data.
- Installed files match the build manifest.

### `DIST-510` Rehearse Migration And Recovery

Before testing, record an explicit matrix of app version, sidecar protocol,
data-home format, SQLite schema, supported input range, and expected result.

Test:

- old app + old data;
- new app + automatic verified pre-migration backup;
- interrupted migration at each durable boundary;
- old app opening newer compatible and incompatible data;
- same-version reinstall;
- supported binary rollback without migration;
- recovery of the named pre-migration backup before an older binary is used when
  direct rollback is incompatible.

Acceptance:

- Forward migration is transactional or restores the verified named backup.
- Every case matches the compatibility policy approved in `DESK-010`.
- An old app refuses incompatible newer data before any mutable write.
- Direct binary rollback is claimed only for compatible formats/schemas;
  otherwise the documented workflow restores the named backup first.
- Recovery requires no manual SQL and never instructs deletion of the active data
  root.

### `DIST-520` Establish Release Identity

Every artifact/runtime must expose:

- SemVer app version;
- commit SHA;
- sidecar protocol version;
- data-home format version;
- current SQLite schema version and supported schema range;
- release channel;
- build type (`dev`, `beta`, or signed `stable`).

Acceptance:

- Validation fails when version or compatibility declarations drift across
  Python, Electron, installer, update metadata, and diagnostics.
- Dev builds are visibly distinct from beta and signed stable builds.
- Runtime health output is sufficient to select the expected compatibility
  matrix row without exposing a private path.
- The same immutable tag cannot be silently rebuilt/replaced.

### Phase 5 Exit

- Windows and required macOS installers pass clean-machine and upgrade
  rehearsal on their target operating systems.
- Data survives install, upgrade, repair, and uninstall per policy.
- Automatic updates remain disabled until signing is complete.

---

## Phase 6: GitHub CI, Releases, And Signed Updates

Capability: `CAP-DESKTOP-DISTRIBUTION`.

The application never runs `git pull`. GitHub delivers built artifacts:

```text
codex/<one-ticket-branch>
  -> pull request
  -> required CI
  -> reviewed merge to main
  -> annotated tag v0.x.y[-beta.n]
  -> GitHub Actions clean Windows and macOS builds
  -> draft GitHub Release
  -> manual smoke approval
  -> published release/update metadata
```

### `CI-600` Add Pull-Request CI

Minimum jobs:

1. Python tests/validators with synthetic data.
2. Existing justified frontend checks.
3. Electron-main security/static tests and the locked desktop package checks.
4. Sidecar packaging smoke tests on Windows and each required macOS
   architecture.
5. Documentation/capability contract.
6. Private-path and release-manifest audit.

Acceptance:

- Default workflow permissions are read-only.
- Fork PRs cannot access signing/telemetry secrets.
- Caches contain no private workspace data.
- Every job has a timeout and useful failure output.
- Release workflow cannot silently bypass required tests.

### `CI-610` Make Release Builds Auditable

Work:

- keep reviewed ecosystem lock inputs under version control;
- pin toolchains/actions by documented policy;
- print toolchain versions;
- hash every published artifact;
- add GitHub artifact attestation when approved.

Acceptance:

- Manifest names every file/SHA-256.
- Build never reads ignored user data.
- Provenance identifies repository, workflow, commit, and tag.

### `REL-620` Build A Draft Release From A Tag

Work:

1. Trigger from the approved tag pattern or explicit manual test.
2. Build the sidecar, Electron bundle, installer, update artifact, and manifest
   on clean target-platform runners. Never cross-build the Python sidecar and
   label it as another operating system.
3. Attach to a draft GitHub Release.
4. Include release notes/checksums.
5. Require smoke approval before publish.

Acceptance:

- Tag and embedded app versions match.
- Draft installer passes offline smoke test.
- No private data/signing secret appears in the release.
- Failure leaves no partial stable release.

### `REL-630` Configure Platform And Update Trust

Work:

1. Have humans provision Windows Authenticode and Apple Developer ID/notarization
   credentials outside Git and agent-visible logs.
2. Expose production credentials only to protected target-platform release jobs;
   pull requests and ordinary CI use unsigned or disposable fixtures.
3. Verify Windows signatures and macOS signing, hardened runtime, and
   notarization before a draft can be promoted.
4. Use immutable HTTPS update artifacts and the signature/verification mechanism
   required by the selected Electron updater and platform.
5. Create and human-verify credential backup/renewal, rotation, revocation, and
   compromise runbooks without recording private material.
6. Test unsigned, invalidly signed, tampered, wrong-channel, and wrong-target
   artifacts.

Acceptance:

- Signing identities, private keys, passwords, and notarization credentials are
  absent from Git history, pull-request jobs, logs, caches, artifacts,
  diagnostics, and agent output.
- Unsigned, wrongly signed, unnotarized where required, and tampered releases are
  rejected.
- A human records backup verification without recording secret material.
- Loss-of-key and compromise consequences/recovery are explicit.

Update-feed verification does not replace platform signing. Public Windows
builds require Authenticode; public macOS builds require Developer ID signing and
notarization.

### `UPD-640` Enable User-Controlled Updates

Initial UX:

- check over HTTPS after the app becomes usable, not on critical startup;
- validate channel, target/architecture, SemVer, signature metadata, and declared
  data compatibility before offering the update;
- display version/release notes;
- let user choose download/install;
- run the approved migration/backup preflight and refuse installation during a
  migration or mutable operation;
- treat offline/no-update as normal;
- do not enable downgrades or force updates; recover by a fixed-forward release
  or the approved backup/installer runbook.

Acceptance:

- Version N installs signed N+1 for the correct channel, target, architecture,
  and compatibility row.
- Wrong-channel, wrong-target, downgrade, corrupt, unsigned, and invalidly signed
  candidates are refused and leave N usable.
- Binary replacement does not modify private data.
- Migration preflight or post-install startup failure follows the verified
  recovery policy and names the usable backup or fixed-forward action.
- Offline, timeout, and no-update results are non-fatal; actionable failures
  create a stable local incident id.

### `REL-650` Publish And Roll Back Runbook

Document:

- promoting a draft;
- verifying installer/updater endpoints;
- halting a bad rollout;
- publishing a fixed forward version;
- prohibition on replacing an existing tag;
- SmartScreen and macOS Gatekeeper expectations;
- recovery installer/schema compatibility.

Acceptance:

- Beta-to-beta update rehearsal passes before stable.
- Prior installer remains available when schema permits recovery.
- Rollback never tells users to delete their data root.

### Phase 6 Exit

- Required CI passes on the release commit.
- Draft is smoke-tested.
- Signed N to N+1 update and failure cases pass.
- Release/rollback runbook is complete.
- Registry evidence marks `CAP-DESKTOP-DISTRIBUTION` Implemented.

---

## Phase 7: Browser Extension Capture

Capability: `CAP-EXTENSION`.

The detailed capture, Discovery Inbox, and email-alert plan lives in
[`job_discovery_capture_plan.md`](job_discovery_capture_plan.md). An unpacked
Chrome developer extension is already dogfooded against the local browser
server with the shared discovery domain and capture-only token scope. Store
packaging, authenticated dynamic-port app discovery, and release compatibility
remain gated on stable desktop lifecycle. The extension is a capture client,
not a second implementation of JD intake. A Safari Web Extension would be a
separate Xcode/signing/store project and is not provided by Electron.

### `EXT-700` Define Capture And Permission Budget

Status: implemented for the current unpacked extension.

Initial actions and fields:

- save current link, selected text, or visible job-page text after explicit user
  action;
- explicit destination: Add to Job Feed, Save job, or Record as applied;
- current page URL;
- optional structured company/title/location/posting id from the visible page;
- capture kind, extractor version, and one request id.

Acceptance:

- No background crawling, bulk scraping, auto-apply, credential access, or
  unrelated page modification.
- Required/optional permissions and stored fields are documented.
- Unsupported pages fall back to selection/paste.

### `EXT-710` Build Minimal Manifest V3 Capture

Status: implemented for the current unpacked extension.

Work:

1. Wake service worker on explicit action.
2. Request smallest loopback/active-page permissions.
3. Obtain the in-memory extension-only token for the exact configured origin.
4. Send one bounded idempotent payload to the capture endpoint.
5. Show Saved, Already captured, Needs job description, App not running, and
   actionable errors.

Acceptance:

- Capture enriches a URL-identical discovery or follows one explicit
  Feed/Saved/Applied destination through server-owned intake logic.
- A usable page capture may receive the existing deterministic advisory preview;
  link-only and partial captures do not claim absent requirements as gaps.
- Link-only/partial captures accept Feed only. Saved/Applied reuse Add job intake
  and create no second source/card.
- Extension invocation or closure never implies Applied, Skipped, or Dismissed.
- Token is not persisted/logged/embedded.
- Extension token cannot access unrelated local APIs.
- No cloud service is used.

### `EXT-720` Handle App Discovery Without A Daemon

Status: planned. The current extension uses the fixed browser-development port;
the sidecar foundation intentionally selects an OS-assigned port.

Order of preference:

1. Tell user to open Career Hub and retry.
2. Threat-model authenticated pairing/discovery; do not scan loopback ports or
   trust any process merely because it owns a familiar port.
3. Evaluate Chrome Native Messaging or `careerhub://` activation only with an
   explicit gesture, narrow message contract, and app-authentication design.

Do not add tray/autostart merely to hide "app not running."

### `EXT-730` Package And Release Extension

Status: planned.

Acceptance:

- Source and built ZIP are auditable/versioned.
- Store credentials remain outside Git.
- Desktop/extension protocol compatibility is declared.
- Incompatible sidecar produces a useful refusal.

### Phase 7 Exit

- One-click capture works while desktop is running.
- Permission/privacy/idempotency/compatibility tests pass.
- Extension failure cannot bypass/corrupt Discovery Inbox or JD intake.
- No discovery affects ledger analytics before explicit promotion.
- The existing `CAP-EXTENSION` implementation remains accurate; store delivery
  and installed-app discovery are recorded limitations until their separate
  evidence exists.

---

## Phase 8: Optional Remote Error Aggregation

Capability impact: optional extension of `CAP-DESKTOP-DIAGNOSTICS`.

### `OBS-800` Decide From Evidence

Evaluate active installs, unreproducible-error frequency, diagnostic-bundle
support cost, privacy risk, current free limits/retention, and account needs.
"Do not add Sentry yet" is a valid result.

### `OBS-810` Add Opt-In Sentry Only If Approved

Required controls:

- disabled by default with visible opt-in/out;
- allowlisted fields and `before_send` scrubbing;
- no session replay initially;
- no JD, resume, Vault, token, URL query, or path content;
- release/commit identity;
- documented sampling/quota;
- no runtime dependency on service availability.

Acceptance:

- Automated privacy fixtures are removed before transmission.
- Offline/service outage is silent and non-blocking.
- User can inspect the categories sent.
- Retention/deletion procedure is documented.

### Phase 8 Exit

- Either a documented local-only decision exists or opt-in telemetry passes
  privacy/outage tests.
- No self-hosted monitoring stack is introduced without a separate need.

---

## Phase 9: Installed Beta And Stable Readiness

No new product features are authorized here.

### `GA-900` Dogfood The Installed Beta

Record with sanitized incident ids:

- startup/update reliability;
- Add job and Save as applied latency;
- duplicate handling;
- ledger/resume persistence through update;
- idle resources/orphan processes;
- diagnostic usefulness;
- extension friction if included.

### `GA-910` Stable Release Gate

Required evidence:

- capability registry/docs match behavior;
- full automated checks pass;
- clean install, update, recovery, offline checks pass;
- performance budgets are measured;
- privacy audit passes;
- limitations/support workflow exist;
- platform-signing credential recovery/renewal ownership is verified;
- release/rollback ownership is known.

Stable outcome:

- A non-developer can install, launch, update, diagnose, and uninstall without
  repository knowledge.
- Private data remains local and survives supported updates.
- Normal use requires no terminal, dev server, Python, Node, AWS, or
  Firebase account.

## Explicitly Deferred

This roadmap does not authorize:

- mobile applications;
- cloud sync or user accounts;
- AWS/Firebase backend;
- scraping/job watching or auto-apply;
- AI API calls;
- resume generation/rendering;
- frontend framework migration;
- Safari Web Extension packaging;
- always-running tray/autostart agent;
- self-hosted logging infrastructure;
- Linux desktop packaging.

Each needs a separate capability, product justification, privacy review, and
acceptance contract.

## Recommended Goal Sequence

`ROAD-000`, the data-root foundation, and `SIDE-200` now have evidence. Stop
desktop work here while the current email, Job Feed, and extension slices are
being completed. Their completion does not automatically authorize the next
desktop ticket.

After an explicit product-work handoff, assign only one goal at a time:

1. `DESK-000`
2. `DESK-010`
3. `PLAT-230`
4. `SEC-240`
5. `SIDE-210`
6. `SIDE-220`
7. `ELEC-300`
8. `ELEC-310`
9. `ELEC-320`
10. `ELEC-330`
11. `ELEC-340`
12. `ELEC-350`

Review Phase 2 security/packaging evidence before scaffolding Electron. Review
the Phase 3 exit before assigning diagnostics or distribution. Current unpacked
extension product work may continue independently; installed-app discovery and
store delivery remain gated on the desktop lifecycle.
