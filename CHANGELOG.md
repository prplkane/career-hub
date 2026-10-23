# Changelog

All notable changes to Career Hub are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). The `VERSION` file
is the single version source.

Until 1.0.0, minor versions may change data formats or workflows; each such
change is called out under **Changed** with its migration path.

## [Unreleased]

### Added

- The browser extension now looks and reads like Career Hub: its stylesheets
  derive from the app's design tokens (Ink & paper, with the Night ink dark
  theme following the browser), use the bundled IBM Plex fonts and the
  briefcase mark, and share the app's buttons and status badge. The popup
  leads with **Save this job**, offers **Save and open workspace**, shows
  **Save selection** only when text is selected and keeps **Save link only**
  as a quiet fallback; a connection badge names one of four states (not
  running, not allowed, not paired, connected) with its next step, in the popup
  and in the assistant alike. The assistant hosts pairing and other-browser
  setup, shows the current page, clears page-bound reviews when the tab
  changes, and folds its boundary notes into one disclosure.
- **Settings → Browser extension** turns the extension endpoint on from the
  installed app and remembers the choice; Firefox, Safari and other Chromium
  IDs can be added there instead of a launch flag. The change applies after a
  confirmed restart, pairing is still required, and the endpoint stays off by
  default (ADR SEC-250 item 4, amended). The extension's error messages now
  point at this setting instead of developer launch flags.

### Fixed

- Browser extension: **Retry** after a failed **Prepare application** repeats
  Prepare instead of running a Save-for-later capture of the last mode.
- Browser extension: the capture review target and the one-shot helper reviews
  (profile fill, saved answers, completed answers) now live in memory-backed
  `storage.session`, so **Review in Career Hub** and **Fill** keep working after
  the browser's idle termination of the service worker. No Profile or answer
  value is stored; a completed answer is kept only as a SHA-256 digest until
  the user saves it.
- A saved job whose posting uses headings the analyzer does not know (for
  example "What we're looking for" or "Must haves") no longer gets a clean
  "no evidence gap" verdict. The digest now records that no requirement
  section was recognized, quotes the unrecognized headings, and the
  assessment shows a "No requirement basis" review item instead. Existing
  job cards are marked stale so a re-analysis picks up the signal.
- Resumes saved by Microsoft Word no longer fail to digest because of the
  invisible markers Word adds to ordinary documents (spelling and grammar
  marks, bookmarks such as `_GoBack`, page-break hints, comment and
  permission ranges). Non-breaking hyphens are kept as text. Content controls,
  fields, tracked changes, drawings and other unsupported constructs are still
  refused.

### Changed

- The application database upgrades to schema version 28 automatically the
  next time the sidecar starts, after writing a verified recovery backup under
  `backups/sqlite-schema-recovery/` as earlier upgrades did. Version 28 gives
  every saved application a `revision` counter and adds indexes for email
  outcome links, contact provenance and the default Job Feed order; no data is
  rewritten and no table is removed.
- Decision and status updates accept an optional `expected_revision`. When it
  is sent and no longer matches, the update is refused with
  `JOB_REVISION_CONFLICT` instead of overwriting newer notes or a newer status;
  requests without it behave as before. Application records and summaries now
  return the current `revision`.
- Reading saved jobs from a database that still needs the schema upgrade now
  reports that preparation is required instead of showing an empty job list.
- Browser extension: the helper field catalog is shared by the worker and the
  side panel from one file; the unused "interrupted" session status and the
  v1 session-storage migration are removed.
- Documentation and in-app Help no longer pass `-DataProfile`, which the
  launcher does not accept; the extension development guide and the full
  qualification workflow describe how the packaged extension smoke runs.

## [0.3.2] - 2026-10-22

The first release of the recreated public repository.

### Changed

