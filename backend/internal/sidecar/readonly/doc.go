// Package readonly composes initialized Data Home admission and the fixed
// resource catalogs into a Workspace. It does not bootstrap, write, discover
// profiles, or own a server.
//
// Open(protocol.StartRecord) accepts only an explicit absolute resource root
// and a separate initialized Data Home; port must be zero and channel dev,
// beta or stable. Same or ancestor overlap in either direction is refused,
// including Windows case aliases and native file identities. OpenForProcess
// additionally takes the Windows process ownership claim before private
// admission; the claim lasts until process exit. Preflight validates
// resources and separation without admitting private data. Fixed resources
// are VERSION (ASCII, at most 1 KiB, one final newline), the static UI
// sentinel and both core catalog files.
//
// Every read is preceded and followed by datahome.Inspect with the expected
// channel; the initial data_home_id is retained and a change refuses data
// with DATA_HOME_IDENTITY_CHANGED. Reads are individual observations, not a
// cross-file transaction, lock or ownership check; the host coordinates
// writers.
//
// Services: Registry loads a fresh registry payload; Vault(ctx, view) and
// Profile apply the same admission to the fixed Vault views; Applications,
// Application, ResumePlan, ResumeLibrary and ApplicationSummaries read
// prepared SQLite storage through the process-owned SQLite guard, with
// missing or older storage reported as preparation-required rather than an
// empty ledger; Documents and DocumentTitles decrypt the authenticated ledger
// with the retained key selected only by the validated header key ID. All
// cleanup is synchronous and ordered, and a failed final admission, cleanup
// or cancellation discards the result.
//
// Error.Code is one of the fixed SIDECAR_*, DATA_HOME_*, VAULT_*,
// APPLICATION_* and DOCUMENT_* categories; every message is fixed and never
// contains paths, catalog labels, identities, native prose or nested causes.
package readonly
