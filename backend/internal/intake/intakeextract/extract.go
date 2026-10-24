package intakeextract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"careerhub.local/backend/internal/skills/resolver"
)

// Extract is the pure byte-to-report boundary. It checks the exact source
// bytes against the caller's verified identity before parsing. The future
// Data Home owner remains responsible for path/source-ID authority, catalog
// revision capture, file publication and crash recovery.
func Extract(ctx context.Context, content []byte, source Source, revision string, terms []resolver.Term) (*Report, error) {
	if ctx == nil || len(content) == 0 {
		return nil, ErrInput
	}
	if len(content) > 10<<20 {
		return nil, ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(content)
	if source.SHA256 != hex.EncodeToString(digest[:]) {
		return nil, ErrInput
	}
	var (
		blocks []Block
		err    error
	)
	switch source.FileType {
	case "txt":
		blocks, err = ParseTXTBlocks(content)
	case "docx":
		blocks, err = ParseDOCXBlocks(ctx, content)
	default:
		return nil, ErrInput
	}
	if err != nil {
		return nil, err
	}
	return Build(ctx, source, revision, blocks, terms)
}
