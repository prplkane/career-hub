package jobdigest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"careerhub.local/backend/internal/fixedregex"
	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

func fixtureRegistry(t *testing.T) *catalog.Snapshot {
	t.Helper()
	core, err := cataloginput.DecodeTerms([]byte(`[{id: python, name: Python, category: programming_language, aliases: [Py]}, {id: java, name: Java, category: programming_language}]`), false)
	if err != nil {
		t.Fatal(err)
	}
	custom, err := cataloginput.DecodeTerms([]byte(`[{id: local_tool, name: LocalTool, category: custom_skill, primary_group_id: custom_tools}, {id: retired, name: RetiredTool, category: custom_skill, archived: true}]`), false)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, failure := catalog.Build(nil, []catalog.Group{{ID: "custom_tools", Name: "My tools"}}, core, custom, nil, nil)
	if failure != nil {
		t.Fatal(failure)
	}
	return snapshot
}
func fixtureInput(t *testing.T) Input {
	t.Helper()
	no := false
	return Input{Registry: fixtureRegistry(t), Profile: EligibilityProfile{SponsorshipNow: OptionalBool{Present: true, Value: &no}},
		Source: "Company: Example Labs\nRole: Data Engineer\n  Salary: $100k–$125k per year  \nResponsibilities\nBuild reliable APIs and services.\nRequirements\n  🚀 â€“ Python and LocalTool.  \n3-5 years of Python development.\nBachelor degree required.\nAt least one of Python or Java.\nNo visa sponsorship.\nPreferred\nLocalTool expertise is valuable.\nUnfamiliar requirement remains visible.\nHow to apply\nEmail your resume to careers@example.com.\nContact Jane Doe, Recruiter at Example Labs: jane@example.com.\n"}
}
func TestCompleteFamiliesAndVerifiedOriginalReferences(t *testing.T) {
	input := fixtureInput(t)
	got, err := Analyze(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Occurrences) != 6 || len(got.TechnologyGroups) != 1 || len(got.Experience) != 1 || len(got.Education) != 1 || len(got.Responsibilities) != 1 || len(got.Compensation.Ranges) != 1 || len(got.Instructions.Items) == 0 || len(got.Contacts.Candidates) == 0 {
		t.Fatalf("missing full family: %d occurrences, %d groups, %d instructions, %d contacts", len(got.Occurrences), len(got.TechnologyGroups), len(got.Instructions.Items), len(got.Contacts.Candidates))
	}
	lines := textcompat.SplitLines(input.Source)
	for _, item := range got.Occurrences {
		points := []rune(textcompat.Strip(lines[item.Line-1]))
		if string(points[item.Start:item.End]) != item.SourceText {
			t.Fatal("offsets point into repaired text")
		}
		if item.SkillID == "local_tool" && (item.PrimaryGroupID != "custom_tools" || item.Origin != "custom") {
			t.Fatal("custom group/origin lost")
		}
		if item.SkillID == "python" && (item.PrimaryGroupID != "programming_languages" || item.Origin != "core") {
			t.Fatal("core group/origin lost")
		}
	}
	if got.Compensation.Ranges[0].Evidence[0].Text != "  Salary: $100k–$125k per year  " {
		t.Fatal("raw compensation evidence trimmed")
	}
	if err := Verify(context.Background(), input, got); err != nil {
		t.Fatal(err)
	}
}

