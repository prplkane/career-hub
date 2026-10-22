package discovery

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func baseCaptureRequest() CaptureRequest {
	return CaptureRequest{"source_kind": "extension", "source_channel": "browser", "request_fingerprint": "r1", "title": "Job"}
}
func TestCaptureInputBoundsAndTypes(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		field         string
		value         any
		code, message string
	}{
		{"source_kind", nil, "", "source_kind is unsupported"},
		{"content_scope", nil, "", "content_scope is unsupported"},
		{"source_channel", false, "", "source_channel must be a string"},
		{"title", nil, "", "title must be a string"},
		{"title", "\xff", "INPUT_UNSUPPORTED", ""},
		{"title", strings.Repeat("界", 501), "", "title is invalid or exceeds its limit"},
		{"posting_identity", strings.Repeat("ß", 300), "", ""}, // source bounds before casefold expansion
		{"posting_identity", strings.Repeat("x", 301), "", "posting_identity is invalid or exceeds its limit"},
		{"source_text", strings.Repeat("a", 500001), "", "source_text exceeds the configured character limit"},
		{"source_text", strings.Repeat(" ", (4<<20)+1), "INPUT_LIMIT", ""},
		{"source_text", "secret\x00text", "", "source_text contains a null byte"},
		{"source_text", "\xff", "INPUT_UNSUPPORTED", ""},
		{"observed_url", "https://person:password@example.invalid/job", "", "observed_url must not contain user information"},
		{"observed_at", false, "", "observed_at must be a non-empty ISO timestamp"},
		{"observed_at", "bad", "", "observed_at must use ISO format"},
		{"expected_target_revision", 1, "", "expected_target_revision requires target_discovery_id"},
		{"metadata", map[string]any{"x": math.Inf(1)}, "", "metadata must contain only finite JSON values"},
		{"metadata", map[string]any{"x": json.Number("NaN")}, "", "metadata must contain only JSON values"},
		{"metadata", map[string]any{"x": json.Number("1e999")}, "", "metadata must contain only finite JSON values"},
		{"metadata", map[string]any{"x": json.Number(strings.Repeat("1", 4097))}, "INPUT_LIMIT", ""},
		{"metadata", map[string]any{"x": "\xff"}, "INPUT_UNSUPPORTED", ""},
		{"metadata", map[string]any{"\xff": "x"}, "INPUT_UNSUPPORTED", ""},
		{"metadata", map[string]any{"x": func() {}}, "", "metadata must contain only JSON values"},
		{"metadata", map[string]any{"x": strings.Repeat("a", 4096)}, "", "metadata exceeds the configured byte limit"},
		{"metadata", []any{1}, "", "metadata must be a mapping"},
	} {
		input := baseCaptureRequest()
		input[tc.field] = tc.value
		got, e := normalizeCapture(ctx, input, "2026-09-09T12:00:00Z")
		if tc.code == "" && tc.message == "" {
			if e != nil || len(got.identity) != 600 {
				t.Fatal("fold expansion", e)
			}
			continue
		}
		if e == nil || tc.code != "" && e.Code != tc.code || tc.message != "" && e.SourceMessage != tc.message {
			t.Fatalf("%s: got %+v want %s/%s", tc.field, e, tc.code, tc.message)
		}
	}
	input := baseCaptureRequest()
	input["extra"] = "field"
	if _, e := normalizeCapture(ctx, input, ""); e == nil || e.Code != "CAPTURE_FIELDS_UNSUPPORTED" {
		t.Fatal("unknown field")
	}
	input = baseCaptureRequest()
	input["source_text"] = strings.Repeat("界", 500000)
	if p, e := normalizeCapture(ctx, input, ""); e != nil || len(p.text) != 1500000 {
		t.Fatal("valid max Unicode source", e)
	}
	if _, e := normalizeCapture(nil, input, ""); e == nil || e.Code != "CONTEXT_REQUIRED" {
		t.Fatal("nil context")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := normalizeCapture(canceled, input, ""); e == nil || e.Code != "WRITE_CANCELED" {
		t.Fatal("cancellation")
	}
	if _, e := CaptureInTransaction(ctx, nil, nil, baseCaptureRequest(), "", nil); e == nil || e.Code != "DATABASE_CONNECTION_INVALID" {
		t.Fatal("nil transaction")
	}
}

func TestCaptureJSONBoundsAndDeterminism(t *testing.T) {
	ctx := context.Background()
	cyclic := map[string]any{}
	cyclic["child"] = cyclic
	if _, e := captureJSON(ctx, cyclic, 4096, true); e == nil || e.Code != "INPUT_LIMIT" {
		t.Fatal("cycle/depth budget")
	}
	if _, e := captureJSON(ctx, make([]any, 8193), 4096, true); e == nil || e.Code != "INPUT_LIMIT" {
		t.Fatal("node budget")
	}
	if _, e := captureJSON(nil, nil, 4096, true); e == nil {
		t.Fatal("nil encoder context")
	}
	input := map[string]any{"z": math.Copysign(0, -1), "a": []any{1.0, 1e15, 1e16, "<>\u2028😀"}}
	want := "{\"a\":[1.0,1000000000000000.0,1e+16,\"<>\u2028😀\"],\"z\":-0.0}"
	for range 20 {
		got, e := captureJSON(ctx, input, 4096, true)
		if e != nil || got != want {
			t.Fatalf("canonical JSON %s/%v", got, e)
		}
	}
	for _, v := range []any{json.Number(""), json.Number("true"), json.Number("01"), json.Number("--1")} {
		if _, e := captureJSON(ctx, map[string]any{"v": v}, 4096, true); e == nil {
			t.Fatal("invalid number accepted")
		}
	}
	inputRequest := baseCaptureRequest()
	inputRequest["metadata"] = input
	before, _ := json.Marshal(inputRequest)
	if _, e := normalizeCapture(ctx, inputRequest, ""); e != nil {
		t.Fatal(e)
	}
	after, _ := json.Marshal(inputRequest)
	if string(before) != string(after) {
		t.Fatal("input mutated")
	}
}

func TestCapturePlanPrivacyAndRawSourceSemantics(t *testing.T) {
	change := SourceChange{RelativePath: "jobs/discovery/captures/a/source.txt", BeforePresent: true, AfterPresent: true, Before: []byte("old private text"), After: []byte("new private text")}
	for _, value := range []any{CaptureResult{Source: &change}, DeleteResult{Sources: []SourceChange{change}}} {
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		var object map[string]any
		if json.Unmarshal(raw, &object) != nil {
			t.Fatal("result shape")
		}
		if _, ok := object["Source"]; ok {
			t.Fatal("source preimage leaked")
		}
		if _, ok := object["Sources"]; ok {
			t.Fatal("source removal preimages leaked")
		}
	}
	text := "First\r\nSecond"
	record := Record{SourceRelativePath: change.RelativePath, ContentSHA256: sourceSHA(text), ContentChars: int64(len(text))}
	files := &captureFault{raw: []byte(text), present: true}
	if raw, e := rawCaptureSource(files, record); e != nil || string(raw) != text {
		t.Fatal("raw capture verification normalized bytes", e)
	}
	record.ContentSHA256 = sourceSHA("First\nSecond")
	if _, e := rawCaptureSource(files, record); e == nil {
		t.Fatal("raw capture hash mismatch hidden")
	}
}
