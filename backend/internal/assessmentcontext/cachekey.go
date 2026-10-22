package assessmentcontext

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"strings"
	"unicode/utf8"
)

const CacheKeyVersion = 2

// EngineVersions must come from the fully composed implementation, not the UI
// or an inferred app release number. Changing a rule/validator requires a bump.
type EngineVersions struct {
	Analyzer, Evaluator, Preview, Receipt, Instructions, Contacts, Validation uint32
}

// CacheCapture contains the exact effective inputs consumed by preview/digest.
// Its content digest must already have been checked against the retained text.
// Neither a hash nor page_visible scope establishes that the capture is one JD.
type CacheCapture struct {
	DiscoveryID, ContentSHA256, Scope, Title, Company, ObservedURL string
}
type CacheInputs struct {
	Month    Month
	Versions EngineVersions
	Capture  CacheCapture
}
type CacheIdentity struct {
	Version   uint32 `json:"version"`
	Candidate string `json:"candidate_revision"`
	Context   string `json:"assessment_context_revision"`
	Key       string `json:"cache_key"`
}

var cacheContextFiles = [...]sourceSpec{
	{"src/data/skill_groups.yaml", "core_groups", true},
	{"src/data/job_technologies.yaml", "core_technologies", true},
	{"career_vault/taxonomy/skill_groups.yaml", "custom_groups", false},
	{"career_vault/taxonomy/technologies.yaml", "custom_technologies", false},
	{"career_vault/hard_blocker_policy.yaml", "blocker_policy", false},
	{"src/data/requirement_concepts.yaml", "requirement_concepts", true},
}

func lowerSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, point := range value {
		if !(point >= '0' && point <= '9' || point >= 'a' && point <= 'f') {
			return false
		}
	}
	return true
}
func cacheError(code string) *Error { return &Error{Stage: "cache_identity", Code: code} }
func (input CacheInputs) Validate() *Error {
	if !input.Month.Valid() {
		return cacheError("ASSESSMENT_MONTH_INVALID")
	}
	v := input.Versions
	for _, value := range [...]uint32{v.Analyzer, v.Evaluator, v.Preview, v.Receipt, v.Instructions, v.Contacts, v.Validation} {
		if value == 0 {
			return cacheError("ASSESSMENT_CACHE_VERSION_INVALID")
		}
	}
	return input.Capture.Validate()
}

// Validate checks only retained capture metadata. Domain consumers without a
// calendar input must not invent a date just to validate a capture.
func (c CacheCapture) Validate() *Error {
	if c.Scope != "page_visible" && c.Scope != "confirmed_full" {
		return cacheError("ASSESSMENT_CACHE_SCOPE_INELIGIBLE")
	}
	if strings.TrimSpace(c.DiscoveryID) == "" || !lowerSHA256(c.ContentSHA256) {
		return cacheError("ASSESSMENT_CACHE_CAPTURE_INVALID")
	}
	for _, value := range [...]string{c.DiscoveryID, c.Title, c.Company, c.ObservedURL} {
		if len(value) > 16<<10 {
			return cacheError("ASSESSMENT_CACHE_INPUT_LIMIT")
		}
		if !utf8.ValidString(value) {
			return cacheError("ASSESSMENT_CACHE_CAPTURE_INVALID")
		}
	}
	return nil
}

// Each field is U64BE(label bytes), label, presence byte, U64BE(value bytes),
// value. The versioned domain is the first field; an encoded field count closes
// the stream. Missing and empty differ, and NUL has no delimiter meaning.
type framedHash struct {
	digest hash.Hash
	count  uint64
}

func newFramedHash(domain string) *framedHash {
	result := &framedHash{digest: sha256.New()}
	result.field("domain", true, []byte(domain))
	return result
}
func (frame *framedHash) number(value uint64) {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], value)
	_, _ = frame.digest.Write(raw[:])
}
func (frame *framedHash) field(label string, present bool, value []byte) {
	frame.number(uint64(len(label)))
	_, _ = frame.digest.Write([]byte(label))
	if present {
		_, _ = frame.digest.Write([]byte{1})
	} else {
		_, _ = frame.digest.Write([]byte{0})
		value = nil // Absent values have one representation.
	}
	frame.number(uint64(len(value)))
	_, _ = frame.digest.Write(value)
	frame.count++
}
func (frame *framedHash) finish(prefix string) string {
	frame.number(frame.count)
	return fmt.Sprintf("%s%x", prefix, frame.digest.Sum(nil))
}

