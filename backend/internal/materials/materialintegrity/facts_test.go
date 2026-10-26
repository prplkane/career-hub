package materialintegrity

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"careerhub.local/backend/internal/materials"
)

func factFixture(t *testing.T) (*FactPolicy, Sources, []FactProof) {
	t.Helper()
	p, e := NewFactPolicy(context.Background(), []NamedTerm{
		{"technology", "Python", "python"}, {"technology", "Java", "java"}, {"technology", "SQL", "sql"}, {"technology", "C++", "cpp"},
		{"employer", "Acme", ""}, {"employer", "OtherCo", ""}, {"title", "Software Engineer", ""}, {"title", "Staff Engineer", ""},
	})
	if e != nil {
		t.Fatal(e)
	}
	s := fixtureIdentity().Sources
	s.PolicySHA256, s.ProofIDs = p.SHA256(), []string{"proof_a"}
	return p, s, []FactProof{{"proof_a", "At Acme, a Software Engineer used Python to reduce latency 90% in January 2024. Built C++ services with SQL."}, {"proof_b", "At OtherCo, a Staff Engineer used Java to reduce latency 99% in February 2025."}}
}

func TestFactGateOwnCitationsAndExactTokenKinds(t *testing.T) {
	p, s, proofs := factFixture(t)
	for _, c := range []struct {
		text    string
		blocked bool
	}{
		{"At Acme as Software Engineer, used Python, SQL and C++ to reduce latency 90% in January 2024.", false},
		{"Used PYTHON to reduce latency 90 %.", false},
		{"Reduced latency 99%.", true},
		{"Reduced latency 9%.", true},
		{"Delivered in February 2025.", true},
		{"Worked at OtherCo.", true},
		{"Worked as Staff Engineer.", true},
		{"Built Java services.", true},
		{"Built unconfigured KlingonDB services.", false}, // explicitly limited configured-name detection
	} {
		t.Run(c.text, func(t *testing.T) {
			result, e := CheckFacts(context.Background(), p, s, []FactClaim{{Path: "summary", Text: c.text, ProofIDs: []string{"proof_a"}}}, proofs, nil)
			if e != nil || (result.Validation.Status == "block") != c.blocked {
				t.Fatalf("result=%+v error=%v", result, e)
			}
			for _, f := range result.Validation.Findings {
				if f.Code != "UNSUPPORTED_FACT_TOKEN" || f.DraftStart < 0 || f.DraftEnd > len(c.text) || f.DraftEnd <= f.DraftStart {
					t.Fatal("invalid token location")
				}
			}
		})
	}
	s.ProofIDs = []string{"proof_a", "proof_b"}
	claims := []FactClaim{{Path: "summary", Text: "Reduced latency 99%.", ProofIDs: []string{"proof_a"}}, {Path: "experiences[0].bullets[0].text", Text: "Built Java services.", ProofIDs: []string{"proof_b"}}}
	result, e := CheckFacts(context.Background(), p, s, claims, proofs, nil)
	if e != nil || len(result.Validation.Findings) != 1 || result.Validation.Findings[0].DraftPath != "summary" {
		t.Fatal("another claim's citation credited")
	}
}

func TestFactsDoNotJoinProofsOrMatchInsideNames(t *testing.T) {
	p, s, _ := factFixture(t)
	s.ProofIDs = []string{"proof_a", "proof_b"}
	for _, sample := range []struct{ text, a, b string }{{"Software Engineer", "Software", "Engineer"}, {"SQL", "Used NoSQL", "Another project"}, {"90%", "Reduced latency 90", "Percent"}} {
		result, e := CheckFacts(context.Background(), p, s, []FactClaim{{Path: "summary", Text: sample.text, ProofIDs: s.ProofIDs}}, []FactProof{{"proof_a", sample.a}, {"proof_b", sample.b}}, nil)
		if e != nil || result.Validation.Status != "block" {
			t.Fatal("cross-proof/substring support invented")
		}
	}
}

func manualFor(t *testing.T, sources Sources, claim FactClaim, finding Finding) ManualFactCheck {
	t.Helper()
	identity, e := ReviewIdentity(sources)
	if e != nil {
		t.Fatal(e)
	}
	return ManualFactCheck{"check_1", claim.Path, digest([]byte(claim.Text)), finding.TokenSHA256, identity, true}
}

