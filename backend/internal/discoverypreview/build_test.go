package discoverypreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/jobevaluation"
	"careerhub.local/backend/internal/jobevidence"
	"careerhub.local/backend/internal/skills/catalog"
)

func parsed(t *testing.T, raw string) cataloginput.Value {
	t.Helper()
	value, err := cataloginput.ParseYAML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func inputFixture(t *testing.T) Input {
	t.Helper()
	terms, err := cataloginput.DecodeTerms([]byte("[{id: python, name: Python, category: programming_language}, {id: rust, name: Rust, category: programming_language}]"), false)
	if err != nil {
		t.Fatal(err)
	}
	registry, regErr := catalog.Build(nil, nil, terms, nil, nil, nil)
	if regErr != nil {
		t.Fatal(regErr)
	}
	source := "Company: Synthetic\nRole: Software Engineer\nRequirements\nPython and Rust.\nResponsibilities\nBuild reliable services.\nHow to apply\nEmail your resume to jobs@example.test.\nContact Jane Doe, Recruiter at Synthetic: jane@example.test.\n"
	sum := sha256.Sum256([]byte(source))
	return Input{Capture: Capture{Identity: assessmentcontext.CacheCapture{DiscoveryID: "synthetic", Scope: "confirmed_full", ContentSHA256: hex.EncodeToString(sum[:]), Company: "Synthetic", Title: "Software Engineer", ObservedURL: "https://example.test/jobs/1"}, Source: source, RelativePath: "jobs/discovery/captures/synthetic.txt"},
		Registry: registry, Evaluation: jobevaluation.Input{Profile: parsed(t, "current_status: citizen\nrequires_sponsorship_now: false\nrequires_future_sponsorship: false\n"), Policy: parsed(t, "{}"),
			Index: jobevidence.Index{"experience:exp": {Text: "python", Kind: "direct", EvidenceIDs: []string{"exp"}}}, DurationMonths: map[string]float64{"exp": 24}},
		CheckSource: func(ctx context.Context, path string) (bool, *jobcard.Error) {
			return path == "jobs/discovery/captures/synthetic.txt", nil
		}}
}
func TestCompleteAdvisoryPreview(t *testing.T) {
	input := inputFixture(t)
	before := input.Registry.CatalogRevision()
	got, err := Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	a := got.Artifact
	if a.SchemaVersion != Version || a.Receipt.SchemaVersion != ReceiptVersion || a.AssessmentMode != "advisory_only" || !a.ApplicationAllowed ||
		a.Assessment.JobID != "synthetic" || len(a.Assessment.MatchedRequired) != 1 || len(a.Assessment.RequirementGaps) != 1 ||
		a.Receipt.ReviewCount < 1 || a.Receipt.ReviewItems[0].Kind != "evidence_not_found" ||
		!strings.Contains(a.Receipt.ReviewItems[0].Text, "Rust") || len(a.Instructions.Items) == 0 || len(a.Contacts.Candidates) == 0 {
		t.Fatalf("incomplete preview: %#v", a)
	}
	if input.Registry.CatalogRevision() != before {
		t.Fatal("preview mutated vocabulary")
	}
	again, err := Build(context.Background(), input)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatal("not deterministic")
	}
	got.Artifact.Assessment.RequirementGaps[0].Message = "modified"
	if again.Artifact.Assessment.RequirementGaps[0].Message == "modified" {
		t.Fatal("separate calls share output")
	}
}
func TestPreviewRejectsCaptureOrSourceMismatch(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Input)
	}{
		{"hash", func(in *Input) { in.Capture.Identity.ContentSHA256 = strings.Repeat("0", 64) }},
		{"source", func(in *Input) { in.Capture.Source += "changed" }},
		{"scope", func(in *Input) { in.Capture.Identity.Scope = "selection" }},
		{"source-path", func(in *Input) { in.Capture.RelativePath = "jobs/incoming/synthetic.txt" }},
		{"escape", func(in *Input) { in.Capture.RelativePath = "jobs/discovery/captures/../private.txt" }},
		{"missing-file", func(in *Input) {
			in.CheckSource = func(context.Context, string) (bool, *jobcard.Error) { return false, nil }
		}},
		{"file-failure", func(in *Input) {
			in.CheckSource = func(context.Context, string) (bool, *jobcard.Error) {
				return false, &jobcard.Error{Code: "PRIVATE_SOURCE"}
			}
		}},
		{"no-check", func(in *Input) { in.CheckSource = nil }},
		{"empty", func(in *Input) { in.Capture.Source = "" }},
		{"encoding", func(in *Input) { in.Capture.Source = "\xff" }},
		{"registry", func(in *Input) { in.Registry = nil }},
		{"profile", func(in *Input) { in.Evaluation.Profile = parsed(t, "[]") }},
		{"policy", func(in *Input) { in.Evaluation.Policy = parsed(t, "[]") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			in := inputFixture(t)
			test.change(&in)
			if got, err := Build(context.Background(), in); got != nil || err == nil {
				t.Fatal("partial or successful invalid preview")
			}
		})
	}
	if got, err := Build(nil, inputFixture(t)); got != nil || !errors.Is(err, ErrInput) {
		t.Fatal("nil context")
	}
}
func TestReceiptOrderAndBounds(t *testing.T) {
	assessment := &jobevaluation.Result{
		EligibilityBlockers: []jobevaluation.Finding{
			{Code: "missing-proof", CandidateFact: jobevaluation.CandidateFact{Statement: "Confirmed"}},
			{Code: "clear", Message: "Clear conflict", SourceEvidence: []any{map[string]any{"line": json.Number("7"), "text": " Exact \u00a0 JD "}}, CandidateFact: jobevaluation.CandidateFact{Statement: "Confirmed fact"}},
		},
		RequirementGaps:             []jobevaluation.RequirementGap{{Message: "Missing required"}},
		MissingPreferred:            []string{"Extra"},
		PolicyReviews:               []jobevaluation.Finding{{Message: "Review policy", CandidateFact: jobevaluation.CandidateFact{Statement: "Fact"}}},
		ClassifiedRequirementReview: []jobevaluation.ClassifiedReview{{Requirement: "required", Status: "manual_review", SourceText: "Required"}},
		ExperienceReview:            []jobevaluation.ExperienceReview{{MinimumYears: "10", Area: strings.Repeat("界", 800), Status: "manual_review"}},
		ManualReview:                []string{"Manual"},
	}
	summary, err := receipt(context.Background(), assessment, []Excerpt{{11, "Unclassified"}}, "page_visible", 1, 2)
	if err != nil || summary.BlockerCount != 1 || summary.ReviewCount != 8 || !summary.Truncated || len(summary.ReviewItems) != 3 ||
		summary.ReviewItems[0].Kind != "evidence_not_found" || summary.ReviewItems[1].Kind != "preferred_evidence_not_found" || summary.ReviewItems[2].Kind != "unknown_vocabulary" {
		t.Fatalf("receipt counts/order: %#v, %v", summary, err)
	}
	only := &jobevaluation.Result{ExperienceReview: assessment.ExperienceReview}
	summary, err = receipt(context.Background(), only, nil, "confirmed_full", 0, 0)
	if err != nil || len([]rune(summary.ReviewItems[0].Text)) != 500 {
		t.Fatal("assembled experience exceeds full text bound")
	}
	for _, invalid := range []any{true, 1.0, json.Number("1.0"), json.Number("1e0"), 0, -1, "1"} {
		if firstExcerpt([]any{map[string]any{"line": invalid, "text": "JD"}}) != nil {
			t.Fatalf("invalid excerpt line accepted: %#v", invalid)
		}
	}
}
func TestUnavailableAndPartialFamilies(t *testing.T) {
	for _, scope := range []string{"link_only", "selection"} {
		summary, err := Unavailable(scope, "ignored", 0, 0)
		instructions, ie := PartialInstructions(scope)
		contacts, ce := PartialContacts(scope)
		if err != nil || ie != nil || ce != nil || summary.Status != "unavailable" || summary.Availability != "partial_capture" ||
			len(summary.ReviewItems) != 0 || instructions.Eligible || contacts.Eligible || instructions.Status != "full_jd_required" || contacts.Status != "full_jd_required" {
			t.Fatal("partial capture appears assessed")
		}
	}
	for _, scope := range []string{"page_visible", "confirmed_full"} {
		summary, err := Unavailable(scope, "context_unavailable", 1, 2)
		if err != nil || summary.Status != "review" || summary.ReviewCount != 1 || summary.ExtraStepCount != 1 || summary.ContactCandidateCount != 2 {
			t.Fatal("unavailable context contract")
		}
		if _, err := PartialInstructions(scope); err == nil {
			t.Fatal("partial instructions for full capture")
		}
		if _, err := PartialContacts(scope); err == nil {
			t.Fatal("partial contacts for full capture")
		}
	}
	for _, test := range []struct {
		scope, reason   string
		steps, contacts int
	}{{"private", "x", 0, 0}, {"confirmed_full", "private error", 0, 0}, {"selection", "x", -1, 0}, {"link_only", "x", 0, 25}} {
		if _, err := Unavailable(test.scope, test.reason, test.steps, test.contacts); err == nil {
			t.Fatal("unsafe unavailable projection")
		}
	}
}
func TestWholePolicyValidation(t *testing.T) {
	good := "schema_version: 1\nclasses: {citizenship_work_authorization: true, active_clearance: false, minimum_experience_years: true}\n"
	if got, err := Policy(context.Background(), parsed(t, good), true); err != nil || len(got.Entries) != 3 || got.Entries[1].Value.Boolean {
		t.Fatal("valid policy")
	}
	defaults, err := Policy(context.Background(), cataloginput.Value{}, false)
	if err != nil || len(defaults.Entries) != 3 {
		t.Fatal("missing policy defaults")
	}
	for _, raw := range []string{"null", "[]", "{}",
		strings.Replace(good, "schema_version: 1", "schema_version: true", 1),
		strings.Replace(good, "schema_version: 1", "schema_version: 1.0", 1),
		strings.Replace(good, "schema_version: 1", "schema_version: 2", 1),
		strings.Replace(good, "active_clearance: false", "active_clearance: 'false'", 1),
		strings.Replace(good, "active_clearance: false, ", "", 1),
		strings.Replace(good, "active_clearance: false", "new_policy: false", 1),
		good + "unknown: private\n"} {
		if _, err := Policy(context.Background(), parsed(t, raw), true); err == nil {
			t.Fatalf("invalid policy allowed: %s", raw)
		}
	}
	if _, err := Policy(nil, parsed(t, good), true); !errors.Is(err, ErrInput) {
		t.Fatal("nil policy context")
	}
}

