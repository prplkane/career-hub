package materials

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/intake/resumeplan"
	"careerhub.local/backend/internal/skills/cataloginput"
)

func sampleCover() CoverDraft {
	return CoverDraft{SchemaVersion: 1, ID: "cover_letter_primary", Kind: "cover_letter", TemplateID: CoverTemplate, JobID: "sample", JobFingerprint: strings.Repeat("a", 64), ProfileRevision: "sha256:" + strings.Repeat("b", 64), EvidenceRevision: "sha256:" + strings.Repeat("c", 64), Status: "draft", Company: "Example", RoleTitle: "Engineer", Candidate: Candidate{Name: "Example Person", Email: "example@example.test"}, Content: CoverContent{Date: "2026-09-10", Salutation: "Dear Hiring Team:", Opening: "A source-backed application.", EvidenceLeadIn: "I offer:", EvidenceItems: []CoverEvidence{}, Closing: "Thank you.", SignOff: "Sincerely,", SignatureName: "Example Person"}}
}

func TestMaterialInputBoundsAndPrivacy(t *testing.T) {
	inputs := [][]byte{nil, []byte("null"), []byte("[]"), []byte(`{"x":"\ud800","x":"valid"}`), []byte(`{"x":"\udc00"}`), []byte(`{"x":"\ud800\u0041"}`), []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}, []byte(`{"x":` + strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65) + "}"), bytes.Repeat([]byte(" "), MaxInputBytes+1)}
	for i, input := range inputs {
		if _, e := decode(input); e == nil {
			t.Fatalf("input %d accepted", i)
		}
	}
	object, e := decode([]byte(`{"x":"\ud83d\ude00","x":"last"}`))
	if e != nil || object["x"] != "last" {
		t.Fatal("valid duplicate-key rule")
	}
	secret := "PRIVATE-SYNTHETIC-TEXT"
	d := sampleCover()
	d.Content.Opening = secret
	for _, value := range []any{d, ResumeDraft{Summary: secret}, Request{Payload: []byte(secret)}, Proposal{Bytes: []byte(secret)}, failure("input", "MATERIAL_STRUCTURE_INVALID")} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, value), secret) {
				t.Fatal("private draft reached diagnostic formatting")
			}
		}
	}
}

func TestMaterialRevisionNumericContract(t *testing.T) {
	for _, c := range []struct {
		input any
		want  int64
		valid bool
	}{
		{json.Number("12"), 12, true}, {"12.0", 12, true}, {json.Number("1.2e1"), 12, true}, {true, 1, true}, {false, 0, true},
		{json.Number("9007199254740993"), 9007199254740993, true}, {"9007199254740993.0", 9007199254740993, true},
		{json.Number("9007199254740993.0"), 0, false}, {json.Number("1e1000000"), 0, false}, {"1e2", 0, false}, {json.Number("0.5"), 0, false}, {json.Number("9223372036854775808"), 0, false},
	} {
		m := model{}
		got := m.integer(c.input, 0, math.MaxInt64, true)
		if (m.err == nil) != c.valid || c.valid && got != c.want {
			t.Fatalf("numeric contract %T/%v", c.input, c.input)
		}
	}
	m := model{}
	m.integer(true, 1, 1, false)
	if m.err == nil {
		t.Fatal("boolean literal version")
	}
}

