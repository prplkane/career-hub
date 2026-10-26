package jobcard

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"careerhub.local/backend/internal/jobs/jobdigest"
	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/cataloginput"
)

const reanalysisSource = "Company: Fresh Labs\nRole: Backend Engineer\nResponsibilities\nBuild services.\nRequirements\nPython experience required.\n"

func reanalysisFixture(t *testing.T) (*Loaded, *jobdigest.Result) {
	t.Helper()
	card := cardFixture(t)
	put(&card, "status", stringValue("draft"))
	put(&card, "title", stringValue("User title"))
	put(&card, "required_skills", cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{stringValue("ManualSkill")}})
	put(&card, "required_capabilities", cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{stringValue("Stale capability")}})
	put(&card, "notes", cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{stringValue("Keep this user note")}})
	put(&card, "intake_idempotency_key", stringValue("synthetic-key-123"))
	put(&card, "content_fingerprint", stringValue(strings.Repeat("0", 64)))
	put(&card, "manual_overrides", cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{
		stringValue("title"), stringValue("required_skills"), stringValue("mentioned_technologies"),
	}})
	oldDigest, parseErr := cataloginput.ParseYAML([]byte(`schema_version: 1
analyzer_version: 11
fields:
  company: {value: Synthetic, state: derived, evidence: []}
  title: {value: User title, state: provided, evidence: []}
  location: {value: '', state: not_stated, evidence: []}
  remote_policy: {value: '', state: not_stated, evidence: []}
  seniority: {value: '', state: not_stated, evidence: []}
unresolved_fields: []
eligibility_gates: {}
classified_requirements: []
mentioned_technologies: [ManualTech]
`))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	put(&card, "digest", oldDigest)
	loaded := validatedReanalysisCard(t, card)
	registry, registryErr := catalog.Build(nil, nil, nil, nil, nil, nil)
	if registryErr != nil {
		t.Fatal(registryErr)
	}
	fresh, digestErr := jobdigest.Analyze(context.Background(), jobdigest.Input{Source: reanalysisSource, SourceURL: get(card, "source_url").Text, Registry: registry})
	if digestErr != nil {
		t.Fatal(digestErr)
	}
	return loaded, fresh
}

func validatedReanalysisCard(t *testing.T, card cataloginput.Value) *Loaded {
	t.Helper()
	report, err := Validate(context.Background(), card, func(context.Context, string) (bool, *Error) { return true, nil })
	if err != nil || !report.SchemaValid() {
		t.Fatalf("synthetic original invalid: %#v %#v", report, err)
	}
	return &Loaded{Card: card, Report: report, RawText: reanalysisSource, RawPresent: true}
}

func TestPrepareReanalysisBuildsSingleCompleteCardWithoutLosingUserData(t *testing.T) {
	loaded, fresh := reanalysisFixture(t)
	before, _ := json.Marshal(loaded.Card)
	card, err := PrepareReanalysis(context.Background(), loaded, fresh)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(loaded.Card)
	if string(before) != string(after) {
		t.Fatal("pure transform mutated input")
	}
	if get(card, "status").Text != "draft" || get(card, "title").Text != "User title" || get(card, "company").Text != fresh.Fields["company"].Value ||
		get(card, "content_fingerprint").Text != fresh.ContentFingerprint || get(card, "intake_idempotency_key").Text != "synthetic-key-123" ||
		get(card, "notes").Items[0].Text != "Keep this user note" {
		t.Fatal("fresh fields or retained identity/user data missing")
	}
	if skills := get(card, "required_skills").Items; len(skills) != 1 || skills[0].Text != "ManualSkill" {
		t.Fatal("manual skill was not replayed")
	}
	if get(card, "required_capabilities").Kind != "sequence" || len(get(card, "required_capabilities").Items) != 0 {
		t.Fatal("stale non-overridden capability survived")
	}
	info := get(card, "digest")
	if get(info, "analyzer_version").Text != strconv.Itoa(jobdigest.AnalyzerVersion) || get(get(get(info, "fields"), "title"), "state").Text != "provided" ||
		len(get(info, "mentioned_technologies").Items) != 1 || get(info, "mentioned_technologies").Items[0].Text != "ManualTech" {
		t.Fatal("fresh v12 digest or digest-only override missing")
	}
	if overrides := get(card, "manual_overrides").Items; len(overrides) != 3 {
		t.Fatal("manual override provenance missing")
	}
	encoded, encodeErr := EncodeCard(context.Background(), card)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	reopened, parseErr := cataloginput.ParseYAML(encoded)
	if parseErr != nil || !sameCardValue(card, reopened) {
		t.Fatal("prepared card did not round-trip through stored YAML")
	}
}

