package intakereview

import (
	"encoding/json"
	"fmt"
	"testing"

	"careerhub.local/backend/internal/intake/intakeread"
)

func extractionFixture() []byte {
	return []byte(`{
  "schema_version":2,"status":"extracted","extractor_version":"vault-intake-0.3","registry_revision":"revision",
  "source":{"id":"src_fictional_1234567890","path":"career_vault/raw/fictional.txt","file_type":"txt","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "blocks":[
    {"id":"b1","order":0,"kind":"paragraph","text":"Example Systems","style":null,"is_bullet":false},
    {"id":"blank","order":1,"kind":"blank","text":"","style":null,"is_bullet":false},
    {"id":"b2","order":2,"kind":"paragraph","text":"Built a local workflow","style":null,"is_bullet":true}
  ],
  "section_hints":[],"technology_ids":[],"technology_mentions":[],"technology_resolution_issues":[],
  "date_candidates":[],"bullet_candidates":[],"unresolved_fields":[]
}`)
}

func TestBuildCreatesOnlyNonemptyDecisionsInExtractionOrder(t *testing.T) {
	result, failure := Build(extractionFixture(), nil)
	if failure != nil || result == nil || result.NewDecisionCount != 2 || result.PreservedDecisionCount != 0 {
		t.Fatal(result, failure)
	}
	if result.Validation.Status != "success_with_warnings" || result.Validation.UnassignedBlockCount != 2 {
		t.Fatal("new review validation changed", result.Validation)
	}
	review, parseFailure := intakeread.ParseReview(result.Bytes)
	if parseFailure != nil || len(review.BlockReviews) != 2 || review.BlockReviews[0].BlockID != "b1" || review.BlockReviews[1].BlockID != "b2" {
		t.Fatal("new review is not source ordered", review, parseFailure)
	}
	var document map[string]any
	if json.Unmarshal(result.Bytes, &document) != nil || document["status"] != "review" {
		t.Fatal("review is not a JSON-compatible YAML document")
	}
}

func TestBuildPreservesRichExistingReviewAndAddsMissingBlock(t *testing.T) {
	existing := []byte(`{
  "extraction_schema_version":2,
  "source":{"id":"src_fictional_1234567890","path":"career_vault/raw/fictional.txt","file_type":"txt","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "entities":[{"id":"experience_one","entity_type":"experience","organization":"Example Systems","title":"Engineer","technologies":["core"],"pending_skills":[{"name":"Fictional tool"}],"user_note":"keep this"}],
  "block_reviews":[{"source_ref":{"source_id":"src_fictional_1234567890","block_id":"b1"},"category":"experience","disposition":"assigned","owner_id":"experience_one","suggested_owner_ids":[],"user_note":"verified"}],
  "approval_decisions":[{"subject_kind":"entity","subject_id":"experience_one","action":"source_only"}]
}`)
	result, failure := Build(extractionFixture(), existing)
	if failure != nil || result == nil || result.NewDecisionCount != 1 || result.PreservedDecisionCount != 1 {
		t.Fatal(result, failure)
	}
	var document map[string]any
	if json.Unmarshal(result.Bytes, &document) != nil {
		t.Fatal("result is not JSON")
	}
	entities := document["entities"].([]any)
	entity := entities[0].(map[string]any)
	if entity["organization"] != "Example Systems" || entity["user_note"] != "keep this" || entity["review_state"] != "active" {
		t.Fatal("existing card was not preserved and normalized", entity)
	}
	blocks := document["block_reviews"].([]any)
	first := blocks[0].(map[string]any)
	if first["user_note"] != "verified" || first["owner_id"] != "experience_one" {
		t.Fatal("existing block decision was not preserved", first)
	}
	if len(document["approval_decisions"].([]any)) != 1 {
		t.Fatal("approval decision was not preserved")
	}
	if revision, revisionFailure := Revision(result.Bytes); revisionFailure != nil || revision != result.Revision {
		t.Fatal("build result did not retain its normalized revision", revision, revisionFailure)
	}
}

func TestRevisionMatchesPythonNormalizedModel(t *testing.T) {
	raw := []byte(`{
  "extraction_schema_version":2,
  "source":{"id":"src_fictional_1234567890","path":"fictional.docx","file_type":"docx","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "schema_version":3,
  "entities":[{"id":"e1","entity_type":"experience","organization":"Example Co","title":"","review_state":"queued","pending_skills":[{"name":"Example skill","primary_group_id":"example_group"}]}],
  "block_reviews":[{"source_ref":{"source_id":"src_fictional_1234567890","block_id":"b1"},"category":"experience","disposition":"assigned","owner_id":"e1","suggested_owner_ids":["e2"]}],
  "approval_decisions":[{"subject_kind":"entity","subject_id":"e1","action":"merge","target_id":"v1","field_resolutions":{"role":"source"}}]
}`)
	got, failure := Revision(raw)
	if failure != nil || got != "sha256:d3892eba216dbfd746d3cb6a5f73ec3477188626df7732d4eb7e3e32e1ade5dd" {
		t.Fatal("revision differs from the Python model projection", got, failure)
	}
}

func TestBuildRefusesUnsafeRefreshes(t *testing.T) {
	base := `{"extraction_schema_version":2,"source":{"id":"src_fictional_1234567890","path":"career_vault/raw/fictional.txt","file_type":"txt","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"entities":[],"block_reviews":%s,"approval_decisions":[]}`
	for _, test := range []struct {
		name, blocks, code string
	}{
		{"duplicate", `[{"source_ref":{"source_id":"src_fictional_1234567890","block_id":"b1"}},{"source_ref":{"source_id":"src_fictional_1234567890","block_id":"b1"}}]`, "REVIEW_BUILD_DUPLICATE_BLOCK"},
		{"stale", `[{"source_ref":{"source_id":"src_fictional_1234567890","block_id":"missing"}}]`, "REVIEW_BUILD_STALE_BLOCK"},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := []byte(fmt.Sprintf(base, test.blocks))
			if result, failure := Build(extractionFixture(), raw); result != nil || failure == nil || failure.Code != test.code {
				t.Fatal(result, failure)
			}
		})
	}
	mismatch := []byte(`{"extraction_schema_version":2,"source":{"id":"other","path":"career_vault/raw/fictional.txt","file_type":"txt","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"block_reviews":[]}`)
	if result, failure := Build(extractionFixture(), mismatch); result != nil || failure == nil || failure.Code != "REVIEW_BUILD_SOURCE_MISMATCH" {
		t.Fatal(result, failure)
	}
}