func TestNoCandidateSkillsOrVocabularyCreated(t *testing.T) {
	input := fixtureInput(t)
	before := input.Registry.Skills()
	input.Source = "Requirements\nPython, RetiredTool and UnregisteredSkill."
	got, err := Analyze(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Occurrences) != 1 || got.Occurrences[0].SkillID != "python" || !reflect.DeepEqual(before, input.Registry.Skills()) {
		t.Fatal("archived/unregistered vocabulary became a skill")
	}
	empty, err := catalog.Build(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	input.Registry = empty
	got, err = Analyze(context.Background(), input)
	if err != nil || len(got.Occurrences) != 0 || len(got.RequiredSkills) != 0 {
		t.Fatal("empty registry inferred skills")
	}
}

func TestInputsAndLimitsDiscardEverything(t *testing.T) {
	base := fixtureInput(t)
	tests := []struct {
		name   string
		change func(*Input)
	}{
		{"nil-registry", func(in *Input) { in.Registry = nil }},
		{"zero-registry", func(in *Input) { in.Registry = &catalog.Snapshot{} }},
		{"source-bytes", func(in *Input) { in.Source = strings.Repeat("a", MaxSourceBytes+1) }},
		{"source-runes", func(in *Input) { in.Source = strings.Repeat("a", MaxSourceRunes+1) }},
		{"source-lines", func(in *Input) { in.Source = strings.Repeat("x\n", MaxLines+1) }},
		{"title-limit", func(in *Input) { in.Title = strings.Repeat("x", 4097) }},
		{"company-limit", func(in *Input) { in.Company = strings.Repeat("x", 4097) }},
		{"url-limit", func(in *Input) { in.SourceURL = strings.Repeat("x", 65537) }},
		{"status-limit", func(in *Input) { in.Profile.CurrentStatus = strings.Repeat("x", 4097) }},
		{"invalid-utf8-source", func(in *Input) { in.Source = string([]byte{255}) }},
		{"invalid-utf8-label", func(in *Input) { in.Title = string([]byte{255}) }},
		{"invalid-utf8-company", func(in *Input) { in.Company = string([]byte{255}) }},
		{"invalid-utf8-url", func(in *Input) { in.SourceURL = string([]byte{255}) }},
		{"invalid-utf8-status", func(in *Input) { in.Profile.CurrentStatus = string([]byte{255}) }},
		{"presence-contradiction", func(in *Input) { in.Profile.SponsorshipNow.Present = false }},
		{"url-parse", func(in *Input) { in.Source = "Role: Engineer"; in.SourceURL = "https://%zz" }},
		{"amount-overflow", func(in *Input) { in.Source = "Salary $" + strings.Repeat("9", 309) + "-$" + strings.Repeat("9", 310) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			test.change(&input)
			got, err := Analyze(context.Background(), input)
			if err == nil || got != nil {
				t.Fatal("partial/invalid result")
			}
			if strings.Contains(fmt.Sprintf("%+v", err), "Example") {
				t.Fatal("error contains private input")
			}
		})
	}
	if got, err := Analyze(nil, base); got != nil || !errors.Is(err, ErrInput) {
		t.Fatal("nil context")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := Analyze(canceled, base); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation")
	}
}

func TestPrivateUseInputAndConcurrentReuse(t *testing.T) {
	input := fixtureInput(t)
	input.Source = "Company: \ue000Example\ue001\nRole: Engineer\nRequirements\n\ue002Python\ue003 and LocalTool."
	want, err := Analyze(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			got, err := Analyze(context.Background(), input)
			if err != nil || !reflect.DeepEqual(want, got) {
				t.Error("concurrent immutable input reuse")
			}
		}()
	}
	workers.Wait()
	want.RequiredSkills[0] = "Mutated output"
	got, err := Analyze(context.Background(), input)
	if err != nil || got.RequiredSkills[0] == "Mutated output" {
		t.Fatal("result aliases retained state")
	}
}

type cancelAfter struct {
	context.Context
	calls, at int
}

