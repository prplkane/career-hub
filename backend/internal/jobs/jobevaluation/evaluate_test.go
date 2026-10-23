package jobevaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/jobs/jobevidence"
	"careerhub.local/backend/internal/skills/cataloginput"
)

func textValue(text string) cataloginput.Value { return cataloginput.Value{Kind: "string", Text: text} }
func number(text string) cataloginput.Value    { return cataloginput.Value{Kind: "float", Text: text} }
func sequence(values ...cataloginput.Value) cataloginput.Value {
	return cataloginput.Value{Kind: "sequence", Items: values}
}
func object(values ...cataloginput.Entry) cataloginput.Value {
	return cataloginput.Value{Kind: "mapping", Entries: values}
}
func entry(key string, value cataloginput.Value) cataloginput.Entry {
	return cataloginput.Entry{Key: textValue(key), Value: value}
}
func set(value *cataloginput.Value, key string, field cataloginput.Value) {
	for i := range value.Entries {
		if value.Entries[i].Key.Text == key {
			value.Entries[i].Value = field
			return
		}
	}
	value.Entries = append(value.Entries, entry(key, field))
}
func base(t *testing.T) Input {
	t.Helper()
	job, err := cataloginput.ParseYAML([]byte(`id: synthetic
required_skills: [Python]
required_platforms: []
required_capabilities: []
preferred_skills: []
education_requirements: []
experience_requirements: []
technology_groups: []
visa_signals:
  citizenship_required: null
  clearance_required: null
  no_visa_or_opt: null
  sponsorship_mentioned: null
`))
	if err != nil {
		t.Fatal(err)
	}
	return Input{Job: job, Profile: object(), Policy: cataloginput.Value{Kind: "null"},
		Index:          jobevidence.Index{"experience:exp": {Text: "python", Kind: "direct", EvidenceIDs: []string{"exp"}}},
		DurationMonths: map[string]float64{"exp": 24}}
}

func expectFailure(t *testing.T, ctx context.Context, input Input, code string) {
	t.Helper()
	result, err := Evaluate(ctx, input)
	if result != nil || err == nil || err.Code != code {
		t.Fatalf("expected atomic %s, got result=%v error=%v", code, result != nil, err)
	}
	for _, format := range []string{"%s", "%v", "%+v", "%#v"} {
		if rendered := fmt.Sprintf(format, err); rendered != "Job evaluation could not be completed." {
			t.Fatalf("error formatting exposes source data: %q", rendered)
		}
	}
}

