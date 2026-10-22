package materialintegrity

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestFinalTextOrderWhitespaceAndOffsets(t *testing.T) {
	for _, c := range []struct {
		name, expected, actual      string
		match                       bool
		draftOffset, artifactOffset int
	}{
		{"same", "Synthetic résumé\nEvidence.", "Synthetic résumé\nEvidence.", true, -1, -1},
		{"layout", "  Name\n\nRésumé\t90%\n", "Name\u00a0Résumé\r\n90% \f", true, -1, -1},
		{"ucd15-spaces", "A\u001cB\u2003C", "A B C", true, -1, -1},
		{"changed-number", "Résumé 90%", "Résumé 99%", false, 10, 10},
		{"changed-case", "API", "api", false, 0, 0},
		{"changed-punctuation", "A, B", "A B", false, 1, 1},
		{"changed-order", "first\nsecond", "second\nfirst", false, 0, 0},
		{"truncated", "A B", "A", false, 1, 1},
		{"duplicated", "A B", "A B B", false, 3, 3},
		{"no-accent-fold", "é", "e\u0301", false, 0, 0},
		{"original-byte-offset", " \nA\tXYZ", "A\u00a0XZ", false, 5, 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, e := CompareText(context.Background(), c.expected, c.actual)
			if e != nil || got.Match != c.match || got.DraftOffset != c.draftOffset || got.ArtifactOffset != c.artifactOffset {
				t.Fatalf("comparison=%+v, error=%v", got, e)
			}
			if (got.ExpectedSHA256 == got.ActualSHA256) != c.match {
				t.Fatal("normalization identity mismatch")
			}
		})
	}
}

