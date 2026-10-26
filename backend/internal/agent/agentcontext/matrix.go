package agentcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"careerhub.local/backend/internal/jobs/jobevaluation"
	"careerhub.local/backend/internal/jobs/jobevidence"
)

// Matrix is the source-v2 requirement/evidence projection, not a fit score.
// Taxonomy IDs describe vocabulary, never proof of a candidate's skill.
type Matrix struct {
	SchemaVersion      int         `json:"schema_version"`
	JobID              string      `json:"job_id"`
	ApplicationAllowed bool        `json:"application_allowed"`
	ReviewLane         string      `json:"review_lane"`
	Requirements       []MatrixRow `json:"requirements"`
}

type MatrixRow struct {
	ID                  string          `json:"id"`
	Priority            string          `json:"priority"`
	Category            string          `json:"category"`
	Requirement         string          `json:"requirement"`
	Operator            string          `json:"operator,omitempty"`
	Alternatives        *[]string       `json:"alternatives,omitempty"`
	MatchedAlternatives *[]string       `json:"matched_alternatives,omitempty"`
	MinimumYears        json.RawMessage `json:"minimum_years,omitempty"`
	MaximumYears        json.RawMessage `json:"maximum_years,omitempty"`
	SourceText          *string         `json:"source_text,omitempty"`
	SourceEvidence      *[]any          `json:"source_evidence,omitempty"`
	Status              string          `json:"status"`
	DirectEvidenceIDs   []string        `json:"direct_evidence_ids"`
	TaxonomySkillIDs    []string        `json:"taxonomy_skill_ids"`
	ConceptIDs          []string        `json:"concept_ids,omitempty"`
}

type matrixJob struct {
	ID           string   `json:"id"`
	Skills       []string `json:"required_skills"`
	Platforms    []string `json:"required_platforms"`
	Capabilities []string `json:"required_capabilities"`
	Preferred    []string `json:"preferred_skills"`
	Education    []string `json:"education_requirements"`
	Experience   []struct {
		Area    string          `json:"area"`
		Minimum json.RawMessage `json:"minimum_years"`
		Maximum json.RawMessage `json:"maximum_years"`
	} `json:"experience_requirements"`
	Groups json.RawMessage `json:"technology_groups"`
	Digest json.RawMessage `json:"digest"`
}
type matrixGroup struct {
	Priority     string   `json:"requirement"`
	Technologies []string `json:"technologies"`
}
type matrixClassified struct {
	Category string `json:"category"`
	Name     string `json:"name"`
	Priority string `json:"requirement"`
	Text     string `json:"text"`
	Evidence []any  `json:"evidence"`
}

func contextStopped(ctx context.Context) *Error {
	if ctx == nil || ctx.Err() != nil {
		return failure("CANCELED")
	}
	return nil
}
func copiedStrings(values []string) []string { return append([]string{}, values...) }

func decodeNumbers(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(destination)
}