func (c *cancelAfter) Err() error {
	c.calls++
	if c.at > 0 && c.calls >= c.at {
		return context.Canceled
	}
	return nil
}
func TestEveryObservedCancellationCheckpoint(t *testing.T) {
	input := Input{Source: "Role: Engineer\nRequirements\nPython", Registry: fixtureRegistry(t)}
	baseline := &cancelAfter{Context: context.Background()}
	result, err := Analyze(baseline, input)
	if err != nil {
		t.Fatal(err)
	}
	total := baseline.calls
	if total < 20 || total > 5000 {
		t.Fatalf("unexpected checkpoint envelope %d", total)
	}
	for at := 1; at <= total; at++ {
		ctx := &cancelAfter{Context: context.Background(), at: at}
		got, err := Analyze(ctx, input)
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("Analyze checkpoint %d/%d: %v", at, total, err)
		}
	}
	replay := &cancelAfter{Context: context.Background()}
	if err := Verify(replay, input, result); err != nil {
		t.Fatal(err)
	}
	for at := 1; at <= replay.calls; at++ {
		ctx := &cancelAfter{Context: context.Background(), at: at}
		if err := Verify(ctx, input, result); !errors.Is(err, context.Canceled) {
			t.Fatalf("Verify checkpoint %d: %v", at, err)
		}
	}
	t.Logf("Analyze %d / Verify %d observed checkpoints (one synthetic scenario)", total, replay.calls)
}

func TestStaleAndModifiedArtifactsFailReplay(t *testing.T) {
	input := fixtureInput(t)
	result, err := Analyze(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(*Result){
		"fingerprint":    func(r *Result) { r.ContentFingerprint = strings.Repeat("0", 64) },
		"quote":          func(r *Result) { r.Occurrences[0].SourceText = "Java" },
		"line":           func(r *Result) { r.Occurrences[0].Line++ },
		"offset":         func(r *Result) { r.Occurrences[0].Start++ },
		"identity":       func(r *Result) { r.Occurrences[0].SkillID = "java" },
		"origin":         func(r *Result) { r.Occurrences[0].Origin = "custom" },
		"group":          func(r *Result) { r.Occurrences[0].PrimaryGroupID = "custom_tools" },
		"alternative":    func(r *Result) { r.TechnologyGroups[0].Technologies[0] = "LocalTool" },
		"compensation":   func(r *Result) { r.Compensation.Ranges[0].Minimum = "1" },
		"evidence":       func(r *Result) { r.Provenance.Experience[0].Text = "Invented evidence" },
		"education":      func(r *Result) { r.Education[0] = "Invented degree" },
		"instruction-id": func(r *Result) { r.Instructions.Items[0].Fingerprint = strings.Repeat("0", 64) },
		"contact-id":     func(r *Result) { r.Contacts.Candidates[0].CandidateID = strings.Repeat("0", 64) },
		"contact-quote":  func(r *Result) { r.Contacts.Candidates[0].SourceText = "Contact somebody else" },
		"coverage":       func(r *Result) { r.Coverage.ContextLines++ },
		"eligibility": func(r *Result) {
			gate := r.Gates["sponsorship"]
			gate.Status = "incompatible"
			r.Gates["sponsorship"] = gate
		},
		"missing-family": func(r *Result) { r.RequiredPlatforms = nil },
		"unknown-field":  func(r *Result) { r.Fields["invented"] = Field{} },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			var changed Result
			if err := json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			change(&changed)
			if err := Verify(context.Background(), input, &changed); !errors.Is(err, ErrSourceMismatch) {
				t.Fatalf("modified artifact accepted: %v", err)
			}
		})
	}
	shifted := input
	shifted.Source = " " + input.Source
	if err := Verify(context.Background(), shifted, result); !errors.Is(err, ErrSourceMismatch) {
		t.Fatal("normalized fingerprint hid exact source drift")
	}
	changedProfile := input
	yes := true
	changedProfile.Profile.SponsorshipNow.Value = &yes
	if err := Verify(context.Background(), changedProfile, result); !errors.Is(err, ErrSourceMismatch) {
		t.Fatal("profile drift accepted")
	}
	if err := Verify(context.Background(), input, nil); !errors.Is(err, ErrInput) {
		t.Fatal("nil candidate")
	}
	if err := Verify(nil, input, result); !errors.Is(err, ErrInput) {
		t.Fatal("nil replay context")
	}
}

