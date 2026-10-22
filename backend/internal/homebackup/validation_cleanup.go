package homebackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"slices"

	"careerhub.local/backend/internal/runtimepaths"
)

// Validation adds at most the four canonical physical SQLite files to a
// normalized payload. These are private-copy limits, not larger archive limits.
var validationCleanupLimits = snapshotCleanupLimits{MaxFiles + 4, maxInventoryNodes + 16, MaxTotalBytes + 4*MaxEntryBytes}

const MaxValidationCleanupFiles = MaxFiles + 4
const MaxValidationCleanupDirectories = maxInventoryNodes + 17 // Includes the stage root.

// Never serialized, recovered by directory scan or returned to a renderer.
// The producing validator captures this only for its own newly reserved stage.
// It is evidence, not deletion authority; completion and admission are checked
// again by cleanupValidationCopy after all validator/finish handles have closed.
type validationCleanupWitness struct {
	snapshot, scope string
	files           []File
	directories     []string
	used            bool
}

// normalized is the exact validated payload. SQLite may have changed or removed
// its PRIVATE physical companions on close, so capture their current bytes, not
// the original/archived manifest. Unknown files anywhere in the stage refuse the
// witness. No active SQLite connection or active-source hashing occurs here.
func captureValidationCleanup(ctx context.Context, root *runtimepaths.Root, snapshot string, reservation *runtimepaths.DirectoryReservation, normalized []File, physical bool) (*validationCleanupWitness, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("BACKUP_CANCELED")
	}
	if root == nil || root.RequireProcessOwnership() != nil {
		return nil, failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	files, p := cleanupFiles(snapshot, normalized, backupCleanupLimits)
	if p != nil {
		return nil, p
	}
	if reservation == nil || reservation.Check() != nil {
		return nil, failure("BACKUP_SNAPSHOT_CHANGED")
	}
	scope, q := root.CredentialScope()
	if q != nil {
		return nil, failure("BACKUP_CLEANUP_SCOPE_UNAVAILABLE")
	}
	if physical {
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			name := ".physical/" + databasePath + suffix
			present, q := root.Exists(snapshot + "/" + name)
			if q != nil || !present && suffix == "" {
				return nil, failure("BACKUP_CLEANUP_TREE_CHANGED")
			}
			if !present {
				continue
			}
			file, p := fingerprintValidationCopy(ctx, root, snapshot, name)
			if p != nil {
				return nil, p
			}
			files = append(files, file)
		}
	}
	files, p = cleanupFiles(snapshot, files, validationCleanupLimits)
	if p != nil {
		return nil, p
	}
	directories, p := inspectSnapshotCleanup(ctx, root, snapshot, files, validationCleanupLimits.nodes)
	if p != nil {
		return nil, p
	}
	if reservation.Check() != nil {
		return nil, failure("BACKUP_SNAPSHOT_CHANGED")
	}
	return &validationCleanupWitness{snapshot: snapshot, scope: scope.Fingerprint(), files: files, directories: directories}, nil
}

func fingerprintValidationCopy(ctx context.Context, root *runtimepaths.Root, snapshot, name string) (file File, problem *Error) {
	lease, q := root.OpenBackupRead(ctx, snapshot+"/"+name, MaxEntryBytes)
	if q != nil {
		return file, failure("BACKUP_CLEANUP_TREE_CHANGED")
	}
	defer func() {
		if lease.Close() != nil {
			file, problem = File{}, failure("BACKUP_SOURCE_CLOSE_FAILED")
		}
	}()
	hash := sha256.New()
	n, err := io.CopyBuffer(hash, io.NewSectionReader(lease, 0, lease.Size()), make([]byte, 64<<10))
	if ctx.Err() != nil {
		return file, failure("BACKUP_CANCELED")
	}
	if err != nil || n != lease.Size() || lease.Check() != nil {
		return file, failure("BACKUP_CLEANUP_TREE_CHANGED")
	}
	return File{RelativePath: name, SizeBytes: n, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

// Called only after terminal owner success; the result remains separate from the
// already committed restore. It must never cause an automatic restore retry.
func cleanupValidationCopy(ctx context.Context, root *runtimepaths.Root, channel string, witness *validationCleanupWitness, finish restoreFinishResult, ops snapshotCleanupOperations) (SnapshotCleanupResult, *Error) {
	unstarted := unstartedSnapshotCleanup()
	if witness == nil || witness.used {
		return unstarted, failure("BACKUP_CLEANUP_PERMIT_INVALID")
	}
	witness.used = true
	if !finish.Complete || !finish.SemanticValidated || finish.JournalRemoval != restoreRemoved ||
		finish.Receipt.Outcome != runtimepaths.FilePublished || !finish.Receipt.CleanupConfirmed ||
		(finish.MarkerPublication != runtimepaths.FilePublished && finish.MarkerPublication != runtimepaths.FileNotPublished) {
		return unstarted, failure("BACKUP_RESTORE_COMMIT_UNCONFIRMED")
	}
	scope, p := snapshotCleanupAdmission(ctx, root, channel)
	if p != nil {
		return unstarted, p
	}
	if scope != witness.scope {
		return unstarted, failure("BACKUP_CLEANUP_SCOPE_CHANGED")
	}
	permit, p := prepareSnapshotCleanupWithLimits(ctx, root, channel, witness.snapshot, witness.files, validationCleanupLimits)
	if p != nil {
		return unstarted, p
	}
	if !slices.Equal(permit.directories, witness.directories) {
		return unstarted, failure("BACKUP_CLEANUP_TREE_CHANGED")
	}
	return consumeSnapshotCleanup(ctx, root, permit, ops)
}
