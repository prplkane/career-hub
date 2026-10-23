package applicationreview

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/jobs/assessmentcontext"
)

// VersionedAssessment is a first-party storage record, not a renderer DTO.
// Its artifact remains bounded raw text on read: a malformed derived cache is
// not permission to skip source validation or an excuse to hide source errors.
// Legacy save_assessment still accepts ONLY its original sha256 revisions.
type VersionedAssessment struct {
	JobID, JobFingerprint, CandidateRevision, ContextRevision, ComputedAt string
	AnalyzerVersion, AssessmentVersion                                    int64
	Artifact                                                              string
}

func (VersionedAssessment) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted stored assessment]")
}
func (v VersionedAssessment) Identity() map[string]any {
	return map[string]any{"job_fingerprint": v.JobFingerprint, "job_analyzer_version": v.AnalyzerVersion,
		"candidate_revision": v.CandidateRevision, "assessment_context_revision": v.ContextRevision, "assessment_version": v.AssessmentVersion}
}
func ReadVersionedAssessment(ctx context.Context, tx *sql.Tx, id string) (*VersionedAssessment, *Error) {
	if ctx == nil || tx == nil || ctx.Err() != nil {
		return nil, unsupported("CANCELED")
	}
	e := engine{ctx: ctx, tx: tx}
	return e.versioned(id)
}
func (e *engine) versioned(id string) (*VersionedAssessment, *Error) {
	row, p := e.one("SELECT "+assessmentColumns()+" FROM job_assessments WHERE job_id=?", id)
	if p != nil || row == nil {
		return nil, p
	}
	out := &VersionedAssessment{}
	for key, target := range map[string]*string{"job_id": &out.JobID, "job_fingerprint": &out.JobFingerprint,
		"candidate_revision": &out.CandidateRevision, "assessment_context_revision": &out.ContextRevision,
		"computed_at": &out.ComputedAt, "assessment_json": &out.Artifact} {
		value, ok := row[key].(string)
		if !ok {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		*target = value
	}
	for key, target := range map[string]*int64{"job_analyzer_version": &out.AnalyzerVersion, "assessment_version": &out.AssessmentVersion} {
		value, ok := row[key].(int64)
		if !ok {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		*target = value
	}
	return out, nil
}

// SaveVersionedAssessment borrows the caller's transaction. Explicit v2
// identities survive intact; no prefix stripping or "legacy-compatible" hash.
// Only the composed producer creates this canonical artifact. This method does
// not authenticate a locally edited checksum or authorize a network caller.
func SaveVersionedAssessment(ctx context.Context, tx *sql.Tx, value VersionedAssessment) (*VersionedAssessment, *Error) {
	if ctx == nil || tx == nil || ctx.Err() != nil {
		return nil, unsupported("CANCELED")
	}
	if value.JobID == "" || len(value.JobID) > 256 || !utf8.ValidString(value.JobID) ||
		strings.ContainsAny(value.JobID, "/\\:\x00") || value.JobID == "." || value.JobID == ".." {
		return nil, unsupported("VERSIONED_ID_INVALID")
	}
	if _, p := sha(value.JobFingerprint, "job_fingerprint", false); p != nil {
		return nil, p
	}
	if !assessmentcontext.SavedRevisionsValid(assessmentcontext.Revisions{Candidate: value.CandidateRevision, Context: value.ContextRevision}) ||
		value.AnalyzerVersion < 0 || value.AssessmentVersion < 1 {
		return nil, unsupported("VERSIONED_IDENTITY_INVALID")
	}
	clock, err := time.Parse("2006-01-02T15:04:05Z", value.ComputedAt)
	if err != nil || clock.Year() < 1 || clock.Year() > 9999 || clock.Format("2006-01-02T15:04:05Z") != value.ComputedAt {
		return nil, unsupported("CLOCK_RANGE")
	}
	if len(value.Artifact) > maxCellBytes || !utf8.ValidString(value.Artifact) ||
		!json.Valid([]byte(value.Artifact)) || !storedJSONSafe(value.Artifact) || !versionedTokenBound(ctx, value.Artifact) {
		return nil, unsupported("ASSESSMENT_JSON_UNSUPPORTED")
	}
	// Validate structure budgets without coercing numbers or rewriting the
	// producer's canonical bytes (the cache checksum covers exact encoding).
	var decoded any
	d := json.NewDecoder(strings.NewReader(value.Artifact))
	d.UseNumber()
	if d.Decode(&decoded) != nil || !boundedJSON(decoded) {
		return nil, unsupported("ASSESSMENT_JSON_UNSUPPORTED")
	}
	if _, ok := decoded.(map[string]any); !ok {
		return nil, unsupported("ASSESSMENT_JSON_UNSUPPORTED")
	}
	e := engine{ctx: ctx, tx: tx, clock: value.ComputedAt}
	if p := e.requireJob(value.JobID); p != nil {
		return nil, p
	}
	_, p := e.exec(`INSERT INTO job_assessments(job_id,job_fingerprint,job_analyzer_version,candidate_revision,assessment_context_revision,assessment_version,assessment_json,computed_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(job_id) DO UPDATE SET job_fingerprint=excluded.job_fingerprint,job_analyzer_version=excluded.job_analyzer_version,candidate_revision=excluded.candidate_revision,assessment_context_revision=excluded.assessment_context_revision,assessment_version=excluded.assessment_version,assessment_json=excluded.assessment_json,computed_at=excluded.computed_at`,
		value.JobID, value.JobFingerprint, value.AnalyzerVersion, value.CandidateRevision, value.ContextRevision, value.AssessmentVersion, value.Artifact, value.ComputedAt)
	if p != nil {
		return nil, p
	}
	stored, p := e.versioned(value.JobID)
	if p != nil {
		return nil, p
	}
	if stored == nil || *stored != value {
		return nil, unsupported("VERSIONED_READBACK_FAILED")
	}
	return stored, nil
}

// Limit structural work before allocating an object graph. Scalar JSON arrays
// can be small in bytes but contain hundreds of thousands of nodes.
func versionedTokenBound(ctx context.Context, raw string) bool {
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	for count := 0; count <= 20000; count++ {
		if ctx == nil || ctx.Err() != nil {
			return false
		}
		_, err := d.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
	}
	return false
}