func TestManualFactChecksAreExactAndRevisionBound(t *testing.T) {
	p, s, proofs := factFixture(t)
	claim := FactClaim{Path: "summary", Text: "Built Java services with 99% coverage.", ProofIDs: []string{"proof_a"}}
	before, e := CheckFacts(context.Background(), p, s, []FactClaim{claim}, proofs, nil)
	if e != nil || len(before.Validation.Findings) != 2 {
		t.Fatal("expected two unsupported tokens")
	}
	check := manualFor(t, s, claim, before.Validation.Findings[0])
	result, e := CheckFacts(context.Background(), p, s, []FactClaim{claim}, proofs, []ManualFactCheck{check})
	if e != nil || result.Validation.Status != "block" || result.Validation.Findings[0].Code != "MANUAL_FACT_CHECK" || result.Validation.Findings[1].Code != "UNSUPPORTED_FACT_TOKEN" {
		t.Fatal("one manual check approved other facts")
	}
	if len(result.CitedDemandIDs) != 0 || !slices.Equal(result.ManualCheckIDs, []string{"check_1"}) {
		t.Fatal("manual token promoted into cited vocabulary")
	}
	for _, mutate := range []func(*ManualFactCheck){
		func(c *ManualFactCheck) { c.Confirmed = false },
		func(c *ManualFactCheck) { c.ReviewSHA256 = digest([]byte("old source")) },
		func(c *ManualFactCheck) { c.ClaimSHA256 = digest([]byte("old claim")) },
	} {
		bad := check
		mutate(&bad)
		if out, e := CheckFacts(context.Background(), p, s, []FactClaim{claim}, proofs, []ManualFactCheck{bad}); out != nil || e == nil || e.Code != "MATERIAL_MANUAL_CHECK_STALE" {
			t.Fatal("stale manual check accepted")
		}
	}
	changed := s
	changed.DraftRevision++
	if out, e := CheckFacts(context.Background(), p, changed, []FactClaim{claim}, proofs, []ManualFactCheck{check}); out != nil || e == nil || e.Code != "MATERIAL_MANUAL_CHECK_STALE" {
		t.Fatal("approval survived a draft revision change")
	}
}

func TestPlaceholdersCannotBeApprovedOrCited(t *testing.T) {
	p, s, _ := factFixture(t)
	for _, text := range []string{"[PERSONALIZE: company motivation]", "TODO", "<metric>", "TBD", "[ADD 99%]"} {
		claim := FactClaim{Path: "content.opening", Text: text, ProofIDs: []string{"proof_a"}}
		proofs := []FactProof{{"proof_a", text}}
		result, e := CheckFacts(context.Background(), p, s, []FactClaim{claim}, proofs, nil)
		if e != nil || len(result.Validation.Findings) != 1 || result.Validation.Findings[0].Code != "UNRESOLVED_PLACEHOLDER" {
			t.Fatal("source placeholder not blocked")
		}
		check := manualFor(t, s, claim, result.Validation.Findings[0])
		result, e = CheckFacts(context.Background(), p, s, []FactClaim{claim}, proofs, []ManualFactCheck{check})
		if e != nil || result.Validation.Status != "block" || len(result.ManualCheckIDs) != 0 {
			t.Fatal("manual approval bypassed placeholder")
		}
	}
}

func TestFactVocabularyDistinguishesSourceAndCitations(t *testing.T) {
	p, s, proofs := factFixture(t)
	claims := []FactClaim{{Path: "summary", Text: "Used Python.", ProofIDs: []string{"proof_a"}}, {Path: "skills[0].keywords[0]", Text: "SQL", sourceOnly: true}, {Path: "candidate.website", Text: "https://java.example.test/", sourceOnly: true}}
	result, e := CheckFacts(context.Background(), p, s, claims, proofs, nil)
	if e != nil || !slices.Equal(result.ObservedDemandIDs, []string{"python", "sql"}) || !slices.Equal(result.CitedDemandIDs, []string{"python"}) || !slices.Equal(result.SourceControlledDemandIDs, []string{"sql"}) {
		t.Fatal("coverage mixed evidence types or contact links")
	}
	claims[1].Text = "TODO"
	result, e = CheckFacts(context.Background(), p, s, claims, proofs, nil)
	if e != nil || result.Validation.Status != "block" {
		t.Fatal("source-controlled placeholder accepted")
	}
}

