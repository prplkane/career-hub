// Package ledger validates decrypted document-index metadata and supplies
// its two permitted read projections. It performs no I/O, key lookup or
// decryption. Successful parsing does not imply authentication; callers must
// authenticate the encrypted ledger and validate storage membership before
// publishing it.
//
// Parse accepts nonempty scalar-UTF-8 JSON of at most 20 MiB with the exact
// two-field v1 root and exactly 13 fields per document record. Record order
// and raw values are preserved. Duplicate fields or IDs, missing, unknown or
// wrong-case fields, wrong scalar types, out-of-bound values and invalid
// date ranges fail with no partial records. Numeric fields require lexical
// integers; the stored MIME type must be PDF, PNG or JPEG; dates are empty
// or exact YYYY-MM-DD, and timestamps use the aware ISO predicate from
// calendarinput. String bounds count code points; an unpaired surrogate
// returns DOCUMENT_LEDGER_INPUT_UNSUPPORTED rather than being replaced. The
// filename check is metadata only: never use the original filename as a
// blob path, because storage is addressed by the validated document ID.
//
// Metadata returns an owned record slice plus the SHA-256 revision of the
// exact encrypted bytes; includeArchived=true is the default projection.
// Titles returns only active, nonblank, whitespace-stripped, deduplicated
// titles, with casefold-equal titles kept distinct and ordered by scalar
// value. Metadata is not a model-context type; Titles is an application
// boundary, not an OS-level restriction on other programs.
//
// Errors carry only a code and fixed text; parser causes, field names and
// document values are never formatted. Inputs must not be mutated during a
// call.
package ledger
