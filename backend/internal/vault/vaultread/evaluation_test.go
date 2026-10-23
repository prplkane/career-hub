package vaultread

import (
	"context"
	"reflect"
	"testing"
	"time"

	"careerhub.local/backend/internal/jobs/jobevidence"
)

func TestPrepareEvaluationValidatesWholeVaultBeforeEvidence(t *testing.T) {
	today := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		edit    func(*fixtureReader)
		invalid bool
	}{
		{"valid", func(*fixtureReader) {}, false},
		{"invalid_archive", replaceYAML(experienceIndex, "category: technical", "category: technical, archived: 1"), true},
		{"invalid_reference", replaceYAML(projectIndex, "related_experience: [exp]", "related_experience: [private_missing]"), true},
		{"invalid_calendar", replaceYAML(experienceIndex, "category: technical", "category: technical, date_range: {start: '2026-02-30'}"), true},
		{"unrelated_profile_validation", replaceYAML(profileIndex, "major: Computer Science", ""), true},
		{"contact_null_allowed", appendYAML(profileIndex, "contact: null\n"), false},
		{"archive_kept_for_references_not_evidence", replaceYAML(experienceIndex, "category: technical", "category: technical, archived: true"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := validFixture()
			tc.edit(f)
			got, err := PrepareEvaluation(context.Background(), f, today)
			if tc.invalid {
				if err == nil || got != nil {
					t.Fatal("invalid full Vault returned evidence")
				}
				return
			}
			if err != nil || len(f.reads) != vaultFileCount {
				t.Fatalf("bounded one-load preparation failed: %v", err)
			}
			matches, failure := jobevidence.Find(context.Background(), "Engineer", got.Index)
			if failure != nil {
				t.Fatal(failure)
			}
			if tc.name == "archive_kept_for_references_not_evidence" {
				if len(matches.Direct) != 0 {
					t.Fatal("archived evidence used")
				}
			} else if !reflect.DeepEqual(matches.Direct, []string{"exp"}) {
				t.Fatal("source ID lost")
			}
			if got.Eligibility.Kind != "mapping" {
				t.Fatal("eligibility lost")
			}
		})
	}
	for _, f := range []*fixtureReader{nil, validFixture()} {
		if f == nil {
			if got, err := PrepareEvaluation(context.Background(), nil, today); err == nil || got != nil {
				t.Fatal("nil reader accepted")
			}
			continue
		}
		if got, err := PrepareEvaluation(nil, f, today); err == nil || got != nil || len(f.reads) != 0 {
			t.Fatal("canceled input performed reads")
		}
	}
}