func TestFinalTextBoundsAndCancellation(t *testing.T) {
	for _, value := range []string{"", " \n", "text\x00", string([]byte{0xff}), strings.Repeat("x", MaxTextBytes+1)} {
		for _, pair := range [][2]string{{value, "valid"}, {"valid", value}} {
			if got, e := CompareText(context.Background(), pair[0], pair[1]); got != nil || e == nil {
				t.Fatal("invalid text accepted")
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		if got, e := CompareText(ctx, "valid", "valid"); got != nil || e == nil || e.Code != "MATERIAL_INTEGRITY_CANCELED" {
			t.Fatal("canceled check accepted")
		}
	}
	if strings.Contains(fmt.Sprintf("%+v", failure("private-marker")), "private-marker") {
		t.Fatal("diagnostic value leak")
	}
}

var fixtureTime = time.Date(2026, 9, 12, 12, 0, 0, 123000, time.UTC)
var fixtureArtifact = []byte("Synthetic resume\n")

func fixtureIdentity() Identity {
	revision := int64(2)
	return Identity{
		Sources: Sources{JobID: "synthetic_job", Kind: "resume", MaterialID: "resume_primary", DraftRevision: 3,
			DraftSHA256: digest([]byte("draft")), JobFingerprint: digest([]byte("job")),
			ProfileRevision: "sha256:" + digest([]byte("profile")), EvidenceRevision: "sha256:" + digest([]byte("evidence")),
			PlanRevision: &revision, PlanFingerprint: "sha256:" + digest([]byte("plan")), TemplateID: "canonical_ats_resume_v1",
			ProofIDs: []string{"proof_a", "proof_z"}, ManualCheckIDs: []string{}, PolicySHA256: digest([]byte("policy"))},
		RendererVersion: "synthetic-renderer-v1", ValidatorVersion: ValidatorVersion, Format: "txt", Filename: "resume.txt",
		SizeBytes: int64(len(fixtureArtifact)), SHA256: digest(fixtureArtifact), TextSHA256: digest([]byte("Synthetic resume")),
		Metadata:   FormatMetadata{TargetPages: 1, PageCountUnavailable: "not_paginated", PageBudget: "unavailable"},
		Validation: Validation{Status: "pass", Findings: []Finding{}},
	}
}

func manifestFixture(t *testing.T, id Identity) (*Manifest, []byte) {
	t.Helper()
	m, e := NewManifest(id, fixtureTime)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := Encode(m)
	if e != nil {
		t.Fatal(e)
	}
	return m, raw
}

func artifactFinding(code, severity string) Finding {
	return Finding{Severity: severity, Code: code, DraftPath: "artifact", DraftStart: -1, DraftEnd: -1, ArtifactStart: -1, ArtifactEnd: -1}
}

func TestManifestCanonicalRoundTripAndOwnedSnapshot(t *testing.T) {
	id := fixtureIdentity()
	m, raw := manifestFixture(t, id)
	id.Sources.ProofIDs[0] = "changed"
	*id.Sources.PlanRevision = 9
	if m.Identity.Sources.ProofIDs[0] != "proof_a" || *m.Identity.Sources.PlanRevision != 2 {
		t.Fatal("manifest borrows caller storage")
	}
	decoded, e := Decode(raw)
	if e != nil || decoded.ArtifactID != m.ArtifactID {
		t.Fatal("canonical decode failed")
	}
	again, e := Encode(decoded)
	if e != nil || !bytes.Equal(again, raw) {
		t.Fatal("wire is not stable")
	}
	if _, e := Verify(raw, digest(raw), m.Identity.Sources, fixtureArtifact); e != nil {
		t.Fatal(e)
	}
	for _, prose := range []string{"claim", "proof_text", "raw_jd", "Synthetic resume"} {
		if bytes.Contains(raw, []byte(prose)) {
			t.Fatal("manifest contains prose payload")
		}
	}
}

func TestManifestExactIdentityAndTimestamp(t *testing.T) {
	m, original := manifestFixture(t, fixtureIdentity())
	later, e := NewManifest(fixtureIdentity(), fixtureTime.Add(time.Hour))
	if e != nil || m.ArtifactID != later.ArtifactID {
		t.Fatal("same export cannot reuse identity")
	}
	newBytes, e := Encode(later)
	if e != nil || bytes.Equal(newBytes, original) {
		t.Fatal("creation receipt was not preserved independently")
	}
	if _, e := Verify(newBytes, digest(original), later.Identity.Sources, fixtureArtifact); e == nil || e.Code != "MATERIAL_MANIFEST_CHANGED" {
		t.Fatal("changed receipt accepted")
	}
	for _, mutate := range []func(*Identity){
		func(x *Identity) { x.RendererVersion = "synthetic-renderer-v2" },
		func(x *Identity) { x.Sources.DraftRevision++ },
		func(x *Identity) { x.Sources.PolicySHA256 = digest([]byte("policy-v2")) },
		func(x *Identity) { x.SHA256 = digest([]byte("different bytes")) },
	} {
		id := fixtureIdentity()
		mutate(&id)
		other, e := NewManifest(id, fixtureTime)
		if e != nil || other.ArtifactID == m.ArtifactID {
			t.Fatal("changed export reused identity")
		}
	}
}

func TestManifestRefusesNoncanonicalAndUnknownWire(t *testing.T) {
	_, raw := manifestFixture(t, fixtureIdentity())
	for _, bad := range [][]byte{
		nil, bytes.Repeat([]byte("x"), MaxManifestBytes+1), []byte{0xff},
		append(bytes.Clone(raw), []byte("{}")...), raw[:len(raw)-1],
		bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1),
		bytes.Replace(raw, []byte(`"format":"txt"`), []byte(`"format":"pdf","format":"txt"`), 1),
		bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":1.0`), 1),
		bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":true`), 1),
		bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1),
		bytes.Replace(raw, []byte(`"schema_version"`), []byte(`"Schema_Version"`), 1),
		bytes.Replace(raw, []byte(`"page_count":null,`), nil, 1),
		bytes.Replace(raw, []byte(`"findings":[]`), []byte(`"findings":null`), 1),
		bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":1,"raw_jd":"private"`), 1),
		bytes.Replace(raw, []byte(`"job_id":"synthetic_job"`), []byte(`"job_id":"\ud800"`), 1),
	} {
		if _, e := Decode(bad); e == nil {
			t.Fatal("noncanonical/unsupported manifest accepted")
		}
	}
}

