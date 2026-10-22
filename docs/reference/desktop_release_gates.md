# Desktop Release Gates

> **Status: needs rewrite.** The intent below is still accurate, but its tool pins and gates were written for the retired Python/PyInstaller sidecar.
> Until this document is rewritten, use [native packaging](../../packaging/README.md) and default runtime cutover. See the
> [documentation index](../README.md); historical comparisons use the
> pinned reference export.

The repository now contains a development-grade Electron host and a packaged
Python `onedir` sidecar. This is not yet a public installer or a signed release.
The browser launcher remains the normal dogfood path until the native gates
below pass.

## Pinned Foundation Matrix

| Boundary | Pinned foundation | Release meaning |
| --- | --- | --- |
| Application | `VERSION` = `0.3.0` | One SemVer source across metadata, sidecar health, installer, and updater status |
| Electron | `44.7.0` | Chromium `152.0.7977.130`, embedded Node `24.21.0`; upstream support ends 2027-03-02. Track the latest 44.x patch and move to a supported major before that date ([DESK-030](../adr/DESK-030_desktop_shell.md)) |
| Build Node | `24.19.0` | Exact maintainer/CI engine for Forge and tests |
| Electron Forge | `7.11.2` | ASAR package foundation plus one Windows Squirrel maker; publication remains a separate release action |
| Windows installer | `@electron-forge/maker-squirrel` `7.11.2`, `electron-squirrel-startup` `1.0.1` | Per-user dev installer only; MSI, WiX, MSIX, and parallel formats are excluded |
| Sidecar packaging | Native Go executables (`go-onedir`), Go `1.27.3`, CGO disabled | Native build only; no cross-platform relabeling ([native packaging](../../packaging/README.md)) |
| Windows runtime | Windows 10 or later, x64 | Must be proven on clean Windows x64; 32-bit is unsupported |
| Windows build baseline | Windows SDK `10.0.26100.8876` | Use this serviced 26100 baseline or a separately reviewed later servicing update |
| macOS runtime | macOS 13 Ventura or later, arm64 | Must be proven on clean Apple silicon hardware/runner |
| macOS build baseline | Xcode `26.6`, macOS SDK `26.5`, deployment target `13.0` | Xcode 26.6 itself requires a macOS 26.2+ build host |
| macOS x64 | Deferred | Add only with user demand plus native build, signing, and maintained smoke evidence |
| Linux | Development evidence only | Never publish or describe the current Linux package as a supported product |

