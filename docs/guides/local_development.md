# Local development

Career Hub uses a Go backend and Electron desktop host. The currently qualified
desktop platform is Windows x64. Node 24.19.0, Electron 44.7.0 and Go 1.27.3 are
pinned; backend dependencies are recorded in `backend/go.mod` and `go.sum`.

## Setup and start

```powershell
npm ci --ignore-scripts
npm --prefix desktop ci
node desktop/node_modules/electron/install.js
npm --prefix desktop run build:go-sidecar
.\scripts\start_career_hub.ps1
```

Electron 42+ downloads its binary when its CLI first runs. The explicit
installer above also prepares tests that launch `electron.exe` directly; it
uses the locked Electron package's bundled checksums. See the
[Electron installation change](https://www.electronjs.org/docs/latest/breaking-changes#behavior-changed-electron-no-longer-downloads-itself-via-postinstall-script).

Use the pinned Node on PATH. If Go is elsewhere, set `CAREER_HUB_GO_EXECUTABLE`
to its absolute path. The builder normally requires a clean checkout. For
local development only, `node desktop/scripts/build-go-sidecar.cjs --allow-dirty`
creates an explicitly marked artifact; installer preparation refuses it.

The launcher opens the desktop; it does not start a Python browser server.
Close the window to stop the owned backend. Ordinary development uses
`%LOCALAPPDATA%\CareerHub\dev`, with separate Electron session storage.
`-GoPreview` uses the separate preview identity and Home. The desktop verifies
the fixed artifact before startup and never falls back to Python. Old launcher
parameters for arbitrary Data Roots, ports and direct repository data are no
longer accepted. Never point a test at private data.

## Native agent and maintenance

Open Settings > Local agent connection to obtain the verified executable and
current server address. All nine product commands use this running service;
refresh after restart. Follow [agent workflows](agent_workflows.md) for
purpose/task arguments, confirmations and evidence boundaries.

Backup, restore, interrupted-operation recovery and legacy document conversion
remain explicit desktop operations. Stop other writers and follow the UI
confirmations. Existing Python Homes are not automatically selected or converted.

## Checks and packages

For everyday UI code checks, use `npm run test:fast` (also `npm test`). For
deterministic user actions against a fresh synthetic Go service, use
`npm run test:workflows` after installing Chromium with
`npx playwright install chromium`. By default the suite builds current Go source.
To reuse a freshly built sidecar, set `CAREER_HUB_SIDECAR_BINARY` to its executable
path (absolute or relative to the repo root); an invalid path fails setup. The
caller must ensure it matches the source being tested. Unset it to build locally
again. Each scenario creates its own Home and prints evidence relative to OS
temp, keeping personal usernames out of copied receipts. See
[testing strategy](../process/testing_strategy.md) for test selection, diagnosis and
controlled agentic testing rules.

```powershell
Push-Location backend
go test ./...
Pop-Location
npm --prefix desktop test
npm --prefix desktop run check
npm run test:extension-compat
```

Native tests that require actual external reference interpreters or an exact
packaged artifact are explicitly gated; their omission does not prove those
qualifications. See the migration evidence for completed installed and packaged
checks and their limits. Python remains a development dependency for the
independent skill audit, public-tree gate and extension builder; it is not a
product runtime. Retirement of old Python implementation tests is tracked in
CH-74.37.

Build the standard installer from clean source with
`npm --prefix desktop run make:windows:dev`. Stable packaging uses the same
Go runtime and does not publish a release. See [packaging](../../packaging/README.md).

## Local checks before push

Saving a commit stays fast. Enable the optional pre-push hook **once per checkout**:

```powershell
npm run hooks:install
```

It enables Git's worktree-specific configuration and sets the hook path only for
this checkout. It refuses to replace existing hooks. For an existing hook, add
`python -m scripts.pre_push "$@"` to its pre-push flow, preserving stdin and both
Git arguments, and propagate any nonzero exit. To disable this checkout's setting:
`git config --worktree --unset core.hooksPath`. Do not delete shared hooks.

Prerequisites: Python 3, Git, Node/npm, Gitleaks 8 (`gitleaks git`), and the pinned
Go toolchain, Electron and Chromium for selected native/browser tests. Prepare
dependencies using the setup commands above and `npx playwright install chromium`.
The hook never installs tools. Missing tools, failed checks and timeouts stop push.

```powershell
npm run check:quick                 # Python + fast UI + desktop code checks
npm run check:local -- --profile native  # Fresh build + native supervisor integration
npm run check:local -- --base origin/main  # Select from branch diff + current edits
npm run check:broad                 # Standard broad local qualification
git commit                         # Save locally; no test hook
git push                           # Check, then upload only if checks pass
```

Keep a clean checkout before pushing: the hook tests the committed files at
HEAD, checks every outgoing commit snapshot for privacy, and scans outgoing
history with Gitleaks. A secret/private file committed then deleted still fails.
These scanners detect known patterns and forbidden files, not every possible
personal identifier. Commit messages/authorship and previously published history
are not certified for public release; use the separate public-export review.
If a remote base is missing locally, fetch it and retry. New-branch selection
excludes only the destination remote's fetched history. Branch pushes at HEAD
are supported; non-HEAD branches and tags stop with instructions for separate
qualification. Ref deletion sends no new file content.

Each command prints RUN/PASS/FAIL/ERROR, duration and a report path relative to
OS temp. SKIP means outside the selected scope, never a successful test. The
JSON report points to per-command local logs; failures stop later checks. Use
those logs to identify the assertion or missing prerequisite. The 30-minute
overall ceiling is a maximum, not a waiting period. Nothing invokes an AI model.
The hook currently repeats its selected checks even if manually run earlier.

The runner uses a fresh local sidecar build when required and ignores a stale
workflow-binary override. Local builds are marked `--allow-dirty`; release builds
still require clean source. All app test data is synthetic. No tests run when an
ordinary user launches the application.

The Windows `native` profile builds the sidecar once, compiles a disposable Go
startup fixture, then runs the three `go-*.integration.test.js` files serially.
It covers supervisor startup/shutdown, ownership, backup/restore and synthetic
credential recovery. Missing prerequisites fail the run. Broad and affected
backend/desktop selections include it; ordinary quick checks leave these cases
explicitly skipped. The manually dispatched GitHub qualification uses this same
profile and reuses its sidecar for later smokes. Routine CI does not enable it.

`git push --no-verify` bypasses hooks. CI remains an independent smaller safety
net, but does not certify omitted local scenarios. Do not bypass a failure to
claim qualification; record incomplete checks and their resolution. See the
[local/CI table](../process/testing_strategy.md#local-and-github-responsibilities).

## Browser extension

Use `.\scripts\start_career_hub.ps1 -EnableExtension` to admit the bundled
extension at port 8766, or `-ExtensionOrigin <origin>` for one explicit origin.
The options cannot be combined. Ordinary startup admits no extension origin.
Pairing stays local and does not authorize unattended scraping or submission.
The renderer and other websites cannot change host launch arguments.

## Developer coordination

Run `npm run backlog:status` before non-trivial work and follow the
engineering ledger. Use separate worktrees for
independent writers. Keep generated artifacts and synthetic fixtures outside
tracked product resources; do not force-add ignored private files.