const candidateKeyPrefix = "ch-candidate-v2:sha256:"
const contextKeyPrefix = "ch-context-v2:sha256:"
const assessmentKeyPrefix = "ch-assessment-v2:sha256:"

// ReadCacheIdentity introduces v2 without rewriting v1 comparison operations.
// Projection-equivalent candidate edits intentionally retain candidate identity;
// selected mapping order, eligibility field presence and every raw effective
// catalog/policy/concept input are also framed because consumers observe them.
// A key match requires fresh Vault/catalog/policy validity and artifact checks
// before reuse. No validation result or assessment is synthesized by this call.
func ReadCacheIdentity(ctx context.Context, data, resources Reader, input CacheInputs) (CacheIdentity, *Error) {
	if err := canceled(ctx); err != nil {
		return CacheIdentity{}, err
	}
	if err := input.Validate(); err != nil {
		return CacheIdentity{}, err
	}
	set := readSet{ctx: ctx, data: data, resources: resources}
	candidate, err := candidateIdentity(&set)
	if err != nil {
		return CacheIdentity{}, err
	}
	result := CacheIdentity{Version: CacheKeyVersion, Candidate: candidate}
	assessmentContext := newFramedHash("career-hub/assessment-context/v2")
	for _, spec := range cacheContextFiles {
		raw, present, err := set.observe(spec)
		if err != nil {
			return CacheIdentity{}, err
		}
		root := "data:"
		if spec.resource {
			root = "resource:"
		}
		assessmentContext.field(root+spec.path, present, raw)
	}
	assessmentContext.field("evaluation_month", true, []byte(fmt.Sprintf("%04d-%02d", input.Month.Year, input.Month.Number)))
	v := input.Versions
	for _, version := range [...]struct {
		label string
		value uint32
	}{{"analyzer", v.Analyzer}, {"evaluator", v.Evaluator}, {"preview", v.Preview}, {"receipt", v.Receipt},
		{"instructions", v.Instructions}, {"contacts", v.Contacts}, {"validation", v.Validation}} {
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], version.value)
		assessmentContext.field("version:"+version.label, true, raw[:])
	}
	result.Context = assessmentContext.finish(contextKeyPrefix)
	complete := newFramedHash("career-hub/discovery-assessment/v2")
	c := input.Capture
	for _, value := range [...]struct{ label, text string }{
		{"candidate", result.Candidate}, {"context", result.Context}, {"discovery_id", c.DiscoveryID},
		{"content_sha256", c.ContentSHA256}, {"scope", c.Scope}, {"title", c.Title},
		{"company", c.Company}, {"observed_url", c.ObservedURL},
	} {
		complete.field(value.label, true, []byte(value.text))
	}
	result.Key = complete.finish(assessmentKeyPrefix)
	if err := set.verify(); err != nil {
		return CacheIdentity{}, err
	}
	return result, nil
}

// CacheKeyDisposition is intentionally not a "reuse" permission. Even a match
// still requires current source validation and a complete, valid cached artifact.
type CacheKeyDisposition string

const (
	CacheKeyRecompute       CacheKeyDisposition = "recompute"
	CacheKeyLegacy          CacheKeyDisposition = "legacy_recompute"
	CacheKeyInvalid         CacheKeyDisposition = "invalid_recompute"
	CacheKeyNeedsValidation CacheKeyDisposition = "match_needs_validation"
)

func validKey(value, prefix string) bool {
	return strings.HasPrefix(value, prefix) && lowerSHA256(strings.TrimPrefix(value, prefix))
}

// CheckCacheKey has no persistence side effects. Old identities remain readable
// history, but never qualify for v2 reuse. An unavailable evaluator must surface
// unavailable/review rather than falling back to a legacy advisory verdict.
func CheckCacheKey(stored string, current CacheIdentity) CacheKeyDisposition {
	if current.Version != CacheKeyVersion || !validKey(current.Candidate, candidateKeyPrefix) ||
		!validKey(current.Context, contextKeyPrefix) || !validKey(current.Key, assessmentKeyPrefix) {
		return CacheKeyInvalid
	}
	if stored == "" {
		return CacheKeyRecompute
	}
	if validKey(stored, "sha256:") {
		return CacheKeyLegacy
	}
	if !validKey(stored, assessmentKeyPrefix) {
		return CacheKeyInvalid
	}
	if stored != current.Key {
		return CacheKeyRecompute
	}
	return CacheKeyNeedsValidation
}
