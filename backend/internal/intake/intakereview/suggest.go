package intakereview

import (
	"math/big"
	"sort"
	"strings"

	"careerhub.local/backend/internal/intake/intakeread"
)

const (
	legacyExperienceRule   = "experience_section_date_anchor_v1"
	experienceRule         = "experience_section_date_anchor_v2"
	profileRule            = "profile_contact_blocks_v2"
	educationRule          = "education_section_entry_group_v1"
	summaryRule            = "summary_section_content_v1"
	skillRule              = "skills_section_group_v1"
	projectRule            = "projects_section_entry_group_v2"
	certificationRule      = "certifications_section_entry_v1"
	legacyProjectRule      = "projects_section_entry_group_v1"
	unsectionedProjectRule = "projects_unsectioned_fallback_v1"
)

type SuggestionStats struct {
	CardCount              int `json:"card_count"`
	NewCardCount           int `json:"new_card_count"`
	SuggestedLinkCount     int `json:"suggested_link_count"`
	AutoAssignedLinkCount  int `json:"auto_assigned_link_count"`
	ProfileCardCount       int `json:"profile_card_count"`
	ExperienceCardCount    int `json:"experience_card_count"`
	EducationCardCount     int `json:"education_card_count"`
	ProjectCardCount       int `json:"project_card_count"`
	SummaryCardCount       int `json:"summary_card_count"`
	SkillCardCount         int `json:"skill_card_count"`
	CertificationCardCount int `json:"certification_card_count"`
	StructuralBlockCount   int `json:"structural_block_count"`
}

type SuggestionResult struct {
	Bytes      []byte
	Revision   string
	Review     intakeread.Review
	Validation intakeread.ReviewValidation
	Stats      SuggestionStats
}

type cardStats struct {
	cards, fresh, suggested, assigned int
}

type sectionSpan struct {
	typeName           string
	header, start, end *big.Int
}

type suggestionState struct {
	extraction intakeread.Extraction
	document   map[string]any
	entities   []map[string]any
	entityByID map[string]map[string]any
	blocks     map[string]map[string]any
}

// Suggest applies the deterministic Phase 1 card detectors to one strict
// extraction/review pair. It is pure: revision admission and publication are
// owned by applicationwrite.
func Suggest(extractionRaw, reviewRaw []byte) (*SuggestionResult, *Error) {
	extraction, extractionFailure := intakeread.ParseExtraction(extractionRaw)
	if extractionFailure != nil {
		return nil, fail("REVIEW_SUGGEST_EXTRACTION_INVALID")
	}
	review, reviewFailure := intakeread.ParseReview(reviewRaw)
	if reviewFailure != nil || review.Source.ID != extraction.Source.ID || review.Source.SHA256 != extraction.Source.SHA256 {
		return nil, fail("REVIEW_SUGGEST_REVIEW_INVALID")
	}
	document, decodeFailure := decodeReviewDocument(reviewRaw, review)
	if decodeFailure != nil {
		return nil, fail("REVIEW_SUGGEST_REVIEW_INVALID")
	}
	state, stateFailure := newSuggestionState(extraction, document)
	if stateFailure != nil {
		return nil, stateFailure
	}
	state.backfillCategories(review)
	profile := state.suggestProfile()
	experience := state.suggestExperience()
	education := state.suggestEducation()
	summary := state.suggestSummary()
	skills := state.suggestSkills()
	projects := state.suggestProjects()
	certifications := state.suggestCertifications()
	structural := state.classifyStructuralHeaders()

	state.document["entities"] = mapsToAny(state.entities)
	encoded, encodeFailure := encodeDocument(state.document)
	if encodeFailure != nil || len(encoded) > intakeread.MaxReviewBytes {
		return nil, fail("REVIEW_SUGGEST_RESULT_LIMIT")
	}
	resultReview, parseFailure := intakeread.ParseReview(encoded)
	if parseFailure != nil {
		return nil, fail("REVIEW_SUGGEST_RESULT_INVALID")
	}
	validation := intakeread.ValidateReview(extraction, resultReview)
	if validation.Status == "error" {
		return nil, fail("REVIEW_SUGGEST_RESULT_INVALID")
	}
	stats := SuggestionStats{
		CardCount:             profile.cards + experience.cards + education.cards + summary.cards + skills.cards + projects.cards + certifications.cards,
		NewCardCount:          profile.fresh + experience.fresh + education.fresh + summary.fresh + skills.fresh + projects.fresh + certifications.fresh,
		SuggestedLinkCount:    profile.suggested + experience.suggested + education.suggested + summary.suggested + skills.suggested + projects.suggested + certifications.suggested,
		AutoAssignedLinkCount: profile.assigned + experience.assigned + education.assigned + summary.assigned + skills.assigned + projects.assigned + certifications.assigned,
		ProfileCardCount:      profile.cards, ExperienceCardCount: experience.cards, EducationCardCount: education.cards,
		ProjectCardCount: projects.cards, SummaryCardCount: summary.cards, SkillCardCount: skills.cards,
		CertificationCardCount: certifications.cards, StructuralBlockCount: structural,
	}
	return &SuggestionResult{encoded, revision(encoded), resultReview, validation, stats}, nil
}

