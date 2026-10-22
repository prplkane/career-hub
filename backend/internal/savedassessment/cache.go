package savedassessment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"unicode/utf8"

	"careerhub.local/backend/internal/applicationreview"
	"careerhub.local/backend/internal/jobevaluation"
)

// The private tag keeps the source assessment shape intact for raw internal
// readers. Public responses strip it. The checksum detects accidental changes;
// a filesystem owner can forge it, so it is not authentication or encryption.
type cacheTag struct {
	Version  int    `json:"format_version"`
	Checksum string `json:"sha256"`
}
type artifact struct {
	*jobevaluation.Result
	JobStatus string   `json:"job_status"`
	Mode      string   `json:"assessment_mode"`
	Cache     cacheTag `json:"_go_saved_assessment"`
}

func (p *Prepared) record(clock string) applicationreview.VersionedAssessment {
	return applicationreview.VersionedAssessment{JobID: p.id, JobFingerprint: p.fingerprint, AnalyzerVersion: p.analyzer,
		CandidateRevision: p.revisions.Candidate, ContextRevision: p.revisions.Context, AssessmentVersion: jobevaluation.Version, ComputedAt: clock}
}
func identityMatches(a, b applicationreview.VersionedAssessment) bool {
	return a.JobID == b.JobID && a.JobFingerprint == b.JobFingerprint && a.AnalyzerVersion == b.AnalyzerVersion &&
		a.CandidateRevision == b.CandidateRevision && a.ContextRevision == b.ContextRevision && a.AssessmentVersion == b.AssessmentVersion
}
func checksum(record applicationreview.VersionedAssessment, value artifact) (string, *Error) {
	value.Cache.Checksum = ""
	body, err := json.Marshal(value)
	if err != nil || len(body) > maxArtifactBytes {
		return "", fail("ARTIFACT_LIMIT")
	}
	identity := record.Identity()
	identity["job_id"] = record.JobID
	identity["computed_at"] = record.ComputedAt
	prefix, err := json.Marshal(identity)
	if err != nil {
		return "", fail("ARTIFACT_INVALID")
	}
	h := sha256.New()
	_, _ = h.Write([]byte("career-hub/saved-assessment-artifact/v1\x00"))
	_, _ = h.Write(prefix)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil)), nil
}
func pack(record applicationreview.VersionedAssessment, value *jobevaluation.Result, status string) (string, *Error) {
	a := artifact{Result: value, JobStatus: status, Mode: "advisory_only", Cache: cacheTag{Version: 1}}
	hash, p := checksum(record, a)
	if p != nil {
		return "", p
	}
	a.Cache.Checksum = hash
	raw, err := json.Marshal(a)
	if err != nil || len(raw) > maxArtifactBytes {
		return "", fail("ARTIFACT_LIMIT")
	}
	return string(raw), nil
}

// Preflight before allocating JSON objects. Depth/node/number-token bounds are
// conservative and apply to corrupt stored inputs too. Canonical round-trip
// rejects duplicate keys, replaced surrogates, missing/unknown structured
// fields, aliases and trailing data before checksum equality can qualify reuse.
func boundedArtifact(raw []byte) bool {
	if len(raw) == 0 || len(raw) > maxArtifactBytes || !utf8.Valid(raw) {
		return false
	}
	depth, nodes := 0, 0
	inString, escape := false, false
	token := 0
	for _, c := range raw {
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
			nodes++
			token = 0
		case '{', '[':
			depth++
			nodes++
			token = 0
		case '}', ']':
			depth--
			token = 0
		case ':', ',', ' ', '\t', '\r', '\n':
			token = 0
		default:
			if token == 0 {
				nodes++
			}
			token++
		}
		if depth < 0 || depth > 32 || nodes > 10000 || token > 4096 {
			return false
		}
	}
	return !inString && depth == 0 && json.Valid(raw)
}
func (p *Prepared) restore(record applicationreview.VersionedAssessment) *jobevaluation.Result {
	raw := []byte(record.Artifact)
	if !identityMatches(record, p.record(record.ComputedAt)) || !boundedArtifact(raw) {
		return nil
	}
	var a artifact
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	if d.Decode(&a) != nil || a.Result == nil || a.Cache.Version != 1 || a.JobID != p.id || a.SchemaVersion != 2 ||
		!a.ApplicationAllowed || a.JobStatus != p.status || a.Mode != "advisory_only" {
		return nil
	}
	canonical, err := json.Marshal(a)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil
	}
	hash, problem := checksum(record, a)
	if problem != nil || a.Cache.Checksum != hash {
		return nil
	}
	return a.Result
}

// These are the stable summary keys used by features/jobs.js, not inferred
// labels from an arbitrary request. Historical unknown keys remain readable.
func findingExists(a *jobevaluation.Result, key string) bool {
	switch key {
	case "recommendation", "visa", "restrictions", "seniority", "location":
		return true
	case "experience:none":
		return len(a.ExperienceReview) == 0
	}
	requirements := len(a.RequirementGaps)
	for _, r := range a.ClassifiedRequirementReview {
		if r.Requirement == "required" && r.Status == "manual_review" {
			requirements++
		}
	}
	if key == "requirements:none" {
		return requirements == 0
	}
	for _, v := range []struct {
		prefix string
		count  int
	}{{"experience:", len(a.ExperienceReview)}, {"requirement:", requirements}} {
		if len(key) > len(v.prefix) && key[:len(v.prefix)] == v.prefix {
			suffix := key[len(v.prefix):]
			n, err := strconv.Atoi(suffix)
			return err == nil && n >= 0 && n < v.count && strconv.Itoa(n) == suffix
		}
	}
	return false
}
