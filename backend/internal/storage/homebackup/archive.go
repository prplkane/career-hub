package homebackup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"

	"careerhub.local/backend/internal/textcompat"
)

// An explicit metadata-read budget prevents unbounded central-directory reads
// before zip.NewReader returns. This is an I/O bound, not a peak-heap claim.
const MaxMetadataReadBytes int64 = 128 << 20
const MaxArchiveBytes int64 = MaxTotalBytes + MaxMetadataReadBytes

type archiveSource struct {
	source    io.ReaderAt
	ctx       context.Context
	remaining int64
	metadata  bool
}

func (s *archiveSource) ReadAt(p []byte, off int64) (int, error) {
	if s.ctx != nil && s.ctx.Err() != nil {
		return 0, s.ctx.Err()
	}
	if s.metadata && int64(len(p)) > s.remaining {
		return 0, failure("BACKUP_METADATA_LIMIT")
	}
	n, err := s.source.ReadAt(p, off)
	if s.metadata {
		s.remaining -= int64(n)
	}
	return n, err
}

type Archive struct {
	manifest Manifest
	entries  map[string]*zip.File
}

// Inspect reads metadata and a bounded manifest only. Keep the ReaderAt open and
// immutable until Verify/restore finishes; this function does not own a file or
// grant a filesystem lease, credential access or extraction authority.
func Inspect(ctx context.Context, source io.ReaderAt, size int64) (archive *Archive, problem *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("BACKUP_CANCELED")
	}
	defer func() {
		if ctx.Err() != nil {
			archive, problem = nil, failure("BACKUP_CANCELED")
		}
	}()
	if source == nil || size < 0 || size > MaxArchiveBytes {
		return nil, failure("BACKUP_SIZE_LIMIT")
	}
	input := &archiveSource{source: source, ctx: ctx, remaining: MaxMetadataReadBytes, metadata: true}
	z, err := zip.NewReader(input, size)
	if err != nil {
		return nil, failure("BACKUP_ARCHIVE_INVALID")
	}
	input.metadata = false
	if len(z.File) > MaxFiles+1 {
		return nil, failure("BACKUP_FILE_LIMIT")
	}
	entries := make(map[string]*zip.File, len(z.File))
	seen := make(map[string]bool, len(z.File))
	var manifestFile *zip.File
	for _, item := range z.File {
		key := textcompat.Fold(item.Name)
		if !safeName(item.Name) || !item.Mode().IsRegular() || item.Flags&1 != 0 {
			return nil, failure("BACKUP_ENTRY_INVALID")
		}
		if seen[key] {
			return nil, failure("BACKUP_PATH_COLLISION")
		}
		seen[key] = true
		if item.Name == ManifestName {
			manifestFile = item
		} else {
			entries[item.Name] = item
		}
	}
	if manifestFile == nil || manifestFile.UncompressedSize64 > MaxManifestBytes {
		return nil, failure("BACKUP_MANIFEST_INVALID")
	}
	r, err := manifestFile.Open()
	if err != nil {
		return nil, failure("BACKUP_MANIFEST_INVALID")
	}
	raw, err := io.ReadAll(io.LimitReader(r, MaxManifestBytes+1))
	closeErr := r.Close()
	if err != nil || closeErr != nil || uint64(len(raw)) != manifestFile.UncompressedSize64 {
		return nil, failure("BACKUP_MANIFEST_INVALID")
	}
	manifest, problem := ParseManifest(raw)
	if problem != nil {
		return nil, problem
	}
	if len(entries) != len(manifest.Files) {
		return nil, failure("BACKUP_MEMBERSHIP_INVALID")
	}
	for _, item := range manifest.Files {
		entry, ok := entries[item.RelativePath]
		if !ok || entry.UncompressedSize64 != uint64(item.SizeBytes) {
			return nil, failure("BACKUP_MEMBERSHIP_INVALID")
		}
	}
	if ctx.Err() != nil {
		return nil, failure("BACKUP_CANCELED")
	}
	// Inspection's cancellation scope ends here. Verify has its own context;
	// retaining this context would make a successfully inspected archive unusable
	// after the caller cancels its inspection deadline.
	input.ctx = nil
	return &Archive{manifest, entries}, nil
}

func (a *Archive) Manifest() Manifest {
	if a == nil {
		return Manifest{}
	}
	out := a.manifest
	out.Files = append([]File{}, out.Files...)
	if out.SQLiteSchemaVersion != nil {
		n := *out.SQLiteSchemaVersion
		out.SQLiteSchemaVersion = &n
	}
	return out
}

// Verify streams every exact retained entry with the declared byte ceiling and
// verifies ZIP CRC plus manifest SHA-256. It writes nothing and cannot repair an
// inconsistent archive. Callers must not treat Inspect alone as payload proof.
// Cancellation is cooperative between reads; ReaderAt must not block indefinitely.
func (a *Archive) Verify(ctx context.Context) *Error {
	if ctx == nil || ctx.Err() != nil {
		return failure("BACKUP_CANCELED")
	}
	if a == nil || a.entries == nil {
		return failure("BACKUP_ARCHIVE_INVALID")
	}
	buffer := make([]byte, 64<<10)
	for _, item := range a.manifest.Files {
		if ctx.Err() != nil {
			return failure("BACKUP_CANCELED")
		}
		r, err := a.entries[item.RelativePath].Open()
		if err != nil {
			return failure("BACKUP_PAYLOAD_INVALID")
		}
		hash := sha256.New()
		limited := io.LimitReader(r, item.SizeBytes+1)
		var read int64
		for {
			if ctx.Err() != nil {
				_ = r.Close()
				return failure("BACKUP_CANCELED")
			}
			n, e := limited.Read(buffer)
			read += int64(n)
			_, _ = hash.Write(buffer[:n])
			if read > item.SizeBytes {
				_ = r.Close()
				return failure("BACKUP_PAYLOAD_INVALID")
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				_ = r.Close()
				return failure("BACKUP_PAYLOAD_INVALID")
			}
		}
		if r.Close() != nil || read != item.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != item.SHA256 {
			return failure("BACKUP_PAYLOAD_INVALID")
		}
	}
	if ctx.Err() != nil {
		return failure("BACKUP_CANCELED")
	}
	return nil
}