func TestPrepareReanalysisRefusesUnseenOrUnsupportedInput(t *testing.T) {
	for _, test := range []struct {
		name, code string
		change     func(*Loaded, *jobdigest.Result)
	}{
		{"ready", "JOB_REANALYSIS_NOT_DRAFT", func(l *Loaded, _ *jobdigest.Result) { put(&l.Card, "status", stringValue("ready")) }},
		{"source-mismatch", "JOB_REANALYSIS_SOURCE_MISMATCH", func(_ *Loaded, r *jobdigest.Result) { r.ContentFingerprint = strings.Repeat("0", 64) }},
		{"raw-id-mismatch", "JOB_REANALYSIS_SOURCE_MISMATCH", func(l *Loaded, _ *jobdigest.Result) {
			put(&l.Card, "raw_jd_path", stringValue("jobs/incoming/another.txt"))
		}},
		{"unknown-metadata", "JOB_REANALYSIS_UNSUPPORTED_FIELD", func(l *Loaded, _ *jobdigest.Result) { put(&l.Card, "future_user_field", stringValue("preserve me")) }},
		{"unsupported-override", "JOB_REANALYSIS_OVERRIDE_INVALID", func(l *Loaded, _ *jobdigest.Result) {
			put(&l.Card, "manual_overrides", cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{stringValue("status")}})
		}},
		{"missing-digest-fields", "JOB_REANALYSIS_DIGEST_INVALID", func(_ *Loaded, r *jobdigest.Result) { r.Fields = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			loaded, fresh := reanalysisFixture(t)
			test.change(loaded, fresh)
			loaded.Report = validatedReanalysisCard(t, loaded.Card).Report
			card, err := PrepareReanalysis(context.Background(), loaded, fresh)
			if err == nil || err.Code != test.code || card.Kind != "" {
				t.Fatalf("expected %s, got %#v %#v", test.code, card, err)
			}
		})
	}
}

func TestReanalysisSourceIDMatchesPythonStemCases(t *testing.T) {
	for _, test := range []struct{ path, want string }{
		{"jobs/incoming/Synthetic.txt", "synthetic"},
		{"jobs/incoming/My Job.v2.txt", "my_job_v2"},
		{"jobs/incoming/.bashrc", "bashrc"},
		{"jobs/incoming/!!!.txt", "job_e84c538e"},
		{"jobs/incoming/Straße.txt", "strasse"},
		{"jobs/incoming/東京-ML.txt", "東京_ml"},
		{"jobs/incoming/x\u0301.txt", "x"},
	} {
		if got := reanalysisSourceID(test.path); got != test.want {
			t.Fatalf("%s: got %s, want %s", test.path, got, test.want)
		}
	}
}

func TestPrepareReanalysisRetainsManualListBeyondInteractiveLimit(t *testing.T) {
	loaded, fresh := reanalysisFixture(t)
	skills := cataloginput.Value{Kind: "sequence", Items: make([]cataloginput.Value, 1001)}
	for index := range skills.Items {
		skills.Items[index] = stringValue("ManualSkill" + strconv.Itoa(index))
	}
	put(&loaded.Card, "required_skills", skills)
	loaded.Report = validatedReanalysisCard(t, loaded.Card).Report
	card, err := PrepareReanalysis(context.Background(), loaded, fresh)
	if err != nil || len(get(card, "required_skills").Items) != len(skills.Items) {
		t.Fatalf("validated manual list was dropped: %#v", err)
	}
	values := make([]any, len(skills.Items))
	for index, item := range skills.Items {
		values[index] = item.Text
	}
	if _, err := EditDetails(context.Background(), loaded, map[string]any{"required_skills": values}); err == nil || err.Code != "JOB_DETAILS_INVALID" {
		t.Fatal("interactive edit limit unexpectedly widened")
	}
}
