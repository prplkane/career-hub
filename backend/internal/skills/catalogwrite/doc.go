// Package catalogwrite implements explicit private catalog mutations: custom
// group rename, archive and restore, and single-record group and skill
// edits, through the recoverable catalog journal. Core files, evidence and
// ledger records are never modified.
//
// New(resourceRoot, dataRoot).RenameCustomGroup(ctx, id, payload, revision)
// follows a fixed order: reject unknown payload keys before filesystem
// access; read the effective registry and its exact raw custom inputs
// together; check the raw revision before core, not-found and name
// validation; locate the raw custom group and preserve its ID, position,
// archived state and cascade IDs; strip the name with Unicode whitespace
// rules and enforce 1 to 160 scalar values; validate the current effective
// core groups plus the candidate raw custom groups against current effective
// skills; encode only private groups; recheck both raw revisions; replace
// the file through retained managed directory handles, comparing the
// expected bytes again; reload and verify the written groups and unchanged
// skills. An identical cleaned name still rewrites the file. Missing
// directories are never created.
//
// The optimistic token is catalog.RawRevision of the custom files, so
// whitespace or comment changes make an edit stale. Output is LF YAML with
// ordered id, name, archived and optional cascade_archived_skill_ids fields
// and quoted strings; only the edited file is rewritten.
//
// Calls hold the per-instance gate and the shared operationgate lease
// through replacement, readback and cleanup; the lease is non-reentrant. Raw
// preconditions are observations, not cross-process compare-and-swap, so the
// host must exclude non-cooperating writers. Error.Committed stays true when
// replacement succeeded but verification or close failed; callers must offer
// reload rather than retrying the old token. Cancellation is cooperative
// between filesystem calls, and verification still runs after replacement.
package catalogwrite
