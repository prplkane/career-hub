# DESK-010e: Derive The Desktop Support Matrix At Host Pinning Time

- **Status:** Accepted; foundation versions pinned, target-host acceptance pending
- **Decision date:** 2026-08-27
- **Evidence tickets:** `SIDE-210`, `ELEC-300`, `ELEC-340`

## Context

The required products are Windows x64 and macOS arm64. Electron and its
bundled Chromium periodically raise minimum operating-system versions, so a
number written before the Electron major is pinned would become a stale promise.
macOS x64 also adds a separate native sidecar, signing, CI, and test obligation.

## Decision Method

Windows x64 and macOS arm64 are required build and smoke-test targets. Linux
and mobile desktop hosts are outside this phase. Browser-extension delivery,
including Safari, is governed separately and does not change the desktop-host
support matrix.

`ELEC-300` pins Electron `44.0.0` (Chromium `152.0.7977.54`, embedded Node
`24.18.1`) and build Node `24.19.0`. Electron 44 requires macOS 13 or later,
publishes only 64-bit Windows binaries, and remains in upstream support through
2027-03-02. Career Hub therefore targets Windows 10 or later on x64 and macOS
13 or later on arm64.

The minimum OS matrix is Windows 10 on x64 and macOS 13 on arm64. The release
build baselines are Windows SDK `10.0.26100.8876` and Xcode `26.6`
with macOS SDK `26.5` and deployment target `13.0`. Xcode 26.6 requires a macOS
26.2 or later build host; that build-host requirement does not raise the
application deployment target. A later serviced SDK may replace either pin
only through a reviewed toolchain update. Verify the full matrix on clean
target systems before advertising support.

Add macOS x64 only when user/device evidence shows a real need, every native
dependency and PyInstaller build supports x64, a signed/notarized artifact can
be produced, and a maintained x64 smoke environment exists. Otherwise record it
as unsupported rather than shipping an untested universal label.

## Alternatives

- Promise current OS versions now: rejected as unverified and time-sensitive.
- Build a universal macOS binary immediately: rejected until x64 demand and
  native-sidecar evidence justify its extra release surface.
- Treat Linux development as product support: rejected because no Linux
  installer, signing, or lifecycle contract is planned.

## Consequences

The support matrix is evidence-backed and may move with a future Electron major.
Builds run on their target operating system; PyInstaller is not treated as a
cross-compiler.

The exact matrix and remaining release evidence are maintained in
[`desktop_release_gates.md`](../reference/desktop_release_gates.md). Primary references are
the [Electron 44 release](https://releases.electronjs.org/release/v44.0.0),
[Electron schedule](https://releases.electronjs.org/schedule),
[Apple Xcode system requirements](https://developer.apple.com/xcode/system-requirements),
and [Microsoft Windows SDK downloads](https://learn.microsoft.com/en-us/windows/apps/windows-sdk/downloads).

## Reversal Conditions

Any architecture or minimum-OS change requires dependency, clean-install,
startup, signing, and upgrade/rollback evidence for the changed target.
