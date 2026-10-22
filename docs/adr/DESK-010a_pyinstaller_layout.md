# DESK-010a: Select The Packaged-Sidecar Layout By Measurement

- **Status:** `onedir` selected for integration; target-host confirmation pending
- **Decision date:** 2026-08-27
- **Evidence ticket:** `SIDE-220`

## Context

PyInstaller `onedir` is directly inspectable and starts in place. `onefile`
looks simpler to distribute, but extracts at runtime and can add startup,
antivirus, temporary-file, diagnostics, and Electron spawn risk. The repository
previously had no packaged comparison, so choosing from source-mode timing
would have been false precision.

## Decision Method

`SIDE-220` will build both candidates twice from a clean checkout on Windows
x64 and macOS arm64, then record build time, complete artifact and installed
size, manifest contents, ten cold starts, ten warm starts, idle resources,
temporary extraction behavior, packaged tracebacks, and Electron
`extraResource` spawn behavior.

Both candidates must first pass the sidecar protocol smoke test, contain no
private data or developer path, leave no retained process, preserve useful
sanitized diagnostics, and pass platform malware/notarization checks. Choose
`onefile` only if it passes those gates, is within both 10 percent and 500 ms of
`onedir` median readiness, and reduces installed footprint by at least 15
percent or removes a demonstrated installer reliability failure. Otherwise
choose `onedir`. Only the selected layout becomes a production build.

The first implementation comparison on Linux x86_64 is explicitly exploratory,
but it is decisive enough to choose the integration path under the default
rule. `onefile` reduced stored footprint by 64.8% while adding more than five
seconds to both cold and warm readiness, a second bootloader process, and
temporary extraction. `onedir` is therefore the only layout carried into
Electron. Windows x64 and macOS arm64 must repeat the full clean-build and
ten-sample matrix before release. See
`docs/history/benchmarks/side220_linux_exploratory_comparison.md`.

## Alternatives

- Choose `onedir` immediately: safer, but would skip required evidence.
- Choose `onefile` for appearance: rejected because a single file is not a
  reliability result.
- Maintain both indefinitely: rejected because it doubles packaging and
  support paths.

## Consequences

`SIDE-210` makes both candidates reproducible enough to compare. Release work
retains target-host confirmation gates, and the default remains `onedir` when
evidence is tied or incomplete.

## Reversal Conditions

Repeat `SIDE-220` after a material PyInstaller, operating-system, signing,
antivirus, or installer change. A reversal requires the same two-platform
evidence and a migration-safe Electron resource layout.
