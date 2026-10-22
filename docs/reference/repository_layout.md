# Repository and Local Storage Layout

Career Hub uses three separate boundaries. A path belongs to exactly one of
them: tracked source, reproducible local build output, or private user data.
Keeping those boundaries visible prevents an old installer, a dependency
folder, or a legacy data copy from being mistaken for application source.

## Tracked source

| Path | Purpose |
| --- | --- |
| `backend/` | Go module: the `career-hub-sidecar` server, the `career-hub-agent` client, and domain packages under `backend/internal/` ([backend README](../../backend/README.md)) |
| `src/intake_ui_static/` | Browser UI (HTML, ES modules, CSS) served by the Go sidecar |
| `src/data/` | Shared read-only catalogs packaged with the sidecar |
| `src/agent_workflow.py` | Development-only wrapper for the skill audit; no product commands |
| `desktop/src/`, `desktop/ui/` | Electron host and protected startup UI |
| `extension/` | Browser extension source |
| `packaging/` | Sidecar packaging notes and build measurements |
| `scripts/` | Maintainer, audit, and local-launch commands (Python, PowerShell) |
| `tests/`, `desktop/tests/` | Python tooling tests, Node unit tests, and rendered smoke checks |
| `.agents/` | Repository skills for agent workflows |
| `docs/` | Documentation grouped by reader: `guides/`, `reference/`, `process/`, `product/`, `history/` and `adr/` ([index](../README.md)) |
| `.backlog/` | Engineering work ledger: `tasks/` holds open work, `completed/` finished work |
| `VERSION` | The single application-version source |

`src/` keeps its historical name because the sidecar, the packaging manifest,
and tests reference these paths. It no longer contains Python product code.

The Python-era repository-data development mode is gone: the app keeps all
data in the Data Home outside the checkout. Folders such as `career_vault/`,
`applications/`, `jobs/` and `resume_versions/` are ignored outright because an
older checkout may still hold local data in them; the public-tree gate blocks
them as a backstop. `outputs/` remains for packaging output.

## Generated and replaceable

These paths are ignored by Git and can be reproduced from tracked inputs:

- `desktop/.generated/` and `desktop/out/` — prepared Electron metadata,
  packaged app, and the current Windows installer;
- `packaging/build/` and `packaging/dist/` — native Go sidecar build output
  (`packaging/dist/go-onedir/`);
- `outputs/desktop/` — legacy local copies of installer artifacts;
- `playwright-report/` and `test-results/` — rendered-test output;
- `.venv/`, `node_modules/`, and `desktop/node_modules/` — installed
  dependencies. These are also replaceable but omitted from normal cleanup
  because reinstalling them is slow.

Preview the bounded generated-output cleanup:

```powershell
.\scripts\clean_generated_artifacts.ps1
```

Apply it only when no package build is running:

```powershell
npm run clean:generated
```

Pass `-IncludeDependencies -Apply` directly to the PowerShell script only when
both `node_modules/` folders should also be reinstalled (`.venv/` is never
removed by the cleaner). The cleaner validates
every exact target below the repository, rejects links, and never includes a
Career Hub Data Home, repository worktree, source directory, or operating-system
credential store.

Preparing a new desktop package also removes version-named directories below
`outputs/desktop/windows/` except the current `VERSION`. That folder is a
legacy build archive, not an update history. Tagged release artifacts will
belong in the release system when public distribution exists.

## Private user data

The desktop stores each release channel's Data Home under
`%LOCALAPPDATA%\CareerHub\<channel>` (`dev`, `stable`, or
`dev-go-preview`), resolved by `desktop/src/app_paths.js`. Settings shows the
active path. It is outside Git and outside the installed application. Gmail App
Passwords are stored separately in Windows Credential Manager under a
release-channel-scoped target and are never inside the data directory.

Homes created by the retired Python browser launcher
(`%USERPROFILE%\CareerHubData\<profile>`) and ignored private files still
present inside a developer checkout are legacy copies. They are never adopted
implicitly. Do not remove them merely because they are ignored; bring data
across with the desktop's explicit import or restore controls, verify the
result, and remove the source as a separate user decision.

## Installed Windows application

The development Squirrel installer owns `%LOCALAPPDATA%\CHDesktopDev`; stable
uses a different identity. This directory contains executable payloads,
not Career Hub Data Home. Squirrel cleans superseded versions during its
upgrade lifecycle and deliberately permits the current version plus one
immediately previous payload. The one previous payload is bounded installer
recovery state, not an active version or repository file. Career Hub does not
recursively delete inside the installer directory itself.

The Windows installer smoke fails if the current payload is absent, more than
two app/package versions remain, a delta package appears, or reinstall touches
the synthetic Data Home. This follows Squirrel's documented retention contract
instead of adding a second unsafe updater.

## Worktrees and scratch space

Agent worktrees live directly under `tmp/<name>/` (older ones under
`tmp/worktrees/`). The folder name does not always match the branch, so check
`git worktree list` before removing anything there. `.codex_tmp/`, `.tmp/`, and
`to_do/` are local scratch/history paths; they are intentionally excluded from
the automatic generated-output cleaner because their recovery value cannot be
inferred.

### Branch and worktree review

When a branch's PR merges, the writer that closes its ledger task removes the
worktree (`git worktree remove`, never `--force`) and deletes the local branch.
Never delete, without asking the user, a branch that has unmerged commits,
uncommitted changes, or an open ledger task. Deleting a remote branch always
needs the user's approval.

Run the full review below when `git worktree list` grows past a handful of
entries or after a large merge. List first; delete only after the user has seen
the list.

1. `git fetch --prune` updates `origin/main` and drops remote-tracking refs
   whose branches were deleted on GitHub.
2. For each local branch, `git cherry origin/main <branch>` prints one `+` line
   per commit whose change is not in main. Zero `+` lines means the work was
   merged, even through a squash or rebase where `git branch --merged` reports
   nothing.
3. For each worktree, `git -C <path> status --porcelain` must be empty.
   Uncommitted work exists only in that folder and is never deleted without
   the user.
4. Remove clean, merged worktrees with `git worktree remove <path>`, then
   `git worktree prune`. Delete their branches with `git branch -d`; use `-D`
   only for a branch that step 2 proved merged.
5. A branch whose commits were reworked before landing still shows `+` lines.
   Treat it as a question for the user, not a deletion.

`git worktree remove` also deletes ignored files. Check
`git -C <path> status --ignored` for anything that is not a build or test cache
first. Go marks its module cache read-only, so a worktree that set
`GOMODCACHE` inside itself needs `go clean -modcache` with that `GOMODCACHE`
before its folder can be deleted.
