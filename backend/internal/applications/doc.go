// Package applications owns application-ledger projections and transaction-local
// mutations. It does not select a Data Home, open database paths, migrate
// schemas, commit caller transactions, or submit applications.
//
// ReadSummaries takes a caller-configured read-only *sql.DB and a bounded
// context; SummariesInTransaction and ResumeLibraryInTransaction run the same
// projections inside a caller-owned current-schema *sql.Tx. The owner keeps
// schema preparation, transaction cleanup and file/Vault coherence. A schema
// below schema.Version fails with SCHEMA_PREPARATION_REQUIRED and a newer one
// with SCHEMA_TOO_NEW; an unprepared database is never reported as empty. The
// mutations in mutations.go are transaction-local registration, decision,
// status and start primitives: the caller supplies a guarded writer
// transaction, normalized inputs and synchronous rollback on any error, and
// responses are projected before commit.
//
// Compatibility rules: IDs are deduplicated in input order without trimming
// and bound as SQL parameters in batches of 900, never spliced into SQL.
// Independent SELECTs are used, so a concurrent writer can change state
// between enrichment reads. Stored scalar values, nulls and legacy status
// strings are preserved as stored. A selected attachment uses its attachment
// label; a submitted resume uses the library display name, including archived
// identities; the latest event is the highest event_id. Plan JSON is selected
// but never parsed or exposed, and no private file contents are added.
//
// Limits: at most 10,000 IDs, 4,096 bytes per ID and 1 MiB of ID bytes per
// request; at most 1 MiB per scanned cell, 8 MiB scanned per request and
// 8 MiB of encoded result JSON. ValidateSummaryRequest exposes the input-only
// check. Failures return no partial result. Error carries fixed stage and
// code metadata; its wrapped cause is for typed inspection only and must
// never be logged or shown, because driver text can contain private values.
package applications
