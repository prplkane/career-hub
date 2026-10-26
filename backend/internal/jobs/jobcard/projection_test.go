package jobcard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
)

func TestJobProjectionPreservesFieldsAndLegacyCompensation(t *testing.T) {
	card := cardFixture(t)
	raw := "\ufeffCompany: Synthetic\nSalary: $100,000 - $120,000 per year\nBuild services. 😀\n"
	report, err := Validate(context.Background(), card, func(context.Context, string) (bool, *Error) { return true, nil })
	if err != nil || !report.SchemaValid() {
		t.Fatal("fixture", report, err)
	}
	out, err := Project(context.Background(), &Loaded{Card: card, Report: report, RawText: raw, RawPresent: true}, "jobs/normalized/synthetic.yaml", "2026-09-12T00:00:00+00:00")
	if err != nil || out["valid"] != true || out["title"] != "Engineer" || out["analysis_stale"] != true || out["source_chars"] != len([]rune(strings.TrimPrefix(raw, "\ufeff"))) {
		t.Fatalf("projection: %+v %v", out, err)
	}
	encoded, _ := json.Marshal(out["compensation"])
	if !strings.Contains(string(encoded), `"minimum":100000`) {
		t.Fatal("missing compensation", string(encoded))
	}
	if out["content_fingerprint"] == "" || len(out["validation_errors"].([]string)) != 0 {
		t.Fatal("incomplete projection")
	}
}

func TestJobProjectionKeepsInvalidAndCancellationHonest(t *testing.T) {
	card := cardFixture(t)
	put(&card, "digest", cataloginput.Value{Kind: "mapping", Entries: []cataloginput.Entry{{Key: cataloginput.Value{Kind: "int", Text: "1"}, Value: cataloginput.Value{Kind: "string", Text: "private"}}}})
	out, e := Project(context.Background(), &Loaded{Card: card, Report: &Report{Issues: []Issue{{"digest", "invalid"}}}}, "jobs/normalized/synthetic.yaml", "stamp")
	if e != nil || out["valid"] != false || out["digest"] != nil {
		t.Fatal("unsupported mapping published", out, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, e := Project(ctx, &Loaded{Card: card, Report: &Report{}}, "jobs/normalized/synthetic.yaml", "stamp"); out != nil || e == nil {
		t.Fatal("cancellation published")
	}
}
