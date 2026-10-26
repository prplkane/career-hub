package readonly

import (
	"context"
	"testing"
)

func intakeViewExtraction() string {
	return `{
  "schema_version":2,"status":"extracted","extractor_version":"vault-intake-0.3","registry_revision":"revision",
  "source":{"id":"src_fictional_1234567890","path":"career_vault/raw/fictional.txt","file_type":"txt","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "blocks":[{"id":"b1","order":0,"kind":"paragraph","text":"Example Systems","style":null,"is_bullet":false}],
  "section_hints":[],"technology_ids":[],"technology_mentions":[],"technology_resolution_issues":[],
  "date_candidates":[],"bullet_candidates":[],"unresolved_fields":[]
}`
}

func intakeViewReview() string {
	return `{
  "extraction_schema_version":2,
  "source":{"id":"src_fictional_1234567890","path":"career_vault/raw/fictional.txt","file_type":"txt","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "entities":[],"block_reviews":[{"source_ref":{"source_id":"src_fictional_1234567890","block_id":"b1"}}],"approval_decisions":[]
}`
}

func TestIntakeReviewReadRejectsInvalidIDBeforeWorkspace(t *testing.T) {
	if result, err := (&Workspace{}).IntakeReview(context.Background(), "../escape"); result != nil || errorCode(err) != "INTAKE_SOURCE_ID_INVALID" {
		t.Fatal(result, err)
	}
}

func errorCode(err error) string {
	if typed, ok := err.(*Error); ok {
		return typed.Code
	}
	return ""
}
