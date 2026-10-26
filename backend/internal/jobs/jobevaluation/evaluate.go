package jobevaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"careerhub.local/backend/internal/jobs/jobevidence"
)

type evaluation struct {
	ctx        context.Context
	input      Input
	job        *job
	result     *Result
	budget     resultBudget
	memoBudget resultBudget
	memo       map[string]jobevidence.ConceptMatches
	relevant   stringSet
	scanCost   int64
	queryWork  int64
}

// Evaluate runs every deterministic review stage or returns nil and one static
// error. It has no ambient date, filesystem, cache, HTTP, network or AI access.
// A result is advisory even if a confirmed policy finding recommends Review.
func Evaluate(ctx context.Context, input Input) (*Result, *Error) {
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	job, err := project(ctx, input)
	if err != nil {
		return nil, err
	}
	state := &evaluation{ctx: ctx, input: input, job: job, result: newResult(job.id),
		budget: resultBudget{ctx: ctx}, memoBudget: resultBudget{ctx: ctx}, memo: map[string]jobevidence.ConceptMatches{}, relevant: stringSet{}}
	if err := state.budget.reserve(state.result); err != nil {
		return nil, err
	}
	for _, stage := range []func() *Error{
		state.validateEvidence, state.eligibility, state.requirements,
		state.groups, state.experience, state.classified, state.coverage, state.finish,
	} {
		if err := stopped(ctx); err != nil {
			return nil, err
		}
		if err := stage(); err != nil {
			return nil, err
		}
	}
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	return state.result, nil
}

func retain[T any](s *evaluation, destination *[]T, value T) *Error {
	if err := s.budget.reserve(value); err != nil {
		return err
	}
	*destination = append(*destination, value)
	return nil
}

func (s *evaluation) hint(category, requirement string, ids, concepts []string) *Error {
	if len(ids) == 0 {
		return nil
	}
	return retain(s, &s.result.TaxonomyHints, TaxonomyHint{Category: category, Requirement: requirement, SkillIDs: ids, ConceptIDs: concepts})
}

func (s *evaluation) gap(category, requirement, source string, concepts, alternatives []string) *Error {
	gap := RequirementGap{Code: "required_evidence_not_found", Status: "evidence_not_found", Severity: "high", Category: category,
		Requirement: requirement, Source: source, ConceptIDs: concepts, Alternatives: alternatives,
		Message: fmt.Sprintf("No direct Career Vault evidence found for required %s: %s.", category, requirement)}
	if category == "capability" {
		gap.Severity = "medium"
	}
	if alternatives != nil {
		gap.Operator = "any_of"
	}
	return retain(s, &s.result.RequirementGaps, gap)
}

