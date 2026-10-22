package resumeplan

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestLiteralSuggestionsStayExact(t *testing.T) {
	for _, c := range []struct {
		claim, term string
		match       bool
	}{
		{"İ ı", "i", true}, {"ſ", "s", true}, {"K", "k", true}, {"Σςσ", "σσσ", true},
		{"ß", "ss", false}, {"ﬀ", "ff", false}, {"Pythonic", "Python", false},
		{"Pythonβ", "Python", true}, {"Pythonſ", "Python", false},
		{"C++ and .NET", "C++", true}, {"[a.*b]", "a.*b", true}, {"aXXXXb", "a.*b", false},
	} {
		if literalMatch(literalFold(c.claim), literalFold(c.term)) != c.match {
			t.Fatal("literal matching changed", c)
		}
	}
}
func TestPlanBoundsAndPrivateErrors(t *testing.T) {
	for name, raw := range map[string][]byte{
		"bytes":         []byte(strings.Repeat("x", MaxPlanBytes+1)),
		"depth":         []byte(`{"x":` + strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65) + "}"),
		"unicode":       []byte("{\"x\":\"\xff\"}"),
		"surrogate":     []byte(`{"x":"\ud800","x":"ignored"}`),
		"low-surrogate": []byte(`{"x":"\udc00"}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := Parse(raw); e == nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
	for _, v := range []json.Number{"3e1000000000", "3e-1000000000"} {
		if n, ok := number(v, false); ok && n != 0 {
			t.Fatal("extreme exponent accepted")
		}
	}
	e := &Error{Stage: "test", Code: "PRIVATE", SourceClass: "private source"}
	for _, format := range []string{"%s", "%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, e), "private source") {
			t.Fatal("private error leaked")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Validate(ctx, Plan{}, Catalog{}, ""); e == nil {
		t.Fatal("cancellation ignored")
	}
	if _, e := ReadCatalog(ctx, nil, ""); e == nil {
		t.Fatal("catalog cancellation ignored")
	}
	if _, e := JSON(strings.Repeat("x", MaxResponseBytes)); e == nil {
		t.Fatal("response budget ignored")
	}
}
func TestPlanEncodingEscapesOnlyRequiredCharacters(t *testing.T) {
	p := Default("job", strings.Repeat("1", 64), Catalog{VaultRevision: "sha256:" + strings.Repeat("a", 64)})
	p.Experiences = []Experience{{BlockID: "one", Decision: "exclude", ProofIDs: []string{}, Reason: "<>&\u2028\u2029\b\t\n\f\r\x00\\u2028😀"}}
	raw, e := PlanJSON(p)
	if e != nil || !json.Valid(raw) {
		t.Fatal("encoding failed", e)
	}
	if !strings.Contains(string(raw), "<>&\u2028\u2029") || !strings.Contains(string(raw), `\\u2028`) {
		t.Fatal("non-control text changed")
	}
	parsed, e := Parse(raw)
	if e != nil || parsed.Experiences[0].Reason != p.Experiences[0].Reason {
		t.Fatal("encoding roundtrip failed", e)
	}
}

func TestDefensiveValidationAndIssueBudget(t *testing.T) {
	c := Catalog{VaultRevision: "sha256:" + strings.Repeat("a", 64), Experiences: []ExperienceSource{{ID: "exp", Company: "Company", Role: "Role", Proofs: []Proof{{ID: "proof", Claim: "Fact"}}}}, Projects: []ProjectSource{{ID: "project", Name: "Project", Proofs: []Proof{{ID: "pp", Claim: "Fact"}}}}}
	p := Default("job", strings.Repeat("1", 64), c)
	p.Experiences = []Experience{{BlockID: "exp", Decision: "exclude", ProofIDs: []string{"proof"}}, {BlockID: "other", Decision: "include", ProofIDs: []string{}}}
	// Bypass normalization only in this defensive unit check. External editor
	// requests always pass Parse and cannot retain this excluded content.
	p.Projects = []Project{{BlockID: "project", ProofIDs: []string{"pp"}}}
	v, e := Validate(context.Background(), p, c, p.JobFingerprint)
	if e != nil {
		t.Fatal(e)
	}
	if !containsIssue(v, "PLAN_EXCLUSION_HAS_CONTENT") || !containsIssue(v, "PLAN_PROJECT_SECTION_DISABLED") {
		t.Fatal("defensive guards missing")
	}
	p.Experiences = []Experience{{BlockID: "exp", Decision: "include", ProofIDs: []string{}}}
	v, e = Validate(context.Background(), p, c, p.JobFingerprint)
	if e != nil || !containsIssue(v, "PLAN_BULLET_TARGET_REQUIRED") {
		t.Fatal("missing target guard")
	}
	p.Experiences = make([]Experience, 1001)
	for i := range p.Experiences {
		p.Experiences[i] = Experience{BlockID: "missing", Decision: "exclude"}
	}
	if _, e = Validate(context.Background(), p, c, p.JobFingerprint); e == nil || e.Code != "RESUME_PLAN_ISSUE_LIMIT" {
		t.Fatal("partial issue list admitted")
	}
}
func containsIssue(v *Validation, code string) bool {
	for _, i := range v.Issues {
		if i.Code == code {
			return true
		}
	}
	return false
}
