# DESK-010f: Fail Closed Across App, Data-Home, And SQLite Versions

- **Status:** Accepted
- **Decision date:** 2026-08-27
- **Evidence tickets:** `DATA-130`, `ELEC-330`

## Context

An application version, data-home layout, and SQLite schema evolve at different
rates. Treating them as one number would either block harmless binary rollback
or allow an older binary to write a structure it does not understand.

## Decision

Track three independent values:

1. the Semantic Version application identity from `VERSION`;
2. the monotonic integer data-home format in the data-home marker; and
3. the monotonic SQLite `PRAGMA user_version` schema.

Before any mutable work, startup reads and validates the marker and existing
database version. A data-home or schema newer than the binary supports fails
with a stable, content-minimized incompatibility error and performs no write.
An older supported format may migrate only through explicit ordered migrations.

Before a format or schema migration, create and verify a pre-migration backup,
record the original and target versions in recovery state, and make the active
replacement recoverable. A binary rollback is supported only while the older
binary still supports the active format and schema. After an irreversible
migration, rollback means restoring the verified pre-migration backup; it does
not mean opening newer data with an older binary. No release promises rollback
across an irreversible migration without that backup.

## Alternatives

- Gate only on application version: rejected because storage compatibility is
  not equivalent to release identity.
- Let SQLite or file errors reveal incompatibility lazily: rejected because
  writes may already have occurred.
- Automatically downgrade data: rejected because information loss and partial
  downgrade behavior cannot be made implicit.

## Consequences

Migration code must declare supported ranges, preserve a known-good recovery
copy, and test interruption. Channels remain isolated, so installing an older
stable build cannot be used as an implicit beta-data rollback.

## Reversal Conditions

Change this policy only with synthetic forward/backward fixtures proving that
unsupported writers still fail before mutation and that the documented recovery
copy restores every checksummed payload.
