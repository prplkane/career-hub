package intakeview

import (
	"encoding/json"
	"testing"
	"time"
)

func viewExtractionFixture() []byte {
	return []byte(`{
  "source":{"id":"src_fictional_1234567890","path":"folder\\fictional.txt","file_type":"txt","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "blocks":[
    {"id":"b1","order":"0","kind":"paragraph","text":"Example Systems","is_bullet":"yes"},
    {"id":"blank","order":1,"kind":"blank","text":""}
  ],
  "section_hints":[{"section_type":"experience","source_text":"Experience","source_ref":{"source_id":"src_fictional_1234567890","block_id":"b1"}}],
  "technology_ids":["fictional_tool"],
  "technology_mentions":[{"technology_id":"fictional_tool","canonical_name":"Fictional Tool","source_text":"Tool","source_ref":{"source_id":"src_fictional_1234567890","block_id":"b1","start":"0","end":"4"}}],
  "date_candidates":[{"source_text":"2024","start_text":"2024","source_ref":{"source_id":"src_fictional_1234567890","block_id":"b1"}}],
  "bullet_candidates":[{"id":"bullet_1","text":"Example Systems","source_ref":{"source_id":"src_fictional_1234567890","block_id":"b1"}}],
  "unresolved_fields":[]
}`)
}

func TestExtractionProjectsSourceDefaultsAndCounts(t *testing.T) {
	stamp := time.Unix(1700000000, 123456700)
	result, failure := Extraction(viewExtractionFixture(), stamp, true)
	if failure != nil || result == nil {
		t.Fatal(result, failure)
	}
	if result.ID != "src_fictional_1234567890" || result.SourceName != "fictional.txt" || result.ModifiedAt != "2023-11-14T22:13:20.123457+00:00" || !result.ReviewExists {
		t.Fatal("metadata differs", result)
	}
	if result.Counts != (ExtractionCounts{2, 1, 1, 1, 1, 1}) {
		t.Fatal("counts differ", result.Counts)
	}
	first := result.Blocks[0].(map[string]any)
	second := result.Blocks[1].(map[string]any)
	if first["order"].(json.Number) != "0" || first["style"] != nil || first["is_bullet"] != true || second["is_bullet"] != false {
		t.Fatal("block defaults differ", first, second)
	}
	section := result.SectionHints[0].(map[string]any)
	mention := result.TechnologyMentions[0].(map[string]any)
	date := result.DateCandidates[0].(map[string]any)
	bullet := result.BulletCandidates[0].(map[string]any)
	if section["extractor"] != "spacy_entity_ruler" || mention["extractor"] != "spacy_phrase_matcher" || date["end_text"] != nil || date["current"] != false || bullet["extractor"] != "document_bullet" {
		t.Fatal("candidate defaults differ", section, mention, date, bullet)
	}
	ref := mention["source_ref"].(map[string]any)
	if ref["start"].(json.Number) != "0" || ref["end"].(json.Number) != "4" {
		t.Fatal("source offsets differ", ref)
	}
}

func TestReviewProjectsBackfillValidationCompletionAndNullApproval(t *testing.T) {
	reviewRaw := []byte(`{
  "extraction_schema_version":2,
  "source":{"id":"src_fictional_1234567890","path":"folder\\fictional.txt","file_type":"txt","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "entities":[{"id":"experience_one","entity_type":"experience","organization":"Example Systems","title":"Engineer"}],
  "block_reviews":[{"source_ref":{"source_id":"src_fictional_1234567890","block_id":"b1"},"disposition":"assigned","owner_id":"experience_one"}],
  "approval_decisions":[{"subject_kind":"entity","subject_id":"experience_one","action":"create","field_resolutions":{"organization":"source","role":"source"}}]
}`)
	result, failure := Review(viewExtractionFixture(), reviewRaw, nil, false, time.Unix(1700000001, 0))
	if failure != nil || result == nil {
		t.Fatal(result, failure)
	}
	if result.SourceName != "fictional.txt" || result.ApprovalApplication != nil || len(result.Blocks) != 1 {
		t.Fatal("review metadata differs", result)
	}
	entities := result.Review["entities"].([]any)
	entity := entities[0].(map[string]any)
	if entity["review_state"] != "active" || entity["emails"] == nil || entity["user_note"] != nil {
		t.Fatal("entity defaults differ", entity)
	}
	block := result.Review["block_reviews"].([]any)[0].(map[string]any)
	if block["category"] != "experience" || block["user_note"] != nil {
		t.Fatal("category backfill differs", block)
	}
	if result.Validation.Status != "success" || len(result.Validation.Issues) != 0 || result.Completion.Status != "ready" || result.Completion.ReadyToApprove || result.Completion.ApprovalIssueCount != 1 {
		t.Fatal("workflow projection differs", result.Validation, result.Completion)
	}
	issue := result.Completion.ApprovalIssues[0]
	if issue.Code != "MISSING_APPROVAL_FIELD" || issue.Message != "Experience card requires source dates before creating a Career Vault record." || issue.EntityID == nil || *issue.EntityID != "experience_one" || issue.BlockID != nil {
		t.Fatal("approval issue differs", issue)
	}
	encoded, err := json.Marshal(result)
	if err != nil || !json.Valid(encoded) || string(encoded) == "" {
		t.Fatal("review response is not JSON", err)
	}
}

func TestReviewProjectsApprovalDefaults(t *testing.T) {
	reviewRaw := []byte(`{
  "extraction_schema_version":2,
  "source":{"id":"src_fictional_1234567890","path":"folder\\fictional.txt","file_type":"txt","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "entities":[],"block_reviews":[{"source_ref":{"source_id":"src_fictional_1234567890","block_id":"b1"}}]
}`)
	approval := []byte(`{
  "source_id":"src_fictional_1234567890","applied_at":"2026-09-20T12:00:00Z",
  "review_revision":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "previous_vault_revision":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "vault_revision":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
  "changes":[{"subject_kind":"entity","subject_id":"e1","action":"create"}]
}`)
	result, failure := Review(viewExtractionFixture(), reviewRaw, approval, true, time.Unix(1700000000, 0))
	if failure != nil || result == nil || result.ApprovalApplication == nil {
		t.Fatal(result, failure)
	}
	application := result.ApprovalApplication
	if application["status"] != "applied" || application["previous_registry_revision"] != nil || len(application["changed_files"].([]any)) != 0 {
		t.Fatal("approval defaults differ", application)
	}
	change := application["changes"].([]any)[0].(map[string]any)
	if change["entity_type"] != nil || change["field_resolutions"] != nil {
		t.Fatal("approval change defaults differ", change)
	}
}
