package homebackup

import (
	"context"
	"slices"
	"sort"
	"strings"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

// A permit is private, single-use and never serialized. The producing owner may
// prepare it only for its own verified snapshot and consume it only after that
// operation has a verified terminal outcome. File equality alone is not disposal
// authority. No directory search, mtime retention or renderer-supplied path exists.
// The maintenance owner must serialize preparation/consumption; this is not a
// concurrent capability or a persisted authorization to retry after restart.
type snapshotCleanupPermit struct {
	snapshot, scope, channel string
	files                    []File
	directories              []string
	maxNodes                 int
	used                     bool
}

type snapshotCleanupLimits struct {
	files, nodes int
	bytes        int64
}

var backupCleanupLimits = snapshotCleanupLimits{MaxFiles, maxInventoryNodes, MaxTotalBytes}

// SnapshotCleanupResult contains observed effects, not authority to retry.
// Its zero value is unreported/unknown; unstartedSnapshotCleanup records no dispatch.
type SnapshotCleanupResult struct {
	Attempted          bool
	RemovedFiles       int
	RemovedDirectories int
	LastRemoval        runtimepaths.BackupRemovalOutcome
	SnapshotRemoval    runtimepaths.BackupRemovalOutcome
	Complete           bool
}

type snapshotCleanupOperations struct {
	beforeRemove func(int) *Error
	file         func(context.Context, *runtimepaths.Root, string, File) (runtimepaths.BackupRemovalOutcome, *runtimepaths.Error)
	directory    func(context.Context, *runtimepaths.Root, string) (runtimepaths.BackupRemovalOutcome, *runtimepaths.Error)
}

func unstartedSnapshotCleanup() SnapshotCleanupResult {
	return SnapshotCleanupResult{LastRemoval: runtimepaths.BackupNotRemoved, SnapshotRemoval: runtimepaths.BackupNotRemoved}
}

func snapshotCleanupAdmission(ctx context.Context, root *runtimepaths.Root, channel string) (string, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return "", failure("BACKUP_CANCELED")
	}
	if root == nil || root.RequireProcessOwnership() != nil {
		return "", failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	if channel != "stable" && channel != "dev" {
		return "", failure("BACKUP_CHANNEL_INVALID")
	}
	classification, p := datahome.ClassifyOwnedRecovery(root, &channel)
	if p != nil || classification == nil || classification.Cardinality != datahome.RecoveryNone || classification.Marker != datahome.RecoveryMarkerInitialized {
		return "", failure("BACKUP_CLEANUP_RECOVERY_REQUIRED")
	}
	scope, q := root.CredentialScope()
	if q != nil {
		return "", failure("BACKUP_CLEANUP_SCOPE_UNAVAILABLE")
	}
	return scope.Fingerprint(), nil
}

func prepareSnapshotCleanup(ctx context.Context, root *runtimepaths.Root, channel, snapshot string, files []File) (*snapshotCleanupPermit, *Error) {
	return prepareSnapshotCleanupWithLimits(ctx, root, channel, snapshot, files, backupCleanupLimits)
}

func prepareSnapshotCleanupWithLimits(ctx context.Context, root *runtimepaths.Root, channel, snapshot string, files []File, limits snapshotCleanupLimits) (*snapshotCleanupPermit, *Error) {
	scope, p := snapshotCleanupAdmission(ctx, root, channel)
	if p != nil {
		return nil, p
	}
	files, p = cleanupFiles(snapshot, files, limits)
	if p != nil {
		return nil, p
	}
	directories, p := inspectSnapshotCleanup(ctx, root, snapshot, files, limits.nodes)
	if p != nil {
		return nil, p
	}
	return &snapshotCleanupPermit{snapshot: snapshot, scope: scope, channel: channel, files: files, directories: directories, maxNodes: limits.nodes}, nil
}

func cleanupFiles(snapshot string, files []File, limits snapshotCleanupLimits) ([]File, *Error) {
	if !strings.HasPrefix(snapshot, snapshotPrefix) || !lowerHex(strings.TrimPrefix(snapshot, snapshotPrefix), 32) || files == nil || len(files) > limits.files {
		return nil, failure("BACKUP_CLEANUP_TARGET_INVALID")
	}
	files = slices.Clone(files)
	sort.Slice(files, func(i, j int) bool { return files[i].RelativePath < files[j].RelativePath })
	var total int64
	for index, file := range files {
		if !safeName(file.RelativePath) || file.SizeBytes < 0 || file.SizeBytes > MaxEntryBytes || !lowerHex(file.SHA256, 64) ||
			(index > 0 && files[index-1].RelativePath == file.RelativePath) || file.SizeBytes > limits.bytes-total {
			return nil, failure("BACKUP_CLEANUP_WITNESS_INVALID")
		}
		total += file.SizeBytes
	}
	return files, nil
}

// The complete tree is checked before the first removal. Later per-file native
// hashing and empty-directory-only deletion refuse changes without broadening
// the witness. This is not an atomic tree snapshot against hostile native writers.
func inspectSnapshotCleanup(ctx context.Context, root *runtimepaths.Root, snapshot string, files []File, maxNodes int) ([]string, *Error) {
	expected := make(map[string]File, len(files))
	for _, file := range files {
		expected[file.RelativePath] = file
	}
	directories := []string{}
	nodes, found := 0, 0
	var walk func(string, int) *Error
	walk = func(relative string, depth int) *Error {
		if ctx.Err() != nil {
			return failure("BACKUP_CANCELED")
		}
		physical := snapshot
		if relative != "" {
			physical += "/" + relative
		}
		entries, present, q := root.ListDirectoryEntries(physical)
		if q != nil || !present {
			return failure("BACKUP_CLEANUP_TREE_CHANGED")
		}
		for _, entry := range entries {
			nodes++
			if nodes > maxNodes || depth+1 > runtimepaths.MaxComponents {
				return failure("BACKUP_INVENTORY_LIMIT")
			}
			name := entry.Name
			if relative != "" {
				name = relative + "/" + name
			}
			if !safeName(name) {
				return failure("BACKUP_CLEANUP_TREE_CHANGED")
			}
			if entry.IsDirectory {
				if _, wasFile := expected[name]; wasFile {
					return failure("BACKUP_CLEANUP_TREE_CHANGED")
				}
				if p := walk(name, depth+1); p != nil {
					return p
				}
			} else {
				file, ok := expected[name]
				if !ok || file.SizeBytes != entry.Size {
					return failure("BACKUP_CLEANUP_TREE_CHANGED")
				}
				p := verifyRestoreFile(ctx, root, snapshot+"/"+name, file)
				if ctx.Err() != nil {
					return failure("BACKUP_CANCELED")
				}
				if p != nil {
					return failure("BACKUP_CLEANUP_TREE_CHANGED")
				}
				found++
			}
		}
		// Postorder: all children precede their parent; the snapshot itself is last.
		directories = append(directories, relative)
		return nil
	}
	if p := walk("", strings.Count(snapshot, "/")+1); p != nil {
		return nil, p
	}
	if ctx.Err() != nil {
		return nil, failure("BACKUP_CANCELED")
	}
	if found != len(files) {
		return nil, failure("BACKUP_CLEANUP_TREE_CHANGED")
	}
	return directories, nil
}

func consumeSnapshotCleanup(ctx context.Context, root *runtimepaths.Root, permit *snapshotCleanupPermit, ops snapshotCleanupOperations) (result SnapshotCleanupResult, problem *Error) {
	result = unstartedSnapshotCleanup()
	if permit == nil || permit.used {
		return result, failure("BACKUP_CLEANUP_PERMIT_INVALID")
	}
	permit.used = true
	check := func() *Error {
		scope, p := snapshotCleanupAdmission(ctx, root, permit.channel)
		if p != nil {
			return p
		}
		if scope != permit.scope {
			return failure("BACKUP_CLEANUP_SCOPE_CHANGED")
		}
		return nil
	}
	if p := check(); p != nil {
		return result, p
	}
	directories, p := inspectSnapshotCleanup(ctx, root, permit.snapshot, permit.files, permit.maxNodes)
	if p != nil {
		return result, p
	}
	if !slices.Equal(directories, permit.directories) {
		return result, failure("BACKUP_CLEANUP_TREE_CHANGED")
	}
	before := func(index int) *Error {
		if ops.beforeRemove != nil {
			if p := ops.beforeRemove(index); p != nil {
				return p
			}
		}
		return check()
	}
	if ops.file == nil {
		ops.file = func(ctx context.Context, r *runtimepaths.Root, path string, f File) (runtimepaths.BackupRemovalOutcome, *runtimepaths.Error) {
			return r.RemoveBackupFile(ctx, path, f.SizeBytes, f.SHA256)
		}
	}
	if ops.directory == nil {
		ops.directory = func(ctx context.Context, r *runtimepaths.Root, path string) (runtimepaths.BackupRemovalOutcome, *runtimepaths.Error) {
			return r.RemoveEmptyBackupDirectory(ctx, path)
		}
	}
	for index, file := range permit.files {
		if p := before(index); p != nil {
			return result, p
		}
		result.Attempted = true
		var q *runtimepaths.Error
		result.LastRemoval, q = ops.file(ctx, root, permit.snapshot+"/"+file.RelativePath, file)
		if result.LastRemoval == runtimepaths.BackupRemoved {
			result.RemovedFiles++
		}
		if q != nil || result.LastRemoval != runtimepaths.BackupRemoved {
			return result, failure("BACKUP_CLEANUP_UNCONFIRMED")
		}
	}
	for index, directory := range directories {
		if p := before(len(permit.files) + index); p != nil {
			return result, p
		}
		path := permit.snapshot
		if directory != "" {
			path += "/" + directory
		}
		result.Attempted = true
		var q *runtimepaths.Error
		result.LastRemoval, q = ops.directory(ctx, root, path)
		if directory == "" {
			result.SnapshotRemoval = result.LastRemoval
		}
		if result.LastRemoval == runtimepaths.BackupRemoved {
			result.RemovedDirectories++
		}
		if q != nil || result.LastRemoval != runtimepaths.BackupRemoved {
			return result, failure("BACKUP_CLEANUP_UNCONFIRMED")
		}
	}
	if p := check(); p != nil {
		return result, p
	}
	result.Complete = true
	return result, nil
}
