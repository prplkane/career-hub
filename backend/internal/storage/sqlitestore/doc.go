// Package sqlitestore opens SQLite databases at explicitly supplied,
// host-owned local paths with the pure-Go driver. It does not establish
// filesystem containment, discover a Data Home, migrate a schema or expose a
// product route; the caller closes every database and bounds every query.
//
// OpenReadOnly(ctx, absolutePath) returns a *sql.DB limited to one physical
// and one idle connection. Each connection opens with URI mode=ro,
// busy_timeout(5000) and query_only(1). Missing files fail without creation.
// Open forces the physical connection through a Conn lease; it does not
// validate content or schema, so a corrupt file fails on the first statement
// with the driver's NOTADB code. The adapter builds the URI itself: Unicode,
// percent, question-mark and fragment characters are escaped as path data;
// Windows paths use file:///C:/...; UNC, device and alternate-stream paths
// are refused. A returned database is not a SQL sandbox: trusted callers use
// fixed queries and never expose arbitrary SQL, ATTACH or extension loading.
//
// OpenReadWriteExisting uses mode=rw, foreign keys, FULL synchronous mode,
// memory-only temporary storage and IMMEDIATE transactions, with the same
// path validation and pool size; it never creates files. BackupIntoEmpty
// copies a source connection into an already-reserved empty destination in
// steps of 128 pages, checking cancellation between steps and capping the
// image at 512 MiB; a WAL source image is converted to DELETE mode after the
// copy. VerifyBackup checks the expected schema version and full integrity
// but not foreign-key consistency, because a recovery image must preserve
// broken legacy data. A failed backup never authorizes deleting the source
// or restoring from an incomplete copy.
//
// Open failures have fixed text and preserve their cause through Unwrap for
// typed inspection without logging path-bearing errors. The busy timeout is
// accumulated sleep for a lock, not a hard deadline; cancellation does not
// preempt a blocked native call, so the host owns overall deadlines and
// keeps connection and rows ownership until cleanup completes.
package sqlitestore
