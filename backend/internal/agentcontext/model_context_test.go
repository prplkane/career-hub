package agentcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/jobevaluation"
)

type modelCase struct {
	Bundle        bool                 `json:"bundle"`
	Job           json.RawMessage      `json:"job"`
	Review        jobevaluation.Result `json:"review"`
	Sources       ModelSources         `json:"sources"`
	Shortlist     *[]string            `json:"shortlist"`
	MatrixPath    string               `json:"matrix_path"`
	WorksheetPath string               `json:"worksheet_path"`
	JobCardPath   string               `json:"job_card_path"`
}

func jsonFixture(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func baseModelCase(t *testing.T) modelCase {
	t.Helper()
	base := emptyEvidenceCase()
	for key, value := range map[string]any{"company": "Synthetic firm", "title": "Engineer", "location": "US", "remote_policy": "remote", "seniority": "entry", "responsibilities": []string{"Build"}, "visa_signals": map[string]any{"sponsorship_mentioned": nil}, "contact": "PRIVATE_JOB_CONTACT"} {
		base.Job[key] = value
	}
	base.Review.RelevantEvidenceIDs = []string{"p", "e", "c", "e"}
	base.Review.EligibilityBlockers = []jobevaluation.Finding{}
	base.Review.MissingPreferred = []string{"Desired"}
	base.Review.ManualReview = []string{"Verify"}
	base.Review.TaxonomyHints = []jobevaluation.TaxonomyHint{{SkillIDs: []string{"s", "s"}}}
	return modelCase{
		Job: jsonFixture(t, base.Job), Review: base.Review,
		Sources: ModelSources{
			Experiences: json.RawMessage(`[{"id":"e","company":"Firm","role":"Engineer","dates":"2020–2023","category":"work","technologies":["Go"],"proof_points":["Built 😀","Verified"],"notes":"PRIVATE_NOTES","contacts":["PRIVATE_CONTACT"]},{"id":"archived","archived":true,"proof_points":["PRIVATE_ARCHIVED"]},null,42]`),
			Projects:    json.RawMessage(`[{"id":"p","name":"Tool","category":"side","summary":"Local tool","technologies":["SQL"],"proof_points":["Shipped"],"related_experience":["e"],"private":"PRIVATE_PROJECT"}]`),
			Education:   json.RawMessage(`{"schools":[{"id":"school","institution":"School","degree":"BS","gpa":3.5,"honors":["Honors"],"notes":"PRIVATE_SCHOOL"},{"id":"old","archived":true,"notes":"PRIVATE_OLD_SCHOOL"}],"certifications":[{"id":"c","name":"Certification","credential":"Test","completed":"2025","proof_points":["Exam"],"secret":"PRIVATE_CERT"}]}`),
			Profile:     json.RawMessage(`{"name":"PRIVATE_NAME","contact":{"email":"PRIVATE_EMAIL"},"current_location":"Here","target_location":"US","location_flexibility":"relocate"}`),
			Visa:        json.RawMessage(`{"work_authorization":"Permitted","requires_future_sponsorship":true,"document_path":"PRIVATE_VISA_PATH"}`),
			Skills:      json.RawMessage(`[{"id":"s","skill_family":"Technical","keywords":["PRIVATE_KEYWORDS"]}]`),
			StyleRules:  json.RawMessage(`{"resume_voice":{"tone":"plain"},"bullet_rules":["Exact proof"],"selection_rules":{"policy":"relevant"},"forbidden":["Fabrication"],"document_rules":{"private":"PRIVATE_STYLE"}}`),
		},
		JobCardPath: "jobs/normalized/fixture.yaml",
	}
}

func TestModelContextLimitsAndIsolation(t *testing.T) {
	c := baseModelCase(t)
	ctx := context.Background()
	input := ModelContextInput{Sources: c.Sources, JobCardPath: c.JobCardPath}
	raw, err := BuildModelContext(ctx, c.Job, c.Review, input)
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(raw)
	input.Sources.Experiences[0] = '!'
	c.Review.ManualReview[0] = "Changed"
	if !bytes.Equal(raw, before) {
		t.Fatal("mutable output")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if v, e := BuildModelContext(cancelled, c.Job, c.Review, input); v != nil || e == nil || e.Code != "AGENT_CONTEXT_CANCELED" {
		t.Fatal("cancel")
	}
	c = baseModelCase(t)
	input = ModelContextInput{Sources: c.Sources}
	c.Review.JobID = "other"
	if v, e := BuildModelContext(ctx, c.Job, c.Review, input); v != nil || e == nil || e.Code != "AGENT_CONTEXT_MODEL_IDENTITY" {
		t.Fatal("identity")
	}
	c = baseModelCase(t)
	input = ModelContextInput{Sources: c.Sources}
	input.Sources.Profile = json.RawMessage(strings.Repeat(" ", jobevaluation.MaxInputBytes+1))
	if v, e := BuildModelContext(ctx, c.Job, c.Review, input); v != nil || e == nil || e.Code != "AGENT_CONTEXT_MODEL_LIMIT" {
		t.Fatal("input limit")
	}
	input.Sources.Profile = json.RawMessage(strings.Repeat("[", 70) + "0" + strings.Repeat("]", 70))
	if v, e := BuildModelContext(ctx, c.Job, c.Review, input); v != nil || e == nil {
		t.Fatal("depth bound")
	}
	input.Sources.Profile = json.RawMessage(`{"target_location":"` + strings.Repeat("a", 200000) + `"}`)
	input.ShortlistIDs = &[]string{}
	if _, e := BuildModelContext(ctx, c.Job, c.Review, input); e != nil {
		t.Fatal("valid bounded metadata")
	}
	input = ModelContextInput{Sources: c.Sources, ShortlistIDs: &[]string{"e"}}
	// Repetition expansion is bounded independently from encoded source bytes.
	input.Sources.Experiences = json.RawMessage(`[{"id":"e","company":"` + strings.Repeat("a", 10000) + `","role":"Role","dates":"2025","category":"work","technologies":[],"proof_points":[]}]`)
	ids := make([]string, 1000)
	for i := range ids {
		ids[i] = "e"
	}
	input.ShortlistIDs = &ids
	if v, e := BuildModelContext(ctx, c.Job, c.Review, input); v != nil || e == nil || e.Code != "AGENT_CONTEXT_MODEL_LIMIT" {
		t.Fatal("expanded output bound")
	}
}