func newSuggestionState(extraction intakeread.Extraction, document map[string]any) (*suggestionState, *Error) {
	entityValues, entitiesOK := document["entities"].([]any)
	blockValues, blocksOK := document["block_reviews"].([]any)
	if !entitiesOK || !blocksOK {
		return nil, fail("REVIEW_SUGGEST_REVIEW_INVALID")
	}
	state := &suggestionState{extraction: extraction, document: document, entityByID: map[string]map[string]any{}, blocks: map[string]map[string]any{}}
	for _, value := range entityValues {
		entity, ok := value.(map[string]any)
		id, _ := entity["id"].(string)
		if !ok || id == "" {
			return nil, fail("REVIEW_SUGGEST_REVIEW_INVALID")
		}
		state.entities = append(state.entities, entity)
		state.entityByID[id] = entity
	}
	for _, value := range blockValues {
		block, ok := value.(map[string]any)
		ref, refOK := block["source_ref"].(map[string]any)
		id, _ := ref["block_id"].(string)
		if !ok || !refOK || id == "" {
			return nil, fail("REVIEW_SUGGEST_REVIEW_INVALID")
		}
		state.blocks[id] = block
	}
	return state, nil
}

func (s *suggestionState) backfillCategories(review intakeread.Review) {
	backfilled, _ := intakeread.BackfillReviewCategories(s.extraction, review)
	for _, item := range backfilled.BlockReviews {
		if item.Category != nil && s.blocks[item.BlockID]["category"] == nil {
			s.blocks[item.BlockID]["category"] = *item.Category
		}
	}
}

func newEntity(id, entityType, rule string) map[string]any {
	entity := map[string]any{
		"id": id, "entity_type": entityType, "review_state": "active",
		"suggestion_rule": rule, "user_note": nil,
	}
	for _, field := range []string{"name", "headline", "organization", "title", "institution", "degree", "credential", "major", "gpa", "date_text", "location", "content"} {
		entity[field] = ""
	}
	for _, field := range []string{"honors", "emails", "phones", "links", "technologies", "pending_skills", "items"} {
		entity[field] = []any{}
	}
	return entity
}

func (s *suggestionState) addEntity(entity map[string]any, prepend bool) {
	id, _ := entity["id"].(string)
	if prepend {
		s.entities = append([]map[string]any{entity}, s.entities...)
	} else {
		s.entities = append(s.entities, entity)
	}
	s.entityByID[id] = entity
}

func mapsToAny(values []map[string]any) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func order(value string) *big.Int {
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return new(big.Int)
	}
	return result
}

func sortedBlocks(values []intakeread.Block) []intakeread.Block {
	result := append([]intakeread.Block(nil), values...)
	sort.SliceStable(result, func(i, j int) bool { return order(result[i].Order).Cmp(order(result[j].Order)) < 0 })
	return result
}

