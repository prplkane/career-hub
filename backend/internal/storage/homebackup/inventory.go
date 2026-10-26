package homebackup

import (
	"context"
	"sort"
	"strings"
	"time"

	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/textcompat"
)

const maxInventoryNodes = 2*MaxFiles + 8

type inventoryFile struct {
	RelativePath string
	SizeBytes    int64
	Modified     time.Time
}

type inventory struct {
	Files         []inventoryFile
	Directories   []string
	Database      bool
	DatabaseIndex int
	Companions    []string
}

type inventoryReader interface {
	ListDirectoryEntries(string) ([]runtimepaths.DirectoryEntry, bool, *runtimepaths.Error)
}

func selectedRoots(values []string) ([]string, *Error) {
	if values == nil {
		return []string{"applications", "career_vault", "documents", "jobs", "outputs", "resume_versions", "templates", "logs"}, nil
	}
	if len(values) == 0 || len(values) > 8 {
		return nil, failure("BACKUP_SELECTION_INVALID")
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !allowedRoot(value) || seen[value] {
			return nil, failure("BACKUP_SELECTION_INVALID")
		}
		seen[value] = true
	}
	return append([]string{}, values...), nil
}

// Inventory is read-only and does not hash/copy files. SQLite companions are
// recorded separately, never treated as ordinary ZIP payload: the consistent
// database image must come from the reserved-writer snapshot helper. Ordinary
// file limits here exclude that image; the assembled manifest must check the
// combined file count and size again before publication.
func collectInventory(ctx context.Context, source inventoryReader, selection []string) (*inventory, *Error) {
	return collectInventoryMode(ctx, source, selection, false)
}

// collectRestoreInventory includes physical database/companion bytes. These are
// rollback witnesses, never a substitute for the normalized export image or
// logical database validation. The quiescent restore owner calls this only after
// releasing source snapshot/validation connections.
func collectRestoreInventory(ctx context.Context, source inventoryReader, selection []string) (*inventory, *Error) {
	return collectInventoryMode(ctx, source, selection, true)
}

func collectInventoryMode(ctx context.Context, source inventoryReader, selection []string, physical bool) (*inventory, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("BACKUP_CANCELED")
	}
	if source == nil {
		return nil, failure("BACKUP_SOURCE_UNAVAILABLE")
	}
	roots, problem := selectedRoots(selection)
	if problem != nil {
		return nil, problem
	}
	result := &inventory{Files: []inventoryFile{}, Directories: []string{}, Companions: []string{}}
	nodes := 0
	var total int64
	seen := map[string]bool{}
	var visit func(string, int, bool) *Error
	visit = func(relative string, depth int, required bool) *Error {
		if ctx.Err() != nil {
			return failure("BACKUP_CANCELED")
		}
		entries, present, pathProblem := source.ListDirectoryEntries(relative)
		if ctx.Err() != nil {
			return failure("BACKUP_CANCELED")
		}
		if pathProblem != nil {
			return failure("BACKUP_SOURCE_UNAVAILABLE")
		}
		if !present {
			if required {
				return failure("BACKUP_SOURCE_CHANGED")
			}
			return nil
		}
		result.Directories = append(result.Directories, relative)
		if len(entries) > runtimepaths.MaxDirectoryEntries {
			return failure("BACKUP_INVENTORY_LIMIT")
		}
		// Copy the reader's slice before imposing the source's casefold ordering.
		entries = append([]runtimepaths.DirectoryEntry{}, entries...)
		sort.SliceStable(entries, func(i, j int) bool { return textcompat.Fold(entries[i].Name) < textcompat.Fold(entries[j].Name) })
		for _, entry := range entries {
			if ctx.Err() != nil {
				return failure("BACKUP_CANCELED")
			}
			nodes++
			if nodes > maxInventoryNodes || depth+1 > runtimepaths.MaxComponents {
				return failure("BACKUP_INVENTORY_LIMIT")
			}
			if !safeName(entry.Name) || strings.Contains(entry.Name, "/") {
				return failure("BACKUP_PATH_INVALID")
			}
			name := relative + "/" + entry.Name
			if len(name) > runtimepaths.MaxPathBytes {
				return failure("BACKUP_PATH_INVALID")
			}
			key := textcompat.Fold(name)
			if seen[key] {
				return failure("BACKUP_PATH_COLLISION")
			}
			seen[key] = true
			isDatabase := key == databasePath
			isCompanion := key == databasePath+"-wal" || key == databasePath+"-shm" || key == databasePath+"-journal"
			if entry.IsDirectory {
				if isDatabase || isCompanion {
					return failure("BACKUP_DATABASE_PATH_INVALID")
				}
				if p := visit(name, depth+1, true); p != nil {
					return p
				}
				continue
			}
			if isDatabase && !physical {
				result.Database = true
				result.DatabaseIndex = len(result.Files)
				continue
			}
			if isCompanion && !physical {
				result.Companions = append(result.Companions, name)
				continue
			}
			if entry.Size < 0 || entry.Size > MaxEntryBytes {
				return failure("BACKUP_FILE_LIMIT")
			}
			if total > MaxTotalBytes-entry.Size {
				return failure("BACKUP_TOTAL_LIMIT")
			}
			total += entry.Size
			if len(result.Files) >= MaxFiles {
				return failure("BACKUP_FILE_LIMIT")
			}
			result.Files = append(result.Files, inventoryFile{RelativePath: name, SizeBytes: entry.Size, Modified: entry.Modified})
		}
		return nil
	}
	for _, root := range roots {
		if p := visit(root, 1, false); p != nil {
			return nil, p
		}
	}
	if len(result.Companions) > 0 && !result.Database {
		return nil, failure("BACKUP_DATABASE_ORPHANED_FILES")
	}
	return result, nil
}
