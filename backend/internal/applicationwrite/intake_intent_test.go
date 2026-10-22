package applicationwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/intakeextract"
	"careerhub.local/backend/internal/resumelibrary"
)

type intakeTesting interface {
	Helper()
	Fatal(...any)
}

func fixtureIntakeIntent(t intakeTesting) intakeIntent {
	t.Helper()
	home := datahome.Info{FormatVersion: 2, Channel: "stable", Initialized: true, DataHomeID: "dh_0123456789abcdef0123456789abcdef"}
	operation := strings.Repeat("a", 32)
	resumeID := "Resume-Fictional"
	sourceSHA := digestBytes([]byte("Synthetic resume with no private data."))
	rawStem := "resume_" + digestBytes([]byte(resumeID))[:24] + "_digest_" + operation
	rawPath := "career_vault/raw/" + rawStem + ".txt"
	sourceID := "src_" + rawStem + "_" + sourceSHA[:10]
	revision := strings.Repeat("b", 64)
	report, err := intakeextract.Build(context.Background(), intakeextract.Source{
		ID: sourceID, Path: rawPath, FileType: "txt", SHA256: sourceSHA,
	}, revision, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	encodedReport, err := json.Marshal(report) // JSON is a valid YAML 1.2 document.
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := func(state, stage string) []byte {
		t.Helper()
		raw, e := json.Marshal(intakeDiagnostic{1, operation, sourceID, state, stage, ""})
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	files := []intakeCatalogWitness{
		{catalogstore.CoreGroupsPath, true, 1, digestBytes([]byte("g"))},
		{catalogstore.CustomGroupsPath, false, 0, ""},
		{catalogstore.CoreSkillsPath, true, 1, digestBytes([]byte("s"))},
		{catalogstore.CustomSkillsPath, false, 0, ""},
	}
	return intakeIntent{
		Version: 1, Home: home, OperationID: operation,
		Resume:  intakeResumeIdentity{resumeID, "fictional.txt", sourceSHA, 38, resumelibrary.Witness{ResumeID: resumeID, Present: true, SHA256: digestBytes([]byte("row"))}},
		Catalog: files, RegistryRevision: revision, RawPath: rawPath, SourceID: sourceID,
		ReportPath:     "career_vault/intake_runs/" + sourceID + "/extracted.yaml",
		DiagnosticPath: "career_vault/logs/native-intake/" + operation + ".json",
		Report:         encodedReport, ReportSHA256: digestBytes(encodedReport),
		ProvisionalDiagnostic: diagnostic("publishing", "publication"),
		TerminalDiagnostic:    diagnostic("success", "completed"),
	}
}

func sealedIntake(t intakeTesting, p intakeIntent) []byte {
	t.Helper()
	payload, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(discoveryEnvelope{Payload: payload, SHA256: digestBytes(payload)})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestIntakeIntentCanonicalRoundTrip(t *testing.T) {
	p := fixtureIntakeIntent(t)
	raw, e := encodeIntakeIntent(p)
	if e != nil || len(raw) == 0 || len(raw) > intakeIntentLimit {
		t.Fatalf("valid fictional intent refused: %v", e)
	}
	got, e := decodeIntakeIntent(raw, p.Home)
	if e != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("round trip changed intent: %v", e)
	}
	otherHome := p.Home
	otherHome.DataHomeID = "dh_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if got, e := decodeIntakeIntent(raw, otherHome); e == nil || e.Code != "INTAKE_INTENT_HOME_CHANGED" || !reflect.DeepEqual(got, intakeIntent{}) {
		t.Fatal("different Data Home reused an intake journal")
	}
	for _, corrupt := range [][]byte{
		append([]byte(" "), raw...),
		append(append([]byte{}, raw...), '\n'),
		bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		bytes.Replace(raw, []byte(`"sha256":"`), []byte(`"unexpected":0,"sha256":"`), 1),
		bytes.Repeat([]byte("x"), intakeIntentLimit+1),
	} {
		if got, e := decodeIntakeIntent(corrupt, p.Home); e == nil || e.Code != "INTAKE_INTENT_INVALID" || !reflect.DeepEqual(got, intakeIntent{}) {
			t.Fatal("noncanonical or oversized journal accepted")
		}
	}
}

func TestIntakeIntentDOCXFormatWitness(t *testing.T) {
	p := fixtureIntakeIntent(t)
	p.Resume.FileName = "fictional.docx"
	p.RawPath = strings.TrimSuffix(p.RawPath, ".txt") + ".docx"
	var report intakeextract.Report
	if err := json.Unmarshal(p.Report, &report); err != nil {
		t.Fatal(err)
	}
	report.Source.Path, report.Source.FileType = p.RawPath, "docx"
	var err error
	p.Report, err = json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	p.ReportSHA256 = digestBytes(p.Report)
	raw, e := encodeIntakeIntent(p)
	if e != nil {
		t.Fatal("supported DOCX intent refused", e)
	}
	if got, e := decodeIntakeIntent(raw, p.Home); e != nil || !reflect.DeepEqual(got, p) {
		t.Fatal("supported DOCX intent did not round trip", e)
	}
}

func TestIntakeIntentRejectsChangedWitnessAndForeignPaths(t *testing.T) {
	cases := []struct {
		name   string
		change func(*intakeIntent)
	}{
		{"bad-home", func(p *intakeIntent) { p.Home.DataHomeID = "not-a-home" }},
		{"bad-operation", func(p *intakeIntent) { p.OperationID = strings.Repeat("z", 32) }},
		{"resume-row-mismatch", func(p *intakeIntent) { p.Resume.ID = "../other" }},
		{"trimmed-resume-id", func(p *intakeIntent) { p.Resume.ID = " leading" }},
		{"unsupported-file", func(p *intakeIntent) { p.Resume.FileName = "fictional.pdf" }},
		{"inactive-row", func(p *intakeIntent) { p.Resume.Row.Present = false }},
		{"different-row", func(p *intakeIntent) { p.Resume.Row.ResumeID = "other" }},
		{"raw-outside", func(p *intakeIntent) { p.RawPath = "../raw/foreign.txt" }},
		{"source-id", func(p *intakeIntent) { p.SourceID = "another" }},
		{"report-outside", func(p *intakeIntent) { p.ReportPath = "other/extracted.yaml" }},
		{"diagnostic-outside", func(p *intakeIntent) { p.DiagnosticPath = "other/intake.json" }},
		{"catalog-order", func(p *intakeIntent) { p.Catalog[0], p.Catalog[1] = p.Catalog[1], p.Catalog[0] }},
		{"catalog-hash", func(p *intakeIntent) { p.Catalog[0].SHA256 = "wrong" }},
		{"catalog-absence", func(p *intakeIntent) { p.Catalog[1].Size = 1 }},
		{"registry-revision", func(p *intakeIntent) { p.RegistryRevision = strings.Repeat("c", 64) }},
		{"report-hash", func(p *intakeIntent) { p.ReportSHA256 = strings.Repeat("d", 64) }},
		{"source-hash", func(p *intakeIntent) { p.Resume.SHA256 = strings.Repeat("e", 64) }},
		{"source-size", func(p *intakeIntent) { p.Resume.Size = 11 << 20 }},
		{"provisional-stage", func(p *intakeIntent) {
			p.ProvisionalDiagnostic = bytes.Replace(p.ProvisionalDiagnostic, []byte("publication"), []byte("other"), 1)
		}},
		{"diagnostic-private-field", func(p *intakeIntent) {
			p.ProvisionalDiagnostic = bytes.Replace(p.ProvisionalDiagnostic, []byte(`"stage":"publication"`), []byte(`"stage":"publication","resume_text":"private"`), 1)
		}},
		{"terminal-state", func(p *intakeIntent) {
			p.TerminalDiagnostic = bytes.Replace(p.TerminalDiagnostic, []byte("success"), []byte("failure"), 1)
		}},
		{"oversized-report", func(p *intakeIntent) {
			p.Report = bytes.Repeat([]byte("x"), (2<<20)+1)
			p.ReportSHA256 = digestBytes(p.Report)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixtureIntakeIntent(t)
			tc.change(&p)
			if raw, e := encodeIntakeIntent(p); raw != nil || e == nil || e.Code != "INTAKE_INTENT_INVALID" {
				t.Fatal("invalid proposed intent encoded")
			}
			if got, e := decodeIntakeIntent(sealedIntake(t, p), p.Home); e == nil || e.Code != "INTAKE_INTENT_INVALID" || !reflect.DeepEqual(got, intakeIntent{}) {
				t.Fatal("recovery accepted invalid sealed intent")
			}
		})
	}
}

func FuzzDecodeIntakeIntent(f *testing.F) {
	p := fixtureIntakeIntent(f)
	f.Add(sealedIntake(f, p))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"payload":null,"sha256":""}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > intakeIntentLimit+1 {
			t.Skip()
		}
		got, e := decodeIntakeIntent(raw, p.Home)
		if e != nil {
			if !reflect.DeepEqual(got, intakeIntent{}) {
				t.Fatal("failed decode returned private partial intent")
			}
			return
		}
		reencoded, error := encodeIntakeIntent(got)
		if error != nil || !bytes.Equal(reencoded, raw) {
			t.Fatal("accepted journal changed under canonical encoding")
		}
	})
}

