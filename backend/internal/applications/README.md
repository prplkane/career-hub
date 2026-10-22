# Application-summary read model (migration pilot)

`ReadSummaries` ports the selected-row/enrichment semantics of Python
`application_history.get_application_summaries`. It is not a product route or a
database owner. Callers supply a correctly configured read-only `*sql.DB`, own its
close and path/permission policy, and pass a bounded context. The package does
not create directories, open filenames or migrate schemas. Its read operations
do not write records.

`SummariesInTransaction` exposes the same summary projection inside an existing
caller-owned, admitted **current-schema** `*sql.Tx`. Together with
`ResumeLibraryInTransaction`, it lets an owner assemble agent inputs from one
SQL snapshot without acquiring another connection. It performs no schema
preparation/check, transaction cleanup or registration; those responsibilities
stay with the owner. The original `ReadSummaries` independent-SELECT behavior
is unchanged. Neither helper establishes file/Vault coherence by itself.

The synthetic native SQLite test commits application and resume changes from
a second WAL connection between reads: the existing reader retains both old
values, and a fresh reader sees both committed values. It also covers duplicate
and absent IDs, detached projections, read-only enforcement, cancellation,
closed transactions and late enrichment failure without partial output. The
fixture is an isolated copy of the pinned Python oracle's empty database; this
does not qualify the final agent owner or an installed command.

CH-58 adds transaction-local registration/decision/status/start primitives in
`mutations.go`. They do not open or commit a database and are not complete owning
product services yet. The caller must supply a current-schema guarded writer
transaction, normalized/bounded inputs and synchronous rollback on any error.
The exact response is projected before commit; registration returns summaries,
not full histories. See the in-progress write contract.

Empty requested IDs return an empty object without accessing a database.
`ValidateSummaryRequest` exposes the same input-only admission used by the
query model, so the owning workspace can reject oversized/unsupported requests
before opening either filesystem root. Duplicate IDs still consume budget;
the validator does not normalize, deduplicate or mutate caller input.
Otherwise schema versions below 27 return empty summaries; versions above 27
fail closed. Missing-database handling belongs to the caller. General Python
`get_application` and `list_applications` may bootstrap/migrate: they are not
aliases for this read-only operation.

The opt-in [workspace service](../sidecar/readonly/README.md#ch-27-application-summary-service)
now composes this model with process-owned Windows storage and fresh Data Home
admission. It does not expose a new HTTP route or turn summaries into list/detail.

## Compatibility

- Deduplicate IDs in input order, without trimming or normalization. Bind values
  as SQL parameters in batches of 900; never construct SQL from ID contents.
- Read the same selected columns in the same phase order. Keep independent
  SELECTs, not a newly introduced transaction. A concurrent writer can therefore
  change state between enrichment reads; this is not a coherent snapshot API.
- Preserve SQLite JSON-compatible scalar storage values, nulls and legacy status
  strings. REAL values keep a JSON decimal/exponent marker even when numerically
  integral; INTEGER-affinity columns are not necessarily integer storage. Do not
  coerce arbitrary integer flags to a strict 0/1 enum.
- A selected attachment uses its attachment label. A submitted resume uses the
  library display name, including archived identities. Latest event means highest
  `event_id`, not greatest timestamp. An event with no known resume stays unknown.
- Preserve the characterized legacy shared-prefetch behavior: a stale resume ID
  on an unrecorded row can resolve when another requested row causes that ID to
  be loaded. Do not silently repair stored data during a language migration.
- Return independent identity objects. Plan JSON is selected but not parsed or
  exposed. No resume history, private file contents or inferred application data
  is added.

## Explicit safety limits

Requests accept at most 10,000 IDs, 4,096 bytes per ID and 1 MiB total ID bytes.
Every scanned cell, including hidden IDs/plan JSON, is charged: at most 1 MiB per
cell and 8 MiB scanned bytes (scalar overhead is conservatively eight bytes).
The result map must encode within 8 MiB of Go JSON, including repeated identities,
keys and escaping. The response envelope is the caller's separate budget.

These are intentional bounded-pilot deltas from Python, not blanket source
equivalence. Invalid UTF-8 selected TEXT fails; BLOB and nonfinite values cannot
be exported as JSON values. Hidden BLOB flags retain source truth semantics.
Validation occurs after the SQLite driver allocates/scans each value. Exact
encoded-size checks temporarily encode one record; neither limit establishes a
driver or process peak-memory ceiling.

Failures return no partial result. `Error` has fixed stage/code metadata and a
fixed message. Its wrapped cause is only for typed driver-code inspection: never
log it or expose native error text, which may contain private values or paths.
Context propagation does not guarantee prompt native-driver cancellation under
file locks; the SQLite driver pilot measures that separately.

## Verification boundary

The standard-library scripted driver tests query ordering, parameter binding,
900/901 boundaries, selected types, enrichment, independent outputs, encoded
budgets, cancellation propagation and row/connection cleanup. They do not prove
SQLite execution, WAL/locking, file containment or packaged runtime readiness.
The actual-Python synthetic oracle and native SQLite probe must establish those
additional supported boundaries separately.
