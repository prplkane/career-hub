package homebackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path"
	"reflect"
	"strings"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

type snapshotPreparation struct {
	Namespace   runtimepaths.DirectoryCreationOutcome
	CopiedFiles int
	LastFile    runtimepaths.StreamPublication
}

// withPreparedSnapshot is a staging component, not a complete backup command.
// The admitted maintenance caller must stop managed and legacy/file writers,
// validate the full staged payload in consume, and preserve publication facts
// independently if consume publishes an archive before subsequent cleanup fails.
// A metadata rescan detects observed drift, not hostile same-user/ABA mutation.
// Namespace and partial contents are retained on every outcome, never retried.
func withPreparedSnapshot(ctx context.Context, dataRoot, snapshot string, selection []string, consume func(*runtimepaths.Root, []File, *int64) *Error) (result snapshotPreparation, problem *Error) {
	return prepareSnapshotWith(ctx, dataRoot, snapshot, selection, consume, nil)
}

// afterCopy is a private deterministic drift seam, never a product callback.
func prepareSnapshotWith(ctx context.Context, dataRoot, snapshot string, selection []string, consume func(*runtimepaths.Root, []File, *int64) *Error, afterCopy func()) (result snapshotPreparation, problem *Error) {
	result.Namespace = runtimepaths.DirectoryNotCreated
	result.LastFile = runtimepaths.StreamPublication{Outcome: runtimepaths.FileNotPublished, CleanupConfirmed: true}
	if ctx == nil || ctx.Err() != nil {
		return result, failure("BACKUP_CANCELED")
	}
	if consume == nil || !strings.HasPrefix(snapshot, snapshotPrefix) || !lowerHex(strings.TrimPrefix(snapshot, snapshotPrefix), 32) {
		return result, failure("BACKUP_SNAPSHOT_INVALID")
	}
	selected, p := selectedRoots(selection)
	if p != nil {
		return result, p
	}
	root, pathProblem := runtimepaths.Open(dataRoot)
	if pathProblem != nil {
		return result, failure("BACKUP_SOURCE_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			problem = failure("BACKUP_SNAPSHOT_CLOSE_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return result, failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	if _, p := root.EnsureDirectory(strings.TrimSuffix(snapshotPrefix, "/")); p != nil {
		return result, failure("BACKUP_SNAPSHOT_UNAVAILABLE")
	}
	reservation, outcome, pathProblem := root.ReserveDirectory(snapshot)
	result.Namespace = outcome
	if pathProblem != nil {
		return result, failure("BACKUP_SNAPSHOT_RESERVATION_FAILED")
	}
	defer func() {
		if reservation.Close() != nil {
			problem = failure("BACKUP_SNAPSHOT_CLOSE_FAILED")
		}
	}()
	problem = withDatabaseSnapshot(ctx, dataRoot, snapshot, func(source *runtimepaths.Root, database *databaseSnapshot) *Error {
		before, p := collectInventory(ctx, source, selected)
		if p != nil {
			return p
		}
		if before.Database && database == nil {
			return failure("BACKUP_SOURCE_CHANGED")
		}
		files := make([]File, 0, len(before.Files)+1)
		var schema *int64
		if before.Database {
			value := database.SchemaVersion
			schema = &value
		}
		var total int64
		for _, entry := range before.Files {
			total += entry.SizeBytes
		}
		if before.Database {
			if len(before.Files)+1 > MaxFiles || database.File.SizeBytes > MaxTotalBytes-total {
				return failure("BACKUP_TOTAL_LIMIT")
			}
		}
		for _, directory := range before.Directories {
			if ctx.Err() != nil {
				return failure("BACKUP_CANCELED")
			}
			if _, p := source.EnsureDirectory(snapshot + "/" + directory); p != nil {
				return failure("BACKUP_SNAPSHOT_UNAVAILABLE")
			}
		}
		for index, entry := range before.Files {
			if before.Database && index == before.DatabaseIndex {
				files = append(files, database.File)
				result.CopiedFiles++
			}
			file, facts, p := copyStagedFile(ctx, source, snapshot, entry)
			result.LastFile = facts
			if p != nil {
				return p
			}
			files = append(files, file)
			result.CopiedFiles++
		}
		if before.Database && before.DatabaseIndex == len(before.Files) {
			files = append(files, database.File)
			result.CopiedFiles++
		}
		if afterCopy != nil {
			afterCopy()
		}
		after, p := collectInventory(ctx, source, selected)
		if p != nil {
			return p
		}
		if !reflect.DeepEqual(before.Files, after.Files) || !reflect.DeepEqual(before.Directories, after.Directories) || before.Database != after.Database || before.DatabaseIndex != after.DatabaseIndex {
			return failure("BACKUP_SOURCE_CHANGED")
		}
		if reservation.Check() != nil {
			return failure("BACKUP_SNAPSHOT_CHANGED")
		}
		return consume(source, files, schema)
	})
	return result, problem
}

func copyStagedFile(ctx context.Context, root *runtimepaths.Root, snapshot string, entry inventoryFile) (file File, result runtimepaths.StreamPublication, problem *Error) {
	result = runtimepaths.StreamPublication{Outcome: runtimepaths.FileNotPublished, CleanupConfirmed: true}
	lease, p := root.OpenBackupRead(ctx, entry.RelativePath, MaxEntryBytes)
	if p != nil {
		return file, result, failure("BACKUP_SOURCE_UNAVAILABLE")
	}
	defer func() {
		if lease.Close() != nil {
			problem = failure("BACKUP_SOURCE_CLOSE_FAILED")
		}
	}()
	if lease.Size() != entry.SizeBytes {
		return file, result, failure("BACKUP_SOURCE_CHANGED")
	}
	if _, p := root.EnsureDirectory(path.Dir(snapshot + "/" + entry.RelativePath)); p != nil {
		return file, result, failure("BACKUP_SNAPSHOT_UNAVAILABLE")
	}
	digest := sha256.New()
	produce := func(out io.Writer) error {
		n, err := io.CopyBuffer(io.MultiWriter(out, digest), io.NewSectionReader(lease, 0, lease.Size()), make([]byte, 64<<10))
		if err != nil || n != entry.SizeBytes {
			return failure("BACKUP_SOURCE_COPY_FAILED")
		}
		return nil
	}
	// CreateBackupStream independently hashes/re-reads every produced byte before
	// its no-replace publication. This callback additionally binds expected size.
	verify := func(_ io.ReaderAt, size int64) error {
		if size != entry.SizeBytes {
			return failure("BACKUP_SOURCE_CHANGED")
		}
		return nil
	}
	result, p = root.CreateBackupStream(ctx, snapshot+"/"+entry.RelativePath, MaxEntryBytes, produce, verify)
	if p != nil {
		if ctx.Err() != nil {
			return file, result, failure("BACKUP_CANCELED")
		}
		return file, result, failure("BACKUP_SOURCE_COPY_FAILED")
	}
	file = File{RelativePath: entry.RelativePath, SizeBytes: entry.SizeBytes, SHA256: hex.EncodeToString(digest.Sum(nil))}
	return file, result, nil
}
