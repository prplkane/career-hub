# DESK-010b: Isolate App Data By Product And Release Channel

- **Status:** Accepted
- **Decision date:** 2026-08-27
- **Evidence tickets:** `PLAT-230`, `ELEC-310`

## Context

Development, beta, and stable binaries must not silently share mutable Career
Vault, SQLite, document, secret, or migration state. The Electron host
also needs conventional local paths without making shared Python code discover
storage from the working directory.

## Decision

The display name is **Career Hub** and the filesystem application identifier is
`CareerHub`. Installed defaults are:

```text
Windows: %LOCALAPPDATA%\CareerHub\<dev|beta|stable>\
macOS:   ~/Library/Application Support/CareerHub/<dev|beta|stable>/
```

`RuntimePaths` remains the wrapper-neutral boundary. An installed host supplies
the resolved resource and data roots explicitly. The direct Python launcher may
derive these defaults, but development CLI compatibility continues to use the
repository only when no installed or selected Data Home was requested.

Each channel has its own data marker, database, files, backups, and secret
namespace. A machine-local `launcher.json` may live at the product root, but it
contains only profile pointers and never private content. Moving or copying data
between channels is an explicit, validated import/restore operation.

## Alternatives

- One shared product root: rejected because beta or development migrations
  could strand stable data.
- Version-numbered data roots: rejected because every application update would
  require unnecessary data movement.
- Repository-local installed data: rejected because installation resources are
  read-only and may be replaced during updates.

## Consequences

Cross-channel testing requires an explicit synthetic fixture or import. A user
can install beta beside stable without either binary mutating the other's home.
Backups must name their source channel without recording an absolute path.

## Reversal Conditions

Change the identifier or path only with a tested, recoverable migration that
handles collisions and leaves the former home intact until validation passes.