func TestIntakeIntentHashesLegacyResumeIDBeforeNaming(t *testing.T) {
	p := fixtureIntakeIntent(t)
	id := "../legacy/Résumé #1"
	p.Resume.ID, p.Resume.Row.ResumeID = id, id
	stem := "resume_" + digestBytes([]byte(id))[:24] + "_digest_" + p.OperationID
	p.RawPath = "career_vault/raw/" + stem + ".txt"
	p.SourceID = "src_" + stem + "_" + p.Resume.SHA256[:10]
	p.ReportPath = "career_vault/intake_runs/" + p.SourceID + "/extracted.yaml"
	var report intakeextract.Report
	if err := json.Unmarshal(p.Report, &report); err != nil {
		t.Fatal(err)
	}
	report.Source.ID, report.Source.Path = p.SourceID, p.RawPath
	var err error
	p.Report, err = json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	p.ReportSHA256 = digestBytes(p.Report)
	p.ProvisionalDiagnostic, err = json.Marshal(intakeDiagnostic{1, p.OperationID, p.SourceID, "publishing", "publication", ""})
	if err != nil {
		t.Fatal(err)
	}
	p.TerminalDiagnostic, err = json.Marshal(intakeDiagnostic{1, p.OperationID, p.SourceID, "success", "completed", ""})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.RawPath, id) || strings.Contains(p.ReportPath, id) {
		t.Fatal("DB identifier became a filesystem component")
	}
	raw, e := encodeIntakeIntent(p)
	if e != nil {
		t.Fatal("valid legacy identifier refused", e)
	}
	if got, e := decodeIntakeIntent(raw, p.Home); e != nil || !reflect.DeepEqual(got, p) {
		t.Fatal("legacy identifier recovery changed the exact witness", e)
	}
}
