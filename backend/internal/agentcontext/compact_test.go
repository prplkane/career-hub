package agentcontext

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/jobevaluation"
	"careerhub.local/backend/internal/skills/catalog"
)

type compactCase struct {
	Registry  bool                 `json:"registry"`
	Job       map[string]any       `json:"job"`
	Matrix    Matrix               `json:"matrix"`
	Review    jobevaluation.Result `json:"review"`
	Model     map[string]any       `json:"model"`
	Purpose   string               `json:"purpose"`
	Mode      string               `json:"mode"`
	Persisted bool                 `json:"persisted"`
	Groups    []catalog.Group      `json:"groups"`
	Failure   *string              `json:"failure"`
}

func emptyCompactCase() compactCase {
	return compactCase{Job: map[string]any{"id": "fixture"}, Matrix: Matrix{JobID: "fixture", Requirements: []MatrixRow{}}, Review: emptyEvidenceCase().Review, Model: map[string]any{"job": map[string]any{"id": "fixture"}, "candidate_direct_evidence": []any{}}, Purpose: "assessment", Groups: []catalog.Group{}}
}

func TestCompactFailureBoundaries(t *testing.T) {
	c := emptyCompactCase()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if v, e := BuildCompactAssessment(ctx, c.Matrix, jsonFixture(t, c.Job), c.Review, jsonFixture(t, c.Model), c.Purpose); v != nil || e == nil {
		t.Fatal("cancel")
	}
	if v, e := BuildCompactAssessment(context.Background(), c.Matrix, jsonFixture(t, c.Job), c.Review, jsonFixture(t, c.Model), "unknown"); v != nil || e == nil {
		t.Fatal("purpose")
	}
	for _, value := range []any{nil, true, -1, json.Number("1.5"), "2"} {
		c.Job["digest"] = map[string]any{"analysis_coverage": map[string]any{"source_lines": value}}
		if v, e := BuildCompactAssessment(context.Background(), c.Matrix, jsonFixture(t, c.Job), c.Review, jsonFixture(t, c.Model), c.Purpose); v != nil || e == nil {
			t.Fatal("invalid card source count")
		}
	}
	c = emptyCompactCase()
	c.Review.JobID = "other"
	if v, e := BuildCompactAssessment(context.Background(), c.Matrix, jsonFixture(t, c.Job), c.Review, jsonFixture(t, c.Model), c.Purpose); v != nil || e == nil {
		t.Fatal("identity")
	}
	if v, e := BuildRegistryContext(context.Background(), jsonFixture(t, c.Job), "unknown", true, nil, nil); v != nil || e == nil {
		t.Fatal("registry mode")
	}
	if v, e := BuildRegistryContext(ctx, jsonFixture(t, c.Job), "off", true, nil, nil); v != nil || e == nil {
		t.Fatal("registry cancel")
	}
	c.Job["digest"] = map[string]any{}
	if v, e := BuildRegistryContext(context.Background(), jsonFixture(t, c.Job), "exact-auto", true, []catalog.Group{{ID: "g", Name: strings.Repeat("x", 3<<20)}}, nil); v != nil || e == nil {
		t.Fatal("registry output expansion")
	}
}
