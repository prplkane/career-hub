package discoverypreview

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/jobs/jobdigest"
	"careerhub.local/backend/internal/jobs/jobevaluation"
	"careerhub.local/backend/internal/textcompat"
)

func bounded(value string) string {
	value = strings.Join(strings.FieldsFunc(value, textcompat.IsSpace), " ")
	count := 0
	for offset := range value {
		if count == MaxTextRunes {
			return value[:offset]
		}
		count++
	}
	return value
}
func sourceLine(value any) (int, bool) {
	var number int64
	switch v := value.(type) {
	case int:
		if v < 1 {
			return 0, false
		}
		return v, true
	case int64:
		number = v
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			return 0, false
		}
		var err error
		number, err = strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	if number < 1 || number > int64(math.MaxInt) {
		return 0, false
	}
	return int(number), true
}
func firstExcerpt(values []any) *Excerpt {
	for _, value := range values {
		fields, ok := value.(map[string]any)
		if !ok {
			continue
		}
		text, _ := fields["text"].(string)
		text = bounded(text)
		line, ok := sourceLine(fields["line"])
		if text != "" && ok {
			return &Excerpt{line, text}
		}
	}
	return nil
}
func unknown(digest *jobdigest.Result) []Excerpt {
	result := []Excerpt{}
	for _, item := range digest.Coverage.ReviewItems {
		if text := bounded(item.Text); item.Line > 0 && text != "" {
			result = append(result, Excerpt{item.Line, text})
		}
	}
	return result
}
func receipt(ctx context.Context, assessment *jobevaluation.Result, vocabulary []Excerpt, scope string, steps, contacts int) (Receipt, error) {
	result := Receipt{SchemaVersion: ReceiptVersion, Scope: scope, Availability: "available",
		Blockers: []Blocker{}, ReviewItems: []Review{}, UnknownVocabularyCount: len(vocabulary),
		ExtraStepCount: max(0, steps), ContactCandidateCount: max(0, contacts)}
	incomplete, reviews := 0, 0
	for _, finding := range assessment.EligibilityBlockers {
		if err := stopped(ctx); err != nil {
			return Receipt{}, err
		}
		excerpt, candidate := firstExcerpt(finding.SourceEvidence), bounded(finding.CandidateFact.Statement)
		if excerpt == nil || candidate == "" {
			incomplete++
			continue
		}
		result.BlockerCount++
		if len(result.Blockers) < MaxFindings {
			result.Blockers = append(result.Blockers, Blocker{bounded(finding.Code), bounded(finding.Message), *excerpt, candidate})
		}
	}
	add := func(item Review) {
		if item.Text == "" {
			return
		}
		reviews++
		if len(result.ReviewItems) < MaxFindings {
			result.ReviewItems = append(result.ReviewItems, item)
		}
	}
	for _, item := range assessment.RequirementGaps {
		if err := stopped(ctx); err != nil {
			return Receipt{}, err
		}
		kind := "evidence_not_found"
		if item.Category == "coverage" {
			kind = "coverage"
		}
		add(Review{Kind: kind, Text: bounded(item.Message)})
	}
	for _, item := range assessment.MissingPreferred {
		if err := stopped(ctx); err != nil {
			return Receipt{}, err
		}
		if bounded(item) != "" {
			add(Review{Kind: "preferred_evidence_not_found", Text: bounded("No direct Career Vault evidence found for preferred skill: " + item + ".")})
		}
	}
	for _, item := range vocabulary {
		if err := stopped(ctx); err != nil {
			return Receipt{}, err
		}
		line := item.Line
		add(Review{Kind: "unknown_vocabulary", Text: item.Text, Line: &line})
	}
	for _, finding := range assessment.PolicyReviews {
		if err := stopped(ctx); err != nil {
			return Receipt{}, err
		}
		item := Review{Kind: "policy_review", Text: bounded(finding.Message), CandidateFact: bounded(finding.CandidateFact.Statement), SourceEvidence: firstExcerpt(finding.SourceEvidence)}
		if item.SourceEvidence != nil {
			line := item.SourceEvidence.Line
			item.Line = &line
		}
		add(item)
	}
	for _, item := range assessment.ClassifiedRequirementReview {
		if err := stopped(ctx); err != nil {
			return Receipt{}, err
		}
		if item.Requirement == "required" && item.Status == "manual_review" {
			add(Review{Kind: "required_requirement", Text: bounded(item.SourceText)})
		}
	}
	for _, item := range assessment.ExperienceReview {
		if err := stopped(ctx); err != nil {
			return Receipt{}, err
		}
		if item.Status == "manual_review" {
			add(Review{Kind: "experience", Text: bounded(fmt.Sprintf("Verify %s+ years in %s.", item.MinimumYears, bounded(item.Area)))})
		}
	}
	for _, item := range assessment.ManualReview {
		if err := stopped(ctx); err != nil {
			return Receipt{}, err
		}
		add(Review{Kind: "manual_review", Text: bounded(item)})
	}
	result.ReviewCount = reviews + incomplete
	result.Status, result.Label = "no_clear_conflict", "No clear conflict found"
	if result.BlockerCount > 0 {
		result.Status, result.Label = "clear_conflict", "Clear conflict found"
	} else if result.ReviewCount > 0 {
		result.Status, result.Label = "review", "Review needed"
	}
	result.Truncated = result.BlockerCount > len(result.Blockers) || reviews > len(result.ReviewItems)
	if err := stopped(ctx); err != nil {
		return Receipt{}, err
	}
	return result, nil
}