func TestMaterialQuotedStoreRoundTripAndCancellation(t *testing.T) {
	d := sampleCover()
	d.Content.Opening = "yes no on off null 0123 2026-01-01 <text> & \\u2028\n\u2028\u2029😀"
	input, _ := JSON(d)
	normalized, parseError := ParseCover(input)
	if parseError != nil {
		t.Fatal(parseError.Code)
	}
	// Prepare supplies normalized models. Keep quote-sensitive non-normalized
	// metadata in the store's plain updated_at string to test the YAML boundary.
	store := CoverStore{1, d.JobID, 1, "yes no on off null 0123\n\u2028\u2029😀", []CoverDraft{*normalized}}
	raw, e := StoreYAML(context.Background(), store)
	if e != nil {
		t.Fatal(e.Code)
	}
	loaded, e := storedCover(raw, d.JobID)
	if e != nil || !reflect.DeepEqual(*loaded, store) {
		t.Fatal("typed YAML changed")
	}
	if _, e := StoreYAML(context.Background(), map[string]any{"revision": 1.5}); e == nil {
		t.Fatal("arbitrary YAML schema accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := StoreYAML(ctx, store); e == nil || e.Code != "MATERIAL_CANCELED" {
		t.Fatal("canceled encoding")
	}
	if _, e := StoreYAML(context.Background(), CoverStore{Materials: []CoverDraft{{Content: CoverContent{Opening: strings.Repeat("x", MaxInputBytes+1)}}}}); e == nil {
		t.Fatal("unbounded store")
	}
	if _, e := storedCover([]byte("schema_version: 1\njob_id: sample\nrevision: 1\nupdated_at: 2026-01-01\nmaterials: []\n"), "sample"); e == nil {
		t.Fatal("YAML timestamp silently converted to model string")
	}
}

func TestMaterialLinkAndCalendarBoundaries(t *testing.T) {
	for _, link := range []string{"file:///private/file", "ftp://example.test/file", "https://user:password@example.test/", "https://example.test:999999/", "https://example.test/\\file"} {
		if _, e := ContactLinks(Candidate{Website: link}); e == nil {
			t.Fatal("unsafe renderer link accepted")
		}
	}
	for _, date := range []string{"0000-01-01", "2026-02-30", "２０２６-０９-１０"} {
		d := sampleCover()
		d.Content.Date = date
		if _, e := CoverPlainText(d); e == nil || e.Code != "MATERIAL_LETTER_DATE_INVALID" {
			t.Fatal("invalid calendar preview")
		}
	}
	d := sampleCover()
	d.Content.Date = "2024-02-29"
	if _, e := CoverPlainText(d); e != nil {
		t.Fatal("valid leap day")
	}
}

func TestMaterialRequestAndMissingSourcesWithoutIO(t *testing.T) {
	for _, r := range []Request{{Kind: "other", Operation: "load", JobID: "x"}, {Kind: "resume", Operation: "load", JobID: "../x"}, {Kind: "resume", Operation: "load", JobID: "x", Approve: true}, {Kind: "cover_letter", Operation: "save", JobID: "x", ExpectedRevision: -1}, {Kind: "resume", Operation: "load", JobID: "x", Payload: []byte("null")}} {
		if ValidateRequest(r) == nil {
			t.Fatal("invalid request")
		}
	}
	r := Request{Kind: "resume", Operation: "load", JobID: "sample"}
	if _, e := Prepare(context.Background(), nil, nil, r, "2026-09-10T12:00:00Z", "2026-09-10"); e == nil || e.Code != "MATERIAL_SOURCE_REQUIRED" {
		t.Fatal("missing source configuration")
	}
	if _, e := ReadSources(context.Background(), nil, nil, "sample", true, ""); e == nil {
		t.Fatal("nil SQL/source")
	}
	if _, e := ValidateCover(nil, sampleCover(), CoverRevisions{}, nil); e == nil {
		t.Fatal("nil validation context")
	}
	if _, e := DefaultResume(Sources{}); e == nil {
		t.Fatal("missing Resume Plan")
	}
}

func TestMaterialProfileDefaultsAndPlanFingerprint(t *testing.T) {
	profile, e := cataloginput.ParseYAML([]byte("name: Example Person\ncurrent_location: Phoenix\nlocation_flexibility: Willing to relocate\ncontact:\n  email: example@example.test\n"))
	if e != nil {
		t.Fatal(e)
	}
	education, e := cataloginput.ParseYAML([]byte("schools:\n- id: old\n  archived: true\n  institution: Ignored\n- id: school\n  institution: Example College, Example State University\n  degree: Bachelor of Science\n  major: Computing\n  graduation_date: 'May 2024'\n  gpa: 3.8\n  honors: [Honors]\ncertifications: []\n"))
	if e != nil {
		t.Fatal(e)
	}
	candidate, credential, editor, me := profileSources(profile, education)
	if me != nil {
		t.Fatal(me.Code)
	}
	if candidate.Location != "Phoenix | Open to Relocation" || credential != "Example College, Example State University, B.S. Computing, Class of 2024" {
		t.Fatal("profile defaults")
	}
	if editor["schools"].([]any)[0].(map[string]any)["gpa"] != "3.8" {
		t.Fatal("numeric GPA")
	}
	p := resumeplan.Default("sample", strings.Repeat("a", 64), resumeplan.Catalog{VaultRevision: "sha256:" + strings.Repeat("b", 64)})
	a, me := PlanFingerprint(p)
	if me != nil {
		t.Fatal(me.Code)
	}
	p.TargetPages = 2
	b, me := PlanFingerprint(p)
	if me != nil || a == b {
		t.Fatal("plan fingerprint does not cover page selection")
	}
}
