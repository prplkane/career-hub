package runtimepaths

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
)

type SQLiteFingerprint struct {
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// SyncAndFingerprint flushes and streams one stable, already guarded file after
// all native SQLite writers/backup handles have closed. It never exposes bytes
// or a raw handle. The caller keeps its Root and process ownership alive. File
// flush plus identity/size/time checks do not promise power-loss durability of
// directory entries or defeat a hostile same-account in-place writer.
func (g *SQLiteFiles) SyncAndFingerprint(ctx context.Context, maxBytes int64) (result SQLiteFingerprint, problem *Error) {
	if ctx == nil || ctx.Err() != nil {
		return result, failure("SQLITE_FILE_CANCELED")
	}
	if g == nil || maxBytes <= 0 || maxBytes > 512<<20 {
		return result, failure("SQLITE_FILE_LIMIT")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return result, failure("SQLITE_FILES_CLOSED")
	}
	if e := g.check(); e != nil {
		return result, e
	}
	if e := g.syncMain(); e != nil {
		return result, e
	}
	before, err := g.main.Stat()
	if err != nil {
		return result, failure("FILE_STAT")
	}
	if before.Size() < 0 || before.Size() > maxBytes {
		return result, failure("SQLITE_FILE_LIMIT")
	}
	digest := sha256.New()
	reader := io.NewSectionReader(g.main, 0, before.Size())
	buffer := make([]byte, 64<<10)
	defer clear(buffer)
	var size int64
	for {
		if ctx.Err() != nil {
			return result, failure("SQLITE_FILE_CANCELED")
		}
		n, err := reader.Read(buffer)
		if n > 0 {
			_, _ = digest.Write(buffer[:n])
			size += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil || n == 0 {
			return result, failure("FILE_READ")
		}
	}
	after, err := g.main.Stat()
	if err != nil || size != before.Size() || after.Size() != before.Size() || !before.ModTime().Equal(after.ModTime()) {
		return result, failure("PATH_CHANGED")
	}
	if e := g.check(); e != nil {
		return result, e
	}
	if ctx.Err() != nil {
		return result, failure("SQLITE_FILE_CANCELED")
	}
	return SQLiteFingerprint{SizeBytes: size, SHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}
