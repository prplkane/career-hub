# Changelog

All notable changes to Career Hub are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). The `VERSION` file
is the single version source.

Until 1.0.0, minor versions may change data formats or workflows; each such
change is called out under **Changed** with its migration path.

## [Unreleased]

## [0.3.4] - 2026-11-24

### Security

- The desktop backend now builds with Go 1.27.4, which fixes the reachable
  HTTP server advisory GO-2026-6859.
- `golang.org/x/net` 0.61.0 → 0.62.0 for CVE-2026-78661, a denial of service
  through crafted HTML in the tokenizer that reads job postings and email
  bodies.
- Electron 44.7.0 → 44.9.1 (Chromium 152.0.7977.130 → 152.0.7977.167, Node
  24.21.0 → 24.22.0), for the high-severity advisory on compromised eMudhra
  intermediate certificates. The pin is still upgraded by hand.
- Desktop build tooling: undici 7.30.1 (denial of service on excessive
  content encodings) and fast-uri 3.1.9 (URI scheme, host and path
  confusion). Backend: x/sys 0.49.0, x/text 0.43.0, gopdf 0.38.3 and
  go-humanize 1.1.0.

### Added

- **Settings → Career Vault export** saves your profile, education,
  experience, projects, skills, custom skills and saved short answers as one
  file (`career-vault-export-YYYY-MM-DD.chvault`). **Set up Career Vault** can
  import that file into a new, empty Career Vault, custom skills included, so
  linked records keep their skills. The file is **not encrypted**: it holds
  your contact details and work-authorization facts, so keep it private and
  delete it after importing. Jobs, applications, documents and email and
  extension settings are not included. Import never overwrites existing data,
  checks every skill link first, and can be repeated to finish an interrupted
  import.
- **Job Feed:** tick opportunities to act on several at once. A bar above the
  list offers **Dismiss** (or **Restore** in the dismissed view) and
  **Delete**, which asks first and leaves saved jobs alone; the list head ticks
  the whole page. An item changed elsewhere since the list loaded is left as
  is and named in the result. The detail panel also gets its own **Delete**.
- **Applications → Email updates:** **Dismiss** removes an email update that
  is still waiting (not linked, or needing review), and **Clear unmatched**
  removes every one that matched no saved application, after asking.
  Updates already applied to an application are kept and fold to one line
  below the rest. Deleting Job Feed opportunities now also removes the email
  updates that only pointed at them. A cleared update can come back if
  **Rescan recent email** reads that message again; it is then matched afresh.
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
  default. The extension's error messages now
  point at this setting instead of developer launch flags.

### Fixed

- On Linux, intake run listings now detect a report replaced during a read
  even when the replacement has the same size, modification time and inode number.
- In the dark theme, destructive text buttons (Delete, Flush archived, Remove
  source, Disconnect, Forget email setup) show in the warning colour instead
  of looking like ordinary links.
- **Save for applications:** a resume chosen while the previous one was still
  saving keeps the name filled in from its file instead of being cleared.
- Language, theme and other view choices (last page, Job Feed page size and
  mode, Skills layout, Insights period) are remembered between launches.
  The page gets a new local address at every start, which emptied the
  browser's storage, so a system language of Russian came back each time.
  The desktop app now keeps these choices in its own small file and applies
  them before the first paint.
- Concurrent browser-extension helper requests now consume each review token
  only once, including profile fill, saved answers, and completed answers.
- Mixed JD headings retain evidence of unrecognized sections even when another
  section is understood. Assessments show **Incomplete requirement basis** and
  recommend review instead of reporting a clean match. Re-analyze existing
  saved jobs to refresh their requirement coverage.
- Native Windows schema qualification now exercises the current schema, the
  previous-schema upgrade, and rejection of a newer schema using shared version
  boundaries.
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
- **Profile → Eligibility:** Future sponsorship and STEM OPT eligible start as
  "Not confirmed" instead of reading "Yes" until the saved facts load, and stay
  "Not confirmed" if that load fails.
- **Add resume → Update Career Vault:** extraction progress and errors now show
  in the visible upload message. A failed extraction used to leave "Added
  locally" on screen with no error and no review.
- **Applications:** a row's stage list no longer offers "Reviewed (legacy)",
  which every save refused. A record already in that stage shows it once,
  selected and disabled, and the Stage filter still finds such records.
- **Applications:** stepping through a row's stage list with the keyboard no
  longer saves each step. The row asks for Enter to save or Escape to keep the
  current stage; a pick from the open list still saves at once. Save messages
  name the stage ("Applied") instead of its code.

### Changed

- Fresh workspaces open with profile creation and Career Vault import. The
  welcome screen and Settings use the primary system language by default and
  remember an explicit English or Russian choice across app restarts.
- **Release channel:** public releases now install as **Career Hub** (the
  `stable` channel) instead of **Career Hub Dev**. They are separate apps
  with separate data: Career Hub keeps its data in
  `%LOCALAPPDATA%\CareerHub\stable`, starts empty, and does not read or
  change the Career Hub Dev data in `CareerHub\dev`. Install it with
  `CareerHub-stable-0.3.4-Setup.exe` from the Releases page, not through the
  update check in Career Hub Dev. Nothing moves over by itself. The Career
  Vault can be carried across with the new **Career Vault export**, which
  needs this version running on the old data, because Career Hub Dev 0.3.2
  has no export. Jobs, applications, documents, and the email and extension
  connections are set up again.
