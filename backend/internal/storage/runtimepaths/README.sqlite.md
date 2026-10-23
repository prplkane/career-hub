# Owned SQLite file guard — CH27

`Root.GuardSQLiteFiles(relative) (*SQLiteFiles, *Error)` requires the exact Data
Home already claimed by this dedicated Windows process. A different/unclaimed
root returns `OWNERSHIP_REQUIRED`; ownership is not inferred from a marker,
path prefix or port. There is no skip-ownership option. Non-Windows calls return
`SQLITE_FILES_UNSUPPORTED` without filesystem access.

The trusted caller supplies one fixed database-relative name, never SQL/DSNs or
a companion list. Consumers use `applications/career_hub.sqlite3` and newly
reserved schema-recovery copies. Missing main/parents return (nil,nil) without
creation. Existing paths retain the managed reader's ordinary-file, Unicode/case,
reparse and bounded-directory rules. The main and any present `-journal`,
`-wal` and `-shm` files must have exactly one hardlink.

For an existing main file, the guard retains independently opened native
directory-chain handles and a handle-relative main-file open without delete
sharing. It verifies the main identity/type/links and ancestor bindings, and
qualifies the actual database parent's fixed-NTFS/ACL-capable/non-read-only
volume flags. Acquisition/Check/Close read no file content, create no files and
change no permissions. It permits ordinary in-place writes: this is not a replacement for
native SQLite locking or a coherent cross-query snapshot.

## Main and auxiliary files have different lifetimes

Do not pin every companion against deletion. Read-only SQLite can legitimately
create, update, unlink or recreate WAL/SHM state. Companion observations use
handle-relative no-reparse metadata opens with delete sharing and promptly close
them. `Check` and final `Close` revalidate current names/types/links; valid
presence/identity changes are allowed. No immutable/nolock mode, live database
copy or custom VFS is substituted.

Close every SQLite row/lease/connection/database before closing the guard, then
close its Root. `Close` performs final checks, releases all owned pins even on
failure, and retains its result for repeat calls. Failed verification discards
the application result. An invalidated Root, failed close or ownership error
cannot become an empty/missing database result. A timeout does not prove native
work stopped; never abandon a goroutine and release its guard underneath it.
The dedicated host still owns process deadlines and actual exit.

## Limits and verification

CH-41 adds the explicit `SyncAndFingerprint(ctx, maxBytes)` operation after all
SQLite handles on a recovery image have closed. It reopens the already pinned
main relative to its retained parent with write access solely to flush it, then
streams SHA-256/size from the held read handle using a cleared 64 KiB buffer.
It validates bindings/type/single-link identity and stable size/time around the
operation. The API returns only digest/size, never content/handles. Its maximum
ceiling is 512 MiB; cancellation and unsupported limits fail explicitly. Native
tests verify exact hashes, no content changes, cancellation, size limits and
closed-guard refusal. This is file flushing, not a directory-durability claim.

This is **not VFS confinement**: SQLite still opens independent pathnames.
Observations cannot prevent hostile concurrent companion replacement/hardlinks
or in-place reparse changes. The main pin retains write sharing. SQLite temporary
file policy, arbitrary ATTACH/SQL, drive remapping, external editors/legacy Python
writers, recovery and native other-platform acceptance are not covered.
Existing-home ACLs are not silently repaired. Use only fixed trusted queries
within the reviewed cooperating-backend boundary, never a general SQL endpoint.

Six focused Windows test groups cover actual main/parent rename exclusion,
auxiliary creation/deletion, permitted main in-place changes, hardlinked main
and all companions, missing paths, native junction refusal, late unsafe companions,
sticky close failures and complete handle cleanup. A dedicated subprocess checks
the public ownership requirement and same-identity alias without a production
bypass. The owned summary probe also passes the frozen 26 closed and four live
actual Python/Go comparisons; fingerprints and pending integration gates are in
the migration progress log.