func TestMalformedInputsFailWithoutPartialVerdict(t *testing.T) {
	for _, test := range []struct {
		name, code string
		change     func(*Input)
	}{
		{"job-root", "EVALUATION_JOB_SHAPE", func(in *Input) { in.Job = sequence() }},
		{"profile-root", "EVALUATION_JOB_SHAPE", func(in *Input) { in.Profile = sequence() }},
		{"policy-root", "EVALUATION_JOB_SHAPE", func(in *Input) { in.Policy = sequence() }},
		{"id-kind", "EVALUATION_JOB_SHAPE", func(in *Input) { set(&in.Job, "id", number("1.0")) }},
		{"id-empty", "EVALUATION_JOB_SHAPE", func(in *Input) { set(&in.Job, "id", textValue(" \x1c\u00a0")) }},
		{"id-long", "EVALUATION_JOB_SHAPE", func(in *Input) { set(&in.Job, "id", textValue(strings.Repeat("x", 1025))) }},
		{"wrong-collection", "EVALUATION_JOB_SHAPE", func(in *Input) { set(&in.Job, "required_skills", textValue("Python")) }},
		{"wrong-label", "EVALUATION_JOB_SHAPE", func(in *Input) { set(&in.Job, "required_skills", sequence(number("1.0"))) }},
		{"label-limit", "EVALUATION_INPUT_LIMIT", func(in *Input) {
			set(&in.Job, "required_skills", sequence(textValue(strings.Repeat("x", maxLabelBytes+1))))
		}},
		{"status-limit", "EVALUATION_INPUT_LIMIT", func(in *Input) { set(&in.Profile, "current_status", textValue(strings.Repeat("x", maxLabelBytes+1))) }},
		{"missing-signal", "EVALUATION_JOB_SHAPE", func(in *Input) { set(&in.Job, "visa_signals", object()) }},
		{"non-bool-signal", "EVALUATION_JOB_SHAPE", func(in *Input) {
			signals := get(in.Job, "visa_signals")
			set(&signals, "citizenship_required", textValue("true"))
			set(&in.Job, "visa_signals", signals)
		}},
		{"duplicate-key", "EVALUATION_INPUT_UNSUPPORTED", func(in *Input) { in.Job.Entries = append(in.Job.Entries, entry("id", textValue("duplicate"))) }},
		{"non-string-key", "EVALUATION_INPUT_UNSUPPORTED", func(in *Input) {
			in.Job.Entries = append(in.Job.Entries, cataloginput.Entry{Key: number("1.0"), Value: textValue("private")})
		}},
		{"invalid-utf8", "EVALUATION_INPUT_UNSUPPORTED", func(in *Input) { set(&in.Job, "notes", textValue("\xffprivate")) }},
		{"object", "EVALUATION_INPUT_UNSUPPORTED", func(in *Input) { set(&in.Job, "notes", cataloginput.Value{Kind: "arbitrary-object"}) }},
		{"invalid-int", "EVALUATION_INPUT_UNSUPPORTED", func(in *Input) { set(&in.Job, "notes", cataloginput.Value{Kind: "int", Text: "1.0"}) }},
		{"nan", "EVALUATION_INPUT_UNSUPPORTED", func(in *Input) { set(&in.Job, "notes", number("NaN")) }},
		{"infinity", "EVALUATION_INPUT_UNSUPPORTED", func(in *Input) { set(&in.Job, "notes", number("1e999")) }},
		{"bad-number", "EVALUATION_INPUT_UNSUPPORTED", func(in *Input) { set(&in.Job, "notes", number("\"1.0\"")) }},
		{"input-size", "EVALUATION_INPUT_LIMIT", func(in *Input) { set(&in.Job, "notes", textValue(strings.Repeat("x", MaxInputBytes+1))) }},
		{"depth", "EVALUATION_INPUT_LIMIT", func(in *Input) {
			value := textValue("private")
			for i := 0; i < 66; i++ {
				value = sequence(value)
			}
			set(&in.Job, "notes", value)
		}},
		{"nodes", "EVALUATION_INPUT_LIMIT", func(in *Input) { set(&in.Job, "notes", sequence(make([]cataloginput.Value, maxNodes)...)) }},
		{"requirements", "EVALUATION_REQUIREMENT_LIMIT", func(in *Input) {
			values := make([]cataloginput.Value, MaxRequirements+1)
			for i := range values {
				values[i] = textValue("Python")
			}
			set(&in.Job, "required_skills", sequence(values...))
		}},
		{"group-null", "EVALUATION_GROUPS_UNSUPPORTED", func(in *Input) { set(&in.Job, "technology_groups", cataloginput.Value{Kind: "null"}) }},
		{"group-level", "EVALUATION_JOB_SHAPE", func(in *Input) {
			set(&in.Job, "technology_groups", sequence(object(entry("requirement", textValue("unknown")))))
		}},
		{"group-operator", "EVALUATION_JOB_SHAPE", func(in *Input) {
			set(&in.Job, "technology_groups", sequence(object(entry("requirement", textValue("required")), entry("operator", textValue("all_of")))))
		}},
		{"group-single", "EVALUATION_JOB_SHAPE", func(in *Input) {
			set(&in.Job, "technology_groups", sequence(object(entry("requirement", textValue("required")), entry("operator", textValue("any_of")), entry("technologies", sequence(textValue("Python"))))))
		}},
		{"experience-list", "EVALUATION_JOB_SHAPE", func(in *Input) { set(&in.Job, "experience_requirements", textValue("10 years")) }},
		{"experience-number", "EVALUATION_JOB_SHAPE", func(in *Input) {
			set(&in.Job, "experience_requirements", sequence(object(entry("minimum_years", textValue("ten")))))
		}},
		{"experience-number-range", "EVALUATION_NUMBER_UNSUPPORTED", func(in *Input) {
			set(&in.Job, "experience_requirements", sequence(object(entry("minimum_years", number("10001.0")))))
		}},
		{"experience-max-range", "EVALUATION_JOB_SHAPE", func(in *Input) {
			set(&in.Job, "experience_requirements", sequence(object(entry("minimum_years", number("2.0")), entry("maximum_years", number("1.0")))))
		}},
		{"experience-area", "EVALUATION_JOB_SHAPE", func(in *Input) {
			set(&in.Job, "experience_requirements", sequence(object(entry("minimum_years", number("2.0")), entry("area", number("3.0")))))
		}},
		{"classified-shape", "EVALUATION_JOB_SHAPE", func(in *Input) {
			set(&in.Job, "digest", object(entry("classified_requirements", sequence(textValue("private")))))
		}},
		{"classified-evidence", "EVALUATION_JOB_SHAPE", func(in *Input) {
			set(&in.Job, "digest", object(entry("classified_requirements", sequence(object(entry("category", textValue("technical")), entry("name", textValue("Python")), entry("requirement", textValue("required")), entry("text", textValue("Python required")))))))
		}},
		{"date-evidence", "EVALUATION_EVIDENCE_UNSUPPORTED", func(in *Input) {
			set(&in.Job, "digest", object(entry("eligibility_gates", object(entry("citizenship", object(entry("evidence", sequence(cataloginput.Value{Kind: "date", Text: "2026-01-01"}))))))))
		}},
		{"evidence-size", "EVALUATION_EVIDENCE_LIMIT", func(in *Input) {
			set(&in.Job, "digest", object(entry("eligibility_gates", object(entry("citizenship", object(entry("evidence", sequence(textValue(strings.Repeat("x", 64<<10))))))))))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := base(t)
			test.change(&input)
			expectFailure(t, context.Background(), input, test.code)
		})
	}
}