// Unavailable never implies candidate compatibility. Reason is a fixed service
// category, not exception text; partial capture ignores context failure reasons.
func Unavailable(scope, reason string, steps, contacts int) (Receipt, error) {
	if scope != "link_only" && scope != "selection" && !Eligible(scope) {
		return Receipt{}, ErrInput
	}
	if Eligible(scope) && reason != "context_unavailable" {
		return Receipt{}, ErrInput
	}
	if steps < 0 || contacts < 0 || steps > 24 || contacts > 24 {
		return Receipt{}, ErrInput
	}
	result := Receipt{SchemaVersion: ReceiptVersion, Scope: scope, Status: "unavailable", Label: "Assessment unavailable",
		Availability: "partial_capture", Blockers: []Blocker{}, ReviewItems: []Review{}, ExtraStepCount: steps, ContactCandidateCount: contacts}
	if Eligible(scope) {
		result.Status, result.Label, result.Availability = "review", "Review needed", reason
		result.ReviewCount = 1
		result.ReviewItems = append(result.ReviewItems, Review{Kind: "preview_unavailable", Text: "Career Vault context is not ready for this preview."})
	}
	return result, nil
}
func PartialInstructions(scope string) (Instructions, error) {
	if scope != "link_only" && scope != "selection" {
		return Instructions{}, ErrInput
	}
	scan := applicationanalysis.Scan{SchemaVersion: 1, ExtractorVersion: 1, Status: "full_jd_required"}
	return Instructions{InstructionResult: applicationanalysis.InstructionResult{Scan: scan, Items: []applicationanalysis.Instruction{}}, Message: "Full JD required", Scope: scope}, nil
}
func PartialContacts(scope string) (Contacts, error) {
	if scope != "link_only" && scope != "selection" {
		return Contacts{}, ErrInput
	}
	scan := applicationanalysis.Scan{SchemaVersion: 1, ExtractorVersion: 1, Status: "full_jd_required"}
	return Contacts{ContactResult: applicationanalysis.ContactResult{Scan: scan, Candidates: []applicationanalysis.ContactCandidate{}}, Message: "Full JD required", Scope: scope}, nil
}
func validText(value string, maximum int) bool {
	return len(value) <= maximum && utf8.ValidString(value)
}
