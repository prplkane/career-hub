package materialintegrity

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applicationanalysis"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/materials"
	"careerhub.local/backend/internal/resumeplan"
	"careerhub.local/backend/internal/skills/catalog"
)

func defaultRegistry(t *testing.T, skills ...catalog.Term) *catalog.Snapshot {
	t.Helper()
	s, e := catalog.Build(nil, nil, skills, nil, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func defaultSource(t *testing.T, raw string) materials.Sources {
	t.Helper()
	fp, e := applicationanalysis.SourceFingerprint(raw)
	if e != nil {
		t.Fatal(e)
	}
	return materials.Sources{JobID: "synthetic_job", Company: "NewCo", RoleTitle: "Engineer", JobFingerprint: fp,
		ProfileRevision: "sha256:" + digest([]byte("profile")), Catalog: resumeplan.Catalog{VaultRevision: "sha256:" + digest([]byte("vault")),
			Experiences: []resumeplan.ExperienceSource{{Company: "PastCo", Role: "Staff Engineer"}}}}
}

func TestDefaultPolicyCombinesSourcesWithoutInventingEvidence(t *testing.T) {
	registry, e := catalog.Build(nil, nil,
		[]catalog.Term{{ID: "python", Name: "Python", Aliases: []string{"Py"}, Category: "language"}, {ID: "retired", Name: "RetiredSDK", Category: "language", Archived: true}},
		[]catalog.Term{{ID: "user_sdk", Name: "PrivateSDK", Category: "language"}}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	raw := "Use Py and PrivateSDK."
	source := defaultSource(t, raw)
	policy, info, problem := DefaultFactPolicy(context.Background(), registry, source, raw, nil)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	if info.ActiveSkills != 2 || info.NamedTerms != 7 || info.RegistryRevision != registry.CatalogRevision() {
		t.Fatalf("incorrect source registry summary: %+v", info)
	}
	s := fixtureIdentity().Sources
	s.PolicySHA256, s.ProofIDs = policy.SHA256(), []string{"proof_a"}
	claim := FactClaim{Path: "summary", Text: "At PastCo, a Staff Engineer used Py and PrivateSDK.", ProofIDs: s.ProofIDs}
	result, problem := CheckFacts(context.Background(), policy, s, []FactClaim{claim}, []FactProof{{"proof_a", "Built services."}}, nil)
	if problem != nil || len(result.Validation.Findings) != 4 || len(result.CitedDemandIDs) != 0 {
		t.Fatal("registry/source terms became evidence")
	}
	result, problem = CheckFacts(context.Background(), policy, s, []FactClaim{claim}, []FactProof{{"proof_a", claim.Text}}, nil)
	if problem != nil || result.Validation.Status != "pass" || !slices.Equal(result.CitedDemandIDs, []string{"python", "user_sdk"}) {
		t.Fatal("exact cited JD terms lost")
	}
	claim.Text = "Python and RetiredSDK"
	result, problem = CheckFacts(context.Background(), policy, s, []FactClaim{claim}, []FactProof{{"proof_a", claim.Text}}, nil)
	if problem != nil || len(result.ObservedDemandIDs) != 0 {
		t.Fatal("canonical spelling not present in JD credited as exact vocabulary")
	}
	stale := source
	stale.JobFingerprint = digest([]byte("old JD"))
	if p, _, e := DefaultFactPolicy(context.Background(), registry, stale, raw, nil); p != nil || e == nil || e.Code != "MATERIAL_FACT_JD_STALE" {
		t.Fatal("stale cached JD fingerprint accepted")
	}
}

func TestDefaultPolicyIdentityCollisionsAndInputRefusals(t *testing.T) {
	raw := "Use Python"
	registry := defaultRegistry(t, catalog.Term{ID: "python", Name: "Python", Category: "language"})
	source := defaultSource(t, raw)
	base, _, e := DefaultFactPolicy(context.Background(), registry, source, raw, nil)
	if e != nil {
		t.Fatal(e.Code)
	}
	duplicate, _, e := DefaultFactPolicy(context.Background(), registry, source, raw, []NamedTerm{{"technology", " PYTHON ", ""}})
	if e != nil || duplicate.SHA256() != base.SHA256() {
		t.Fatal("equivalent extra term changed policy/erased vocabulary")
	}
	collision := source
	collision.Company = "Python"
	p, info, e := DefaultFactPolicy(context.Background(), registry, collision, raw, nil)
	if e != nil || info.AmbiguousTerms != 1 || p.SHA256() == base.SHA256() {
		t.Fatal("cross-role collision not kept as a checked uncredited literal")
	}
	for _, terms := range [][]NamedTerm{{{"bogus", "Python", ""}}, {{"technology", "Python", "invented"}}, {{"other", "", ""}}, {{"other", "x\x00", ""}}} {
		if p, _, e := DefaultFactPolicy(context.Background(), registry, source, raw, terms); p != nil || e == nil {
			t.Fatal("invalid explicit configuration accepted")
		}
	}
	for _, r := range []*catalog.Snapshot{nil, defaultRegistry(t)} {
		if p, _, e := DefaultFactPolicy(context.Background(), r, source, raw, nil); p != nil || e == nil {
			t.Fatal("absent/empty registry silently disabled names")
		}
	}
	for _, text := range []string{"", " ", "x\x00", string([]byte{0xff}), strings.Repeat("x", MaxTextBytes+1)} {
		if p, _, e := DefaultFactPolicy(context.Background(), registry, source, text, nil); p != nil || e == nil {
			t.Fatal("invalid raw JD accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, _, e := DefaultFactPolicy(ctx, registry, source, raw, nil); p != nil || e == nil {
		t.Fatal("canceled default policy accepted")
	}
	source.Catalog.Experiences[0].Company = []any{"not", "scalar"}
	if p, _, e := DefaultFactPolicy(context.Background(), registry, source, raw, nil); p != nil || e == nil {
		t.Fatal("collection formatted into an employer name")
	}
}

func TestDefaultPolicyUsesShippedCatalog(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	raw, err := os.ReadFile(filepath.Join(root, "src/data/job_technologies.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	terms, de := cataloginput.DecodeTerms(raw, true)
	if de != nil {
		t.Fatal("shipped terms invalid")
	}
	raw, err = os.ReadFile(filepath.Join(root, "src/data/skill_groups.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	groups, de := cataloginput.DecodeGroups(raw, true)
	if de != nil {
		t.Fatal("shipped groups invalid")
	}
	registry, err := catalog.Build(groups, nil, terms, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	jd := "Python, SQL, Go and TypeScript"
	p, info, e := DefaultFactPolicy(context.Background(), registry, defaultSource(t, jd), jd, nil)
	if e != nil || p == nil || info.ActiveSkills < 100 {
		t.Fatalf("default policy not usable with shipped assets: %+v / %v", info, e)
	}
}
