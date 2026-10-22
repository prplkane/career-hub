package jobcard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
)

func editableFixture(t *testing.T) *Loaded {
	t.Helper()
	card := cardFixture(t)
	digest, e := cataloginput.ParseYAML([]byte(`schema_version: 1
fields:
  company: {value: Synthetic, state: derived, evidence: []}
  title: {value: Engineer, state: derived, evidence: []}
  location: {value: '', state: not_stated, evidence: []}
  remote_policy: {value: '', state: not_stated, evidence: []}
  seniority: {value: '', state: not_stated, evidence: []}
unresolved_fields: [location, company, title]
eligibility_gates: {}
classified_requirements: []
mentioned_technologies: []
`))
	if e != nil {
		t.Fatal(e)
	}
	put(&card, "digest", digest)
	put(&card, "manual_overrides", cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{stringValue("location")}})
	report, err := Validate(context.Background(), card, syntheticCheck)
	if err != nil || !report.SchemaValid() {
		t.Fatalf("fixture invalid: %#v %#v", report, err)
	}
	return &Loaded{Card: card, Report: report}
}

func TestEditDetailsKeepsSourceAndManualDigestOverrides(t *testing.T) {
	loaded := editableFixture(t)
	before := loaded.Card
	beforeJSON, _ := json.Marshal(loaded.Card)
	updates := map[string]any{
		"company": "  New Company  ", "title": "  Senior Engineer  ",
		"required_skills":        []any{" Go ", "Go", "Python"},
		"mentioned_technologies": []any{" Go ", "Go"},
		"eligibility_gates":      map[string]any{"citizenship": map[string]any{"status": "not_stated"}},
		"source_url":             "https://EXAMPLE.test/jobs/1/?src=alert#fragment",
	}
	card, err := EditDetails(context.Background(), loaded, updates)
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, _ := json.Marshal(loaded.Card)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("editing mutated retained input")
	}
	if got := get(card, "company").Text; got != "New Company" {
		t.Fatalf("company=%q", got)
	}
	if got := get(card, "posting_identity").Text; got != "url:https://example.test/jobs/1?src=alert" {
		t.Fatalf("posting identity=%q", got)
	}
	if skills := get(card, "required_skills").Items; len(skills) != 2 || skills[0].Text != "Go" || skills[1].Text != "Python" {
		t.Fatalf("skills=%#v", skills)
	}
	if get(card, "mentioned_technologies").Kind != "null" {
		t.Fatal("digest-only field was copied to card root")
	}
	info := get(card, "digest")
	if values := get(info, "mentioned_technologies").Items; len(values) != 2 || values[0].Text != " Go " || values[1].Text != "Go" {
		t.Fatalf("digest technologies=%#v", values)
	}
	if get(card, "eligibility_gates").Kind != "null" || get(get(info, "eligibility_gates"), "citizenship").Kind != "mapping" {
		t.Fatal("eligibility edit must live in digest only")
	}
	fields := get(info, "fields")
	for _, name := range []string{"company", "title"} {
		field := get(fields, name)
		if get(field, "state").Text != "provided" || len(get(field, "evidence").Items) != 0 {
			t.Fatalf("manual field %s was not marked provided", name)
		}
	}
	if unresolved := get(info, "unresolved_fields").Items; len(unresolved) != 1 || unresolved[0].Text != "location" {
		t.Fatalf("unresolved=%#v", unresolved)
	}
	if overrides := get(card, "manual_overrides").Items; len(overrides) != 7 || overrides[0].Text != "company" || overrides[len(overrides)-1].Text != "title" {
		t.Fatalf("overrides=%#v", overrides)
	}
	if get(card, "raw_jd_path").Text != get(before, "raw_jd_path").Text || get(card, "status").Text != "ready" {
		t.Fatal("edit changed source or lifecycle")
	}
	// The pure transform does not publish; the owned writer must run full
	// validation again before revision-checked persistence.
	report, failure := Validate(context.Background(), card, syntheticCheck)
	if failure != nil || !report.SchemaValid() {
		t.Fatalf("edited card invalid: %#v %#v", report, failure)
	}
}

func TestEditDetailsRejectsUnsupportedAndBoundedInputs(t *testing.T) {
	loaded := editableFixture(t)
	for _, updates := range []map[string]any{
		{"status": "ready"}, {"company": 1},
		{"required_skills": []any{"Go", " "}},
		{"required_skills": "Go"},
		{"experience_requirements": make(chan int)},
		{"company": strings.Repeat("x", 16385)},
	} {
		card, err := EditDetails(context.Background(), loaded, updates)
		if err == nil || err.Code != "JOB_DETAILS_INVALID" || card.Kind != "" {
			t.Fatalf("invalid input accepted: %#v %#v", card, err)
		}
	}
	bad := *loaded
	bad.Report = &Report{Issues: []Issue{{Field: "status", Message: "invalid"}}}
	if _, err := EditDetails(context.Background(), &bad, map[string]any{"title": "X"}); err == nil {
		t.Fatal("invalid source card accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := EditDetails(ctx, loaded, map[string]any{}); err == nil || err.Code != "JOB_CARD_CANCELED" {
		t.Fatal("cancellation ignored")
	}
	if _, err := EditDetails(context.Background(), loaded, map[string]any{"experience_requirements": []any{map[string]any{"minimum_years": json.Number("3"), "area": "Go"}}}); err != nil {
		t.Fatalf("supported JSON numbers refused: %#v", err)
	}
}

func TestDetailPostingIdentity(t *testing.T) {
	for _, tc := range []struct{ source, posting, want string }{
		{"https://EXAMPLE.test/jobs/4/?x=1#detail", "", "url:https://example.test/jobs/4?x=1"},
		{"not-a-url", "", ""},
		{"https://example.test/a", "  ABC  17 ", "posting_id:abc 17"},
	} {
		if got := detailPostingIdentity(tc.source, tc.posting); got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
	}
}
