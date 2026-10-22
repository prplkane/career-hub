# Changelog

All notable changes to Career Hub are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). The `VERSION` file
is the single version source.

Until 1.0.0, minor versions may change data formats or workflows; each such
change is called out under **Changed** with its migration path.

## [Unreleased]

### Changed

- The public repository is now `prplkane/career-hub` (renamed from
  `career-hub-public`). Releases, the update feed and the README badges use
  the new name; the old address keeps working through GitHub's redirect for
  installed 0.3.1 builds.
- The release workflow runs tag builds in the `release` environment, which
  holds the publishing token and admits only `v*` tags.

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

[0.3.1]: https://github.com/prplkane/career-hub/releases/tag/v0.3.1