The Electron version/runtime facts and macOS 13 boundary come from the
[Electron 44.7.0 release](https://releases.electronjs.org/release/v44.7.0) and
[Electron release schedule](https://releases.electronjs.org/schedule). Apple
publishes the Xcode/SDK/build-host matrix in
[Xcode system requirements](https://developer.apple.com/xcode/system-requirements).
Microsoft publishes the selected serviced SDK in
[Windows SDK downloads](https://learn.microsoft.com/en-us/windows/apps/windows-sdk/downloads).
Re-check these primary sources when a target build is cut; a toolchain update is
a reviewed lock change, not an opportunistic release-machine substitution.

The pinned Squirrel maker currently resolves `electron-winstaller` `5.4.4`.
Its package contains architecture-specific `7z-x64` files while Squirrel invokes
generic `7z` names. The package preflight verifies that exact dependency and
copies the x64 executable/library to those generated generic names before Forge
runs. This makes the native build independent of prior lifecycle-script state
without adding a system 7-Zip dependency or committing generated binaries.

Ordinary pull-request CI runs the Electron unit and pinned-foundation checks on
Windows under build Node `24.19.0`. That protects the host contract from source
regressions; it is not a substitute for the clean native package, lifecycle,
credential-store, installer, signing, or notarization gates below.

## Current Windows Installer Evidence

The first bounded Windows installer slice was exercised locally on 2026-09-01.
It is an unsigned development artifact, not a release candidate:

- `Career Hub Dev-0.1.0 Setup.exe`, 223,660,544 bytes, SHA-256
  `C34FFD774FB429D6B5CD94225B1BA7E65A107F319CCAD719F2DBDCCBDCAF337D`;
- one Squirrel package and one installer identity, `CHDesktopDev`; the mutable
  application data root remains the separate `CareerHub/dev` tree;
- install, launch and sidecar health, reinstall, second launch, and uninstall
  passed with a synthetic Data Home marker preserved and zero owned processes;
- the installer output scan found no user profile, worktree, `.codex_tmp`, or
  `CareerHubData` path.

Run the repeatable gate from an account with no existing `CHDesktopDev` install:

```powershell
.\desktop\scripts\smoke-windows-installer.ps1 `
  -InstallerPath '.\desktop\out\make\squirrel.windows\x64\Career Hub Dev-0.1.0 Setup.exe'
```

Squirrel resolves its per-user installation folder through Windows rather than
the child process's `LOCALAPPDATA` environment value. The gate therefore uses
the isolated `CHDesktopDev` install identity while passing a disposable
`LOCALAPPDATA` only to Career Hub itself. It fails if that dev install already
exists, verifies registry, shortcut, process, and data-preservation
postconditions, and removes only its validated test residue. Squirrel
[documents](https://github.com/Squirrel/Squirrel.Windows/blob/develop/docs/using/machine-wide-installs.md)
that uninstall may leave an almost-empty application folder; the gate records
that tombstone separately instead of treating its mere existence as user data.

Version `0.1.1` adds the first installed-upgrade repair slice after a real v1
development Data Home exposed a missing gate. The sidecar now reports the
actionable inner Data Home code, the loading page offers an explicit plan then
apply flow, and a v1 home with no legacy `documents` directory is accepted as
an empty source. A replacement Windows artifact still requires the native
package and lifecycle evidence below before this paragraph becomes installer
evidence rather than source/test evidence.

Version `0.1.2` restores compatibility with existing private schema-27 data.
It includes the schema-26 reusable-answer storage and schema-27 email Subject
routing fields already present in dogfood profiles, preserves those policies
when the older configuration surface saves an existing sender, and applies the
stored header-first filters without retaining raw Subject text. This is a
bounded compatibility release; it does not add automatic data discovery or
cross-channel credential copying.

Version `0.1.3` is a bounded Gmail-connector recovery patch for dogfooding. It
prevents connection mutations from waiting indefinitely behind an active sync,
bounds the renderer's save request, and closes the local IMAP transport without
waiting for a network logout response. It also distinguishes reconnect,
14-day rescan, and permanent local setup removal while retaining imported Feed
items, saved jobs, and applications. The running version is visible in the
persistent footer. Package preparation prunes superseded version folders from
the ignored legacy `outputs/desktop/windows/` archive, while the installer
smoke enforces Squirrel's bounded current-plus-one-previous payload policy and
Data Home preservation. Squirrel intentionally retains that one immediately
previous payload; Career Hub does not add an unsafe second updater that deletes
inside a live install. A replacement installer still requires the native
package and installed-runtime checks below.

Version `0.1.4` closes the diagnostic gap found during that dogfood pass.
Connection saves now have a ten-second local-service deadline in addition to
the renderer deadline, and both connection saves and mailbox syncs expose one
correlated, content-minimized Diagnostics run. The UI distinguishes a server
rejection from a missing browser response and always restores its controls.
Saving remains a local credential/configuration operation; **Sync now** is the
explicit Gmail authentication test. No mailbox address, approved sender, raw
Subject, or App Password is written to the diagnostic events.

Version `0.1.5` fixes the packaged-runtime freeze found in the next Windows
dogfood pass. Opening the first uncached Job Feed detail could lazily import
NumPy and spaCy inside a threaded HTTP request and stop the whole local API;
the Gmail request then reached the socket but could not enter its handler or
write its first diagnostic event. The sidecar now initializes that deterministic
NLP runtime on its owner thread before it opens the listener. A renderer-side,
content-minimized fallback event also preserves the run ID and unconfirmed
response failure in the current Diagnostics view when no server event can
exist. It does not persist mailbox, sender, Subject, or credential content.

Version `0.1.6` fixes a Gmail import queue defect found after the connection
itself was healthy. One retryable malformed or unsupported message previously
stopped the ordered UID batch, leaving newer mail behind it while the terminal
diagnostic still appeared successful. Sync now continues with later messages,
reports `partial` separately from an authentication failure, and records a
`success_with_warnings` diagnostic with only aggregate counters, stable error
codes, and the correlated run ID. Sender addresses, Subjects, message bodies,
and credentials remain outside these diagnostic events. The desktop host also
uses the real path of its exact writable probe as the canonical Data Home. This
keeps the Python containment boundary intact when a trusted Windows package
environment redirects LocalAppData files into LocalCache without changing the
requested directory's real path; no user data is copied or moved.

Version `0.3.0` is the first build published through the update feed below.
Installed `0.2.0` builds have no feed, so moving to `0.3.0` needs one manual
installer run from the public release; later versions then arrive through the
feed. Install the channel whose Data Home holds your data: a Home and its
backups are bound to one channel, and restore refuses another channel's backup.

Version `0.2.0` is the first installer whose local data service is the native
Go sidecar; the Python sidecar is no longer packaged. Squirrel installs only a
strictly higher version, so this minor bump is what lets an existing `0.1.7`
installation accept it. It also carries the post-migration fixes: Build with AI
copies a runnable `career-hub-agent` command, run-directory scans skip template
placeholder files such as `.gitkeep`, and CI runs every UI test file. A `0.1.7`
build has no update feed, so moving to `0.2.0` requires one manual installer
run; later versions can arrive through the feed when one is configured.

Version `0.1.7` advanced the immutable installer version so the sidebar SVG
namespace fix cannot be mistaken for another `0.1.6` reinstall. It also adds a
bounded desktop update controller and a compact Settings surface. Automatic
checks and downloads default on, but Career Hub never restarts silently: an
installed update requires the explicit **Restart and install** action or a later
normal app restart. The built-in Electron updater automatically downloads an
available Squirrel update and does not expose byte progress, so the UI shows an
indeterminate progress bar until the download-complete event, then a completed
bar. The sidecar is stopped through the normal ownership boundary before the
Squirrel installer is allowed to restart the app.

## Desktop Update Boundary

Package preparation accepts one optional `CAREER_HUB_UPDATE_FEED_URL`. It is
validated as a credential-free HTTPS path and copied into signed build metadata;
the renderer never receives the URL. With no URL, Settings truthfully reports
that the development build must be updated with a newer installer and no update
network request occurs.

The private source repository is not a suitable anonymous client feed: private
GitHub release assets require authenticated access, so installed apps get 404.
Career Hub therefore publishes reviewed installers, Squirrel packages and
release metadata to the public repository `prplkane/career-hub` (named
`career-hub-public` until 2026-10-22; the old address redirects), which
also holds the curated source export below. No source beyond that export, no
Data Home and no user record belongs in the feed, and the installed client
holds no credential. See the official
[Electron update guide](https://www.electronjs.org/docs/latest/tutorial/updates),
[Electron `autoUpdater` contract](https://www.electronjs.org/docs/latest/api/auto-updater/)
and [GitHub release-asset authentication](https://docs.github.com/en/rest/releases/assets).

### Published Feed

`.github/workflows/release.yml` builds releases here and publishes them to the
public repository. A `vX.Y.Z` tag that equals `VERSION`, points to a commit on
`main` and has a passed **Tests and public-tree gate** check on that commit
(otherwise the workflow stops; re-run it once CI passes) builds the sidecar and
the Squirrel package for the `dev` channel, or for `stable` when the repository
variable `CAREER_HUB_RELEASE_CHANNEL` says so (any other value fails the
release). It embeds `https://github.com/prplkane/career-hub/releases/latest/download`
as the feed URL (the variable `CAREER_HUB_RELEASE_REPOSITORY` can name another
public repository) and creates a release there with `RELEASES`, the full
`.nupkg`, a renamed `Setup.exe` and `SHA256SUMS.txt`. Its notes start with a
**View source code** button, take the CHANGELOG section without links to this
repository's pull requests, and end with a link to the release's source tag.

The job only reads this repository. Publishing uses the secret
`CAREER_HUB_RELEASE_TOKEN`: a fine-grained token with Contents read and write on
the public repository only, read only by the tag-gated publish step. Before
building, a tag run stops when the public repository is missing or private, or
when the secret is unset; a pull request run refuses a feed that is this
repository. Pull requests that change the workflow or the packaging
configuration run the same build without publishing. Squirrel installs only a
higher version, so every published build needs a `VERSION` bump.

Builds up to 0.3.0 embed this repository's private feed and cannot update
themselves; install the next release once from the public Releases page, after
which updates arrive through **Settings**.

### Public Source Export

`python -m scripts.export_public <checkout of the public repository>` replaces that
checkout's files with a curated snapshot of a commit (`--rev`, default `HEAD`).
It leaves out the engineering ledger, agent instruction files, development
history except the documents current docs still cite, the release workflow
and the Dependabot configuration (dependency updates land here and reach the
public copy through the next export);
files under `public/` (the public README, contributing guide and Gitleaks
configuration) replace their counterparts; links to anything left out become
plain text; and changelog links to private pull requests are dropped. Commit
the result in the public checkout with `git add -f` for the exported files,
because the private `.gitignore` names some source folders. Run the public-tree
gate, Gitleaks and the test suite there before pushing.

One-time owner setup: make `prplkane/career-hub` public, create the token
above, add it as the `CAREER_HUB_RELEASE_TOKEN` secret of the `release`
environment here (deployment rule: tags matching `v*`), keep 2FA on and
`main` protected. Tag runs enter that environment; pull-request dry runs do
not, so they never see the token. For each release, export and merge the
source first, then push the tag: `git tag vX.Y.Z && git push origin vX.Y.Z`.

For Windows Squirrel, the configured path must expose the generated `RELEASES`
file and referenced full `.nupkg` for the exact channel/platform/architecture.
macOS uses different update metadata and requires a signed application. Electron
does not provide this updater on Linux; a future supported Linux build must use
its selected package manager rather than pretending this feed is portable.

## Gates Before Any Public Desktop Build

Before evaluating native release evidence, run the bounded independent review
in `desktop_professional_review_prompt.md`.
Its anti-stall rules are part of the handoff: one CI snapshot per commit, one
bounded follow-up wait per long-running handle, and no repeated full-suite run
without a changed input.

1. **Native, clean, reproducible builds.** Build the sidecar and Electron app
   twice on Windows x64 and macOS arm64 from locked inputs. Compare manifests,
   run the full synthetic suite, and repeat the documented ten-sample startup,
   memory, CPU, and close-to-zero-process measurements.
2. **Platform data protection.** Exercise real Windows Credential Manager and
   DPAPI migration/recovery, plus signed-sidecar macOS data-protection Keychain
   and zero-entry ACL behavior. Test locked/unlocked Keychain, wrong account,
   copied data home, interrupted migration, and verified backup recovery.
3. **Lifecycle and offline behavior.** Run the native matrix in
   `manual_testing.md`: first launch, second-instance race, normal close, OS
   shutdown/logout or parent loss, sidecar crash/retry, macOS close/activate,
   offline startup, network cutoff, explicit recovery, and persistence.
4. **Installer behavior.** Select one installer format per platform. Verify
   clean install without Python/Node, repair/reinstall, uninstall with private
   data preserved, installation-path integrity, and non-ASCII paths.
5. **Platform trust.** By owner decision (2026-10-20, DESK-010d amendment),
   Windows artifacts are published unsigned: first installs show a SmartScreen
   warning, and integrity rests on HTTPS, tag-only CI publishing, a protected
   `main` and 2FA rather than a signature. Authenticode stays optional, for
   example through a free open-source signing programme. Public
   macOS artifacts require Developer ID signing, hardened runtime, notarization,
   stapling, and Gatekeeper verification. Production credentials stay outside
   Git, pull requests, logs, caches, diagnostics, and agent output.
6. **Upgrade and recovery.** Rehearse the version/schema compatibility matrix,
   automatic verified pre-migration backup, interrupted migration, reinstall,
   compatible rollback, and named-backup recovery where direct rollback is not
   compatible.
7. **Auditable release.** Required CI runs on target operating systems with
   read-only default permissions. A tag produces immutable draft artifacts,
    checksums, provenance, and manual smoke evidence. No production feed is
    configured until signing, tamper/wrong-channel rejection, offline behavior,
    and recovery all pass.

## Current Stop Line

Distribute Windows builds only through the release workflow above, never a
locally built package. The local Squirrel gate proves a bounded same-account
lifecycle, but there is no signing identity (by decision), clean-machine and
second-account evidence,
notarized macOS artifact, update feed, remote telemetry, or multi-device
synchronization. Those omissions are deliberate release gates, not hidden
runtime fallbacks.