- **Job Feed → Email:** a simpler mailbox page. New email senders fold to one
  line with the number waiting. Connection settings fold away once the
  mailbox is connected and stay as you leave them after each sync. Source
  policies have their own section: one row per sender with Jobs and
  Applications switches, its capture warnings and Remove, a key to what each
  route does, and a **Save sources** button.
- **Job Feed:** the detail panel stays beside the list and scrolls on its own
  instead of stretching the page. Assessment context, Application checklist
  and People & channels are folded to one line each that still shows the
  verdict or count, and stay as you leave them while you move between
  opportunities. The retained job text now sits above the confirmation that
  asks you to review it.
- **Resume Library:** each resume's actions sit in the same columns on every
  row (Review, Download, Archive or Restore, Delete), whatever their labels
  say; Delete reads as the destructive action, and its place stays empty for
  a resume an application uses.
- **Profile:** importing a resume or setting up a new Career Vault now fills
  given name and family name from a two-word Name, which application forms
  ask for separately. Longer or single-word names are left for you, typed
  values are never replaced, and Profile notes which parts were filled in
  until you save it.
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
- The Job Feed loads with one request instead of two: each page now carries
  `dismissed_total` for the archive badge, counted from the same sources the
  page shows.
- Quieter screens, from a screenshot review of every view against the shared
  design checklist: the tag beside a page heading is plain mono text and goes
  away when it would only repeat the title; Soon, Active, Update available,
  Core, Custom, Remote/Onsite/Hybrid and the Profile section tags read as
  status text with an indicator square instead of filled pills; Insights shows
  the first 25 requested skills with **Show all** at the foot and no longer
  colours every missing skill red; a link-only Job Feed item shows one
  "Full JD required" note instead of two identical panels; the Job Feed pager
  wraps, so a phone-width window no longer scrolls sideways; Profile read mode
  shows an ellipsis on a clipped value and the full text as its title.
- **Experience** fits one window: the record list stays pinned beside the
  editor, rows are compact, company, role, category and dates share one row,
  evidence comes before skills and opens for editing in place, skills fold
  behind **Edit skills**, and the Save bar stays in view. The List/Card toggle
  is gone from Experience, Projects and Education. Typing a category that
  nearly matches an existing one offers the stored spelling, here and on
  Projects.
- Confirmation prompts in the Job Feed, Resume Library and Experience use one
  in-app dialog instead of the browser's: Cancel has focus, Escape declines,
  and permanent deletes are marked as dangerous.
- **Resume Library:** "Review status" sorts by urgency, Needs attention first.
  Choosing a PDF, DOC or RTF in **Save for applications** says it cannot be
  reviewed into Career Vault, which reads DOCX and TXT only. Uploaded PDF,
  DOCX, DOC and RTF files whose contents do not match their type are refused.
- The app opens on **Jobs**, and the window title reads "Career Hub".

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

- **Local API admission:** no HTTP route returns a capability token.
  `GET /api/session` now requires a token like every other `/api/` route and
  returns capabilities only, so another Windows account or program on the PC can
  no longer obtain one. `career-hub-agent` requires `--connection`.
- The desktop window receives its capability token from the Electron host over
  the private startup channel and preload, not from an HTTP route.
- `career-hub-agent` authenticates with a scoped per-start token read from an
  owner-only connection file in the Data Home (`--connection`, shown in
  Settings), not a token fetched over HTTP. The token admits only agent routes
  and is refused from browsers.
- The browser extension must be **paired once** with a one-time code from
  **Settings → Pair browser extension**; its `Origin` header alone no longer
  admits it, because local programs can forge headers. Codes expire after five
  minutes, work once and are voided after five wrong guesses. **Unpair** revokes
  the extension at once. Career Hub stores only a hash of the credential.
- Every start re-verifies that the Data Home root is still private (protected
  DACL for only the owner and SYSTEM, owned by the current user) and refuses to
  start with `DATA_HOME_PERMISSIONS_UNSAFE` otherwise, changing nothing; the
  private data guide documents the repair.
- Go toolchain 1.27.1 → 1.27.3. Fixes eleven reachable standard-library
  vulnerabilities, including GO-2026-6604 (`os.Root.Mkdir` could follow
  junctions out of the root on Windows) in the Data Home path-safety layer,
  HTTP/2 server issues in the local sidecar, and GO-2026-6809 (`crypto/x509`
  trusted compromised intermediate certificates) on the mail TLS connection.
- Electron 44.0.0 → 44.7.0 (Chromium 152.0.7977.54 → 152.0.7977.130); the pin
  now follows the latest patch of a supported major.
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
  owner decision. It publishes only
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
  retired; a `beta` build, launch, Home or backup is refused.
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
connection and sync hardening, and the safe desktop update workflow.

[0.3.2]: https://github.com/prplkane/career-hub/releases/tag/v0.3.2
[0.3.1]: https://github.com/prplkane/career-hub/releases/tag/v0.3.1
