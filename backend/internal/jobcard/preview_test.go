package jobcard

import (
	"context"
	"strings"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
)

func TestDiscoveryProjectionKeepsSeparateManagedSourceBoundary(t *testing.T) {
	for _, path := range []string{"jobs/discovery/captures/synthetic.txt", "jobs/incoming/synthetic.txt", "jobs/discovery/captures/../escape.txt", "jobs/discovery/captures//x.txt"} {
		card := cardFixture(t)
		put(&card, "raw_jd_path", cataloginput.Value{Kind: "string", Text: path})
		calls := 0
		check := func(context.Context, string) (bool, *Error) { calls++; return true, nil }
		report, err := ValidateDiscoveryPreview(context.Background(), card, check)
		want := path == "jobs/discovery/captures/synthetic.txt"
		if err != nil || report.SchemaValid() != want || calls != map[bool]int{true: 1, false: 0}[want] {
			t.Fatal("discovery source boundary")
		}
		report, err = Validate(context.Background(), card, check)
		if err != nil || report.SchemaValid() != (path == "jobs/incoming/synthetic.txt") {
			t.Fatal("stored job source boundary widened")
		}
	}
	card := cardFixture(t)
	put(&card, "raw_jd_path", cataloginput.Value{Kind: "string", Text: "jobs/discovery/captures/synthetic.txt"})
	put(&card, "status", cataloginput.Value{Kind: "string", Text: "unsupported"})
	report, err := ValidateDiscoveryPreview(context.Background(), card, syntheticCheck)
	if err != nil || report.SchemaValid() || !strings.Contains(report.Issues[0].Field, "status") {
		t.Fatal("preview skipped full schema")
	}
}
