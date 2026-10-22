package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestEditRequestBoundariesAndPrecedence(t *testing.T) {
	base := EditRequest{Operation: "confirm", DiscoveryID: " discovery ", ExpectedRevision: int64(1)}
	valid, e := normalizeEdit(base)
	if e != nil || valid.id != "discovery" || valid.revision != 1 {
		t.Fatal("normalization", e)
	}
	for _, revision := range []any{nil, true, false, float64(1), json.Number("1.0"), "1", int64(-1), int64(0)} {
		input := base
		input.ExpectedRevision = revision
		if _, e := normalizeEdit(input); e == nil || e.SourceMessage != "expected_revision must be a positive integer" {
			t.Fatal("revision type/value", revision, e)
		}
	}
	input := base
	input.ExpectedRevision = json.Number("9223372036854775808")
	if _, e := normalizeEdit(input); e == nil || e.Code != "REVISION_LIMIT" {
		t.Fatal("integer overflow")
	}
	for _, id := range []string{"", ".", "C:job", "é:job", "a/b", "a\\b"} {
		input := base
		input.DiscoveryID = id
		if _, e := normalizeEdit(input); e == nil || e.SourceMessage != "discovery_id must be a plain identifier" {
			t.Fatal("identifier", id, e)
		}
	}
	input = base
	input.DiscoveryID = "\xff"
	if _, e := normalizeEdit(input); e == nil || e.Code != "INPUT_UNSUPPORTED" {
		t.Fatal("Unicode boundary")
	}
	input = base
	input.DiscoveryID = strings.Repeat(" ", (2<<20)+1)
	if _, e := normalizeEdit(input); e == nil || e.Code != "INPUT_LIMIT" {
		t.Fatal("raw input budget")
	}
	input = base
	input.Operation = "transition"
	input.State = "promoted"
	input.ExpectedRevision = false
	if _, e := normalizeEdit(input); e == nil || e.SourceMessage != "state cannot be set through the discovery lifecycle editor" {
		t.Fatal("state precedence")
	}
	input = base
	input.Operation = "promote"
	input.DiscoveryID = ""
	input.JobID = false
	if _, e := normalizeEdit(input); e == nil || e.SourceMessage != "job_id must be a string" {
		t.Fatal("promotion validation order")
	}
	input = base
	input.Operation = "overrides"
	input.ExpectedRevision = 0
	input.Overrides = map[string]any{"title": nil, "company": nil, "location": nil}
	if _, e := normalizeEdit(input); e != nil {
		t.Fatal("null clears", e)
	}
	input.Overrides = map[string]any{"title": strings.Repeat("界", 501), "company": nil, "location": nil}
	if _, e := normalizeEdit(input); e == nil || e.SourceMessage != "overrides.title is invalid or exceeds its limit" {
		t.Fatal("scalar count")
	}
	if e := nextRevision(math.MaxInt64); e == nil || e.Code != "REVISION_LIMIT" {
		t.Fatal("stored increment overflow")
	}
	if _, e := EditInTransaction(context.Background(), nil, nil, base, ""); e == nil || e.Code != "DATABASE_CONNECTION_INVALID" {
		t.Fatal("unowned transaction")
	}
	if _, e := EditInTransaction(nil, nil, nil, base, ""); e == nil || e.Code != "DATABASE_CONNECTION_INVALID" {
		t.Fatal("nil context")
	}
}
func TestDiscoveryWriteErrorFormatting(t *testing.T) {
	e := sourceFailure("DiscoveryNotFound", "private synthetic value")
	for _, out := range []string{fmt.Sprint(e), fmt.Sprintf("%+v", e), fmt.Sprintf("%#v", e), fmt.Sprintf("%q", e)} {
		if strings.Contains(out, "private") {
			t.Fatal("private diagnostic formatting")
		}
	}
}