func (s *suggestionState) spans() []sectionSpan {
	orders := map[string]*big.Int{}
	end := big.NewInt(-1)
	for _, block := range s.extraction.Blocks {
		value := order(block.Order)
		orders[block.ID] = value
		if value.Cmp(end) > 0 {
			end = new(big.Int).Set(value)
		}
	}
	type hint struct {
		order *big.Int
		name  string
	}
	hints := []hint{}
	for _, item := range s.extraction.SectionHints {
		if value, ok := orders[item.BlockID]; ok {
			hints = append(hints, hint{new(big.Int).Set(value), item.SectionType})
		}
	}
	sort.SliceStable(hints, func(i, j int) bool { return hints[i].order.Cmp(hints[j].order) < 0 })
	end.Add(end, big.NewInt(1))
	result := make([]sectionSpan, 0, len(hints))
	for index, item := range hints {
		spanEnd := new(big.Int).Set(end)
		if index+1 < len(hints) {
			spanEnd.Set(hints[index+1].order)
		}
		result = append(result, sectionSpan{item.name, new(big.Int).Set(item.order), new(big.Int).Add(item.order, big.NewInt(1)), spanEnd})
	}
	return result
}

func inSpan(block intakeread.Block, span sectionSpan) bool {
	value := order(block.Order)
	return value.Cmp(span.start) >= 0 && value.Cmp(span.end) < 0
}

func entityCategory(entity map[string]any) string {
	value, _ := entity["entity_type"].(string)
	switch value {
	case "profile", "summary", "experience", "education", "project", "skill", "certification", "other":
		return value
	}
	return ""
}

func blockDisposition(block map[string]any) string {
	value, _ := block["disposition"].(string)
	return value
}

func blockCategory(block map[string]any) string {
	value, _ := block["category"].(string)
	return value
}

func stringSlice(value any) []string {
	raw, _ := value.([]any)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func anyStrings(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func (s *suggestionState) confirmedCategoryGroup(blocks []intakeread.Block, category string) bool {
	found := false
	for _, block := range blocks {
		if strings.TrimSpace(block.Text) == "" {
			continue
		}
		found = true
		decision := s.blocks[block.ID]
		if decision == nil || blockDisposition(decision) != "categorized" || blockCategory(decision) != category {
			return false
		}
	}
	return found
}

func (s *suggestionState) linkProposals(proposals map[string]map[string]bool) (int, int) {
	suggested, assigned := 0, 0
	for blockID, owners := range proposals {
		block := s.blocks[blockID]
		if block == nil {
			continue
		}
		existing := map[string]bool{}
		for _, id := range stringSlice(block["suggested_owner_ids"]) {
			existing[id], owners[id] = true, true
		}
		categories := map[string]bool{}
		for owner := range owners {
			if entity := s.entityByID[owner]; entity != nil {
				if category := entityCategory(entity); category != "" {
					categories[category] = true
				}
			}
		}
		category := blockCategory(block)
		if category == "" && len(categories) == 1 {
			for value := range categories {
				category = value
			}
			block["category"] = category
		}
		if category != "" {
			for owner := range owners {
				if entity := s.entityByID[owner]; entity == nil || entityCategory(entity) != category {
					delete(owners, owner)
				}
			}
		}
		ids := make([]string, 0, len(owners))
		for owner := range owners {
			ids = append(ids, owner)
		}
		sort.Strings(ids)
		if len(ids) == 1 {
			block["disposition"], block["owner_id"], block["suggested_owner_ids"] = "assigned", ids[0], []any{}
			assigned++
			continue
		}
		block["suggested_owner_ids"] = anyStrings(ids)
		for _, id := range ids {
			if !existing[id] {
				suggested++
			}
		}
	}
	return suggested, assigned
}

func addProposal(values map[string]map[string]bool, blockID, ownerID string) {
	if values[blockID] == nil {
		values[blockID] = map[string]bool{}
	}
	values[blockID][ownerID] = true
}