func TestManifestSchemaLimitsAndBindings(t *testing.T) {
	for name, mutate := range map[string]func(*Identity){
		"job-path":          func(x *Identity) { x.Sources.JobID = "../outside" },
		"job-empty":         func(x *Identity) { x.Sources.JobID = "" },
		"job-control":       func(x *Identity) { x.Sources.JobID = "secret\n" },
		"material":          func(x *Identity) { x.Sources.MaterialID = "cover_letter_primary" },
		"kind":              func(x *Identity) { x.Sources.Kind = "unbounded" },
		"revision":          func(x *Identity) { x.Sources.DraftRevision = 0 },
		"plan":              func(x *Identity) { x.Sources.PlanRevision = nil },
		"plan-hash":         func(x *Identity) { x.Sources.PlanFingerprint = "" },
		"hash":              func(x *Identity) { x.Sources.DraftSHA256 = strings.Repeat("F", 64) },
		"renderer":          func(x *Identity) { x.RendererVersion = "../renderer" },
		"validator":         func(x *Identity) { x.ValidatorVersion = "unknown" },
		"filename":          func(x *Identity) { x.Filename = "../resume.txt" },
		"template":          func(x *Identity) { x.Sources.TemplateID = "custom" },
		"empty-file":        func(x *Identity) { x.SizeBytes = 0 },
		"big-file":          func(x *Identity) { x.SizeBytes = MaxArtifactBytes + 1 },
		"big-ids":           func(x *Identity) { x.Sources.ProofIDs = make([]string, 10001) },
		"duplicate-ids":     func(x *Identity) { x.Sources.ProofIDs = []string{"p", "p"} },
		"unsorted-ids":      func(x *Identity) { x.Sources.ProofIDs = []string{"z", "a"} },
		"nil-ids":           func(x *Identity) { x.Sources.ProofIDs = nil },
		"unrecorded-manual": func(x *Identity) { x.Sources.ManualCheckIDs = []string{"check_1"} },
		"status":            func(x *Identity) { x.Validation.Status = "warning" },
		"target":            func(x *Identity) { x.Metadata.TargetPages = 3 },
		"fake-page-budget":  func(x *Identity) { x.Metadata.PageBudget = "pass" },
		"text-pages":        func(x *Identity) { n := 1; x.Metadata.PageCount = &n },
	} {
		t.Run(name, func(t *testing.T) {
			id := fixtureIdentity()
			mutate(&id)
			if _, e := NewManifest(id, fixtureTime); e == nil {
				t.Fatal("invalid identity accepted")
			}
		})
	}
}

func TestManifestFreshnessAndActualBytes(t *testing.T) {
	m, raw := manifestFixture(t, fixtureIdentity())
	for _, mutate := range []func(*Sources){
		func(s *Sources) { s.JobID = "other_job" },
		func(s *Sources) { s.JobFingerprint = digest([]byte("job2")) },
		func(s *Sources) { s.DraftRevision++ },
		func(s *Sources) { s.DraftSHA256 = digest([]byte("draft2")) },
		func(s *Sources) { s.ProfileRevision = "sha256:" + digest([]byte("profile2")) },
		func(s *Sources) { s.EvidenceRevision = "sha256:" + digest([]byte("evidence2")) },
		func(s *Sources) { *s.PlanRevision++ },
		func(s *Sources) { s.PlanFingerprint = "sha256:" + digest([]byte("plan2")) },
		func(s *Sources) { s.ProofIDs = []string{"other_proof"} },
		func(s *Sources) { s.ManualCheckIDs = []string{"other_check"} },
		func(s *Sources) { s.PolicySHA256 = digest([]byte("policy2")) },
	} {
		s := fixtureIdentity().Sources
		mutate(&s)
		if _, e := Verify(raw, digest(raw), s, fixtureArtifact); e == nil || e.Code != "MATERIAL_ARTIFACT_SOURCES_STALE" {
			t.Fatal("stale sources accepted")
		}
	}
	for _, rawArtifact := range [][]byte{nil, []byte("Synthetic resumE\n"), append(bytes.Clone(fixtureArtifact), 'x')} {
		if _, e := Verify(raw, digest(raw), m.Identity.Sources, rawArtifact); e == nil || e.Code != "MATERIAL_ARTIFACT_CHANGED" {
			t.Fatal("changed artifact accepted")
		}
	}
}

