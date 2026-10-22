# DESK-010c: Use One Cross-Language Application Version Source

- **Status:** Implemented for source, PyInstaller, and Electron
- **Decision date:** 2026-08-27
- **Evidence tickets:** `SIDE-210`, `ELEC-300`

## Context

Python previously reported `0.0.0-dev`, while the maintainer browser-smoke
package had an independent tooling version. Electron, Python readiness,
installers, diagnostics, and release artifacts cannot safely carry manually
synchronized application versions.

## Decision

A repository-root `VERSION` text file will be the sole editable application
version. It contains one canonical Semantic Version, without a leading `v`, and
one trailing newline. Release channel is separate metadata and is never inferred
from the version.

Python and Electron build steps read this file. Generated package metadata may
copy the exact value, but builds fail when a checked or embedded value differs.
Readiness, health, About/diagnostic output, executable metadata, installer
metadata, and artifact names use the same value. `SIDE-210` created `VERSION`,
set it to `0.1.0`, embedded it in both measured PyInstaller layouts, and made a
missing or non-canonical resource fail closed. `ELEC-300` made the Electron
build and packaged metadata consume and validate this same value without a
second editable application version.

## Alternatives

- Python module as source: rejected because Node packaging would have to parse
  Python.
- Electron `package.json` as source: rejected because Python and non-Electron
  source mode exist independently.
- Git tags at runtime: rejected because installed builds do not contain a
  dependable Git checkout.

## Consequences

Version bumps become one reviewed edit. Build tooling must validate Semantic
Version syntax and prove every emitted artifact agrees before release.

## Reversal Conditions

Replace the text source only if all build systems can consume a new source
directly and a contract test proves there is still exactly one editable value.