type countContext struct{ calls, stop int }

func (c *countContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *countContext) Done() <-chan struct{}       { return nil }
func (c *countContext) Value(any) any               { return nil }
func (c *countContext) Err() error {
	c.calls++
	if c.stop > 0 && c.calls >= c.stop {
		return context.Canceled
	}
	return nil
}
func TestEveryPreviewCancellationCheckpoint(t *testing.T) {
	in := inputFixture(t)
	baseline := &countContext{}
	if _, err := Build(baseline, in); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= baseline.calls; i++ {
		if got, err := Build(&countContext{stop: i}, in); got != nil || err == nil {
			t.Fatalf("checkpoint %d published", i)
		}
	}
	t.Logf("qualified %d observed preview checkpoints", baseline.calls)
}

func TestReceiptLabelsCoverageAdvisoryDistinctly(t *testing.T) {
	assessment := &jobevaluation.Result{RequirementGaps: []jobevaluation.RequirementGap{
		{Category: "skill", Message: "Missing required"},
		{Category: "coverage", Code: jobevaluation.NoRequirementSectionsCode, Message: "No requirement sections were recognized."},
	}}
	summary, err := receipt(context.Background(), assessment, nil, "page_visible", 0, 0)
	if err != nil || summary.ReviewCount != 2 || len(summary.ReviewItems) != 2 ||
		summary.ReviewItems[0].Kind != "evidence_not_found" || summary.ReviewItems[1].Kind != "coverage" || summary.ReviewItems[1].Line != nil {
		t.Fatalf("coverage advisory receipt kind: %#v, %v", summary, err)
	}
}
