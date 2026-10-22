package intakeread

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReviewCasesMatchSourceOracle(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "migration_intake_review_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Base  map[string]json.RawMessage `json:"base"`
		Cases []struct {
			Name   string                     `json:"name"`
			Valid  bool                       `json:"valid"`
			Set    map[string]json.RawMessage `json:"set"`
			Delete []string                   `json:"delete"`
			Raw    *string                    `json:"raw"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 25 {
		t.Fatal("review corpus incomplete")
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			var input []byte
			if tc.Raw != nil {
				input = []byte(*tc.Raw)
			} else {
				payload := maps.Clone(fixture.Base)
				for _, key := range tc.Delete {
					delete(payload, key)
				}
				maps.Copy(payload, tc.Set)
				var err error
				input, err = json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
			}
			got, failure := ParseReview(input)
			if !tc.Valid {
				if failure == nil || failure.Code == "" || !reflect.DeepEqual(got, Review{}) {
					t.Fatalf("invalid accepted or partial result: %+v / %+v", got, failure)
				}
				return
			}
			if failure != nil {
				t.Fatalf("source-valid case rejected: %s", failure.Code)
			}
			if got.Source.ID != "src_fictional_1234567890" || got.ExtractionSchemaVersion != "2" || got.Status != "review" {
				t.Fatalf("base projection: %+v", got)
			}
			if tc.Name == "full projection" {
				if got.SchemaVersion != "3" || len(got.Entities) != 1 || got.Entities[0].ReviewState != "queued" || got.Entities[0].LegacyRequiredEmpty["organization"] || !got.Entities[0].LegacyRequiredEmpty["title"] {
					t.Fatalf("entity: %+v", got.Entities)
				}
				if len(got.BlockReviews) != 1 || got.BlockReviews[0].BlockID != "b1" || got.BlockReviews[0].Disposition != "assigned" || *got.BlockReviews[0].Category != "experience" || *got.BlockReviews[0].OwnerID != "e1" || got.BlockReviews[0].SuggestedOwnerIDs[0] != "e2" {
					t.Fatalf("block: %+v", got.BlockReviews)
				}
				if len(got.ApprovalDecisions) != 1 || got.ApprovalDecisions[0].Action != "merge" || *got.ApprovalDecisions[0].TargetID != "v1" || got.ApprovalDecisions[0].FieldResolutions["role"] != "source" {
					t.Fatalf("decision: %+v", got.ApprovalDecisions)
				}
			}
			if tc.Name == "empty binary legacy title" {
				if len(got.Entities) != 1 || !got.Entities[0].LegacyRequiredEmpty["title"] {
					t.Fatalf("binary text must normalize before emptiness check: %+v", got.Entities)
				}
			}
		})
	}
}

func TestReviewLimitAndNoPartialResult(t *testing.T) {
	got, failure := ParseReview([]byte(strings.Repeat(" ", MaxReviewBytes+1)))
	if failure == nil || failure.Code != "REVIEW_INPUT_LIMIT" || !reflect.DeepEqual(got, Review{}) {
		t.Fatalf("limit: %+v / %+v", got, failure)
	}
}