// BuildEvidenceMatrix consumes an already validated, bounded JSON Job Card and
// the corresponding current Evaluate result. It is not a replacement for the
// card/digest validator or the coherent-snapshot read owner. Inputs are borrowed
// only for this call; output is detached. No files or private data are read.
func BuildEvidenceMatrix(ctx context.Context, jobJSON []byte, review jobevaluation.Result) (*Matrix, *Error) {
	if err := contextStopped(ctx); err != nil {
		return nil, err
	}
	if len(jobJSON) > jobevaluation.MaxInputBytes {
		return nil, failure("MATRIX_LIMIT")
	}
	var job matrixJob
	if !json.Valid(jobJSON) || decodeNumbers(jobJSON, &job) != nil || job.ID == "" || job.ID != review.JobID ||
		job.Skills == nil || job.Platforms == nil || job.Capabilities == nil || job.Preferred == nil || job.Education == nil || job.Experience == nil {
		return nil, failure("MATRIX_INPUT")
	}
	var digest struct {
		Groups     json.RawMessage `json:"technology_groups"`
		Classified json.RawMessage `json:"classified_requirements"`
	}
	// Python's fallback helpers ignore a non-mapping digest and non-list classified data.
	if len(job.Digest) > 0 && job.Digest[0] == '{' {
		if json.Unmarshal(job.Digest, &digest) != nil {
			return nil, failure("MATRIX_INPUT")
		}
	}
	groupsJSON := job.Groups
	if len(groupsJSON) == 0 {
		groupsJSON = digest.Groups
	}
	groups := []matrixGroup{}
	if len(groupsJSON) > 0 && (json.Unmarshal(groupsJSON, &groups) != nil || groups == nil) {
		return nil, failure("MATRIX_INPUT")
	}
	classified := []matrixClassified{}
	if len(digest.Classified) > 0 && digest.Classified[0] == '[' {
		if decodeNumbers(digest.Classified, &classified) != nil {
			return nil, failure("MATRIX_INPUT")
		}
	}
	count := len(job.Skills) + len(job.Platforms) + len(job.Capabilities) + len(job.Preferred) + len(job.Education) + len(job.Experience) + len(groups) + len(classified)
	if count > jobevaluation.MaxRequirements {
		return nil, failure("MATRIX_LIMIT")
	}
	type key struct{ category, label string }
	required := map[key]jobevaluation.MatchedRequirement{}
	preferred := map[string]jobevaluation.MatchedRequirement{}
	hints := map[key][]string{}
	for _, item := range review.MatchedRequired {
		required[key{item.Category, item.Requirement}] = item
	}
	for _, item := range review.MatchedPreferred {
		preferred[item.Requirement] = item
	}
	for _, item := range review.TaxonomyHints {
		hints[key{item.Category, item.Requirement}] = item.SkillIDs
	}
	experience := map[string][]string{}
	for _, item := range review.ExperienceReview {
		experience[item.Area] = item.EvidenceIDs
	}
	type classifiedKey struct{ category, name, text string }
	classifiedReview := map[classifiedKey]jobevaluation.ClassifiedReview{}
	for _, item := range review.ClassifiedRequirementReview {
		classifiedReview[classifiedKey{item.Category, item.Name, item.SourceText}] = item
	}
	concepts := map[string]map[string]bool{}
	addConcepts := func(label string, ids []string) {
		if concepts[label] == nil {
			concepts[label] = map[string]bool{}
		}
		for _, id := range ids {
			concepts[label][id] = true
		}
	}
	for _, item := range review.MatchedRequired {
		addConcepts(item.Requirement, item.ConceptIDs)
	}
	for _, item := range review.MatchedPreferred {
		addConcepts(item.Requirement, item.ConceptIDs)
	}
	for _, item := range review.RequirementGaps {
		addConcepts(item.Requirement, item.ConceptIDs)
	}
	for _, item := range review.ExperienceReview {
		addConcepts(item.Area, item.ConceptIDs)
	}
	for _, item := range review.ClassifiedRequirementReview {
		addConcepts(item.Name, item.ConceptIDs)
	}
	out := &Matrix{2, job.ID, review.ApplicationAllowed, review.Lane, []MatrixRow{}}
	outputBytes := len(job.ID) + len(review.Lane) + 256
	appendRow := func(row MatrixRow) *Error {
		if err := contextStopped(ctx); err != nil {
			return err
		}
		for id := range concepts[row.Requirement] {
			row.ConceptIDs = append(row.ConceptIDs, id)
		}
		slices.Sort(row.ConceptIDs)
		raw, err := json.Marshal(row)
		outputBytes += len(raw) + 1
		if err != nil {
			return failure("MATRIX_INPUT")
		}
		if outputBytes > jobevaluation.MaxResultBytes {
			return failure("MATRIX_LIMIT")
		}
		// Detach nested provenance and all slices as well as the year tokens.
		var detached MatrixRow
		if decodeNumbers(raw, &detached) != nil {
			return failure("MATRIX_INPUT")
		}
		out.Requirements = append(out.Requirements, detached)
		return nil
	}
	rowFor := func(id, priority, category, label string, direct, taxonomy []string) MatrixRow {
		status := "missing_direct"
		if len(direct) > 0 {
			status = "matched_direct"
		}
		return MatrixRow{ID: id, Priority: priority, Category: category, Requirement: label, Status: status, DirectEvidenceIDs: copiedStrings(direct), TaxonomySkillIDs: copiedStrings(taxonomy)}
	}
	for _, group := range []struct {
		category string
		labels   []string
	}{{"skill", job.Skills}, {"platform", job.Platforms}, {"capability", job.Capabilities}, {"preferred", job.Preferred}} {
		for i, label := range group.labels {
			priority, category, prefix := "required", group.category, "required_"+group.category
			match := required[key{category, label}]
			if category == "preferred" {
				priority, category, prefix = "preferred", "skill", "preferred_skill"
				match = preferred[label]
			}
			row := rowFor(fmt.Sprintf("%s_%02d", prefix, i+1), priority, category, label, match.EvidenceIDs, hints[key{group.category, label}])
			if err := appendRow(row); err != nil {
				return nil, err
			}
		}
	}
	groupCounts := map[string]int{}
	for _, group := range groups {
		if group.Priority != "required" && group.Priority != "preferred" {
			continue
		}
		label, err := jobevidence.TechnologyGroupLabel(ctx, group.Technologies)
		if err != nil {
			return nil, failure("MATRIX_INPUT")
		}
		groupCounts[group.Priority]++
		match := required[key{"technology_group", label}]
		if group.Priority == "preferred" {
			match = preferred[label]
		}
		row := rowFor(fmt.Sprintf("%s_technology_group_%02d", group.Priority, groupCounts[group.Priority]), group.Priority, "technology_group", label, match.EvidenceIDs, hints[key{"technology_group", label}])
		alternatives, matched := copiedStrings(group.Technologies), copiedStrings(match.MatchedAlternatives)
		row.Operator, row.Alternatives, row.MatchedAlternatives = "any_of", &alternatives, &matched
		if err := appendRow(row); err != nil {
			return nil, err
		}
	}
	for i, item := range job.Experience {
		if len(item.Minimum) == 0 {
			return nil, failure("MATRIX_INPUT")
		}
		row := rowFor(fmt.Sprintf("experience_%02d", i+1), "required", "experience", item.Area, experience[item.Area], hints[key{"experience", item.Area}])
		row.MinimumYears, row.MaximumYears, row.Status = item.Minimum, item.Maximum, "manual_review"
		if err := appendRow(row); err != nil {
			return nil, err
		}
	}
	for i, label := range job.Education {
		row := rowFor(fmt.Sprintf("education_%02d", i+1), "required", "education", label, nil, nil)
		row.Status = "manual_review"
		if err := appendRow(row); err != nil {
			return nil, err
		}
	}
	categoryCounts := map[string]int{}
	for _, item := range classified {
		categoryCounts[item.Category]++
		match, found := classifiedReview[classifiedKey{item.Category, item.Name, item.Text}]
		row := rowFor(fmt.Sprintf("classified_%s_%02d", item.Category, categoryCounts[item.Category]), item.Priority, item.Category, item.Name, match.EvidenceIDs, match.TaxonomySkillIDs)
		row.Status = "manual_review"
		if found {
			row.Status = match.Status
		}
		evidence := append([]any{}, item.Evidence...)
		row.SourceText, row.SourceEvidence = &item.Text, &evidence
		if err := appendRow(row); err != nil {
			return nil, err
		}
	}
	if err := contextStopped(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
