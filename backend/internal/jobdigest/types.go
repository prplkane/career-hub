// Package jobdigest builds the complete deterministic, advisory JD digest.
// Owned reads, schema validation, persistence and HTTP activation are separate.
package jobdigest

import (
	"encoding/json"

	"careerhub.local/backend/internal/applicationanalysis"
	"careerhub.local/backend/internal/skills/catalog"
)

const AnalyzerVersion = 12
const MaxSourceBytes = 4 << 20
const MaxSourceRunes = 500000
const MaxLines = 10000
const MaxItems = 10000
const MaxOutputBytes = 16 << 20

// OptionalBool distinguishes absent, explicit null and a confirmed boolean.
type OptionalBool struct {
	Present bool
	Value   *bool
}
type EligibilityProfile struct {
	CitizenshipBlocker, ClearanceBlocker OptionalBool
	SponsorshipNow, SponsorshipFuture    OptionalBool
	CurrentStatus                        string
}

// Input borrows an unchanged validated catalog/profile snapshot for the call.
// Neither source nor registry vocabulary is evidence of candidate experience.
type Input struct {
	Source, Company, Title, SourceURL string
	Registry                          *catalog.Snapshot
	Profile                           EligibilityProfile
}

type Evidence struct {
	Line int    `json:"line"`
	Text string `json:"text"`
	Rule string `json:"rule"`
}
type Field struct {
	Value    string     `json:"value"`
	State    string     `json:"state"`
	Evidence []Evidence `json:"evidence"`
}
type Technology struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Requirement string     `json:"requirement"`
	Category    string     `json:"category"`
	Evidence    []Evidence `json:"evidence"`
}
type TechnologyGroup struct {
	Requirement  string     `json:"requirement"`
	Operator     string     `json:"operator"`
	Technologies []string   `json:"technologies"`
	Evidence     []Evidence `json:"evidence"`
}
type Occurrence struct {
	SkillID            string `json:"skill_id"`
	CanonicalName      string `json:"canonical_name"`
	SourceText         string `json:"source_text"`
	PrimaryGroupID     string `json:"primary_group_id"`
	Origin             string `json:"origin"`
	Requirement        string `json:"requirement"`
	InAlternativeGroup bool   `json:"in_alternative_group"`
	Line               int    `json:"line"`
	Start              int    `json:"start"`
	End                int    `json:"end"`
}
type Experience struct {
	MinimumYears int    `json:"minimum_years"`
	MaximumYears *int   `json:"maximum_years,omitempty"`
	Area         string `json:"area"`
}
type CompensationRange struct {
	Minimum     json.Number `json:"minimum"`
	Maximum     json.Number `json:"maximum"`
	Currency    string      `json:"currency"`
	Period      string      `json:"period"`
	Location    string      `json:"location"`
	DisplayText string      `json:"display_text"`
	Evidence    []Evidence  `json:"evidence"`
}
type Compensation struct {
	Status string              `json:"status"`
	Ranges []CompensationRange `json:"ranges"`
}
type Gate struct {
	Status   string     `json:"status"`
	Reasons  []string   `json:"reasons"`
	Evidence []Evidence `json:"evidence"`
}
type Classified struct {
	Category    string     `json:"category"`
	Name        string     `json:"name"`
	Requirement string     `json:"requirement"`
	Text        string     `json:"text"`
	Evidence    []Evidence `json:"evidence"`
}
type Provenance struct {
	Technologies     []Technology `json:"technologies"`
	Experience       []Evidence   `json:"experience_requirements"`
	Education        []Evidence   `json:"education_requirements"`
	Responsibilities []Evidence   `json:"responsibilities"`
}
type ReviewItem struct {
	Line    int    `json:"line"`
	Section string `json:"section"`
	Text    string `json:"text"`
	Rule    string `json:"rule"`
}
type Coverage struct {
	SourceLines      int          `json:"source_lines"`
	RelevantLines    int          `json:"relevant_lines"`
	StructuredLines  int          `json:"structured_lines"`
	NeedsReviewLines int          `json:"needs_review_lines"`
	ContextLines     int          `json:"context_lines"`
	ReviewItems      []ReviewItem `json:"review_items"`
}
type Result struct {
	ContentFingerprint    string                                `json:"content_fingerprint"`
	Instructions          applicationanalysis.InstructionResult `json:"application_instructions"`
	Contacts              applicationanalysis.ContactResult     `json:"contact_candidates"`
	Fields                map[string]Field                      `json:"fields"`
	RequiredSkills        []string                              `json:"required_skills"`
	RequiredPlatforms     []string                              `json:"required_platforms"`
	PreferredSkills       []string                              `json:"preferred_skills"`
	MentionedTechnologies []string                              `json:"mentioned_technologies"`
	TechnologyGroups      []TechnologyGroup                     `json:"technology_groups"`
	Occurrences           []Occurrence                          `json:"skill_occurrences"`
	Responsibilities      []string                              `json:"responsibilities"`
	Experience            []Experience                          `json:"experience_requirements"`
	Education             []string                              `json:"education_requirements"`
	Compensation          Compensation                          `json:"compensation"`
	VisaSignals           map[string]*bool                      `json:"visa_signals"`
	Gates                 map[string]Gate                       `json:"eligibility_gates"`
	Classified            []Classified                          `json:"classified_requirements"`
	Provenance            Provenance                            `json:"provenance"`
	Coverage              Coverage                              `json:"analysis_coverage"`
	UnresolvedFields      []string                              `json:"unresolved_fields"`
}
