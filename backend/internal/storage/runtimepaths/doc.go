// Package runtimepaths provides explicitly rooted reads and bounded
// managed-file mutation primitives. It does not discover or bootstrap a Data
// Home, apply ACLs or take a global writer lease; separate Roots and
// processes need caller coordination.
//
// Paths. Open(root) requires an absolute, existing directory more specific
// than a filesystem root; there is no cwd, environment or ~ lookup. Local
// Windows drive paths and absolute POSIX paths are accepted; UNC, device and
// drive-relative paths are refused. Ancestors are opened one component at a
// time through os.Root and bound by native identity to retained parent
// handles, rechecked before and after each operation. Relative components
// reject traversal, dot or empty segments, alternate data streams, control
// characters, trailing dots or spaces and reserved device names on every OS.
// Directory scans select children by NFC(name).casefold() with the Unicode
// 15 full fold; equivalent or different-case siblings fail, and 66 scalars
// whose normalization differs between Unicode 15 and the pinned x/text data
// are refused. Links, reparse points, special files and leaf directories are
// failures, never absent files. Bounds: 4,096 path bytes, 64 components, 255
// bytes per component, 10,000 entries per scan, 16 MiB per ordinary read or
// write; ReadObjectFile allows 32 MiB and ReadSingleLinkWindow 256 KiB
// windows. Every Error has a fixed Code and no path or OS text.
//
// Reads. Check observes metadata only. ReadFile, ReadObjectFile and
// ReadSingleLinkWindow open one regular file, read at most limit+1 bytes and
// recheck identity, size, mtime and parents; failed postchecks return no
// bytes. Exists, ListNames, ListFileNames and ListDirectoryEntries report
// metadata without content.
//
// Creation and replacement (Windows). OpenOrCreate, EnsureDirectory,
// CreateFile, ProbeWritable, ReplaceFile, CreateBackupStream,
// MoveDirectoryNoReplace, RemoveBackupFile and RemoveEmptyBackupDirectory
// stage into an exclusively created sibling, Sync, reopen the stage by
// identity without delete sharing, verify bytes and publish with one
// handle-relative no-replace rename; an intervening same-name creator is
// refused by that native operation. Ambiguous results report not_x, x or
// unknown, and an unknown outcome must never be retried blindly. A committed
// result stays committed when later verification or cleanup fails. File
// Sync is not directory durability.
//
// Ownership (Windows). ClaimForProcess takes an irreversible per-process
// claim on one Data Home, keyed by volume serial and file index under the
// Global\CareerHub.DataHomeOwner.v1. namespace and retained with no-delete
// root pins until process exit; a contender gets OWNERSHIP_IN_USE.
// RequireProcessOwnership verifies the claim without acquiring it.
// CheckLocalVolume requires a fixed NTFS volume with persistent ACLs that is
// not read-only. GuardSQLiteFiles pins a database's directory chain and main
// file (single hardlink, no delete sharing) while leaving -journal, -wal and
// -shm free to be created, changed and removed; close all SQLite handles
// before closing the guard, then the Root. SyncAndFingerprint flushes a
// pinned image and streams its SHA-256 up to 512 MiB. Ownership excludes
// cooperating Go backends only, not external editors or privileged
// processes.
package runtimepaths
