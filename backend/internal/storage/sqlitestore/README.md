# SQLite driver foundation

Maintainer-only candidate adapter; not a production filesystem boundary or
replacement backend. Use disposable synthetic fixtures, or an explicitly
reviewed host-owned local path. Never discover or open the user's Data Home.

## API and responsibility

```go
func OpenReadOnly(ctx context.Context, absolutePath string) (*sql.DB, error)
```

The caller closes the returned database and supplies bounded contexts for every
query. One physical connection and one idle connection are permitted. Each new
connection receives URI `mode=ro`, `busy_timeout(5000)` and `query_only(1)`.
Missing files fail without creation. There is no bootstrap, migration, lookup of
environment-selected data paths, route or general-purpose SQL endpoint.

Open acquires and returns a `db.Conn(ctx)` lease, forcing the physical connection
and its configuration without a ping or additional `SELECT 1`. It does not
validate database content or schema. A corrupt existing file can therefore open
successfully and fail with SQLite `NOTADB` (26) when the caller subsequently
executes `PRAGMA user_version`, matching the Python connector/query boundary.

The adapter constructs the URI; it does not accept caller DSN parameters.
Unicode, percent, question-mark and fragment characters are escaped as path
data. Windows uses `file:///C:/...`; UNC/device namespaces, reserved device
components and alternate-data-stream syntax are refused. These are lexical
checks, not proof that a local-looking path is not a network-mounted filesystem.
Only reviewed local filesystems belong in the pilot.

Open failures have fixed text and preserve their cause through `Unwrap`, allowing
`errors.As` to inspect `*sqlite.Error` without logging path-bearing errors. The
typed query/probe boundary owns sanitization of later query errors. A returned
`*sql.DB` is not a SQL sandbox: trusted callers must use fixed queries, not expose
arbitrary SQL, ATTACH or extension loading. Disabling query_only cannot turn the
main `mode=ro` connection into a writable main database.

## Explicit preparation and recovery (CH-41)

CH-41 additionally supplies explicit preparation primitives, not changes to
`OpenReadOnly` semantics:

- `OpenReadWriteExisting` uses `mode=rw`, foreign keys, FULL synchronous mode,
  memory-only temporary storage and IMMEDIATE transactions. Missing files are
  not created. The same URI/path validation and one-connection pool apply.
- `BackupIntoEmpty` requires an already-reserved ordinary empty destination and
  a separate source connection. The caller retains native guards and reserves
  the database writer separately. Native backup copies at most 128 pages per
  step, checks cancellation between steps and caps the image at 512 MiB.
- A WAL source's page1 retains WAL-format flags after backup; the copied image
  is converted to DELETE mode after stepping/finishing. Destination close is
  observed before verification. The native regression requires a standalone
  copy containing the committed WAL-only row while another writer is blocked.
- `VerifyBackup` checks the expected schema version and full integrity, not
  foreign-key consistency (a recovery image must preserve broken legacy data
  too). The caller separately flushes/fingerprints its guarded file and publishes
  completion metadata before migration. Failure does not authorize deletion of
  the source or automatic restoration from an incomplete copy.

The pinned driver's backup `Commit` returns the destination on successful
finish, allowing checked Close; on finish failure it closes internally and does
not expose a separate close error. Native tests check terminal handle cleanup.
These synchronous primitives do not promise forced OS/driver cancellation.
The owned service and remaining startup/crash gates are described in
`docs/history/migration/application_schema_contract.md`.

## Candidate and provenance

Selected candidate: `modernc.org/sqlite v1.60.1`, with exactly its upstream
`modernc.org/libc v1.77.1`. YAML `v4.0.0-rc.6` and x/text `v0.42.0` are
unchanged by this pin. Modules were fetched through `proxy.golang.org` with `sum.golang.org`
verification; `go.sum` retains module hashes and `go mod verify` checks the cache.
SQLite tag source commit: `b122d0417c01508beb55158faedeb64a0c5bfd8a`.
SQLite module sum: `h1:/blz53O951KWFOso4QQvEs/Fq6cDBKLtMVrYNSeJVKw=`.

This is the selected pin, not a claim of latest version. v1.60.1 (2026-10-14
update from v1.58.0) keeps the transpiled SQLite at 3.53.4, including its
journal-rollback correction. Driver changes: a fault reading a WAL `-shm`
mapping now fails the statement instead of crashing the process,
`vfs.FS.Close` refuses while a database is open, multi-parameter binding is
no longer quadratic, and Go 1.26 or later is required. The upstream release/security documentation and retained license
notices were inspected, but a complete current vulnerability-database/reachable
dependency scan is not established. Candidate-only status remains; this is not
a vulnerability-free assertion or production adoption decision.

Primary references:

- [Pinned module and exact libc](https://gitlab.com/cznic/sqlite/-/blob/v1.60.1/go.mod)
- [Driver changelog](https://gitlab.com/cznic/sqlite/-/blob/master/CHANGELOG.md)
- [SQLite 3.53.4 release](https://sqlite.org/releaselog/3_53_4.html)
- [SQLite security guidance](https://sqlite.org/security.html)
- [Native URI semantics](https://sqlite.org/uri.html)
- [Read-only WAL behavior](https://sqlite.org/wal.html#readonly)

The separate `modernc.org/sqlite/vfs.New(fs.FS)` is deliberately not used.
Verified in v1.58.0 and again in v1.60.1: `vfs/patches.go` has I/O method version 1 (no SHM hooks), and
`vfs/vfs_windows_amd64.go` implements lock/unlock as no-ops and reserved-lock
checks as always unlocked. It cannot establish live Python locking/WAL parity.
No `immutable`, `nolock`, in-memory copy, snapshot substitution or custom VFS
is used by this pilot.

## Measured behavior and cancellation limits

Windows native test evidence with Go 1.27.1 and Python 3.12.14:

- Typed INTEGER/TEXT/BLOB/NULL reads, missing-file noncreation, immutable main
  database contents in DELETE mode, write rejection even after query_only is
  disabled, and reconnected per-connection settings pass.
- A long recursive query is cooperatively cancelled and the connection remains
  usable. **A 100ms deadline under a separate Python exclusive lock took 5033ms
  to return**, near the configured 5000ms busy timeout. The 100ms deadline is a
  stress-test target, not an approved product latency requirement or an
  automatic candidate-rejection gate. The source `_connect_read_only` explicitly
  uses `timeout=5` and `PRAGMA busy_timeout = 5000`. Open's five-second context
  likewise does not promise cancellation of blocking OS calls or
  context-insensitive driver work.
- Separate Python processes create live DELETE and WAL databases, keep them
  open, commit another row, and the same Go reader sees the new committed row.
  Python owns a five-second command/lock deadline; the harness has bounded
  pipes, a 15-second process deadline and one-second process WaitDelay.
- DELETE reader open caused no file changes. WAL reader open changed auxiliary
  bytes while three files remained. WAL/SHM files remained after this observed
  close ordering. This is explicitly not a zero-filesystem-write guarantee.

A read-only audit of the pinned driver explains the busy result: its translated
SQLite default busy callback and pager lock-retry loop do not inspect the
interrupt flag, and Windows sleep delegates to libc's `time.Sleep`. There are
also separate cancellation coverage gaps: `conn.query` calls `newStmt` (including
`prepareV2`) before `stmt.query` installs its interrupt watcher; that watcher
stops when the initial query returns, and `rows.Next` has no context watcher.
Go's `database/sql` cancellation closes rows, but close waits for an active
`Next` call. These prepare/row-iteration observations are source evidence, not
additional executed timing reproductions.

The minimum proposed host policy retains the source-compatible 5000ms busy
handler, uses bounded contexts, stops scheduling work after cancellation, and
keeps connection/rows ownership until cleanup completes. A five-second busy
timeout is accumulated sleep for a locking event, not a hard overall operation
deadline: repeated statements/events and OS calls can take longer. Any finite
overall response/shutdown policy belongs to the host/worker boundary and must
explicitly account for an operation still running after cancellation. A strict
process-stop requirement needs a supervised worker/process policy, not an
unbounded abandoned goroutine. No retry adapter or driver fork is implemented.
See [SQLite busy timeout](https://sqlite.org/c3ref/busy_timeout.html) and
[interrupt semantics](https://sqlite.org/c3ref/interrupt.html).

Native SQLite reports `3.53.4`, source ID
`2026-07-24 19:02:57 bf7c7f30031888f4e796e429ab3978879485813aaca6f641c7b33e4e09459bcc`.
Sorted compile options joined with LF plus a final LF have SHA256
`e08918d66caa484a6929317e24d92db1d6a078fc115dbf15adb10994f869babf`.
The `COMPILER=gcc-12-win32` option describes the translated upstream build, not
a local CGO compiler invocation. Python's SQLite is `3.53.1`, compile-options
SHA256 `78a403189848361dd1aaa12c32e9a3bd2d9cf72d553c0ad9c465b2e965fe233a`.
Tests emit full Go compile options and both fingerprints for reproducibility.

The ordinary native VFS opens main/journal/WAL/SHM files by pathname. Prechecking
with the existing managed-path adapter does not eliminate replacement races or
confine later auxiliary opens. Host ACL ownership, stable ancestor permissions,
network-volume exclusion and cross-process mutation policy are future gates.
Same-user malicious processes remain outside any claim made here. Windows
synthetic evidence does not establish macOS/Linux native locking/recovery,
packaging, durable writes, crash safety, memory bounds or full application parity.

## Focused verification

From `backend`, using the pinned Go executable and an isolated verified module
cache (`GOTOOLCHAIN=local`, `GOPROXY=off`, `CGO_ENABLED=0`):

```text
go test -buildvcs=false ./internal/storage/sqlitestore -count=1 -v -timeout=45s -cover
go vet -buildvcs=false ./internal/storage/sqlitestore
go test -buildvcs=false -c -o ../tmp/sqlitestore.test.exe ./internal/storage/sqlitestore
go mod verify
```

Set `CAREERHUB_SQLITE_PYTHON` to the explicit pinned Python 3.12.14 executable
to run both live-writer cases. Without it those tests report SKIP, not parity.
The VCS flag is a local ownership workaround, not release provenance. Set test
TEMP/TMP to an owned fixture directory; no private files are needed.

Final focused Windows run with CGO disabled: all tests pass, including both
Python subprocess cases, with 95.6% adapter statement coverage. Focused vet,
native test-executable build and module verification pass. Passing tests include
the explicit characterization of the failed hard-deadline gate above; they do
not convert that limitation into a passing production requirement. Fifteen
retained dependency notice files were checked byte-for-byte against their
verified module sources; see `backend/licenses/sqlite-pilot/README.md`.
