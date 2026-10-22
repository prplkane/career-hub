package homebackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path"
	"strings"

	"careerhub.local/backend/internal/runtimepaths"
)

const restorePrefix = ".career_hub_staging/restores/"

// withExtractedRestore is internal preparation, not restore authorization. The
// maintenance owner must separately admit destination identity/channel/recovery,
// stop all writers and validate both active and staged semantics. No active root,
// marker, journal or credential is changed here. Retain partial staging on every
// outcome; never adopt an existing namespace or retry an uncertain file write.
// consume may validate, not activate: the archive and namespace remain pinned
// until it returns, and late close failures invalidate readiness, not disk facts.
func withExtractedRestore(ctx context.Context, root *runtimepaths.Root, archivePath, snapshot string, consume func(Manifest) *Error) (result snapshotPreparation, problem *Error) {
	result.Namespace = runtimepaths.DirectoryNotCreated
	result.LastFile = runtimepaths.StreamPublication{Outcome: runtimepaths.FileNotPublished, CleanupConfirmed: true}
	if ctx == nil || ctx.Err() != nil {
		return result, failure("BACKUP_CANCELED")
	}
	if consume == nil || !strings.HasPrefix(snapshot, restorePrefix) || !lowerHex(strings.TrimPrefix(snapshot, restorePrefix), 32) {
		return result, failure("BACKUP_SNAPSHOT_INVALID")
	}
	if !strings.HasPrefix(archivePath, "backups/") || !strings.HasSuffix(archivePath, ".zip") || !safeName(archivePath) {
		return result, failure("BACKUP_SOURCE_UNAVAILABLE")
	}
	if root == nil || root.RequireProcessOwnership() != nil {
		return result, failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	lease, pathProblem := root.OpenBackupRead(ctx, archivePath, MaxArchiveBytes)
	if pathProblem != nil {
		return result, failure("BACKUP_SOURCE_UNAVAILABLE")
	}
	defer func() {
		if lease.Close() != nil {
			problem = failure("BACKUP_SOURCE_CLOSE_FAILED")
		}
	}()
	archive, problem := Inspect(ctx, lease, lease.Size())
	if problem != nil {
		return result, problem
	}
	manifest := archive.Manifest()
	if problem = validatePayloadPaths(manifest.Files); problem != nil {
		return result, problem
	}
	for _, file := range manifest.Files {
		// Check the full prefixed path before creating anything: portable device
		// names, Unicode/depth/length rules and existing unsafe ancestors apply
		// even to a currently missing staging directory. This reserves nothing.
		if ctx.Err() != nil {
			return result, failure("BACKUP_CANCELED")
		}
		if root.Check(snapshot+"/"+file.RelativePath) != nil {
			return result, failure("BACKUP_PAYLOAD_PATH_INVALID")
		}
	}
	// Reject corrupt payload before creating a staging namespace. The same lease
	// remains open while extracting; size/CRC/SHA are checked again per entry.
	if problem = archive.Verify(ctx); problem != nil {
		return result, problem
	}
	if _, pathProblem = root.EnsureDirectory(strings.TrimSuffix(restorePrefix, "/")); pathProblem != nil {
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
	for _, file := range manifest.Files {
		if ctx.Err() != nil {
			return result, failure("BACKUP_CANCELED")
		}
		if _, pathProblem = root.EnsureDirectory(path.Dir(snapshot + "/" + file.RelativePath)); pathProblem != nil {
			return result, failure("BACKUP_SNAPSHOT_UNAVAILABLE")
		}
		produce := func(out io.Writer) error {
			reader, err := archive.entries[file.RelativePath].Open()
			if err != nil {
				return failure("BACKUP_PAYLOAD_INVALID")
			}
			hash := sha256.New()
			written, err := io.CopyBuffer(io.MultiWriter(out, hash), io.LimitReader(reader, file.SizeBytes+1), make([]byte, 64<<10))
			closed := reader.Close()
			if err != nil || closed != nil || written != file.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
				return failure("BACKUP_PAYLOAD_INVALID")
			}
			return nil
		}
		verify := func(_ io.ReaderAt, size int64) error {
			// The primitive independently rereads every byte and binds the digest
			// to the producer's verified stream before no-replace publication.
			if size != file.SizeBytes {
				return failure("BACKUP_PAYLOAD_INVALID")
			}
			return nil
		}
		result.LastFile, pathProblem = root.CreateBackupStream(ctx, snapshot+"/"+file.RelativePath, file.SizeBytes, produce, verify)
		if result.LastFile.Outcome == runtimepaths.FilePublished {
			result.CopiedFiles++
		}
		if pathProblem != nil {
			if ctx.Err() != nil {
				return result, failure("BACKUP_CANCELED")
			}
			return result, failure("BACKUP_RESTORE_EXTRACTION_FAILED")
		}
	}
	if lease.Check() != nil || reservation.Check() != nil {
		return result, failure("BACKUP_SNAPSHOT_CHANGED")
	}
	problem = consume(archive.Manifest())
	if ctx.Err() != nil {
		return result, failure("BACKUP_CANCELED")
	}
	if lease.Check() != nil || reservation.Check() != nil {
		return result, failure("BACKUP_SNAPSHOT_CHANGED")
	}
	return result, problem
}
