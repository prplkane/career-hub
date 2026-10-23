// Package jobevaluation evaluates the complete in-memory job review contract.
// It does not load jobs, validate managed paths, publish cache entries, or submit
// applications. The owned caller must validate the full Job Card and digest.
package jobevaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
)

// Version must participate in the assessment cache engine matrix. Version 5
// excludes archived registry edges during preparation and corrects OPT text
// confirmation and threshold-crossing rounding. A source-v4 receipt is not a
// compatible evaluation receipt.
const Version = 5

const (
	MaxRequirements = 2048
	MaxInputBytes   = 16 << 20
	MaxResultBytes  = 32 << 20
	MaxQueryWork    = 512 << 20
	maxNodes        = 100000
	maxLabelBytes   = 16 << 10
)

type Error struct{ Code string }

func (*Error) Error() string                    { return "Job evaluation could not be completed." }
func (e *Error) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, e.Error()) }
func stopped(ctx context.Context) *Error {
	if ctx == nil || ctx.Err() != nil {
		return &Error{Code: "EVALUATION_CANCELED"}
	}
	return nil
}

type CandidateFact struct {
	Field     string `json:"field"`
	Value     any    `json:"value"`
	Statement string `json:"statement"`
}

type Finding struct {
	PolicyClass    string        `json:"policy_class"`
	Code           string        `json:"code"`
	Message        string        `json:"message"`
	Source         string        `json:"source"`
	SourceEvidence []any         `json:"source_evidence"`
	CandidateFact  CandidateFact `json:"candidate_fact"`
	Status         string        `json:"status"`
	ReviewReason   string        `json:"review_reason,omitempty"`
}

type RequirementGap struct {
	Code         string   `json:"code"`
	Status       string   `json:"status"`
	Severity     string   `json:"severity"`
	Category     string   `json:"category"`
	Requirement  string   `json:"requirement"`
	Message      string   `json:"message"`
	Source       string   `json:"source"`
	ConceptIDs   []string `json:"concept_ids,omitempty"`
	Operator     string   `json:"operator,omitempty"`
	Alternatives []string `json:"alternatives,omitempty"`
}

type MatchedRequirement struct {
	Category            string   `json:"category,omitempty"`
	Requirement         string   `json:"requirement"`
	EvidenceIDs         []string `json:"evidence_ids"`
	ConceptIDs          []string `json:"concept_ids,omitempty"`
	Operator            string   `json:"operator,omitempty"`
	Alternatives        []string `json:"alternatives,omitempty"`
	MatchedAlternatives []string `json:"matched_alternatives,omitempty"`
}

type TaxonomyHint struct {
	Category    string   `json:"category"`
	Requirement string   `json:"requirement"`
	SkillIDs    []string `json:"skill_ids"`
	ConceptIDs  []string `json:"concept_ids,omitempty"`
}

type ExperienceReview struct {
	MinimumYears   json.Number     `json:"minimum_years"`
	MaximumYears   json.RawMessage `json:"maximum_years,omitempty"`
	Area           string          `json:"area"`
	Status         string          `json:"status"`
	EvidenceIDs    []string        `json:"evidence_ids"`
	SourceEvidence []any           `json:"source_evidence"`
	ConceptIDs     []string        `json:"concept_ids,omitempty"`
	DerivedYears   *json.Number    `json:"derived_years,omitempty"`
	CandidateFact  *CandidateFact  `json:"candidate_fact,omitempty"`
}

type ClassifiedReview struct {
	Category         string   `json:"category"`
	Name             string   `json:"name"`
	Requirement      string   `json:"requirement"`
	SourceText       string   `json:"source_text"`
	SourceEvidence   []any    `json:"source_evidence"`
	Status           string   `json:"status"`
	EvidenceIDs      []string `json:"evidence_ids"`
	TaxonomySkillIDs []string `json:"taxonomy_skill_ids"`
	ConceptIDs       []string `json:"concept_ids,omitempty"`
}