func TestEvidenceSnapshotRefusals(t *testing.T) {
	for _, test := range []struct {
		name, code string
		change     func(*Input)
	}{
		{"source-count", "EVALUATION_EVIDENCE_LIMIT", func(in *Input) {
			for i := 0; i <= jobevidence.MaxSources; i++ {
				in.Index[fmt.Sprint(i)] = jobevidence.Source{}
			}
		}},
		{"kind", "EVALUATION_EVIDENCE_INVALID", func(in *Input) { in.Index["bad"] = jobevidence.Source{Text: "python", Kind: "inferred"} }},
		{"text", "EVALUATION_EVIDENCE_INVALID", func(in *Input) { in.Index["bad"] = jobevidence.Source{Text: "\xff", Kind: "direct"} }},
		{"id", "EVALUATION_EVIDENCE_INVALID", func(in *Input) {
			in.Index["bad"] = jobevidence.Source{Text: "python", Kind: "direct", EvidenceIDs: []string{strings.Repeat("x", 1025)}}
		}},
		{"ids-count", "EVALUATION_EVIDENCE_LIMIT", func(in *Input) {
			in.Index["bad"] = jobevidence.Source{Kind: "direct", EvidenceIDs: make([]string, jobevidence.MaxSources+1)}
		}},
		{"bytes", "EVALUATION_EVIDENCE_LIMIT", func(in *Input) {
			in.Index["bad"] = jobevidence.Source{Text: strings.Repeat("x", jobevidence.MaxIndexBytes), Kind: "direct"}
		}},
		{"duration-negative", "EVALUATION_DURATION_UNSUPPORTED", func(in *Input) { in.DurationMonths["exp"] = -1 }},
		{"duration-nan", "EVALUATION_DURATION_UNSUPPORTED", func(in *Input) { in.DurationMonths["exp"] = math.NaN() }},
		{"duration-infinite", "EVALUATION_DURATION_UNSUPPORTED", func(in *Input) { in.DurationMonths["exp"] = math.Inf(1) }},
		{"duration-range", "EVALUATION_DURATION_UNSUPPORTED", func(in *Input) { in.DurationMonths["exp"] = 120001 }},
		{"expansion-text", "EVALUATION_EVIDENCE_INVALID", func(in *Input) { in.Expansions = map[string][]string{"python": {"\xff"}} }},
		{"expansion-key", "EVALUATION_EVIDENCE_INVALID", func(in *Input) { in.Expansions = map[string][]string{"\xff": {"Python"}} }},
		{"expansion-count", "EVALUATION_EVIDENCE_LIMIT", func(in *Input) { in.Expansions = map[string][]string{"python": make([]string, 20001)} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := base(t)
			test.change(&input)
			expectFailure(t, context.Background(), input, test.code)
		})
	}
}

