package homebackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"

	"careerhub.local/backend/internal/runtimepaths"
)

// Verify physical bytes, including original SQLite and any WAL/SHM/journal.
// Original evidence MUST describe the quiescent physical source, not the
// different normalized database image emitted by SQLite backup. Logical/schema
// validation is an additional admission step owned by the restore orchestrator.
// Empty ordinary directories are allowed (archive manifests do not record them).
// Read leases guard each hash; this is not an atomic/hostile-ABA-proof tree lease.
func verifyRestoreTree(ctx context.Context, root *runtimepaths.Root, physical, logical string, files []File, destinations []string) *Error {
	if ctx == nil || ctx.Err() != nil {
		return failure("BACKUP_CANCELED")
	}
	if root == nil || root.RequireProcessOwnership() != nil {
		return failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	// Check is a file-path guard and rejects existing directories. The typed
	// ListDirectoryEntries walk below applies confinement and directory checks.
	if !allowedRoot(logical) || !safeName(physical) || len(files) > MaxFiles || len(destinations) == 0 || len(destinations) > 3 {
		return failure("BACKUP_RESTORE_TREE_INVALID")
	}
	for _, target := range destinations {
		if !safeName(target) {
			return failure("BACKUP_RESTORE_TREE_INVALID")
		}
	}
	checkDestinations := func(name string) *Error {
		// Existing components are checked by typed traversal. Moves add only
		// fixed safe prefixes, but can exceed total path/depth limits. Check
		// these before moving, including empty directories absent from manifests.
		for _, target := range destinations {
			path := target + strings.TrimPrefix(name, logical)
			if len(path) > runtimepaths.MaxPathBytes || strings.Count(path, "/")+1 > runtimepaths.MaxComponents {
				return failure("BACKUP_RESTORE_PATH_LIMIT")
			}
		}
		return nil
	}
	expected := map[string]File{}
	for _, file := range files {
		if strings.HasPrefix(file.RelativePath, logical+"/") {
			expected[file.RelativePath] = file
		}
	}
	nodes, found := 0, 0
	var walk func(string, string, int) *Error
	walk = func(path, name string, depth int) *Error {
		if p := checkDestinations(name); p != nil {
			return p
		}
		if ctx.Err() != nil {
			return failure("BACKUP_CANCELED")
		}
		entries, present, p := root.ListDirectoryEntries(path)
		if p != nil || !present {
			return failure("BACKUP_RESTORE_TREE_CHANGED")
		}
		for _, entry := range entries {
			nodes++
			if nodes > maxInventoryNodes || depth+1 > runtimepaths.MaxComponents {
				return failure("BACKUP_INVENTORY_LIMIT")
			}
			relative, physicalName := name+"/"+entry.Name, path+"/"+entry.Name
			if p := checkDestinations(relative); p != nil {
				return p
			}
			if entry.IsDirectory {
				if _, file := expected[relative]; file {
					return failure("BACKUP_RESTORE_TREE_CHANGED")
				}
				if p := walk(physicalName, relative, depth+1); p != nil {
					return p
				}
				continue
			}
			file, exists := expected[relative]
			if !exists || file.SizeBytes != entry.Size {
				return failure("BACKUP_RESTORE_TREE_CHANGED")
			}
			if p := verifyRestoreFile(ctx, root, physicalName, file); p != nil {
				return p
			}
			found++
		}
		return nil
	}
	if p := walk(physical, logical, 1); p != nil {
		return p
	}
	if found != len(expected) {
		return failure("BACKUP_RESTORE_TREE_CHANGED")
	}
	if ctx.Err() != nil {
		return failure("BACKUP_CANCELED")
	}
	return nil
}

func verifyRestoreFile(ctx context.Context, root *runtimepaths.Root, path string, file File) *Error {
	digest, p := fingerprintRestoreFile(ctx, root, path, file.SizeBytes)
	if p != nil {
		return p
	}
	if digest != file.SHA256 {
		return failure("BACKUP_RESTORE_TREE_CHANGED")
	}
	return nil
}

func fingerprintRestoreFile(ctx context.Context, root *runtimepaths.Root, path string, size int64) (digest string, problem *Error) {
	if ctx == nil || ctx.Err() != nil {
		return "", failure("BACKUP_CANCELED")
	}
	lease, p := root.OpenBackupRead(ctx, path, size)
	if p != nil {
		return "", failure("BACKUP_RESTORE_TREE_CHANGED")
	}
	defer func() {
		if lease.Close() != nil {
			digest = ""
			problem = failure("BACKUP_SOURCE_CLOSE_FAILED")
		}
	}()
	if lease.Size() != size {
		return "", failure("BACKUP_RESTORE_TREE_CHANGED")
	}
	hash := sha256.New()
	count, err := io.CopyBuffer(hash, io.NewSectionReader(lease, 0, lease.Size()), make([]byte, 64<<10))
	if ctx.Err() != nil {
		return "", failure("BACKUP_CANCELED")
	}
	if err != nil || count != size || lease.Check() != nil {
		return "", failure("BACKUP_RESTORE_TREE_CHANGED")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
