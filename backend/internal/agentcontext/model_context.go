package agentcontext

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"unicode/utf8"

	"careerhub.local/backend/internal/jobevaluation"
)

// ModelSources contains JSON-compatible values from one admitted, validated
// snapshot, not paths to read. Nil represents an empty/null document, NOT proof
// that a required file exists. The future read owner must check file presence
// and YAML compatibility before calling this pure layer.
type ModelSources struct {
	Experiences json.RawMessage `json:"experiences"`
	Projects    json.RawMessage `json:"projects"`
	Education   json.RawMessage `json:"education"`
	Profile     json.RawMessage `json:"profile"`
	Visa        json.RawMessage `json:"visa"`
	Skills      json.RawMessage `json:"skills"`
	StyleRules  json.RawMessage `json:"style_rules"`
}

type ModelContextInput struct {
	Sources ModelSources
	// Nil uses all sorted relevant IDs; a pointer to an empty list includes none.
	ShortlistIDs  *[]string
	MatrixPath    string
	WorksheetPath string
	JobCardPath   string
}

// EvidenceContext is the shared, purpose-independent part of all three agent
// packets. Nothing is saved; a caller can hash this complete snapshot before
// applying purpose-specific compact projections.
type EvidenceContext struct {
	Matrix       *Matrix         `json:"matrix"`
	Worksheet    *Worksheet      `json:"worksheet"`
	ModelContext json.RawMessage `json:"model_context"`
}

func BuildEvidenceContext(ctx context.Context, jobJSON []byte, review jobevaluation.Result, input ModelSources, jobCardPath string) (*EvidenceContext, *Error) {
	matrix, err := BuildEvidenceMatrix(ctx, jobJSON, review)
	if err != nil {
		return nil, err
	}
	sources, err := decodeModelSources(ctx, input)
	if err != nil {
		return nil, err
	}
	catalog, err := buildSourceCatalog(ctx, sources)
	if err != nil {
		return nil, err
	}
	selectionCatalog := make([]CatalogEvidence, 0, len(catalog.order))
	for _, id := range catalog.order {
		item := catalog.items[id]
		entry := CatalogEvidence{ID: id, EvidenceType: item["evidence_type"].(string), RelatedExperience: []string{}}
		if entry.EvidenceType == "project" {
			refs, ok := item["related_experience"].([]any)
			if !ok {
				return nil, failure("MODEL_INPUT")
			}
			for _, v := range refs {
				ref, ok := v.(string)
				if !ok {
					return nil, failure("MODEL_INPUT")
				}
				entry.RelatedExperience = append(entry.RelatedExperience, ref)
			}
		}
		selectionCatalog = append(selectionCatalog, entry)
	}
	experiences := make([]SelectionExperience, 0, len(sources.experiences))
	for _, item := range sources.experiences {
		// Catalog preparation already checked required fields on every active row.
		experiences = append(experiences, SelectionExperience{item["id"].(string), item["company"], item["role"], item["dates"], item["category"], item["proof_points"]})
	}
	worksheet, ids, err := BuildSelectionWorksheet(ctx, *matrix, selectionCatalog, experiences, 6)
	if err != nil {
		return nil, err
	}
	var job map[string]any
	if decodeNumbers(jobJSON, &job) != nil {
		return nil, failure("MODEL_INPUT")
	}
	model, err := composeModelContext(ctx, job, review, ModelContextInput{Sources: input, ShortlistIDs: &ids, JobCardPath: jobCardPath}, sources, catalog)
	if err != nil {
		return nil, err
	}
	return &EvidenceContext{matrix, worksheet, model}, nil
}

type modelSources struct {
	experiences, projects, skills   []map[string]any
	education, profile, visa, style map[string]any
}

// sourceCatalog keeps Python dictionary insertion order, including when a later
// project/certification replaces an earlier ID without moving that ID's slot.
type sourceCatalog struct {
	order []string
	items map[string]map[string]any
}

type contextJSONBudget struct {
	ctx                   context.Context
	bytes, nodes, maximum int
}