func TestAggregateBudgetsAndAmountBoundaries(t *testing.T) {
	engine, err := rules.ForSource("x")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"work", "cache", "items"} {
		a := &analysis{ctx: context.Background(), engine: engine, cache: map[string]fixedregex.Text{}}
		switch mode {
		case "work":
			a.work = 128 << 20
			a.encoded("x")
		case "cache":
			a.cacheBytes = 8 << 20
			a.encoded("x")
		case "items":
			a.items = MaxItems
			a.item()
		}
		if !errors.Is(a.err, ErrLimit) {
			t.Fatal("aggregate budget " + mode)
		}
	}
	if _, err := amount(strings.Repeat("1", 1025)); !errors.Is(err, ErrLimit) {
		t.Fatal("amount length")
	}
	for _, invalid := range []string{"NaN", "+Inf", "1e999", "not-a-number"} {
		if _, err := amount(invalid); !errors.Is(err, ErrInput) {
			t.Fatal("invalid amount")
		}
	}
	if got, err := amount("€١٢.٥k"); err != nil || got != "12500" {
		t.Fatal("unicode decimal amount")
	}
	large := &Result{RequiredSkills: []string{strings.Repeat("x", MaxOutputBytes+1)}}
	if err := outputBound(context.Background(), reflect.ValueOf(large)); !errors.Is(err, ErrLimit) {
		t.Fatal("aggregate string bytes")
	}
	large = &Result{Occurrences: make([]Occurrence, 10001)}
	if err := outputBound(context.Background(), reflect.ValueOf(large)); !errors.Is(err, ErrLimit) {
		t.Fatal("aggregate output nodes")
	}
}