func TestFactPolicyAndInputRefusals(t *testing.T) {
	p, s, proofs := factFixture(t)
	for _, terms := range [][]NamedTerm{
		{{"regex", ".*", ""}}, {{"technology", "", ""}}, {{"employer", "Acme", "tech"}},
		{{"technology", "Python", "a"}, {"technology", "PYTHON", "b"}},
		{{"technology", strings.Repeat("x", 4097), ""}},
	} {
		if _, e := NewFactPolicy(context.Background(), terms); e == nil {
			t.Fatal("invalid fact policy accepted")
		}
	}
	a, e := NewFactPolicy(context.Background(), []NamedTerm{{"technology", "PYTHON", "python"}, {"technology", "Python", "python"}})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := NewFactPolicy(context.Background(), []NamedTerm{{"technology", "python", "python"}})
	if a.SHA256() != b.SHA256() {
		t.Fatal("policy identity depends on duplicate display spelling")
	}
	claim := FactClaim{Path: "summary", Text: "Python 90%", ProofIDs: []string{"proof_a"}}
	for _, mutate := range []func(*FactClaim){
		func(c *FactClaim) { c.Path = "arbitrary.private.prose" },
		func(c *FactClaim) { c.Text = "bad\x00text" },
		func(c *FactClaim) { c.Text = strings.Repeat("x", MaxTextBytes+1) },
		func(c *FactClaim) { c.ProofIDs = []string{"missing"} },
		func(c *FactClaim) { c.ProofIDs = []string{"proof_a", "proof_a"} },
	} {
		bad := claim
		mutate(&bad)
		if out, e := CheckFacts(context.Background(), p, s, []FactClaim{bad}, proofs, nil); out != nil || e == nil {
			t.Fatal("invalid fact input accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, e := CheckFacts(ctx, p, s, []FactClaim{claim}, proofs, nil); out != nil || e == nil {
		t.Fatal("canceled gate accepted")
	}
	for _, value := range []any{claim, proofs[0], NamedTerm{"technology", "private marker", ""}} {
		if strings.Contains(fmt.Sprintf("%+v", value), "private marker") {
			t.Fatal("private text in diagnostic")
		}
	}
}

func TestTypedCoverProjectionDoesNotBorrowGlobalEvidence(t *testing.T) {
	s := fixtureIdentity().Sources
	d := materials.CoverDraft{SchemaVersion: 1, ID: "cover_letter_primary", Kind: "cover_letter", TemplateID: materials.CoverTemplate, JobID: s.JobID, JobFingerprint: s.JobFingerprint, ProfileRevision: s.ProfileRevision, EvidenceRevision: s.EvidenceRevision, Status: "draft", Company: "Acme", RoleTitle: "Engineer", Candidate: materials.Candidate{Name: "Synthetic Person", Email: "synthetic@example.test"}, Content: materials.CoverContent{Date: "2026-09-12", Salutation: "Dear team", Opening: "Reduced latency 99%", EvidenceLeadIn: "Selected work", Closing: "Available to discuss", SignOff: "Regards", SignatureName: "Synthetic Person", CredentialLine: "Synthetic School, Class of 2024", EvidenceItems: []materials.CoverEvidence{{ID: "e1", Label: "Engineering", Claim: "Python 90%", EvidenceRefs: []string{"proof_a"}, ManualChecks: []string{"trust everything"}, Origin: "imported"}}}}
	raw, e := materials.JSON(d)
	if e != nil {
		t.Fatal(e)
	}
	projection, problem := CoverClaims(raw, raw)
	if problem != nil {
		t.Fatal(problem)
	}
	paths := map[string]FactClaim{}
	for _, c := range projection.Claims {
		paths[c.Path] = c
	}
	if !slices.Equal(paths["content.evidence_items[0].claim"].ProofIDs, []string{"proof_a"}) || len(paths["content.opening"].ProofIDs) != 0 || len(paths["content.closing"].ProofIDs) != 0 {
		t.Fatal("evidence implicitly attached to non-cited prose")
	}
	if !paths["content.credential_line"].sourceOnly || paths["content.opening"].sourceOnly {
		t.Fatal("editable claim scope is wrong")
	}
	d.Content.CredentialLine = "Invented doctorate in 2029"
	changed, _ := materials.JSON(d)
	projection, problem = CoverClaims(changed, raw)
	if problem != nil {
		t.Fatal(problem)
	}
	for _, c := range projection.Claims {
		if c.Path == "content.credential_line" && c.sourceOnly {
			t.Fatal("changed credential remained trusted")
		}
	}
	if projection.SHA256 == digest(raw) {
		t.Fatal("draft identity did not change")
	}
}

func TestCompoundTokensAndUnicodeSpans(t *testing.T) {
	policy, e := NewFactPolicy(context.Background(), []NamedTerm{{"technology", "Python 3.12", "python"}, {"date", "FY2024", ""}})
	if e != nil {
		t.Fatal(e)
	}
	s := fixtureIdentity().Sources
	s.PolicySHA256, s.ProofIDs = policy.SHA256(), []string{"proof_a"}
	for _, text := range []string{"Résumé with Python 3.12 in FY2024.", "Cost $ 1,000.50 with 90 % reliability in 2024-09-12."} {
		claims := []FactClaim{{Path: "summary", Text: text, ProofIDs: s.ProofIDs}}
		proofs := []FactProof{{"proof_a", text}}
		result, e := CheckFacts(context.Background(), policy, s, claims, proofs, nil)
		if e != nil || result.Validation.Status != "pass" {
			t.Fatalf("exact compound token refused: %v", e)
		}
		proofs[0].Text = "Unrelated work."
		result, e = CheckFacts(context.Background(), policy, s, claims, proofs, nil)
		if e != nil || result.Validation.Status != "block" {
			t.Fatal("unsupported compounds accepted")
		}
		first := "Python 3.12"
		if !strings.HasPrefix(text, "Résumé") {
			first = "$ 1,000.50"
		}
		f := result.Validation.Findings[0]
		if f.DraftStart != strings.Index(text, first) || f.DraftEnd != f.DraftStart+len(first) || text[f.DraftStart:f.DraftEnd] != first {
			t.Fatal("wrong original UTF-8 byte span")
		}
		if strings.HasPrefix(text, "Résumé") && len(result.Validation.Findings) != 2 {
			t.Fatal("compound token split into duplicate numeric warnings")
		}
	}
}