// Conservative escaped-JSON accounting bounds projections BEFORE serialization.
// It accepts only decoded JSON values, never arbitrary marshaler implementations.
func (b *contextJSONBudget) check(value any, depth int) *Error {
	if err := contextStopped(b.ctx); err != nil {
		return err
	}
	b.nodes++
	b.bytes += 8
	if depth > 64 || b.nodes > 200000 || b.bytes > b.maximum {
		return failure("MODEL_LIMIT")
	}
	switch v := value.(type) {
	case nil, bool:
	case string:
		if !utf8.ValidString(v) {
			return failure("MODEL_INPUT")
		}
		b.bytes += 6 * len(v)
	case json.Number:
		if len(v) > 4300 || !json.Valid([]byte(v)) {
			return failure("MODEL_INPUT")
		}
		b.bytes += len(v)
	case []any:
		if len(v) > 200000-b.nodes {
			return failure("MODEL_LIMIT")
		}
		for _, item := range v {
			if err := b.check(item, depth+1); err != nil {
				return err
			}
		}
	case []string:
		if len(v) > 200000-b.nodes {
			return failure("MODEL_LIMIT")
		}
		for _, item := range v {
			if err := b.check(item, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		if len(v) > (200000-b.nodes)/2 {
			return failure("MODEL_LIMIT")
		}
		for key, item := range v {
			if err := b.check(key, depth+1); err != nil {
				return err
			}
			if err := b.check(item, depth+1); err != nil {
				return err
			}
		}
	default:
		return failure("MODEL_INPUT")
	}
	if b.bytes > b.maximum {
		return failure("MODEL_LIMIT")
	}
	return nil
}

func activeContextRecords(value any) []map[string]any {
	out := []map[string]any{}
	items, _ := value.([]any)
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		archived, _ := m["archived"].(bool)
		if !archived {
			out = append(out, m)
		}
	}
	return out
}
func modelMapping(value any) (map[string]any, *Error) {
	if value == nil || value == false || value == "" {
		return map[string]any{}, nil
	}
	if v, ok := value.(map[string]any); ok {
		return v, nil
	}
	if v, ok := value.([]any); ok && len(v) == 0 {
		return map[string]any{}, nil
	}
	if v, ok := value.(json.Number); ok {
		if number, err := strconv.ParseFloat(string(v), 64); err == nil && number == 0 {
			return map[string]any{}, nil
		}
	}
	return nil, failure("MODEL_INPUT")
}
func decodeModelSources(ctx context.Context, input ModelSources) (modelSources, *Error) {
	out := modelSources{}
	values := make([]any, 0, 7)
	budget := contextJSONBudget{ctx: ctx, maximum: jobevaluation.MaxInputBytes}
	rawBytes := 0
	for _, raw := range []json.RawMessage{input.Experiences, input.Projects, input.Skills, input.Education, input.Profile, input.Visa, input.StyleRules} {
		rawBytes += len(raw)
		if rawBytes > jobevaluation.MaxInputBytes {
			return out, failure("MODEL_LIMIT")
		}
		var value any
		if len(raw) > 0 && (!json.Valid(raw) || decodeNumbers(raw, &value) != nil) {
			return out, failure("MODEL_INPUT")
		}
		if err := budget.check(value, 0); err != nil {
			return out, err
		}
		values = append(values, value)
	}
	out.experiences = activeContextRecords(values[0])
	out.projects = activeContextRecords(values[1])
	out.skills = activeContextRecords(values[2])
	for i, to := range [](*map[string]any){&out.education, &out.profile, &out.visa, &out.style} {
		var err *Error
		*to, err = modelMapping(values[i+3])
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func projectContextFields(item map[string]any, keys []string, required bool) (map[string]any, *Error) {
	out := make(map[string]any, len(keys))
	for _, key := range keys {
		value, exists := item[key]
		if !exists && required {
			return nil, failure("MODEL_FIELD_MISSING")
		}
		if exists {
			out[key] = value
		}
	}
	return out, nil
}

func buildSourceCatalog(ctx context.Context, sources modelSources) (sourceCatalog, *Error) {
	out := sourceCatalog{order: []string{}, items: map[string]map[string]any{}}
	for _, group := range []struct {
		kind  string
		items []map[string]any
		keys  []string
	}{
		{"experience", sources.experiences, []string{"id", "company", "role", "dates", "category", "technologies", "proof_points"}},
		{"project", sources.projects, []string{"id", "name", "category", "summary", "technologies", "proof_points"}},
		{"certification", activeContextRecords(sources.education["certifications"]), []string{"id", "name", "credential", "completed", "proof_points"}},
	} {
		for _, item := range group.items {
			if err := contextStopped(ctx); err != nil {
				return sourceCatalog{}, err
			}
			projected, err := projectContextFields(item, group.keys, true)
			if err != nil {
				return sourceCatalog{}, err
			}
			id, ok := projected["id"].(string)
			if !ok || id == "" {
				return sourceCatalog{}, failure("MODEL_INPUT")
			}
			projected["evidence_type"] = group.kind
			if group.kind == "project" {
				refs, exists := item["related_experience"]
				if !exists {
					refs = []any{}
				}
				projected["related_experience"] = refs
			}
			if _, exists := out.items[id]; !exists {
				out.order = append(out.order, id)
			}
			out.items[id] = projected
		}
	}
	return out, nil
}

func fieldOr(item map[string]any, key string, fallback any) any {
	if v, exists := item[key]; exists {
		return v
	}
	return fallback
}
func sortedUnique(values []string) []string {
	out := copiedStrings(values)
	slices.Sort(out)
	return slices.Compact(out)
}

// BuildModelContext is the pure source-v2 document. Returned bytes contain only
// allowlisted candidate fields and selected evidence; unrelated contacts, notes,
// archived records and unselected proof never leak into the document. Provenance
// paths are inert labels supplied by the owner, not resolved or opened here.
// JSON value parity does not yet imply source-compatible ordered fingerprints.
func BuildModelContext(ctx context.Context, jobJSON []byte, review jobevaluation.Result, input ModelContextInput) (json.RawMessage, *Error) {
	if err := contextStopped(ctx); err != nil {
		return nil, err
	}
	if len(jobJSON) > jobevaluation.MaxInputBytes {
		return nil, failure("MODEL_LIMIT")
	}
	var job map[string]any
	if !json.Valid(jobJSON) || decodeNumbers(jobJSON, &job) != nil || job == nil {
		return nil, failure("MODEL_INPUT")
	}
	jobID, ok := job["id"].(string)
	if !ok || jobID == "" || jobID != review.JobID {
		return nil, failure("MODEL_IDENTITY")
	}
	sources, err := decodeModelSources(ctx, input.Sources)
	if err != nil {
		return nil, err
	}
	catalog, err := buildSourceCatalog(ctx, sources)
	if err != nil {
		return nil, err
	}
	return composeModelContext(ctx, job, review, input, sources, catalog)
}

func composeModelContext(ctx context.Context, job map[string]any, review jobevaluation.Result, input ModelContextInput, sources modelSources, catalog sourceCatalog) (json.RawMessage, *Error) {
	directIDs := sortedUnique(review.RelevantEvidenceIDs)
	allDirect := copiedStrings(directIDs)
	if input.ShortlistIDs != nil {
		directIDs = copiedStrings(*input.ShortlistIDs)
	}
	if len(directIDs) > maxSelectionRecords {
		return nil, failure("MODEL_LIMIT")
	}
	direct := []any{}
	selected := map[string]bool{}
	for _, id := range directIDs {
		item, exists := catalog.items[id]
		if !exists {
			return nil, failure("MODEL_DIRECT_REFERENCE")
		}
		direct = append(direct, item)
		selected[id] = true
	}
	omitted := []string{}
	for _, id := range allDirect {
		if !selected[id] {
			omitted = append(omitted, id)
		}
	}
	taxonomyByID := map[string]map[string]any{}
	for _, item := range sources.skills {
		id, ok := item["id"].(string)
		if !ok {
			return nil, failure("MODEL_INPUT")
		}
		taxonomyByID[id] = item
	}
	taxonomyIDs := []string{}
	for _, hint := range review.TaxonomyHints {
		taxonomyIDs = append(taxonomyIDs, hint.SkillIDs...)
	}
	taxonomy := []any{}
	for _, id := range sortedUnique(taxonomyIDs) {
		item, exists := taxonomyByID[id]
		if !exists {
			return nil, failure("MODEL_TAXONOMY_REFERENCE")
		}
		projection, err := projectContextFields(item, []string{"id", "skill_family"}, true)
		if err != nil {
			return nil, err
		}
		taxonomy = append(taxonomy, projection)
	}
	jobView, err := projectContextFields(job, []string{"id", "company", "title", "location", "remote_policy", "seniority", "responsibilities", "visa_signals"}, true)
	if err != nil {
		return nil, err
	}
	candidate := map[string]any{
		"current_location":            fieldOr(sources.profile, "current_location", ""),
		"target_location":             fieldOr(sources.profile, "target_location", ""),
		"location_flexibility":        fieldOr(sources.profile, "location_flexibility", ""),
		"work_authorization":          fieldOr(sources.visa, "work_authorization", ""),
		"requires_future_sponsorship": fieldOr(sources.visa, "requires_future_sponsorship", nil),
	}
	education, ok := job["education_requirements"].([]any)
	if !ok {
		return nil, failure("MODEL_INPUT")
	}
	if len(education) > 0 {
		schools := []any{}
		for _, school := range activeContextRecords(sources.education["schools"]) {
			view, _ := projectContextFields(school, []string{"id", "institution", "degree", "major", "location", "dates", "graduation_date", "gpa", "status", "honors"}, false)
			schools = append(schools, view)
		}
		candidate["education"] = schools
	}
	style, err := projectContextFields(sources.style, []string{"resume_voice", "bullet_rules", "selection_rules", "forbidden"}, true)
	if err != nil {
		return nil, err
	}
	// The typed evaluator result is bounded by its producer. Decode its JSON to
	// reuse precise numbers and retain exact nested findings without widening the
	// public document to unrelated assessment fields.
	reviewJSON, marshalErr := json.Marshal(review)
	if marshalErr != nil || len(reviewJSON) > jobevaluation.MaxResultBytes {
		return nil, failure("MODEL_INPUT")
	}
	var reviewValues map[string]any
	if decodeNumbers(reviewJSON, &reviewValues) != nil {
		return nil, failure("MODEL_INPUT")
	}
	unresolved, _ := projectContextFields(reviewValues, []string{"eligibility_blockers", "requirement_gaps", "missing_preferred", "manual_review"}, true)
	paths := map[string]any{"job_card": input.JobCardPath, "job_review": "outputs/job_reviews/" + review.JobID + ".yaml"}
	if input.WorksheetPath != "" {
		paths["selection_worksheet"] = input.WorksheetPath
	}
	output := map[string]any{
		"schema_version": json.Number("2"), "job": jobView,
		"review": map[string]any{
			"application_allowed": review.ApplicationAllowed, "lane": review.Lane, "biggest_gap": review.BiggestGap,
			"evidence_matrix_path": input.MatrixPath,
			"evidence_selection":   map[string]any{"policy": "required_coverage_3_preferred_coverage_1_max_6", "included_direct_evidence_ids": directIDs, "omitted_direct_evidence_ids": omitted, "selection_worksheet_path": input.WorksheetPath},
		},
		"candidate": candidate, "candidate_direct_evidence": direct, "taxonomy_skills": taxonomy, "unresolved_decisions": unresolved, "applicable_style_rules": style, "source_paths": paths,
	}
	budget := contextJSONBudget{ctx: ctx, maximum: jobevaluation.MaxResultBytes}
	if err := budget.check(output, 0); err != nil {
		return nil, err
	}
	raw, marshalErr := json.Marshal(output)
	if marshalErr != nil {
		return nil, failure("MODEL_INPUT")
	}
	if err := contextStopped(ctx); err != nil {
		return nil, err
	}
	return raw, nil
}
