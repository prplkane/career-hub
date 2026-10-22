package publicjobresolver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDiagnosticClosedState(t *testing.T) {
	r, err := NewDiagnostic()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeDiagnostic(r)
	if err != nil {
		t.Fatal(err)
	}
	if got, e := DecodeDiagnostic(raw); e != nil || got != r {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Diagnostic){func(r *Diagnostic) { r.Operation = "arbitrary" }, func(r *Diagnostic) { r.Stage = "private.example" }, func(r *Diagnostic) { r.ErrorCode = "private cause" }, func(r *Diagnostic) {
		r.Sequence = 2
		r.State = "failed"
		r.ErrorCode = "PUBLIC_JOB_RESOLUTION_FAILED"
		r.Committed = true
	}, func(r *Diagnostic) { r.Strategy = "private URL" }, func(r *Diagnostic) { r.ResponseBytes = MaxResponseBytes + 1 }} {
		bad := r
		mutate(&bad)
		if _, e := EncodeDiagnostic(bad); e == nil {
			t.Fatal("unsafe record admitted", bad)
		}
	}
	r.Sequence = 2
	r.State = "success"
	r.Stage = "complete"
	r.Outcome = "duplicate"
	r.Strategy = "semantic_container"
	if _, e := EncodeDiagnostic(r); e != nil {
		t.Fatal(e)
	}
	var doc map[string]any
	json.Unmarshal(raw, &doc)
	doc["url"] = "secret"
	tampered, _ := json.Marshal(doc)
	if _, e := DecodeDiagnostic(tampered); e == nil {
		t.Fatal("unknown diagnostic field admitted")
	}
}
func TestNonfiniteTokenBoundaries(t *testing.T) {
	for _, raw := range []string{`[NaN1]`, `[1NaN]`, `[Infinity0]`, `{NaN:1}`, `{"x":-NaN}`, `[NaNtrue]`} {
		converted, _ := pythonJSONConstants(raw)
		if json.Valid([]byte(converted)) {
			t.Fatalf("invalid JSON became valid: %s -> %s", raw, converted)
		}
	}
	converted, token := pythonJSONConstants(`{"x":NaN,"original":1e99999,"text":"NaN and Infinity"}`)
	if token == "1e99999" || !json.Valid([]byte(converted)) || !strings.Contains(converted, `"NaN and Infinity"`) {
		t.Fatal("token collision or string mutation")
	}
}