func TestLongOriginalEvidenceAndProducerPostconditions(t *testing.T) {
	input := fixtureInput(t)
	input.Source += "\nHow to apply\nEmail your resume to hiring@example.com. " + strings.Repeat("é", 1300) + "\nContact Jill Doe, Recruiter at Example Labs: jill@example.com. " + strings.Repeat("界", 1300)
	result, err := Analyze(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	truncated := false
	for _, item := range result.Instructions.Items {
		truncated = truncated || item.EvidenceTruncated
	}
	if !truncated {
		t.Fatal("long evidence was not explicitly bounded")
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(*Result){
		"field-evidence": func(r *Result) {
			field := r.Fields["company"]
			field.Evidence[0].Text = "fabricated"
			r.Fields["company"] = field
		},
		"technology-evidence":   func(r *Result) { r.Provenance.Technologies[0].Evidence[0].Line = 0 },
		"alternative-evidence":  func(r *Result) { r.TechnologyGroups[0].Evidence[0].Text = "fabricated" },
		"experience-evidence":   func(r *Result) { r.Provenance.Experience[0].Rule = "" },
		"compensation-evidence": func(r *Result) { r.Compensation.Ranges[0].Evidence[0].Text = "fabricated" },
		"gate-evidence": func(r *Result) {
			gate := r.Gates["sponsorship"]
			gate.Evidence[0].Text = "fabricated"
			r.Gates["sponsorship"] = gate
		},
		"coverage-evidence": func(r *Result) { r.Coverage.ReviewItems[0].Line = 0 },
		"occurrence-line":   func(r *Result) { r.Occurrences[0].Line = 0 },
		"occurrence-span":   func(r *Result) { r.Occurrences[0].End++ },
		"occurrence-id":     func(r *Result) { r.Occurrences[0].SkillID = "retired" },
		"fingerprint":       func(r *Result) { r.ContentFingerprint = "fake" },
		"instruction-quote": func(r *Result) { r.Instructions.Items[0].SourceText = "fake" },
		"contact-quote":     func(r *Result) { r.Contacts.Candidates[0].SourceText = "fake" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			var candidate Result
			if err := json.Unmarshal(raw, &candidate); err != nil {
				t.Fatal(err)
			}
			change(&candidate)
			engine, err := rules.ForSource(input.Source)
			if err != nil {
				t.Fatal(err)
			}
			a := &analysis{ctx: context.Background(), input: input, engine: engine, cache: map[string]fixedregex.Text{}, result: &candidate}
			for i, text := range textcompat.SplitLines(input.Source) {
				stripped := textcompat.Strip(text)
				if stripped != "" {
					a.lines = append(a.lines, line{number: i + 1, raw: text, text: stripped, clean: a.clean(stripped)})
				}
			}
			if err := a.verifyReferences(); !errors.Is(err, ErrSourceMismatch) {
				t.Fatalf("producer postcondition accepted %s: %v", name, err)
			}
		})
	}
}

func TestUnrecognizedHeadingsSetNoRequirementSectionsSignal(t *testing.T) {
	input := fixtureInput(t)
	input.Source = "Company: Example Labs\nRole: Data Engineer\nWho you are:\nYou have shipped Python services in production.\nMust haves:\n3+ years of Python development.\nBachelor degree required.\nWhat we're looking for:\nLocalTool expertise is valuable.\n"
	got, err := Analyze(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Coverage.NoRequirementSections {
		t.Fatal("unrecognized headings did not set no_requirement_sections")
	}
	if len(got.RequiredSkills) != 0 || len(got.PreferredSkills) != 0 || len(got.Experience) != 0 || len(got.Education) != 0 || got.Coverage.RelevantLines != 0 {
		t.Fatal("requirement families were populated without a recognized section")
	}
	if len(got.Coverage.UnrecognizedHeadings) != 3 {
		t.Fatalf("unrecognized heading evidence: %#v", got.Coverage.UnrecognizedHeadings)
	}
	for i, want := range []Evidence{{3, "Who you are:", "unrecognized_heading"}, {5, "Must haves:", "unrecognized_heading"}, {8, "What we're looking for:", "unrecognized_heading"}} {
		if got.Coverage.UnrecognizedHeadings[i] != want {
			t.Fatalf("heading evidence %d: got %#v want %#v", i, got.Coverage.UnrecognizedHeadings[i], want)
		}
	}
	if err := Verify(context.Background(), input, got); err != nil {
		t.Fatal(err)
	}
	if err := CheckReferences(context.Background(), input, got); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Result){
		"flag":         func(r *Result) { r.Coverage.NoRequirementSections = false },
		"heading-text": func(r *Result) { r.Coverage.UnrecognizedHeadings[0].Text = "Requirements:" },
		"heading-line": func(r *Result) { r.Coverage.UnrecognizedHeadings[0].Line = 4 },
		"heading-rule": func(r *Result) { r.Coverage.UnrecognizedHeadings[0].Rule = "" },
		"heading-added": func(r *Result) {
			r.Coverage.UnrecognizedHeadings = append(r.Coverage.UnrecognizedHeadings, Evidence{4, "Invented", "unrecognized_heading"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			var changed Result
			if err := json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			change(&changed)
			if err := Verify(context.Background(), input, &changed); !errors.Is(err, ErrSourceMismatch) {
				t.Fatalf("modified coverage signal accepted: %v", err)
			}
		})
	}
	recognized := fixtureInput(t)
	got, err = Analyze(context.Background(), recognized)
	if err != nil || got.Coverage.NoRequirementSections || len(got.Coverage.UnrecognizedHeadings) != 0 {
		t.Fatalf("recognized sections reported the missing-section signal: %v %#v", err, got.Coverage)
	}
	// A preferred-only or technology-stack heading is still a requirement basis.
	recognized.Source = "Role: Engineer\nNice to have:\nPython\nTech stack:\nJava"
	got, err = Analyze(context.Background(), recognized)
	if err != nil {
		t.Fatal(err)
	}
	if got.Coverage.NoRequirementSections {
		t.Fatal("preferred or stack headings were not counted as a requirement section")
	}
}
