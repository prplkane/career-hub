package intakeread

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReviewValidationSourceCases(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "migration_intake_review_validation_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Extraction json.RawMessage            `json:"extraction"`
		Review     map[string]json.RawMessage `json:"review"`
		Cases      []struct {
			Name      string                     `json:"name"`
			ReviewSet map[string]json.RawMessage `json:"review_set"`
			Expected  struct {
				Backfilled     int       `json:"backfilled"`
				Categories     []*string `json:"categories"`
				Status         string    `json:"status"`
				Codes          []string  `json:"codes"`
				ExpectedBlocks int       `json:"expected_blocks"`
				ReviewedBlocks int       `json:"reviewed_blocks"`
				Unassigned     int       `json:"unassigned"`
				Ignored        int       `json:"ignored"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 6 {
		t.Fatal("incomplete validation corpus")
	}
	extraction, failure := ParseExtraction(fixture.Extraction)
	if failure != nil {
		t.Fatalf("extraction: %s", failure.Code)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			payload := maps.Clone(fixture.Review)
			maps.Copy(payload, tc.ReviewSet)
			reviewRaw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			review, failure := ParseReview(reviewRaw)
			if failure != nil {
				t.Fatalf("review: %s", failure.Code)
			}
			before := append([]ReviewBlock(nil), review.BlockReviews...)
			backfilled, count := BackfillReviewCategories(extraction, review)
			if count != tc.Expected.Backfilled {
				t.Fatalf("backfill count %d != %d", count, tc.Expected.Backfilled)
			}
			if !reflect.DeepEqual(review.BlockReviews, before) {
				t.Fatal("backfill mutated input")
			}
			categories := make([]*string, 0, len(backfilled.BlockReviews))
			for _, item := range backfilled.BlockReviews {
				categories = append(categories, item.Category)
			}
			if !reflect.DeepEqual(categories, tc.Expected.Categories) {
				t.Fatalf("categories: %v != %v", categories, tc.Expected.Categories)
			}
			result := ValidateReview(extraction, backfilled)
			codes := []string{}
			for _, issue := range result.Issues {
				codes = append(codes, issue.Code)
			}
			if result.Status != tc.Expected.Status || !reflect.DeepEqual(codes, tc.Expected.Codes) || result.ExpectedBlockCount != tc.Expected.ExpectedBlocks || result.ReviewedBlockCount != tc.Expected.ReviewedBlocks || result.UnassignedBlockCount != tc.Expected.Unassigned || result.IgnoredBlockCount != tc.Expected.Ignored {
				t.Fatalf("validation: %+v; codes %v", result, codes)
			}
			if result.ErrorCount+result.WarningCount != len(result.Issues) {
				t.Fatalf("issue counts: %+v", result)
			}
		})
	}
}