type Result struct {
	SchemaVersion               int                  `json:"schema_version"`
	JobID                       string               `json:"job_id"`
	ApplicationAllowed          bool                 `json:"application_allowed"`
	Lane                        string               `json:"lane"`
	BiggestGap                  string               `json:"biggest_gap"`
	EligibilityBlockers         []Finding            `json:"eligibility_blockers"`
	PolicyReviews               []Finding            `json:"policy_reviews"`
	RequirementGaps             []RequirementGap     `json:"requirement_gaps"`
	MatchedRequired             []MatchedRequirement `json:"matched_required"`
	MatchedPreferred            []MatchedRequirement `json:"matched_preferred"`
	MissingPreferred            []string             `json:"missing_preferred"`
	ExperienceReview            []ExperienceReview   `json:"experience_review"`
	ClassifiedRequirementReview []ClassifiedReview   `json:"classified_requirement_review"`
	ManualReview                []string             `json:"manual_review"`
	TaxonomyHints               []TaxonomyHint       `json:"taxonomy_hints"`
	RelevantEvidenceIDs         []string             `json:"relevant_evidence_ids"`
}

func newResult(id string) *Result {
	return &Result{
		SchemaVersion: 2, JobID: id, ApplicationAllowed: true,
		EligibilityBlockers: []Finding{}, PolicyReviews: []Finding{},
		RequirementGaps: []RequirementGap{}, MatchedRequired: []MatchedRequirement{},
		MatchedPreferred: []MatchedRequirement{}, MissingPreferred: []string{},
		ExperienceReview: []ExperienceReview{}, ClassifiedRequirementReview: []ClassifiedReview{},
		ManualReview: []string{}, TaxonomyHints: []TaxonomyHint{}, RelevantEvidenceIDs: []string{},
	}
}

// Reserve before retaining each result row. This conservative JSON-size upper
// bound avoids allocating an oversized encoding merely to measure it. It walks
// only our output DTOs (not arbitrary objects or methods), counts repeated data
// as repeated wire bytes, and charges omitted fields too. Output is all-or-none.
type resultBudget struct {
	ctx          context.Context
	bytes, nodes int
}

func (b *resultBudget) reserve(value any) *Error { return b.walk(reflect.ValueOf(value), 0) }
func (b *resultBudget) walk(value reflect.Value, depth int) *Error {
	if err := stopped(b.ctx); err != nil {
		return err
	}
	b.nodes++
	b.bytes += 8
	if depth > 70 || b.nodes > maxNodes || b.bytes > MaxResultBytes {
		return &Error{Code: "EVALUATION_RESULT_LIMIT"}
	}
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			return b.walk(value.Elem(), depth+1)
		}
	case reflect.String:
		b.bytes += 6 * value.Len()
	case reflect.Slice, reflect.Array:
		if value.Len() > maxNodes-b.nodes {
			return &Error{Code: "EVALUATION_RESULT_LIMIT"}
		}
		for i := 0; i < value.Len(); i++ {
			if err := b.walk(value.Index(i), depth+1); err != nil {
				return err
			}
		}
	case reflect.Map:
		if value.Len() > (maxNodes-b.nodes)/2 {
			return &Error{Code: "EVALUATION_RESULT_LIMIT"}
		}
		it := value.MapRange()
		for it.Next() {
			if err := b.walk(it.Key(), depth+1); err != nil {
				return err
			}
			if err := b.walk(it.Value(), depth+1); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			b.bytes += 6 * len(value.Type().Field(i).Tag.Get("json"))
			if err := b.walk(value.Field(i), depth+1); err != nil {
				return err
			}
		}
	default:
		b.bytes += 64 // all remaining DTO scalars are finite numbers or booleans
	}
	if b.bytes > MaxResultBytes {
		return &Error{Code: "EVALUATION_RESULT_LIMIT"}
	}
	return nil
}
