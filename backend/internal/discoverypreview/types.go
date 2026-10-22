// Package discoverypreview builds complete advisory capture assessments.
// It has no files, database, browser, network, job promotion or status writes.
package discoverypreview

import (
	"context"
	"errors"

	"careerhub.local/backend/internal/applicationanalysis"
	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/jobdigest"
	"careerhub.local/backend/internal/jobevaluation"
	"careerhub.local/backend/internal/skills/catalog"
)

const Version uint32 = 5

// ReceiptVersion 4 bounds the whole assembled experience message to 500
// codepoints; source v3 only bounded its area before adding surrounding text.
const ReceiptVersion uint32 = 4
const MaxArtifactBytes = 2 << 20
const MaxFindings = 3
const MaxTextRunes = 500

var ErrInput = errors.New("invalid discovery preview input")
var ErrLimit = errors.New("discovery preview limit exceeded")
var ErrSource = errors.New("discovery preview source could not be verified")
var ErrPolicy = errors.New("discovery preview policy is invalid")

type Capture struct {
	Identity             assessmentcontext.CacheCapture
	Source, RelativePath string
}
type Input struct {
	Capture    Capture
	Registry   *catalog.Snapshot
	Evaluation jobevaluation.Input
	// Must observe exactly the managed capture already verified by the owner.
	CheckSource jobcard.RawFileCheck
}
type Excerpt struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}
type Blocker struct {
	Code           string  `json:"code"`
	Message        string  `json:"message"`
	SourceEvidence Excerpt `json:"source_evidence"`
	CandidateFact  string  `json:"candidate_fact"`
}
type Review struct {
	Kind           string   `json:"kind"`
	Text           string   `json:"text"`
	Line           *int     `json:"line,omitempty"`
	SourceEvidence *Excerpt `json:"source_evidence,omitempty"`
	CandidateFact  string   `json:"candidate_fact,omitempty"`
}
type Receipt struct {
	SchemaVersion          uint32    `json:"schema_version"`
	Status                 string    `json:"status"`
	Label                  string    `json:"label"`
	Availability           string    `json:"availability"`
	Scope                  string    `json:"scope"`
	BlockerCount           int       `json:"blocker_count"`
	Blockers               []Blocker `json:"blockers"`
	ReviewCount            int       `json:"review_count"`
	ReviewItems            []Review  `json:"review_items"`
	UnknownVocabularyCount int       `json:"unknown_vocabulary_count"`
	ExtraStepCount         int       `json:"extra_step_count"`
	ContactCandidateCount  int       `json:"contact_candidate_count"`
	Truncated              bool      `json:"truncated"`
}
type Instructions struct {
	applicationanalysis.InstructionResult
	Eligible bool   `json:"eligible"`
	Message  string `json:"message"`
	Scope    string `json:"scope"`
}
type Contacts struct {
	applicationanalysis.ContactResult
	Eligible bool   `json:"eligible"`
	Message  string `json:"message"`
	Scope    string `json:"scope"`
}
type Artifact struct {
	SchemaVersion      uint32                `json:"schema_version"`
	AssessmentMode     string                `json:"assessment_mode"`
	ApplicationAllowed bool                  `json:"application_allowed"`
	Scope              string                `json:"scope"`
	Assessment         *jobevaluation.Result `json:"assessment"`
	UnknownVocabulary  []Excerpt             `json:"unknown_vocabulary_review"`
	Instructions       Instructions          `json:"application_instructions"`
	Contacts           Contacts              `json:"contact_candidates"`
	Receipt            Receipt               `json:"receipt"`
}

// Prepared retains a fresh whole digest separately from the source-compatible
// artifact. It is not a persisted cache hit, admission receipt or application.
type Prepared struct {
	Artifact *Artifact
	Digest   *jobdigest.Result
}

func EngineVersions() assessmentcontext.EngineVersions {
	return assessmentcontext.EngineVersions{
		Analyzer: jobdigest.AnalyzerVersion, Evaluator: jobevaluation.Version,
		Preview: Version, Receipt: ReceiptVersion, Instructions: 1, Contacts: 1,
		Validation: jobcard.ValidationVersion,
	}
}
func stopped(ctx context.Context) error {
	if ctx == nil {
		return ErrInput
	}
	return ctx.Err()
}
func Eligible(scope string) bool { return scope == "page_visible" || scope == "confirmed_full" }