func TestAggregateWorkResultAndMemoLimits(t *testing.T) {
	input := base(t)
	terms := make([]cataloginput.Value, 256)
	for i := range terms {
		terms[i] = textValue(fmt.Sprint("term", i))
	}
	concepts, err := jobevidence.LoadConcepts(context.Background(), sequence(object(entry("id", textValue("concept")), entry("aliases", sequence(textValue("Python"))), entry("evidence_terms", sequence(terms...)))))
	if err != nil {
		t.Fatal(err)
	}
	input.Concepts = concepts
	input.Index["experience:exp"] = jobevidence.Source{Text: strings.Repeat("x", 2<<20), Kind: "direct", EvidenceIDs: []string{"exp"}}
	expectFailure(t, context.Background(), input, "EVALUATION_QUERY_LIMIT")
	input = base(t)
	input.Concepts = concepts
	input.Index = jobevidence.Index{}
	state := evaluation{ctx: context.Background(), input: input, memo: map[string]jobevidence.ConceptMatches{}, queryWork: MaxQueryWork - 1}
	if result, failure := state.match("Python"); failure == nil || failure.Code != "EVALUATION_QUERY_LIMIT" || result.Direct != nil {
		t.Fatal("empty-index normalization work escaped the aggregate budget")
	}
	scans, bytes := concepts.QuerySize("python")
	if scans != 257 || bytes == 0 {
		t.Fatal("concept query work metadata incomplete")
	}
	var absent *jobevidence.Concepts
	if scans, bytes := absent.QuerySize("python"); scans != 1 || bytes != 0 {
		t.Fatal("empty overlay query cost differs")
	}
	if scans, bytes := concepts.QuerySize("unknown"); scans != 1 || bytes != 0 {
		t.Fatal("non-alias query cost differs")
	}
	ids := make([]string, 1000)
	for i := range ids {
		ids[i] = fmt.Sprintf("id%04d-%s", i, strings.Repeat("x", 512))
	}
	values := make([]cataloginput.Value, 20)
	for i := range values {
		values[i] = textValue("Python")
	}
	input = base(t)
	input.Index["experience:exp"] = jobevidence.Source{Text: "python", Kind: "direct", EvidenceIDs: ids}
	set(&input.Job, "required_skills", sequence(values...))
	expectFailure(t, context.Background(), input, "EVALUATION_RESULT_LIMIT")
	input = base(t)
	words := []string{}
	for i := range values {
		words = append(words, fmt.Sprint("term", i))
		values[i] = textValue(words[i])
	}
	input.Index["experience:exp"] = jobevidence.Source{Text: strings.Join(words, " "), Kind: "direct", EvidenceIDs: ids}
	set(&input.Job, "required_skills", sequence())
	set(&input.Job, "technology_groups", sequence(object(entry("requirement", textValue("required")), entry("operator", textValue("any_of")), entry("technologies", sequence(values...)))))
	expectFailure(t, context.Background(), input, "EVALUATION_MEMO_LIMIT")
}

