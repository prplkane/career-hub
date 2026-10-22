package intakeread

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReviewWorkflowSourceCases(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "..", "tests", "fixtures")
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(fixtureDir, name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var base struct {
		Extraction json.RawMessage            `json:"extraction"`
		Review     map[string]json.RawMessage `json:"review"`
	}
	if err := json.Unmarshal(read("migration_intake_review_validation_cases.json"), &base); err != nil {
		t.Fatal(err)
	}
	var approval struct {
		Base map[string]json.RawMessage `json:"base"`
	}
	if err := json.Unmarshal(read("migration_intake_approval_cases.json"), &approval); err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Cases []struct {
			Name               string                     `json:"name"`
			ReviewPresent      *bool                      `json:"review_present"`
			ReviewSet          map[string]json.RawMessage `json:"review_set"`
			ReviewRaw          *string                    `json:"review_raw"`
			ExtractionRaw      *string                    `json:"extraction_raw"`
			Approval           map[string]json.RawMessage `json:"approval"`
			ApprovalRaw        *string                    `json:"approval_raw"`
			WantStatus         string                     `json:"want_status"`
			WantEntities       int                        `json:"want_entities"`
			WantUnresolved     *int                       `json:"want_unresolved"`
			WantApprovalIssues *int                       `json:"want_approval_issues"`
			WantCompletion     struct {
				Status               string   `json:"status"`
				Suggested            *int     `json:"suggested"`
				Categorized          *int     `json:"categorized"`
				Unclassified         *int     `json:"unclassified"`
				QueuedIDs            []string `json:"queued_ids"`
				ValidationErrorCodes []string `json:"validation_error_codes"`
				ApprovalCodes        []string `json:"approval_codes"`
			} `json:"want_completion"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(read("migration_intake_review_workflow_cases.json"), &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases.Cases) < 12 {
		t.Fatal("workflow corpus incomplete")
	}
	for _, tc := range cases.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			input := WorkflowInput{ExtractionRaw: base.Extraction, ReviewPresent: tc.ReviewPresent == nil || *tc.ReviewPresent}
			if tc.ExtractionRaw != nil {
				input.ExtractionRaw = []byte(*tc.ExtractionRaw)
			}
			if input.ReviewPresent {
				if tc.ReviewRaw != nil {
					input.ReviewRaw = []byte(*tc.ReviewRaw)
				} else {
					payload := maps.Clone(base.Review)
					maps.Copy(payload, tc.ReviewSet)
					var err error
					input.ReviewRaw, err = json.Marshal(payload)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if tc.Approval != nil || tc.ApprovalRaw != nil {
				input.ApprovalPresent = true
				if tc.ApprovalRaw != nil {
					input.ApprovalRaw = []byte(*tc.ApprovalRaw)
				} else {
					payload := maps.Clone(approval.Base)
					maps.Copy(payload, tc.Approval)
					var err error
					input.ApprovalRaw, err = json.Marshal(payload)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			got := ReviewWorkflow(input)
			if got.Status != tc.WantStatus || got.ReviewEntityCount != tc.WantEntities || !reflect.DeepEqual(got.UnresolvedBlockCount, tc.WantUnresolved) || !reflect.DeepEqual(got.ApprovalIssueCount, tc.WantApprovalIssues) {
				t.Fatalf("workflow: %+v", got)
			}
			want := tc.WantCompletion
			if want.Status != "" {
				if got.Completion == nil || got.Completion.Status != want.Status {
					t.Fatalf("completion: %+v", got.Completion)
				}
				if want.Suggested != nil && got.Completion.SuggestedBlockCount != *want.Suggested {
					t.Fatalf("suggested: %+v", got.Completion)
				}
				if want.Categorized != nil && got.Completion.CategorizedBlockCount != *want.Categorized {
					t.Fatalf("categorized: %+v", got.Completion)
				}
				if want.Unclassified != nil && got.Completion.UnclassifiedBlockCount != *want.Unclassified {
					t.Fatalf("unclassified: %+v", got.Completion)
				}
				if want.QueuedIDs != nil && !reflect.DeepEqual(got.Completion.QueuedEntityIDs, want.QueuedIDs) {
					t.Fatalf("queued: %+v", got.Completion)
				}
				if want.ValidationErrorCodes != nil && !reflect.DeepEqual(got.Completion.ValidationErrorCodes, want.ValidationErrorCodes) {
					t.Fatalf("validation: %+v", got.Completion)
				}
				if want.ApprovalCodes != nil {
					codes := []string{}
					for _, issue := range got.Completion.ApprovalIssues {
						codes = append(codes, issue.Code)
					}
					if !reflect.DeepEqual(codes, want.ApprovalCodes) {
						t.Fatalf("approval: %v", codes)
					}
				}
			}
		})
	}
}
