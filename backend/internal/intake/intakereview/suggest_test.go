package intakereview

import (
	"encoding/json"
	"fmt"
	"testing"
)

const suggestionSourceID = "src_suggest_1234567890"

type suggestionBlock struct {
	text     string
	kind     string
	isBullet bool
}

type suggestionHint struct {
	section string
	block   int
}
type suggestionMention struct {
	technology string
	block      int
}
type suggestionDate struct {
	text, start, end string
	block            int
	current          bool
}

func suggestionExtraction(t *testing.T, blocks []suggestionBlock, hints []suggestionHint, mentions []suggestionMention, dates []suggestionDate) []byte {
	t.Helper()
	blockValues := make([]any, len(blocks))
	for index, block := range blocks {
		kind := block.kind
		if kind == "" {
			kind = "paragraph"
		}
		blockValues[index] = map[string]any{
			"id": fmt.Sprintf("b%d", index), "order": index, "kind": kind,
			"text": block.text, "style": nil, "is_bullet": block.isBullet,
		}
	}
	hintValues := make([]any, len(hints))
	for index, hint := range hints {
		hintValues[index] = map[string]any{
			"section_type": hint.section, "source_text": blocks[hint.block].text,
			"source_ref": map[string]any{"source_id": suggestionSourceID, "block_id": fmt.Sprintf("b%d", hint.block)},
		}
	}
	mentionValues := make([]any, len(mentions))
	technologyIDs, seenTechnology := []any{}, map[string]bool{}
	for index, mention := range mentions {
		mentionValues[index] = map[string]any{
			"technology_id": mention.technology, "canonical_name": mention.technology, "source_text": mention.technology,
			"source_ref": map[string]any{"source_id": suggestionSourceID, "block_id": fmt.Sprintf("b%d", mention.block)},
		}
		if !seenTechnology[mention.technology] {
			seenTechnology[mention.technology] = true
			technologyIDs = append(technologyIDs, mention.technology)
		}
	}
	dateValues := make([]any, len(dates))
	for index, date := range dates {
		var end any
		if date.end != "" {
			end = date.end
		}
		dateValues[index] = map[string]any{
			"source_text": date.text, "start_text": date.start, "end_text": end, "current": date.current,
			"source_ref": map[string]any{"source_id": suggestionSourceID, "block_id": fmt.Sprintf("b%d", date.block)},
		}
	}
	document := map[string]any{
		"schema_version": 2, "status": "extracted", "extractor_version": "vault-intake-0.3", "registry_revision": "revision",
		"source": map[string]any{"id": suggestionSourceID, "path": "career_vault/raw/fictional.txt", "file_type": "txt", "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		"blocks": blockValues, "section_hints": hintValues, "technology_ids": technologyIDs, "technology_mentions": mentionValues,
		"technology_resolution_issues": []any{}, "date_candidates": dateValues, "bullet_candidates": []any{}, "unresolved_fields": []any{},
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func buildAndSuggest(t *testing.T, extraction []byte) *SuggestionResult {
	t.Helper()
	built, buildFailure := Build(extraction, nil)
	if buildFailure != nil {
		t.Fatal("build failed", buildFailure.Code)
	}
	result, failure := Suggest(extraction, built.Bytes)
	if failure != nil {
		t.Fatal("suggest failed", failure.Code)
	}
	return result
}

func suggestionDocument(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func suggestionEntity(t *testing.T, document map[string]any, id string) map[string]any {
	t.Helper()
	for _, value := range document["entities"].([]any) {
		entity := value.(map[string]any)
		if entity["id"] == id {
			return entity
		}
	}
	t.Fatalf("missing entity %s", id)
	return nil
}

func suggestionBlockReview(t *testing.T, document map[string]any, id string) map[string]any {
	t.Helper()
	for _, value := range document["block_reviews"].([]any) {
		block := value.(map[string]any)
		if block["source_ref"].(map[string]any)["block_id"] == id {
			return block
		}
	}
	t.Fatalf("missing block %s", id)
	return nil
}

func TestSuggestProfileMatchesSourceBackedContactRules(t *testing.T) {
	extraction := suggestionExtraction(t, []suggestionBlock{
		{text: "Example Candidate, M.S.    candidate@example.com"},
		{text: "Software Development Specialist"},
		{text: "(555) 010-2026"},
		{text: "www.linkedin.com/in/example-candidate"},
		{text: "Example City, USA | Open to Relocation"},
		{text: "EXPERIENCE"},
	}, []suggestionHint{{section: "experience", block: 5}}, nil, nil)

	result := buildAndSuggest(t, extraction)
	if result.Stats.ProfileCardCount != 1 || result.Stats.NewCardCount != 1 || result.Stats.AutoAssignedLinkCount != 5 || result.Stats.StructuralBlockCount != 1 {
		t.Fatal("profile stats changed", result.Stats)
	}
	document := suggestionDocument(t, result.Bytes)
	profile := suggestionEntity(t, document, "profile_contact")
	if profile["name"] != "Example Candidate" || profile["headline"] != "Software Development Specialist" || profile["location"] != "Example City, USA | Open to Relocation" {
		t.Fatal("profile text fields changed", profile)
	}
	if fmt.Sprint(profile["emails"]) != "[candidate@example.com]" || fmt.Sprint(profile["phones"]) != "[(555) 010-2026]" || fmt.Sprint(profile["links"]) != "[www.linkedin.com/in/example-candidate]" {
		t.Fatal("profile contacts changed", profile)
	}
	for index := 0; index < 5; index++ {
		block := suggestionBlockReview(t, document, fmt.Sprintf("b%d", index))
		if block["disposition"] != "assigned" || block["owner_id"] != "profile_contact" {
			t.Fatal("profile evidence was not assigned", index, block)
		}
	}
	if suggestionBlockReview(t, document, "b5")["disposition"] != "structural" {
		t.Fatal("section header was not structural")
	}
}

func TestSuggestExperienceParsesSlashHeaderAndPreservesEdits(t *testing.T) {
	extraction := suggestionExtraction(t, []suggestionBlock{
		{text: "EXPERIENCE"},
		{text: "Example SaaS Co / Engineering Lead / Software Engineer\tFeb 2026 - Present"},
		{text: "Built a source-backed platform.", isBullet: true},
	}, []suggestionHint{{section: "experience", block: 0}}, []suggestionMention{{technology: "go", block: 2}}, []suggestionDate{{text: "Feb 2026 - Present", start: "Feb 2026", end: "Present", block: 1, current: true}})

	first := buildAndSuggest(t, extraction)
	if first.Stats.ExperienceCardCount != 1 || first.Stats.AutoAssignedLinkCount != 2 {
		t.Fatal("experience stats changed", first.Stats)
	}
	document := suggestionDocument(t, first.Bytes)
	entity := suggestionEntity(t, document, "experience_b1")
	if entity["organization"] != "Example SaaS Co" || entity["title"] != "Engineering Lead / Software Engineer" || entity["date_text"] != "Feb 2026 - Present" || fmt.Sprint(entity["technologies"]) != "[go]" {
		t.Fatal("experience fields changed", entity)
	}
	entity["organization"] = "Edited Company"
	edited, _ := json.Marshal(document)
	second, failure := Suggest(extraction, edited)
	if failure != nil || second.Stats.NewCardCount != 0 || suggestionEntity(t, suggestionDocument(t, second.Bytes), "experience_b1")["organization"] != "Edited Company" {
		t.Fatal("rerun overwrote a user edit", second, failure)
	}
}

func TestSuggestSectionCategoriesCreateBoundedCards(t *testing.T) {
	extraction := suggestionExtraction(t, []suggestionBlock{
		{text: "SUMMARY"},
		{text: "Backend engineer building reliable systems."},
		{text: "SKILLS"},
		{text: "Programming: Python, SQL"},
		{text: "EDUCATION"},
		{text: "Example State University | Example City, GA"},
		{text: "Bachelor of Science in Information Systems | Expected May 2027"},
		{text: "PROJECTS"},
		{text: "Example Platform"},
		{text: "Built a Go service.", isBullet: true},
		{text: "CERTIFICATIONS"},
		{text: "Professional Scrum Master I | 2026"},
	}, []suggestionHint{
		{section: "summary", block: 0}, {section: "skills", block: 2}, {section: "education", block: 4},
		{section: "projects", block: 7}, {section: "certifications", block: 10},
	}, []suggestionMention{{technology: "python", block: 3}, {technology: "go", block: 9}}, nil)

	result := buildAndSuggest(t, extraction)
	if result.Stats.CardCount != 5 || result.Stats.NewCardCount != 5 || result.Stats.StructuralBlockCount != 5 || result.Stats.AutoAssignedLinkCount != 7 {
		t.Fatal("section suggestion stats changed", result.Stats)
	}
	document := suggestionDocument(t, result.Bytes)
	if suggestionEntity(t, document, "summary_b1")["content"] != "Backend engineer building reliable systems." {
		t.Fatal("summary content changed")
	}
	skill := suggestionEntity(t, document, "skill_b3")
	if skill["name"] != "Programming" || fmt.Sprint(skill["items"]) != "[Python SQL]" || fmt.Sprint(skill["technologies"]) != "[python]" {
		t.Fatal("skill fields changed", skill)
	}
	education := suggestionEntity(t, document, "education_b5")
	if education["institution"] != "Example State University" || education["degree"] != "Bachelor of Science" || education["major"] != "Information Systems" || education["date_text"] != "Expected May 2027" {
		t.Fatal("education fields changed", education)
	}
	project := suggestionEntity(t, document, "project_b8")
	if project["title"] != "Example Platform" || fmt.Sprint(project["items"]) != "[Built a Go service.]" || fmt.Sprint(project["technologies"]) != "[go]" {
		t.Fatal("project fields changed", project)
	}
	certification := suggestionEntity(t, document, "certification_b11")
	if certification["name"] != "Professional Scrum Master I" || certification["date_text"] != "2026" {
		t.Fatal("certification fields changed", certification)
	}
}

func TestSuggestLeavesNonResumeWithoutContactSignalUntouched(t *testing.T) {
	extraction := suggestionExtraction(t, []suggestionBlock{
		{text: "Source: C:/Downloads/resume.docx"},
		{text: "Confirmed clarifications"},
		{text: "Example SaaS Co official position: CTO.", isBullet: true},
	}, nil, nil, nil)
	result := buildAndSuggest(t, extraction)
	if result.Stats.CardCount != 0 || result.Stats.NewCardCount != 0 || len(result.Review.Entities) != 0 {
		t.Fatal("non-resume content created cards", result.Stats)
	}
}