func TestManifestFormatAndFindingRules(t *testing.T) {
	// Wire metadata fixtures are not proof of DOCX/PDF rendering or extraction.
	id := fixtureIdentity()
	id.Format, id.Filename = "docx", "resume.docx"
	id.Metadata.PageCountUnavailable = "layout_engine_unavailable"
	if _, e := NewManifest(id, fixtureTime); e == nil {
		t.Fatal("missing pagination warning accepted")
	}
	id.Validation.Status = "warning"
	id.Validation.Findings = []Finding{artifactFinding("PAGINATION_UNAVAILABLE", "warning")}
	manifestFixture(t, id)
	id = fixtureIdentity()
	id.Format, id.Filename = "pdf", "resume.pdf"
	if _, e := NewManifest(id, fixtureTime); e == nil {
		t.Fatal("PDF without pages accepted")
	}
	pages := 3
	id.Metadata = FormatMetadata{TargetPages: 1, PageCount: &pages, PageBudget: "warning"}
	id.Validation = Validation{Status: "warning", Findings: []Finding{artifactFinding("PAGE_BUDGET_EXCEEDED", "warning")}}
	if _, e := NewManifest(id, fixtureTime); e == nil {
		t.Fatal("three-page resume not blocked")
	}
	id.Validation.Status = "block"
	id.Validation.Findings = append(id.Validation.Findings, artifactFinding("HARD_PAGE_LIMIT", "block"))
	m, raw := manifestFixture(t, id)
	if _, e := Verify(raw, digest(raw), m.Identity.Sources, fixtureArtifact); e == nil || e.Code != "MATERIAL_ARTIFACT_BLOCKED" {
		t.Fatal("blocked artifact returned")
	}

	id = fixtureIdentity()
	id.Sources.Kind, id.Sources.MaterialID, id.Sources.TemplateID = "cover_letter", "cover_letter_primary", "professional_cover_letter_v1"
	id.Sources.PlanRevision, id.Sources.PlanFingerprint = nil, ""
	id.Filename = "cover_letter.txt"
	manifestFixture(t, id)
	id.Sources.ManualCheckIDs = []string{"check_1"}
	id.Validation.Findings = []Finding{{Severity: "pass", Code: "MANUAL_FACT_CHECK", DraftPath: "content.evidence_items[0].claim", DraftStart: 0, DraftEnd: 3, ArtifactStart: -1, ArtifactEnd: -1, TokenSHA256: digest([]byte("90%")), ManualCheckID: "check_1"}}
	manifestFixture(t, id)
	id.Validation.Findings[0].ManualCheckID = "not_cited"
	if _, e := NewManifest(id, fixtureTime); e == nil {
		t.Fatal("uncited manual check accepted")
	}
}

func TestManifestFindingSeverityAndExactLocations(t *testing.T) {
	for _, finding := range []Finding{
		{Severity: "pass", Code: "UNSUPPORTED_FACT_TOKEN", DraftPath: "summary", DraftStart: 0, DraftEnd: 3, ArtifactStart: -1, ArtifactEnd: -1, TokenSHA256: digest([]byte("90%"))},
		{Severity: "block", Code: "UNSUPPORTED_FACT_TOKEN", DraftPath: "summary", DraftStart: -1, DraftEnd: -1, ArtifactStart: -1, ArtifactEnd: -1, TokenSHA256: digest([]byte("90%"))},
		{Severity: "block", Code: "UNRESOLVED_PLACEHOLDER", DraftPath: "summary", DraftStart: 0, DraftEnd: 3, ArtifactStart: -1, ArtifactEnd: -1},
		{Severity: "block", Code: "FINAL_TEXT_MISMATCH", DraftPath: "artifact", DraftStart: -1, DraftEnd: -1, ArtifactStart: -1, ArtifactEnd: -1},
		{Severity: "warning", Code: "PAGE_BUDGET_EXCEEDED", DraftPath: "content.opening", DraftStart: -1, DraftEnd: -1, ArtifactStart: -1, ArtifactEnd: -1},
		{Severity: "warning", Code: "PRIVATE_PROSE", DraftPath: "private content", DraftStart: -1, DraftEnd: -1, ArtifactStart: -1, ArtifactEnd: -1},
	} {
		id := fixtureIdentity()
		id.Validation = Validation{Status: finding.Severity, Findings: []Finding{finding}}
		if _, e := NewManifest(id, fixtureTime); e == nil {
			t.Fatal("unbounded/incorrect finding accepted")
		}
	}
}

func FuzzCanonicalManifest(f *testing.F) {
	m, e := NewManifest(fixtureIdentity(), fixtureTime)
	if e != nil {
		f.Fatal(e)
	}
	raw, e := Encode(m)
	if e != nil {
		f.Fatal(e)
	}
	f.Add(raw)
	f.Add([]byte(`{"schema_version":1}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > MaxManifestBytes+1 {
			return
		}
		m, e := Decode(raw)
		if e != nil {
			return
		}
		encoded, e := Encode(m)
		if e != nil || !bytes.Equal(raw, encoded) {
			t.Fatal("noncanonical acceptance")
		}
	})
}
