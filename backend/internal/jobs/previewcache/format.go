// Package previewcache stores only versioned, disposable Go advisory artifacts.
// It never writes the Python SQLite cache, source documents or user history.
package previewcache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/discovery/discoverypreview"
	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/jobs/jobdigest"
)

const Directory = "cache/go/discovery-preview-v2"
const MaxBytes = 4 << 20
const FormatVersion = 1

var ErrInvalid = errors.New("preview cache is invalid")
var ErrRead = errors.New("preview cache could not be read")
var ErrWrite = errors.New("preview cache could not be saved")
var ErrConflict = errors.New("preview cache changed before publication")

type Disposition string

const (
	Missing Disposition = "missing"
	Stale   Disposition = "stale"
	Invalid Disposition = "invalid"
	Match   Disposition = "match_needs_validation"
)

type Observation struct {
	Path    string
	Present bool
	SHA256  [32]byte
}
type Entry struct {
	Observation Observation
	Disposition Disposition
	Prepared    *discoverypreview.Prepared
}
type payload struct {
	Format      int                             `json:"format_version"`
	DiscoveryID string                          `json:"discovery_id"`
	Identity    assessmentcontext.CacheIdentity `json:"identity"`
	Versions    [7]uint32                       `json:"engine_versions"`
	ComputedAt  string                          `json:"computed_at"`
	Artifact    *discoverypreview.Artifact      `json:"artifact"`
	Digest      *jobdigest.Result               `json:"digest"`
}
type envelope struct {
	Payload json.RawMessage `json:"payload"`
	SHA256  string          `json:"sha256"`
}

func versions() [7]uint32 {
	v := discoverypreview.EngineVersions()
	return [7]uint32{v.Analyzer, v.Evaluator, v.Preview, v.Receipt, v.Instructions, v.Contacts, v.Validation}
}
func validIdentity(identity assessmentcontext.CacheIdentity) bool {
	return assessmentcontext.CheckCacheKey(identity.Key, identity) == assessmentcontext.CacheKeyNeedsValidation
}
func Path(id string) (string, error) {
	clean, err := discovery.NormalizeID(id)
	if err != nil || clean != id {
		return "", ErrInvalid
	}
	hash := sha256.Sum256([]byte(id))
	return Directory + "/" + hex.EncodeToString(hash[:]) + ".json", nil
}
func stopped(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	return ctx.Err()
}

// Canonical producer bytes make duplicate/missing/unknown fields, invalid UTF-8,
// repaired surrogates and extra JSON values cache misses rather than ambiguous
// interpretations. Depth/node limits apply before allocating decoded objects.
func decode(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > MaxBytes || !utf8.Valid(raw) {
		return ErrInvalid
	}
	depth, nodes, quoted, escaped := 0, 0, false, false
	for _, b := range raw {
		if quoted {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				quoted = false
			}
			continue
		}
		switch b {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			nodes++
		case '}', ']':
			depth--
		case ',', ':':
			nodes++
		}
		if depth > 64 || depth < 0 || nodes > 200000 {
			return ErrInvalid
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if decoder.Decode(target) != nil {
		return ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return ErrInvalid
	}
	canonical, err := json.Marshal(target)
	if err != nil || !bytes.Equal(canonical, raw) {
		return ErrInvalid
	}
	return nil
}
func Encode(ctx context.Context, id string, identity assessmentcontext.CacheIdentity, prepared *discoverypreview.Prepared, computed time.Time) ([]byte, error) {
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	if _, err := Path(id); err != nil {
		return nil, err
	}
	if !validIdentity(identity) || prepared == nil || prepared.Artifact == nil || prepared.Digest == nil || computed.Year() < 1 || computed.Year() > 9999 {
		return nil, ErrInvalid
	}
	data := payload{Format: FormatVersion, DiscoveryID: id, Identity: identity, Versions: versions(), ComputedAt: computed.UTC().Format("2006-01-02T15:04:05Z"), Artifact: prepared.Artifact, Digest: prepared.Digest}
	if !headerValid(data) {
		return nil, ErrInvalid
	}
	raw, err := json.Marshal(data)
	if err != nil || len(raw) > MaxBytes-1024 {
		return nil, ErrInvalid
	}
	hash := sha256.Sum256(raw)
	result, err := json.Marshal(envelope{raw, hex.EncodeToString(hash[:])})
	if err != nil || len(result) > MaxBytes {
		return nil, ErrInvalid
	}
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
func headerValid(data payload) bool {
	a := data.Artifact
	if a == nil || data.Digest == nil || a.Assessment == nil || data.Format != FormatVersion || data.Versions != versions() {
		return false
	}
	if !validIdentity(data.Identity) || a.SchemaVersion != discoverypreview.Version || a.AssessmentMode != "advisory_only" || !a.ApplicationAllowed || !discoverypreview.Eligible(a.Scope) {
		return false
	}
	if a.Assessment.SchemaVersion != 2 || a.Assessment.JobID != data.DiscoveryID || !a.Assessment.ApplicationAllowed {
		return false
	}
	if a.Receipt.SchemaVersion != discoverypreview.ReceiptVersion || a.Receipt.Scope != a.Scope || a.Receipt.Availability != "available" {
		return false
	}
	if a.Instructions.SchemaVersion != 1 || a.Instructions.ExtractorVersion != 1 || !a.Instructions.Eligible || a.Instructions.Scope != a.Scope {
		return false
	}
	if a.Contacts.SchemaVersion != 1 || a.Contacts.ExtractorVersion != 1 || !a.Contacts.Eligible || a.Contacts.Scope != a.Scope {
		return false
	}
	clock, err := time.Parse("2006-01-02T15:04:05Z", data.ComputedAt)
	return err == nil && clock.Year() >= 1 && clock.Year() <= 9999 && clock.Format("2006-01-02T15:04:05Z") == data.ComputedAt
}

// Decode supplies a candidate only, never a cache-hit authorization. A checksum
// detects storage corruption, not deliberate edits by a local filesystem owner.
func Decode(ctx context.Context, raw []byte, id string, current assessmentcontext.CacheIdentity) (*discoverypreview.Prepared, Disposition, error) {
	if err := stopped(ctx); err != nil {
		return nil, Invalid, err
	}
	if _, err := Path(id); err != nil || !validIdentity(current) {
		return nil, Invalid, ErrInvalid
	}
	var wrapped envelope
	if decode(raw, &wrapped) != nil {
		return nil, Invalid, nil
	}
	hash := sha256.Sum256(wrapped.Payload)
	if wrapped.SHA256 != hex.EncodeToString(hash[:]) {
		return nil, Invalid, nil
	}
	var data payload
	if decode(wrapped.Payload, &data) != nil {
		return nil, Invalid, nil
	}
	if data.DiscoveryID != id || !reflect.DeepEqual(data.Identity, current) || data.Format != FormatVersion || data.Versions != versions() {
		return nil, Stale, nil
	}
	if !headerValid(data) {
		return nil, Invalid, nil
	}
	if err := stopped(ctx); err != nil {
		return nil, Invalid, err
	}
	return &discoverypreview.Prepared{Artifact: data.Artifact, Digest: data.Digest}, Match, nil
}