- **Upgrading from 0.3.1:** install `CareerHub-dev-0.3.2-Setup.exe` once from
  the [Releases page](https://github.com/prplkane/career-hub/releases); the
  Data Home is kept. 0.3.1 cannot update itself because its feed address was
  retired with the old public repository. Later versions arrive through
  **Settings → Check for updates**.
- The public repository is now `prplkane/career-hub`, recreated with a clean
  history. Releases, the update feed and the README badges use the new name;
  the old `career-hub-public` address no longer answers.
- Releases build and publish in the public repository on its free Actions
  minutes: a `v*` tag there runs the workflow, whose publish job alone can
  write, inside a `release` environment that admits only `v*` tags and uses
  the repository's own token. No personal access token is needed any more.
- `SHA256SUMS.txt` uses LF line endings so `sha256sum -c` works everywhere.

### Security

- Desktop build tooling pins `http-cache-semantics` 4.3.0 (GHSA-ch52-4w7c-c8xp,
  pulled in by Electron Forge). Build-time only; it does not ship in the app.

## [0.3.1] - 2026-10-22

The first release published from the public repository, with working
updates.

### Fixed

- **Check for updates** failed on every installed build because the update
  feed lived in a private repository. Releases and the feed now publish to the
  public repository `prplkane/career-hub` (then named `career-hub-public`).

### Changed

- **Upgrading from 0.3.0:** install `CareerHub-dev-0.3.1-Setup.exe` once from
  the [public Releases page](https://github.com/prplkane/career-hub/releases);
  the Data Home is kept. 0.3.0 cannot update itself because its feed address is
  private. Later versions arrive through **Settings → Check for updates**.

### Added

- `python -m scripts.export_public` writes the curated public source snapshot,
  without the engineering ledger, agent instructions or development history.

## [0.3.0] - 2026-10-21

Security hardening of the local API, routine Go checks in CI, and the first
builds that can update themselves.

### Security

- **Local API admission (ADR SEC-250):** no HTTP route returns a capability token.
  `GET /api/session` now requires a token like every other `/api/` route and
  returns capabilities only, so another Windows account or program on the PC can
  no longer obtain one. `career-hub-agent` requires `--connection`.
- The desktop window receives its capability token from the Electron host over
  the private startup channel and preload, not from an HTTP route (ADR
  SEC-250).
- `career-hub-agent` authenticates with a scoped per-start token read from an
  owner-only connection file in the Data Home (`--connection`, shown in
  Settings), not a token fetched over HTTP. The token admits only agent routes
  and is refused from browsers (ADR SEC-250).
- The browser extension must be **paired once** with a one-time code from
  **Settings → Pair browser extension**; its `Origin` header alone no longer
  admits it, because local programs can forge headers. Codes expire after five
  minutes, work once and are voided after five wrong guesses. **Unpair** revokes
  the extension at once. Career Hub stores only a hash of the credential
  (ADR SEC-250).
- Every start re-verifies that the Data Home root is still private (protected
  DACL for only the owner and SYSTEM, owned by the current user) and refuses to
  start with `DATA_HOME_PERMISSIONS_UNSAFE` otherwise, changing nothing; the
  private data guide documents the repair (ADR SEC-250).
- Go toolchain 1.27.1 → 1.27.3. Fixes eleven reachable standard-library
  vulnerabilities, including GO-2026-6604 (`os.Root.Mkdir` could follow
  junctions out of the root on Windows) in the Data Home path-safety layer,
  HTTP/2 server issues in the local sidecar, and GO-2026-6809 (`crypto/x509`
  trusted compromised intermediate certificates) on the mail TLS connection.
- Electron 44.0.0 → 44.7.0 (Chromium 152.0.7977.54 → 152.0.7977.130); the pin
  now follows the latest patch of a supported major (ADR DESK-030).
- `golang.org/x/net` 0.58.0 → 0.60.0 for CVE-2026-78669, with the required
  `x/sys` 0.48.0 and `x/text` 0.42.0.

### Added

- CI gates for Go formatting, `go vet`, and reachable known vulnerabilities
  (`govulncheck`).
  Routine CI runs them, with `go vet` for Windows and the portable Go suite, on
  Linux whenever backend or shared files change.
- Dependabot version updates for Go, npm, pip, and GitHub Actions.
- Codebase assessment and documentation index.
- Contributor guide, this changelog, `.editorconfig`, `.nvmrc`, and
  `.python-version`.
- A tag-triggered release workflow publishes the Windows update package to a
  GitHub Release of this repository, and installed builds read it as their update
  feed (check in **Settings**, then install). Windows builds are unsigned by
  owner decision (ADR DESK-010d amendment). It publishes only
  when the required **Tests and public-tree gate** check passed on the tagged
  commit, and uses only the workflow's own token.
- **Settings** shows the app version with its short build commit, for example
  `v0.3.0 (abc1234)`, read from the verified sidecar manifest.
- Native desktop integration tests run in local checks and the manual
  qualification workflow.

### Changed

- **Upgrading from 0.2.0:** pair the browser extension once with a code from
  **Settings → Pair browser extension**, and pass `--connection` (shown in
  Settings) to `career-hub-agent`. Data Home format and schema are unchanged;
  if a Home's permissions were loosened, start stops with
  `DATA_HOME_PERMISSIONS_UNSAFE` until repaired per the private data guide.
  Install `CareerHub-dev-0.3.0-Setup.exe` from GitHub Releases over Career Hub
  Dev 0.2.0; the Data Home is kept. Later versions arrive through **Settings**.
- **Two release channels:** `dev` and `stable`. The unused `beta` channel is
  retired; a `beta` build, launch, Home or backup is refused (ADR DESK-010d
  amendment).
- The Windows app, taskbar, Start menu, shortcut and installer use the Steel
  briefcase icon instead of Electron's default.
- Reference documentation describes the Go sidecar and Electron host instead of
  the retired Python runtime.

## [0.2.0] - 2026-10-04

The first release on the native Go backend.

### Changed

- The product backend is a native Go sidecar inside the Electron desktop host.
  The Python product runtime, command-line tools, and PyInstaller packaging are
  retired; Python remains only for developer tooling.
- Agent workflows use the `career-hub-agent` client against the running app,
  and **Build with AI** copies `career-hub-agent` commands.
- Existing Python-era Data Homes are not adopted implicitly; use the explicit
  import and restore controls in Settings.

### Fixed

- Run-directory scans skip template placeholder files left in Python-era Homes.
- Skills table evidence labels no longer overlap the last-used column.

### Security

- Owner-specific data removed from the public tree.
- Desktop build-tool dependencies patched (`fast-uri`, `undici`,
  `brace-expansion`).

## 0.1.0 – 0.1.7 - 2026-08-30 to 2026-09-03

Development-channel builds of the Python runtime in the Electron host:
the secure desktop foundation, Data Home migration at startup, Gmail
connection and sync hardening, and the safe desktop update workflow. These
versions were not tagged; see the Git history before `9e1019a`.

[0.3.2]: https://github.com/prplkane/career-hub/releases/tag/v0.3.2
[0.3.1]: https://github.com/prplkane/career-hub/releases/tag/v0.3.1
