package agentcontext

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"careerhub.local/backend/internal/jobevaluation"
	"careerhub.local/backend/internal/skills/catalog"
)

type CoverageCount struct {
	MatchedDirect       int          `json:"matched_direct"`
	Total               int          `json:"total"`
	Ratio               *json.Number `json:"ratio"`
	UnparsedReviewCount int          `json:"unparsed_review_count"`
}
type RequiredCoverage struct {
	CoverageCount
	ParsingComplete      bool        `json:"parsing_complete"`
	SourceLineCount      json.Number `json:"source_line_count"`
	ManualReviewRequired bool        `json:"manual_review_required"`
}
type Coverage struct {
	Required          RequiredCoverage `json:"required"`
	Preferred         CoverageCount    `json:"preferred"`
	ManualReviewCount int              `json:"manual_review_count"`
}
type Recommendation struct {
	Verdict      string   `json:"verdict"`
	AdvisoryOnly bool     `json:"advisory_only"`
	Reasons      []string `json:"reasons"`
	BiggestGap   string   `json:"biggest_gap"`
}
type CompactAssessment struct {
	Coverage       Coverage         `json:"coverage"`
	Recommendation Recommendation   `json:"recommendation"`
	TopEvidence    []map[string]any `json:"top_evidence"`
}

func decodedContextObject(ctx context.Context, raw []byte, maximum int) (map[string]any, *Error) {
	if err := contextStopped(ctx); err != nil {
		return nil, err
	}
	if len(raw) > maximum {
		return nil, failure("COMPACT_LIMIT")
	}
	var object map[string]any
	if !json.Valid(raw) || decodeNumbers(raw, &object) != nil || object == nil {
		return nil, failure("COMPACT_INPUT")
	}
	budget := contextJSONBudget{ctx: ctx, maximum: maximum}
	if err := budget.check(object, 0); err != nil {
		return nil, err
	}
	return object, nil
}

func summarizeCoverage(matched, count, unparsed int) CoverageCount {
	out := CoverageCount{MatchedDirect: matched, Total: count + unparsed, UnparsedReviewCount: unparsed}
	if out.Total > 0 {
		// Like Python round(x,2), round the binary division result itself, not
		// x*100 followed by integer rounding. Keep float-kind .0 in JSON.
		text := strings.TrimRight(strconv.FormatFloat(float64(matched)/float64(out.Total), 'f', 2, 64), "0")
		if strings.HasSuffix(text, ".") {
			text += "0"
		}
		ratio := json.Number(text)
		out.Ratio = &ratio
	}
	return out
}
func compactCoverage(ctx context.Context, matrix Matrix, card map[string]any) (Coverage, *Error) {
	if len(matrix.Requirements) > jobevaluation.MaxRequirements {
		return Coverage{}, failure("COMPACT_LIMIT")
	}
	required, matchedRequired, preferred, matchedPreferred, manual := 0, 0, 0, 0, 0
	for _, row := range matrix.Requirements {
		if err := contextStopped(ctx); err != nil {
			return Coverage{}, err
		}
		if row.Priority == "required" && slices.Contains([]string{"skill", "platform", "capability", "technology_group", "technical"}, row.Category) {
			required++
			if row.Status == "matched_direct" {
				matchedRequired++
			}
		}
		if row.Priority == "preferred" {
			preferred++
			if row.Status == "matched_direct" {
				matchedPreferred++
			}
		}
		if row.Status == "manual_review" {
			manual++
		}
	}
	digest, _ := card["digest"].(map[string]any)
	analysis := map[string]any{}
	if v, exists := digest["analysis_coverage"]; exists {
		var ok bool
		analysis, ok = v.(map[string]any)
		if !ok {
			return Coverage{}, failure("COMPACT_INPUT")
		}
	}
	items := []any{}
	if v, exists := analysis["review_items"]; exists {
		var ok bool
		items, ok = v.([]any)
		if !ok {
			return Coverage{}, failure("COMPACT_INPUT")
		}
	}
	if len(items) > 10000 {
		return Coverage{}, failure("COMPACT_LIMIT")
	}
	unparsedRequired, unparsedPreferred := 0, 0
	for _, value := range items {
		if item, ok := value.(map[string]any); ok {
			switch item["section"] {
			case "required":
				unparsedRequired++
			case "preferred":
				unparsedPreferred++
			}
		}
	}
	lines := json.Number("0")
	if v, exists := analysis["source_lines"]; exists {
		// Valid Job Cards require a non-negative integer. Preserve arbitrarily
		// large valid integers, not floating-point conversion or machine overflow.
		var ok bool
		lines, ok = v.(json.Number)
		if !ok || len(lines) == 0 || len(lines) > 4300 || strings.ContainsAny(string(lines), "-.eE") {
			return Coverage{}, failure("COMPACT_INPUT")
		}
	}
	rich := len(lines) > 1 && required == 0 && unparsedRequired == 0
	out := Coverage{
		Required:          RequiredCoverage{summarizeCoverage(matchedRequired, required, unparsedRequired), unparsedRequired == 0 && !rich, lines, unparsedRequired > 0 || rich},
		Preferred:         summarizeCoverage(matchedPreferred, preferred, unparsedPreferred),
		ManualReviewCount: manual + unparsedRequired + unparsedPreferred,
	}
	if rich {
		out.ManualReviewCount++
	}
	return out, nil
}
func compactRecommendation(review jobevaluation.Result, coverage Coverage) Recommendation {
	required := coverage.Required
	ratio := 0.0
	if required.Ratio != nil {
		ratio, _ = strconv.ParseFloat(string(*required.Ratio), 64)
	}
	high := 0
	for _, gap := range review.RequirementGaps {
		if gap.Severity == "high" {
			high++
		}
	}
	out := Recommendation{Verdict: "CONSIDER", AdvisoryOnly: true, Reasons: []string{}, BiggestGap: review.BiggestGap}
	if len(review.EligibilityBlockers) > 0 {
		out.Verdict = "NO_GO"
		out.Reasons = append(out.Reasons, review.EligibilityBlockers[0].Message)
	} else {
		if required.UnparsedReviewCount == 0 && required.ParsingComplete && required.Total > 0 && high <= 1 && required.Ratio != nil && ratio >= 0.5 {
			out.Verdict = "GO"
		}
		out.Reasons = append(out.Reasons, "No explicit deterministic eligibility conflict was found.")
	}
	out.Reasons = append(out.Reasons, fmt.Sprintf("Direct evidence covers %d of %d required technical items, including lines routed to review.", required.MatchedDirect, required.Total))
	if required.UnparsedReviewCount > 0 {
		out.Reasons = append(out.Reasons, fmt.Sprintf("%d required line(s) need manual review before a GO verdict.", required.UnparsedReviewCount))
	} else if required.Total == 0 {
		out.Reasons = append(out.Reasons, "No required technical items were parsed; review the source before a GO verdict.")
	}
	if review.BiggestGap != "" {
		out.Reasons = append(out.Reasons, review.BiggestGap)
	}
	out.Reasons = out.Reasons[:min(3, len(out.Reasons))]
	return out
}

