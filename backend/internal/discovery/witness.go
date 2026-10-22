package discovery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"math"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

// RowWitness is the exact typed SQL row identity, not a lossy display record.
// Hashes are private recovery evidence; they contain no captured text or titles.
type RowWitness struct {
	DiscoveryID string `json:"discovery_id"`
	Present     bool   `json:"present"`
	SHA256      string `json:"sha256"`
}

func hashSize(h hash.Hash, n uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], n)
	_, _ = h.Write(b[:])
}
func hashSQLCell(h hash.Hash, value any, remaining *int) bool {
	var data []byte
	var tag byte
	var number uint64
	switch v := value.(type) {
	case nil:
		tag = 0
	case string:
		tag, data = 1, []byte(v)
	case int64:
		tag, number = 2, uint64(v)
	case float64:
		tag, number = 3, math.Float64bits(v)
	case []byte:
		tag, data = 4, v
	default:
		return false
	}
	*remaining -= 9 + len(data)
	if len(data) > MaxCellBytes || *remaining < 0 {
		return false
	}
	_, _ = h.Write([]byte{tag})
	if tag == 1 || tag == 4 {
		hashSize(h, uint64(len(data)))
		_, _ = h.Write(data)
	} else {
		hashSize(h, number)
	}
	return true
}

// MutationWitnesses reads only the selected IDs under the caller's existing
// transaction. It preserves NULL/TEXT/BLOB/INTEGER/REAL distinctions, including
// invalid text bytes in a record being explicitly flushed. No DB is opened.
func MutationWitnesses(ctx context.Context, tx *sql.Tx, ids []string) ([]RowWitness, *WriteError) {
	if ctx == nil || tx == nil {
		return nil, writeFailure("witness", "DATABASE_CONNECTION_INVALID")
	}
	if len(ids) > MaxObservations {
		return nil, writeFailure("witness", "RESULT_LIMIT")
	}
	witnesses := make([]RowWitness, len(ids))
	positions := map[string]int{}
	for i, id := range ids {
		if _, e := writeID(id, "discovery_id"); e != nil {
			return nil, e
		}
		if _, seen := positions[id]; seen {
			return nil, writeFailure("witness", "WITNESS_IDS_INVALID")
		}
		positions[id], witnesses[i] = i, RowWitness{DiscoveryID: id}
	}
	remaining := MaxReadBytes
	for start := 0; start < len(ids); start += 500 {
		if ctx.Err() != nil {
			return nil, writeFailure("context", "WRITE_CANCELED")
		}
		end := min(len(ids), start+500)
		args := make([]any, end-start)
		for i, id := range ids[start:end] {
			args[i] = id
		}
		marks := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
		rows, e := tx.QueryContext(ctx, "SELECT "+columns+" FROM job_discoveries WHERE discovery_id IN ("+marks+")", args...)
		if e != nil {
			return nil, writeFailure("witness", "DATABASE_QUERY_FAILED")
		}
		problem := func() *WriteError {
			count := 0
			for rows.Next() {
				count++
				if count > len(args) {
					return writeFailure("witness", "RESULT_LIMIT")
				}
				cells := make([]any, 20)
				dest := make([]any, 20)
				for i := range cells {
					dest[i] = &cells[i]
				}
				if rows.Scan(dest...) != nil {
					return writeFailure("witness", "DATABASE_SCAN_FAILED")
				}
				id, ok := cells[0].(string)
				index, known := positions[id]
				if !ok || !known || witnesses[index].Present {
					return writeFailure("witness", "WITNESS_IDS_INVALID")
				}
				h := sha256.New()
				_, _ = h.Write([]byte("discovery-row-v1\x00"))
				for _, cell := range cells {
					if !hashSQLCell(h, cell, &remaining) {
						return writeFailure("witness", "RESULT_LIMIT")
					}
				}
				witnesses[index].Present, witnesses[index].SHA256 = true, hex.EncodeToString(h.Sum(nil))
			}
			if rows.Err() != nil {
				return writeFailure("witness", "DATABASE_QUERY_FAILED")
			}
			return nil
		}()
		if rows.Close() != nil {
			return nil, writeFailure("witness", "DATABASE_CLEANUP_FAILED")
		}
		if problem != nil {
			return nil, problem
		}
	}
	return witnesses, nil
}

// CheckPublicationReferences prevents recovery from changing a file that another
// record now references. Rechecks run before any source effects, not just after.
func CheckPublicationReferences(ctx context.Context, tx *sql.Tx, changes []SourceChange) *WriteError {
	if ctx == nil || tx == nil {
		return writeFailure("source", "DATABASE_CONNECTION_INVALID")
	}
	if len(changes) > MaxObservations {
		return writeFailure("source", "RESULT_LIMIT")
	}
	if len(changes) == 0 {
		return nil
	}
	paths := map[string]string{}
	for _, change := range changes {
		id := strings.TrimSuffix(strings.TrimPrefix(change.RelativePath, "jobs/discovery/captures/"), "/source.txt")
		canonical, e := capturePath(id)
		if e != nil || canonical != change.RelativePath {
			return writeFailure("source", "SOURCE_PATH_REJECTED")
		}
		folded := textcompat.Fold(canonical)
		if _, ok := paths[folded]; ok {
			return writeFailure("source", "SOURCE_REFERENCE_CONFLICT")
		}
		paths[folded] = id
	}
	r := &reader{query: tx, ctx: ctx, remaining: MaxReadBytes}
	e := r.rows("source_references", `SELECT discovery_id,source_relative_path FROM job_discoveries WHERE source_relative_path IS NOT NULL AND source_relative_path<>''`, nil, 2, MaxObservations, func(raw []any) error {
		id, e := textCell(raw[0], false)
		if e != nil {
			return e
		}
		path, e := textCell(raw[1], false)
		if e != nil {
			return e
		}
		if owner, ok := paths[textcompat.Fold(strings.ReplaceAll(path, "\\", "/"))]; ok && owner != id {
			return failure("source", "SOURCE_REFERENCE_CONFLICT")
		}
		return nil
	})
	return fromRead(e)
}

// CheckPublicationContent ties proposed committed file bytes to the current
// row, even if somebody edited and re-signed the local integrity envelope.
// The envelope is not a credential or a substitute for a database witness.
func CheckPublicationContent(ctx context.Context, tx *sql.Tx, changes []SourceChange) *WriteError {
	if ctx == nil || tx == nil {
		return writeFailure("source", "DATABASE_CONNECTION_INVALID")
	}
	if len(changes) > MaxObservations {
		return writeFailure("source", "RESULT_LIMIT")
	}
	for _, c := range changes {
		if !c.AfterPresent {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(c.RelativePath, "jobs/discovery/captures/"), "/source.txt")
		path, e := capturePath(id)
		if e != nil || path != c.RelativePath {
			return writeFailure("source", "SOURCE_PATH_REJECTED")
		}
		var storedPath, storedHash, storedChars any
		if tx.QueryRowContext(ctx, `SELECT source_relative_path,content_sha256,content_chars FROM job_discoveries WHERE discovery_id=?`, id).Scan(&storedPath, &storedHash, &storedChars) != nil {
			return writeFailure("source", "DATABASE_QUERY_FAILED")
		}
		h := sha256.Sum256(c.After)
		if storedPath != c.RelativePath || storedHash != hex.EncodeToString(h[:]) || storedChars != int64(utf8.RuneCount(c.After)) {
			return writeFailure("source", "SOURCE_CONTENT_MISMATCH")
		}
	}
	return nil
}
