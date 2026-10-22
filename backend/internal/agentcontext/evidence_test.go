package agentcontext

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/jobevaluation"
)

type evidenceCase struct {
	Job         map[string]any        `json:"job"`
	Review      jobevaluation.Result  `json:"review"`
	Catalog     []CatalogEvidence     `json:"catalog"`
	Experiences []SelectionExperience `json:"experiences"`
	Maximum     int                   `json:"maximum"`
}

func emptyEvidenceCase() evidenceCase {
	return evidenceCase{
		Job:     map[string]any{"id": "fixture", "required_skills": []string{}, "required_platforms": []string{}, "required_capabilities": []string{}, "preferred_skills": []string{}, "experience_requirements": []any{}, "education_requirements": []string{}},
		Review:  jobevaluation.Result{JobID: "fixture", ApplicationAllowed: true, Lane: "review", MatchedRequired: []jobevaluation.MatchedRequirement{}, MatchedPreferred: []jobevaluation.MatchedRequirement{}, TaxonomyHints: []jobevaluation.TaxonomyHint{}, ExperienceReview: []jobevaluation.ExperienceReview{}, RequirementGaps: []jobevaluation.RequirementGap{}, ClassifiedRequirementReview: []jobevaluation.ClassifiedReview{}},
		Catalog: []CatalogEvidence{}, Experiences: []SelectionExperience{}, Maximum: 6,
	}
}

func TestEvidenceFailureAndIsolation(t *testing.T) {
	c := emptyEvidenceCase()
	c.Job["required_skills"] = []string{"hint_only", "matched"}
	c.Review.TaxonomyHints = []jobevaluation.TaxonomyHint{{Category: "skill", Requirement: "hint_only", SkillIDs: []string{"h"}}}
	c.Review.MatchedRequired = []jobevaluation.MatchedRequirement{{Category: "skill", Requirement: "matched", EvidenceIDs: []string{"e"}}}
	raw, _ := json.Marshal(c.Job)
	matrix, err := BuildEvidenceMatrix(context.Background(), raw, c.Review)
	if err != nil {
		t.Fatal(err)
	}
	c.Review.MatchedRequired[0].EvidenceIDs[0] = "mutated"
	if matrix.Requirements[1].DirectEvidenceIDs[0] != "e" || len(matrix.Requirements[0].DirectEvidenceIDs) != 0 || matrix.Requirements[0].Status != "missing_direct" {
		t.Fatal("alias or taxonomy promoted to evidence")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if v, e := BuildEvidenceMatrix(ctx, raw, c.Review); v != nil || e == nil || e.Code != "AGENT_CONTEXT_CANCELED" {
		t.Fatal("cancellation")
	}
	c.Review.JobID = "different"
	if v, e := BuildEvidenceMatrix(context.Background(), raw, c.Review); v != nil || e == nil {
		t.Fatal("identity mismatch")
	}
	c = emptyEvidenceCase()
	c.Job["required_skills"] = make([]string, 2049)
	raw, _ = json.Marshal(c.Job)
	if v, e := BuildEvidenceMatrix(context.Background(), raw, c.Review); v != nil || e == nil {
		t.Fatal("row bound")
	}
	for _, bad := range []string{"{}", "null", "[]", strings.Repeat(" ", jobevaluation.MaxInputBytes+1)} {
		if v, e := BuildEvidenceMatrix(context.Background(), []byte(bad), c.Review); v != nil || e == nil {
			t.Fatal("input bound/shape")
		}
	}
	catalog := []CatalogEvidence{{"e", "experience", nil}}
	experiences := []SelectionExperience{{ID: "e", ProofPoints: []string{"original"}}}
	w, _, err := BuildSelectionWorksheet(context.Background(), *matrix, catalog, experiences, 6)
	if err != nil {
		t.Fatal(err)
	}
	experiences[0].ProofPoints.([]string)[0] = "mutated"
	matrix.Requirements[1].DirectEvidenceIDs[0] = "mutated"
	if w.ExperienceCandidates[0].EvidencePreview.([]any)[0] != "original" || w.Shortlist[0].EvidenceID != "e" {
		t.Fatal("worksheet aliased")
	}
	for _, catalog := range [][]CatalogEvidence{nil, {{"e", "experience", nil}, {"e", "project", nil}}} {
		if v, ids, e := BuildSelectionWorksheet(context.Background(), *matrix, catalog, experiences, 6); v != nil || ids != nil || e == nil {
			t.Fatal("missing/duplicate catalog accepted")
		}
	}
	for _, limit := range []int{0, -1, 10001} {
		if v, ids, e := BuildSelectionWorksheet(context.Background(), *matrix, catalog, experiences, limit); v != nil || ids != nil || e == nil {
			t.Fatal("unsupported limit")
		}
	}
	if v, ids, e := BuildSelectionWorksheet(ctx, *matrix, catalog, experiences, 6); v != nil || ids != nil || e == nil {
		t.Fatal("cancellation")
	}
	oversized := Matrix{JobID: "fixture", Requirements: []MatrixRow{{ID: strings.Repeat("x", 16000), Priority: "required", DirectEvidenceIDs: make([]string, 1000)}}}
	for i := range oversized.Requirements[0].DirectEvidenceIDs {
		oversized.Requirements[0].DirectEvidenceIDs[i] = "e"
	}
	if v, ids, e := BuildSelectionWorksheet(context.Background(), oversized, catalog, experiences, 6); v != nil || ids != nil || e == nil {
		t.Fatal("expanded score budget")
	}
	experiences[0].Company = strings.Repeat("x", 6<<20)
	if v, ids, e := BuildSelectionWorksheet(context.Background(), *matrix, catalog, experiences, 6); v != nil || ids != nil || e == nil {
		t.Fatal("output budget")
	}
}