func compactEvidence(item map[string]any, proofLimit int) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"id", "evidence_type", "company", "role", "dates", "name", "category", "summary", "credential", "completed"} {
		value, exists := item[key]
		if !exists || value == nil {
			continue
		}
		if text, ok := value.(string); ok && text == "" {
			continue
		}
		if list, ok := value.([]any); ok && len(list) == 0 {
			continue
		}
		out[key] = value
	}
	if technologies, ok := item["technologies"].([]any); ok {
		out["technologies"] = technologies[:min(12, len(technologies))]
	}
	if proofs, ok := item["proof_points"].([]any); ok {
		out["proof_points"] = proofs[:min(proofLimit, len(proofs))]
	}
	return out
}

// BuildCompactAssessment never reassesses eligibility or infers candidate
// evidence from registry words. It projects a corresponding validated snapshot;
// unparsed JD lines remain in the denominator and prevent a GO recommendation.
func BuildCompactAssessment(ctx context.Context, matrix Matrix, jobJSON []byte, review jobevaluation.Result, modelJSON []byte, purpose string) (*CompactAssessment, *Error) {
	if !ValidPurpose(purpose) {
		return nil, failure("TASK_INVALID")
	}
	card, err := decodedContextObject(ctx, jobJSON, jobevaluation.MaxInputBytes)
	if err != nil {
		return nil, err
	}
	if card["id"] != matrix.JobID || review.JobID != matrix.JobID {
		return nil, failure("COMPACT_IDENTITY")
	}
	model, err := decodedContextObject(ctx, modelJSON, jobevaluation.MaxResultBytes)
	if err != nil {
		return nil, err
	}
	modelJob, _ := model["job"].(map[string]any)
	if modelJob["id"] != matrix.JobID {
		return nil, failure("COMPACT_IDENTITY")
	}
	coverage, err := compactCoverage(ctx, matrix, card)
	if err != nil {
		return nil, err
	}
	evidence, ok := model["candidate_direct_evidence"].([]any)
	if !ok {
		return nil, failure("COMPACT_INPUT")
	}
	proofLimit, evidenceLimit := 2, 6
	if purpose != "assessment" {
		proofLimit = 3
	}
	if purpose == "application_text" {
		evidenceLimit = 4
	}
	compact := []map[string]any{}
	for _, value := range evidence[:min(evidenceLimit, len(evidence))] {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, failure("COMPACT_INPUT")
		}
		compact = append(compact, compactEvidence(item, proofLimit))
	}
	if err := contextStopped(ctx); err != nil {
		return nil, err
	}
	return &CompactAssessment{coverage, compactRecommendation(review, coverage), compact}, nil
}

type RegistryGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type RegistryContext struct {
	Mode                  string           `json:"mode"`
	WriteAllowed          bool             `json:"write_allowed"`
	Recognized            []map[string]any `json:"recognized"`
	NeedsReview           []map[string]any `json:"needs_review_lines"`
	ActiveGroups          []RegistryGroup  `json:"active_groups"`
	ActiveGroupsTotal     int              `json:"active_groups_total"`
	ActiveGroupsTruncated bool             `json:"active_groups_truncated"`
	Warning               *string          `json:"warning"`
	Policy                string           `json:"policy"`
}

// BuildRegistryContext is vocabulary-only. The owner loads groups only when
// exact-auto is requested for a persisted card with a digest. An optional load
// failure must be a public-safe message, never a raw exception/private path.
func BuildRegistryContext(ctx context.Context, jobJSON []byte, mode string, persisted bool, groups []catalog.Group, loadFailure *string) (*RegistryContext, *Error) {
	if mode != "off" && mode != "exact-auto" {
		return nil, failure("REGISTRY_MODE")
	}
	card, err := decodedContextObject(ctx, jobJSON, jobevaluation.MaxInputBytes)
	if err != nil {
		return nil, err
	}
	out := &RegistryContext{Mode: mode, WriteAllowed: mode == "exact-auto" && persisted, Recognized: []map[string]any{}, NeedsReview: []map[string]any{}, ActiveGroups: []RegistryGroup{}, Policy: "Registry vocabulary is not candidate evidence."}
	digest, ok := card["digest"].(map[string]any)
	if !ok {
		return out, nil
	}
	occurrences := []any{}
	if v, exists := digest["skill_occurrences"]; exists {
		occurrences, ok = v.([]any)
		if !ok {
			return nil, failure("COMPACT_INPUT")
		}
	}
	coverage, _ := digest["analysis_coverage"].(map[string]any)
	reviews := []any{}
	if v, exists := coverage["review_items"]; exists {
		reviews, ok = v.([]any)
		if !ok {
			return nil, failure("COMPACT_INPUT")
		}
	}
	project := func(items []any, maximum int, keys []string) []map[string]any {
		result := []map[string]any{}
		for _, v := range items[:min(maximum, len(items))] {
			if item, ok := v.(map[string]any); ok {
				row := map[string]any{}
				for _, key := range keys {
					row[key] = item[key]
				}
				result = append(result, row)
			}
		}
		return result
	}
	out.Recognized = project(occurrences, 40, []string{"skill_id", "canonical_name", "source_text", "primary_group_id", "origin", "requirement", "line"})
	out.NeedsReview = project(reviews, 12, []string{"line", "section", "text"})
	if out.WriteAllowed {
		groupBudget := contextJSONBudget{ctx: ctx, maximum: jobevaluation.MaxInputBytes}
		if loadFailure != nil {
			if len(*loadFailure) > 4096 {
				return nil, failure("COMPACT_LIMIT")
			}
			warning := "Registry group context is unavailable: " + *loadFailure
			if err := groupBudget.check(warning, 0); err != nil {
				return nil, err
			}
			out.Warning = &warning
		} else {
			if len(groups) > 10000 {
				return nil, failure("COMPACT_LIMIT")
			}
			for _, group := range groups {
				if err := contextStopped(ctx); err != nil {
					return nil, err
				}
				if group.Archived {
					continue
				}
				out.ActiveGroupsTotal++
				if len(out.ActiveGroups) < 40 {
					if err := groupBudget.check([]any{group.ID, group.Name}, 0); err != nil {
						return nil, err
					}
					out.ActiveGroups = append(out.ActiveGroups, RegistryGroup{group.ID, group.Name})
				}
			}
		}
	}
	out.ActiveGroupsTruncated = out.ActiveGroupsTotal > len(out.ActiveGroups)
	if err := contextStopped(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
