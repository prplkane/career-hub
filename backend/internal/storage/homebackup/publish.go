package homebackup

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"strings"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

const snapshotPrefix = ".career_hub_staging/backups/"

// PublishSnapshot packages an already prepared coherent snapshot. It does not
// walk or snapshot live application data. The maintenance owner must first
// quiesce managed writers, copy SQLite with its backup API, and validate the
// complete staged payload. Each source here remains pinned while copied.
// A successful result is a verified archive, not an applied restore.
func PublishSnapshot(ctx context.Context, root *runtimepaths.Root, snapshot, destination string, input Manifest) (runtimepaths.StreamPublication, *Error) {
	initial := runtimepaths.StreamPublication{Outcome: runtimepaths.FileNotPublished, CleanupConfirmed: true}
	if ctx == nil || ctx.Err() != nil {
		return initial, failure("BACKUP_CANCELED")
	}
	if !strings.HasPrefix(snapshot, snapshotPrefix) || !lowerHex(strings.TrimPrefix(snapshot, snapshotPrefix), 32) {
		return initial, failure("BACKUP_SNAPSHOT_INVALID")
	}
	if !strings.HasPrefix(destination, "backups/") || !strings.HasSuffix(destination, ".zip") || !safeName(destination) {
		return initial, failure("BACKUP_DESTINATION_INVALID")
	}
	// Bound the typed input before encoding; ParseManifest additionally checks
	// the exact encoded byte limit. This avoids an unbounded extra JSON copy.
	if len(input.Files) > MaxFiles {
		return initial, failure("BACKUP_MANIFEST_INVALID")
	}
	remaining := MaxManifestBytes
	for _, value := range []string{input.DataHomeID, input.DataHomeChannel} {
		if len(value) > remaining {
			return initial, failure("BACKUP_MANIFEST_INVALID")
		}
		remaining -= len(value)
	}
	for _, file := range input.Files {
		for _, value := range []string{file.RelativePath, file.SHA256} {
			if len(value) > remaining {
				return initial, failure("BACKUP_MANIFEST_INVALID")
			}
			remaining -= len(value)
		}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return initial, failure("BACKUP_MANIFEST_INVALID")
	}
	manifest, problem := ParseManifest(raw)
	if problem != nil {
		return initial, problem
	}
	// Retain only parsed, private metadata; caller edits cannot change the later
	// file selection/digest contract during producer and verifier callbacks.
	raw, err = json.Marshal(manifest)
	if err != nil {
		return initial, failure("BACKUP_MANIFEST_INVALID")
	}
	var producerProblem, verifyProblem *Error
	produce := func(out io.Writer) error {
		producerProblem = writeSnapshot(ctx, root, snapshot, manifest, raw, out)
		if producerProblem != nil {
			return producerProblem
		}
		return nil
	}
	verify := func(source io.ReaderAt, size int64) error {
		archive, p := Inspect(ctx, source, size)
		if p == nil {
			p = archive.Verify(ctx)
		}
		verifyProblem = p
		if p != nil {
			return p
		}
		return nil
	}
	result, pathProblem := root.CreateBackupStream(ctx, destination, MaxArchiveBytes, produce, verify)
	if pathProblem != nil {
		if producerProblem != nil {
			return result, producerProblem
		}
		if verifyProblem != nil {
			return result, verifyProblem
		}
		if ctx.Err() != nil {
			return result, failure("BACKUP_CANCELED")
		}
		return result, failure("BACKUP_PUBLICATION_FAILED")
	}
	return result, nil
}

func writeSnapshot(ctx context.Context, root *runtimepaths.Root, snapshot string, manifest Manifest, raw []byte, out io.Writer) *Error {
	w := zip.NewWriter(out)
	entry, err := w.Create(ManifestName)
	if err != nil {
		return failure("BACKUP_ARCHIVE_WRITE_FAILED")
	}
	if _, err = entry.Write(raw); err != nil {
		return failure("BACKUP_ARCHIVE_WRITE_FAILED")
	}
	buffer := make([]byte, 64<<10)
	for _, file := range manifest.Files {
		if ctx.Err() != nil {
			return failure("BACKUP_CANCELED")
		}
		if problem := copySnapshotEntry(ctx, root, snapshot, file, w, buffer); problem != nil {
			return problem
		}
	}
	if err := w.Close(); err != nil {
		return failure("BACKUP_ARCHIVE_WRITE_FAILED")
	}
	return nil
}

func copySnapshotEntry(ctx context.Context, root *runtimepaths.Root, snapshot string, file File, w *zip.Writer, buffer []byte) (problem *Error) {
	lease, p := root.OpenBackupRead(ctx, snapshot+"/"+file.RelativePath, MaxEntryBytes)
	if p != nil {
		return failure("BACKUP_SNAPSHOT_UNAVAILABLE")
	}
	defer func() {
		if lease.Close() != nil {
			problem = failure("BACKUP_SOURCE_CLOSE_FAILED")
		}
	}()
	if lease.Size() != file.SizeBytes {
		return failure("BACKUP_SNAPSHOT_CHANGED")
	}
	header := &zip.FileHeader{Name: file.RelativePath, Method: zip.Deflate}
	header.SetMode(0600)
	entry, err := w.CreateHeader(header)
	if err != nil {
		return failure("BACKUP_ARCHIVE_WRITE_FAILED")
	}
	n, err := io.CopyBuffer(entry, io.NewSectionReader(lease, 0, lease.Size()), buffer)
	if ctx.Err() != nil {
		return failure("BACKUP_CANCELED")
	}
	if err != nil || n != file.SizeBytes {
		return failure("BACKUP_ARCHIVE_WRITE_FAILED")
	}
	return nil
}