type steppingContext struct {
	context.Context
	cancel    context.CancelFunc
	calls, at int
}

func (ctx *steppingContext) Err() error {
	ctx.calls++
	if ctx.at > 0 && ctx.calls >= ctx.at {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestCancellationAtEveryEvaluationCheckpoint(t *testing.T) {
	input := base(t)
	set(&input.Job, "experience_requirements", sequence(object(entry("minimum_years", number("3.0")), entry("area", textValue("Python")))))
	set(&input.Job, "digest", object(entry("provenance", object(entry("experience_requirements", sequence(object(entry("text", textValue("Requires 3 years")), entry("line", cataloginput.Value{Kind: "int", Text: "1"}))))))))
	parent, cancel := context.WithCancel(context.Background())
	ctx := &steppingContext{Context: parent, cancel: cancel}
	if result, err := Evaluate(ctx, input); err != nil || result == nil {
		t.Fatal("uncanceled baseline failed")
	}
	count := ctx.calls
	cancel()
	for at := 1; at <= count; at++ {
		parent, cancel := context.WithCancel(context.Background())
		ctx := &steppingContext{Context: parent, cancel: cancel, at: at}
		expectFailure(t, ctx, input, "EVALUATION_CANCELED")
		cancel()
	}
	expectFailure(t, nil, input, "EVALUATION_CANCELED")
	t.Logf("verified cancellation at all %d observed checkpoints", count)
}

func TestFreshEvaluationDetachedProvenanceAndMixedGapPrecedence(t *testing.T) {
	input := base(t)
	set(&input.Job, "experience_requirements", sequence(object(entry("minimum_years", number("2.1")), entry("area", textValue("Python")))))
	input.DurationMonths["exp"] = 25
	first, err := Evaluate(context.Background(), input)
	if err != nil || first.ExperienceReview[0].Status != "manual_review" || len(first.EligibilityBlockers) != 0 {
		t.Fatal("rounded threshold was asserted as a match/blocker")
	}
	set(&input.Job, "required_skills", sequence(textValue("Missing")))
	second, err := Evaluate(context.Background(), input)
	if err != nil || second.BiggestGap != second.RequirementGaps[0].Message || second.Lane != "REVIEW_RECOMMENDED" {
		t.Fatal("mixed gap precedence changed")
	}
	set(&input.Job, "experience_requirements", sequence(object(entry("minimum_years", number("10.0")), entry("area", textValue("Python")))))
	set(&input.Job, "digest", object(entry("provenance", object(entry("experience_requirements", sequence(object(entry("text", textValue("Requires ten years")), entry("nested", sequence(textValue("original"))))))))))
	before := fmt.Sprintf("%#v", input)
	third, err := Evaluate(context.Background(), input)
	if err != nil || len(third.EligibilityBlockers) != 1 {
		t.Fatal("complete source-backed shortfall missing")
	}
	third.ExperienceReview[0].SourceEvidence[0].(map[string]any)["nested"].([]any)[0] = "changed output"
	if before != fmt.Sprintf("%#v", input) {
		t.Fatal("output provenance aliases caller input")
	}
	input.Index["experience:exp"] = jobevidence.Source{Text: "java", Kind: "direct", EvidenceIDs: []string{"exp"}}
	fourth, err := Evaluate(context.Background(), input)
	if err != nil || len(fourth.EligibilityBlockers) != 0 || fourth.ExperienceReview[0].Status != "manual_review" {
		t.Fatal("memo escaped the current snapshot")
	}
	if !reflect.DeepEqual(first.RelevantEvidenceIDs, []string{"exp"}) {
		t.Fatal("later evaluations mutated an earlier result")
	}
}

func TestResultJSONPresenceAndPrecision(t *testing.T) {
	input := base(t)
	set(&input.Job, "experience_requirements", sequence(object(entry("minimum_years", cataloginput.Value{Kind: "int", Text: "1"}), entry("maximum_years", cataloginput.Value{Kind: "null"}), entry("area", textValue("Python")))))
	result, err := Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	raw, failure := json.Marshal(result)
	if failure != nil || !strings.Contains(string(raw), `"minimum_years":1,`) || !strings.Contains(string(raw), `"maximum_years":null`) || !strings.Contains(string(raw), `"derived_years":2.0`) || !strings.Contains(string(raw), `"eligibility_blockers":[]`) {
		t.Fatalf("number/presence/empty-list contract differs: %s", raw)
	}
}

func TestNoRequirementSectionsSignalProducesVisibleAdvisory(t *testing.T) {
	input := base(t)
	set(&input.Job, "required_skills", sequence())
	clean, err := Evaluate(context.Background(), input)
	if err != nil || clean.Lane != "PRIORITY_MANUAL" || clean.BiggestGap != "No material evidence gap found by deterministic review." || len(clean.RequirementGaps) != 0 {
		t.Fatalf("absent signal changed the clean verdict: %#v %v", clean, err)
	}
	set(&input.Job, "digest", object(entry("analysis_coverage", object(entry("no_requirement_sections", cataloginput.Value{Kind: "bool", Boolean: false})))))
	if again, err := Evaluate(context.Background(), input); err != nil || !reflect.DeepEqual(again, clean) {
		t.Fatalf("explicit false signal changed the clean verdict: %v", err)
	}
	set(&input.Job, "digest", object(entry("analysis_coverage", object(entry("no_requirement_sections", cataloginput.Value{Kind: "bool", Boolean: true})))))
	first, err := Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !first.ApplicationAllowed || first.Lane != "REVIEW_RECOMMENDED" || len(first.RequirementGaps) != 1 || len(first.EligibilityBlockers) != 0 || len(first.PolicyReviews) != 0 {
		t.Fatalf("missing requirement basis did not surface as an advisory: %#v", first)
	}
	gap := first.RequirementGaps[0]
	if gap.Code != NoRequirementSectionsCode || gap.Status != NoRequirementSectionsStatus || gap.Severity != "medium" || gap.Category != "coverage" ||
		gap.Source != "digest.analysis_coverage.no_requirement_sections" || !strings.Contains(gap.Message, "No requirement sections were recognized") {
		t.Fatalf("advisory finding shape: %#v", gap)
	}
	if first.BiggestGap != gap.Message || strings.Contains(first.BiggestGap, "No material evidence gap") {
		t.Fatalf("clean verdict text survived: %q", first.BiggestGap)
	}
	second, err := Evaluate(context.Background(), input)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("advisory finding is not deterministic")
	}
	// A real evidence gap keeps precedence; the advisory stays visible after it.
	set(&input.Job, "required_skills", sequence(textValue("Missing")))
	mixed, err := Evaluate(context.Background(), input)
	if err != nil || len(mixed.RequirementGaps) != 2 || mixed.RequirementGaps[0].Code != "required_evidence_not_found" || mixed.RequirementGaps[1].Code != NoRequirementSectionsCode || mixed.BiggestGap != mixed.RequirementGaps[0].Message {
		t.Fatalf("gap precedence with the coverage advisory: %#v %v", mixed, err)
	}
	set(&input.Job, "digest", object(entry("analysis_coverage", object(entry("no_requirement_sections", textValue("true"))))))
	expectFailure(t, context.Background(), input, "EVALUATION_JOB_SHAPE")
}
