package applicationreview

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestReviewBoundsAndPrivacy(t *testing.T) {
	cyclic := map[string]any{}
	cyclic["same"] = cyclic
	for _, value := range []any{math.NaN(), math.Inf(1), "\xff", strings.Repeat("x", maxInputBytes+1), cyclic, make([]any, 10001), json.Number("1e999"), []byte("unexpected")} {
		if boundedJSON(value) {
			t.Fatal("unsafe/unbounded value admitted")
		}
	}
	for _, request := range []Request{{}, {Operation: "other", Fields: map[string]any{}}, {Operation: "get_assessment", Fields: map[string]any{"path": "private"}}} {
		if request.validate() == nil {
			t.Fatal("invalid request admitted")
		}
	}
	for _, value := range []any{Request{Operation: "save_assessment", Fields: map[string]any{"assessment": "private-value"}}, &Error{SourceMessage: "private-value"}} {
		for _, pattern := range []string{"%v", "%+v", "%#v", "%s"} {
			if strings.Contains(fmt.Sprintf(pattern, value), "private-value") {
				t.Fatal("private formatting")
			}
		}
	}
}

func TestReviewAssessmentSerializationAndSnapshotTruth(t *testing.T) {
	got, p := assessmentJSON(map[string]any{"a": []any{float64(1), math.Copysign(0, -1), json.Number("18446744073709551617")}, "z": "\\u2028\u2028\x7f<&😀"})
	want := "{\"a\":[1.0,-0.0,18446744073709551617],\"z\":\"\\\\u2028\u2028\x7f<&😀\"}"
	if p != nil || string(got) != want {
		t.Fatalf("source JSON spelling %s", got)
	}
	if _, p := assessmentJSON(map[string]any{"x": strings.Repeat("\x00", maxCellBytes/6+1)}); p == nil {
		t.Fatal("expanded serialized cell unbounded")
	}
	item, p := instructionSnapshot(map[string]any{"fingerprint": strings.Repeat("a", 64), "category": "exact_phrase", "confidence": "review", "source_text": "  exact evidence  ", "line_start": 1, "line_end": 1, "extractor_version": 0, "ignored": "not a stored fact"}, "manual")
	if p != nil || item["source_text"] != "  exact evidence  " || len(item) != 8 {
		t.Fatal("snapshot changed source wording or retained extra input")
	}
}

func TestReviewStoredJSONDoesNotReplaceUnpairedSurrogates(t *testing.T) {
	for _, raw := range []string{`{"a":"\ud800"}`, `{"a":"\udc00"}`, `{"a":"\ud800","a":"later"}`, `{"\ud800":"key"}`, `{"a":` + strings.Repeat("[", 33) + `0` + strings.Repeat("]", 33) + `}`} {
		if !json.Valid([]byte(raw)) || storedJSONSafe(raw) {
			t.Fatal("lossy/deep stored value admitted")
		}
	}
	for _, raw := range []string{`{"a":"\\ud800"}`, `{"a":"\ud83d\ude00"}`, `{"a":"brace [ } and quote \""}`} {
		if !json.Valid([]byte(raw)) || !storedJSONSafe(raw) {
			t.Fatal("valid lexical string refused")
		}
	}
	if storedJSONSafe(`{"x":` + strings.Repeat("1", 4097) + `,"x":0}`) {
		t.Fatal("overwritten oversized integer hidden by decoding")
	}
	value := map[string]any{"numbers": []any{json.Number("1e0"), json.Number("-0"), json.Number("0.00010"), json.Number("10000000000000000.0")}}
	if p := normalizeStoredNumbers(value); p != nil {
		t.Fatal(p)
	}
	raw, err := json.Marshal(value)
	if err != nil || string(raw) != `{"numbers":[1.0,0,0.0001,1e+16]}` {
		t.Fatal("stored numeric spelling", string(raw))
	}
}

func TestReviewNonfiniteStorageIsExplicitlyUnsupported(t *testing.T) {
	for _, raw := range []string{`{"x":NaN}`, `{"x":[Infinity,-Infinity]}`} {
		if !unsupportedJSONConstants(raw) {
			t.Fatal("nonfinite extension not classified")
		}
	}
	for _, raw := range []string{`{"x":"NaN"}`, `{"x":NaN`, `{"x":NotANumber}`, `{"x":"Infinity\""}`} {
		if unsupportedJSONConstants(raw) {
			t.Fatal("malformed or ordinary JSON misclassified")
		}
	}
}