func (s *evaluation) requirements() *Error {
	for _, collection := range []struct {
		category, source string
		values           []string
	}{
		{"skill", "required_skills", s.job.requiredSkills},
		{"platform", "required_platforms", s.job.platforms},
		{"capability", "required_capabilities", s.job.capabilities},
		{"preferred", "preferred_skills", s.job.preferred},
	} {
		preferred := collection.category == "preferred"
		for _, requirement := range collection.values {
			matches, err := s.match(requirement)
			if err != nil {
				return err
			}
			hintConcepts := matches.ConceptIDs
			if preferred {
				hintConcepts = nil // the source hint omits this metadata here
			}
			if err := s.hint(collection.category, requirement, matches.Taxonomy, hintConcepts); err != nil {
				return err
			}
			if len(matches.Direct) != 0 {
				item := MatchedRequirement{Category: collection.category, Requirement: requirement, EvidenceIDs: matches.Direct, ConceptIDs: matches.ConceptIDs}
				destination := &s.result.MatchedRequired
				if preferred {
					item.Category = ""
					destination = &s.result.MatchedPreferred
				}
				if err := retain(s, destination, item); err != nil {
					return err
				}
				s.relevant.add(matches.Direct)
			} else if preferred {
				if err := retain(s, &s.result.MissingPreferred, requirement); err != nil {
					return err
				}
			} else if err := s.gap(collection.category, requirement, collection.source, matches.ConceptIDs, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *evaluation) groups() *Error {
	for _, group := range s.job.groups {
		if group.level == "mentioned" {
			continue
		}
		requirement, failure := jobevidence.TechnologyGroupLabel(s.ctx, group.alternatives)
		if failure != nil {
			return evidenceError(failure)
		}
		direct, taxonomy := stringSet{}, stringSet{}
		matched := []string{}
		for _, alternative := range group.alternatives {
			matches, err := s.match(alternative)
			if err != nil {
				return err
			}
			direct.add(matches.Direct)
			taxonomy.add(matches.Taxonomy)
			if len(matches.Direct) != 0 {
				matched = append(matched, alternative)
			}
		}
		if err := s.hint("technology_group", requirement, taxonomy.sorted(), nil); err != nil {
			return err
		}
		if len(direct) != 0 {
			item := MatchedRequirement{Category: "technology_group", Requirement: requirement,
				Operator: "any_of", Alternatives: group.alternatives, MatchedAlternatives: matched, EvidenceIDs: direct.sorted()}
			destination := &s.result.MatchedRequired
			if group.level == "preferred" {
				destination = &s.result.MatchedPreferred
			}
			if err := retain(s, destination, item); err != nil {
				return err
			}
			s.relevant.add(item.EvidenceIDs)
		} else if group.level == "required" {
			if err := s.gap("technology_group", requirement, "technology_groups", nil, group.alternatives); err != nil {
				return err
			}
		} else if err := retain(s, &s.result.MissingPreferred, requirement); err != nil {
			return err
		}
	}
	return nil
}

// Python round(x, 1) rounds the binary value, not x*10 followed by integer
// rounding. Fixed decimal formatting preserves that tie behavior and the .0 in
// source-facing messages. The validated duration bounds stay below 1e16 years.
func roundedYears(months float64) (json.Number, float64) {
	text := strconv.FormatFloat(months/12, 'f', 1, 64)
	number, _ := strconv.ParseFloat(text, 64) // generated finite fixed decimal
	return json.Number(text), number
}

// Python 3.12 sum(float...) compensates for lost low-order bits. Calendar
// preparation emits integral months, but supported imported duration totals can
// be fractional; preserve the pinned source sum instead of naive addition.
func durationSum(ids []string, durations map[string]float64) float64 {
	hi, lo := 0.0, 0.0
	for _, id := range ids {
		value := durations[id]
		next := hi + value
		if math.Abs(hi) >= math.Abs(value) {
			lo += (hi - next) + value
		} else {
			lo += (value - next) + hi
		}
		hi = next
	}
	return hi + lo
}

func (s *evaluation) experience() *Error {
	for _, requirement := range s.job.experience {
		matches, err := s.match(requirement.area)
		if err != nil {
			return err
		}
		if err := s.hint("experience", requirement.area, matches.Taxonomy, nil); err != nil {
			return err
		}
		item := ExperienceReview{MinimumYears: requirement.minimum, MaximumYears: requirement.maximum, Area: requirement.area,
			Status: "manual_review", EvidenceIDs: matches.Direct, SourceEvidence: requirement.evidence, ConceptIDs: matches.ConceptIDs}
		months := durationSum(matches.Direct, s.input.DurationMonths) // sorted, as in the source sum
		message := fmt.Sprintf("Verify %s years in %s; Career Vault does not store verified duration by domain.", requirement.label, requirement.area)
		if months > 0 {
			derived, rounded := roundedYears(months)
			item.DerivedYears = &derived
			switch {
			case (months/12 >= requirement.threshold) != (rounded >= requirement.threshold):
				// Display rounding is not evidence of meeting or missing the
				// exact threshold. Keep borderline cases advisory, not blockers.
				message = fmt.Sprintf("Career Vault dates total %s months in %s; rounding to %s years crosses the %s-year threshold. Verify manually before applying.",
					strconv.FormatFloat(months, 'f', -1, 64), requirement.area, derived, requirement.minimum)
			case rounded >= requirement.threshold:
				item.Status = "derived_match"
				message = fmt.Sprintf("Derived %s years in %s from Career Vault experience dates (%s required); confirm before applying.", derived, requirement.area, requirement.label)
			default:
				item.Status = "derived_shortfall"
				fact := CandidateFact{Field: "career_vault.experience_dates", Value: derived,
					Statement: fmt.Sprintf("Career Vault dates derive %s years in %s.", derived, requirement.area)}
				item.CandidateFact = &fact
				findingMessage := fmt.Sprintf("Posting requires %s years in %s; Career Vault dates derive %s years.", requirement.label, requirement.area, derived)
				if err := s.policyFinding(minimumExperience, "experience_minimum_shortfall", findingMessage, "experience_requirements", fact, item.SourceEvidence); err != nil {
					return err
				}
				message = fmt.Sprintf("Derived %s years in %s, below required %s years; verify manually or update the Career Vault.", derived, requirement.area, requirement.label)
			}
		}
		if err := retain(s, &s.result.ManualReview, message); err != nil {
			return err
		}
		if err := retain(s, &s.result.ExperienceReview, item); err != nil {
			return err
		}
		s.relevant.add(matches.Direct)
	}
	for _, requirement := range s.job.education {
		if err := retain(s, &s.result.ManualReview, "Verify education requirement manually: "+requirement); err != nil {
			return err
		}
	}
	return nil
}

func (s *evaluation) classified() *Error {
	for _, requirement := range s.job.classified {
		matches, err := s.match(requirement.name)
		if err != nil {
			return err
		}
		status := "manual_review"
		if len(matches.Direct) != 0 {
			status = "matched_direct"
		}
		item := ClassifiedReview{Category: requirement.category, Name: requirement.name, Requirement: requirement.level,
			SourceText: requirement.text, SourceEvidence: requirement.evidence, Status: status,
			EvidenceIDs: matches.Direct, TaxonomySkillIDs: matches.Taxonomy, ConceptIDs: matches.ConceptIDs}
		if err := retain(s, &s.result.ClassifiedRequirementReview, item); err != nil {
			return err
		}
		s.relevant.add(matches.Direct)
		if err := s.hint(requirement.category, requirement.name, matches.Taxonomy, nil); err != nil {
			return err
		}
	}
	return nil
}

// Finding emitted when the digest recognized no requirement section at all.
// Without it an unparsed posting reads exactly like a posting the candidate
// fully covers. It is advisory: the lane moves to Review and nothing blocks.
const (
	NoRequirementSectionsCode         = "requirement_sections_not_recognized"
	NoRequirementSectionsStatus       = "requirement_basis_missing"
	noRequirementSectionsMessage      = "No requirement sections were recognized in this posting; the assessment has no requirement basis. Review the original job description and correct the saved JD facts."
	PartialRequirementSectionsCode    = "requirement_sections_partially_recognized"
	PartialRequirementSectionsStatus  = "requirement_basis_incomplete"
	partialRequirementSectionsMessage = "Some section headings were not recognized in this posting; the assessment may be missing requirements. Review the original job description and correct the saved JD facts."
)

func (s *evaluation) coverage() *Error {
	if s.job.noRequirementSections {
		return retain(s, &s.result.RequirementGaps, RequirementGap{Code: NoRequirementSectionsCode, Status: NoRequirementSectionsStatus,
			Severity: "medium", Category: "coverage", Requirement: "Requirement sections",
			Source: "digest.analysis_coverage.no_requirement_sections", Message: noRequirementSectionsMessage})
	}
	if s.job.unrecognizedRequirementSections {
		return retain(s, &s.result.RequirementGaps, RequirementGap{Code: PartialRequirementSectionsCode, Status: PartialRequirementSectionsStatus,
			Severity: "medium", Category: "coverage", Requirement: "Requirement sections",
			Source: "digest.analysis_coverage.unrecognized_headings", Message: partialRequirementSectionsMessage})
	}
	return nil
}

func (s *evaluation) finish() *Error {
	result := s.result
	classifiedGap := ""
	for _, item := range result.ClassifiedRequirementReview {
		if item.Requirement == "required" && item.Status == "manual_review" {
			classifiedGap = fmt.Sprintf("Review required %s requirement: %s.", item.Category, item.Name)
			break
		}
	}
	experienceReview := false
	for _, item := range result.ExperienceReview {
		if item.Status == "manual_review" || item.Status == "derived_shortfall" {
			experienceReview = true
		}
	}
	result.Lane = "REVIEW_RECOMMENDED"
	if len(result.EligibilityBlockers)+len(result.PolicyReviews)+len(result.RequirementGaps) == 0 && classifiedGap == "" && !experienceReview {
		preferredCount := len(s.job.preferred)
		for _, group := range s.job.groups {
			if group.level == "preferred" {
				preferredCount++
			}
		}
		result.Lane = "PRIORITY_MANUAL"
		if preferredCount > 0 && 2*len(result.MatchedPreferred) < preferredCount {
			result.Lane = "ASSISTED_AUTO_DRAFT"
		}
	}
	switch {
	case len(result.EligibilityBlockers) != 0:
		result.BiggestGap = result.EligibilityBlockers[0].Message
	case len(result.PolicyReviews) != 0:
		result.BiggestGap = result.PolicyReviews[0].Message
	case len(result.RequirementGaps) != 0:
		result.BiggestGap = result.RequirementGaps[0].Message
	case classifiedGap != "":
		result.BiggestGap = classifiedGap
	case len(result.ManualReview) != 0:
		result.BiggestGap = result.ManualReview[0]
	case len(result.MissingPreferred) != 0:
		result.BiggestGap = "Missing preferred evidence: " + result.MissingPreferred[0]
	default:
		result.BiggestGap = "No material evidence gap found by deterministic review."
	}
	result.RelevantEvidenceIDs = s.relevant.sorted()
	if err := s.budget.reserve(result.RelevantEvidenceIDs); err != nil {
		return err
	}
	return s.budget.reserve(result.BiggestGap)
}
