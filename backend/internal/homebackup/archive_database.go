package homebackup

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path"
	"strings"

	"careerhub.local/backend/internal/runtimepaths"
)

// validateArchiveDatabase is preparation only. The restore owner must admit the
// archive, pin its extraction and stop writers before calling it. SQLite opens
// only a fresh manifest-checked copy; archived physical bytes remain the candidate
// and journal evidence. The caller retains all staging/failure facts and must not
// authorize root moves merely because database validation succeeds.
func validateArchiveDatabase(s payloadSource, dataRoot, snapshot string, expected *int64) (version *int64, count int, facts snapshotPreparation, witness *validationCleanupWitness, problem *Error) {
	defer func() {
		if problem != nil {
			witness = nil
		}
	}()
	facts.Namespace = runtimepaths.DirectoryNotCreated
	facts.LastFile = runtimepaths.StreamPublication{Outcome: runtimepaths.FileNotPublished, CleanupConfirmed: true}
	if s.ctx == nil || s.ctx.Err() != nil {
		return nil, 0, facts, nil, failure("BACKUP_CANCELED")
	}
	stage := strings.TrimSuffix(s.prefix, "/")
	if s.prefix != stage+"/" || !strings.HasPrefix(stage, restorePrefix) || !lowerHex(strings.TrimPrefix(stage, restorePrefix), 32) ||
		!strings.HasPrefix(snapshot, snapshotPrefix) || !lowerHex(strings.TrimPrefix(snapshot, snapshotPrefix), 32) {
		return nil, 0, facts, nil, failure("BACKUP_SNAPSHOT_INVALID")
	}
	if s.root == nil || s.root.RequireProcessOwnership() != nil {
		return nil, 0, facts, nil, failure("BACKUP_OWNERSHIP_REQUIRED")
	}
	_, hasDB := s.files[databasePath]
	if !hasDB {
		// Preserve the existing absent/schema/orphan refusal without creating a copy.
		version, count, problem = validatePayloadDatabase(s, dataRoot, expected)
		return
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if f, ok := s.files[databasePath+suffix]; ok && (f.RelativePath != databasePath+suffix || f.SizeBytes < 0 || f.SizeBytes > MaxEntryBytes || !lowerHex(f.SHA256, 64)) {
			return nil, 0, facts, nil, failure("BACKUP_MANIFEST_INVALID")
		}
	}
	if _, p := s.root.EnsureDirectory(strings.TrimSuffix(snapshotPrefix, "/")); p != nil {
		return nil, 0, facts, nil, failure("BACKUP_SNAPSHOT_UNAVAILABLE")
	}
	reservation, outcome, p := s.root.ReserveDirectory(snapshot)
	facts.Namespace = outcome
	if p != nil {
		return nil, 0, facts, nil, failure("BACKUP_SNAPSHOT_RESERVATION_FAILED")
	}
	defer func() {
		if reservation.Close() != nil {
			version, count, problem = nil, 0, failure("BACKUP_SNAPSHOT_CLOSE_FAILED")
		}
	}()
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		file, exists := s.files[databasePath+suffix]
		if !exists {
			continue
		}
		var q *Error
		facts.LastFile, q = copyArchiveDatabaseFile(s, snapshot+"/.physical/"+file.RelativePath, file)
		if facts.LastFile.Outcome == runtimepaths.FilePublished {
			facts.CopiedFiles++
		}
		if q != nil {
			return nil, 0, facts, nil, q
		}
	}
	var normalized []File
	problem = withDatabaseSnapshotFrom(s.ctx, dataRoot, snapshot, snapshot+"/.physical/"+databasePath, func(root *runtimepaths.Root, database *databaseSnapshot) *Error {
		if database == nil {
			return failure("BACKUP_DATABASE_VERIFY_FAILED")
		}
		copy := payloadSource{ctx: s.ctx, root: root, prefix: snapshot + "/", files: map[string]File{databasePath: database.File}}
		normalized = []File{database.File}
		var q *Error
		version, count, q = validatePayloadDatabaseFrom(s, copy, dataRoot, expected)
		return q
	})
	if reservation.Check() != nil {
		problem = failure("BACKUP_SNAPSHOT_CHANGED")
	}
	if problem == nil {
		witness, problem = captureValidationCleanup(s.ctx, s.root, snapshot, reservation, normalized, true)
	}
	if problem != nil {
		version, count = nil, 0
	}
	return
}

func copyArchiveDatabaseFile(s payloadSource, target string, file File) (facts runtimepaths.StreamPublication, problem *Error) {
	facts = runtimepaths.StreamPublication{Outcome: runtimepaths.FileNotPublished, CleanupConfirmed: true}
	lease, p := s.root.OpenBackupRead(s.ctx, s.prefix+file.RelativePath, MaxEntryBytes)
	if p != nil {
		return facts, failure("BACKUP_SOURCE_UNAVAILABLE")
	}
	defer func() {
		if lease.Close() != nil {
			problem = failure("BACKUP_SOURCE_CLOSE_FAILED")
		}
	}()
	if lease.Size() != file.SizeBytes {
		return facts, failure("BACKUP_PAYLOAD_CHANGED")
	}
	if _, p := s.root.EnsureDirectory(path.Dir(target)); p != nil {
		return facts, failure("BACKUP_SNAPSHOT_UNAVAILABLE")
	}
	produce := func(out io.Writer) error {
		hash := sha256.New()
		n, err := io.CopyBuffer(io.MultiWriter(out, hash), io.NewSectionReader(lease, 0, lease.Size()), make([]byte, 64<<10))
		if err != nil || n != file.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
			return failure("BACKUP_PAYLOAD_CHANGED")
		}
		return nil
	}
	verify := func(_ io.ReaderAt, size int64) error {
		if size != file.SizeBytes {
			return failure("BACKUP_PAYLOAD_CHANGED")
		}
		return nil
	}
	facts, p = s.root.CreateBackupStream(s.ctx, target, MaxEntryBytes, produce, verify)
	if p != nil {
		return facts, failure("BACKUP_DATABASE_COPY_FAILED")
	}
	if lease.Check() != nil {
		return facts, failure("BACKUP_PAYLOAD_CHANGED")
	}
	return facts, nil
}
