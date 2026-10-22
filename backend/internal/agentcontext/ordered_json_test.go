package agentcontext

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
)

func TestOrderedContextJSONRefusals(t *testing.T) {
	ctx := context.Background()
	for _, raw := range []string{
		``, `{`, `null null`, `1 2`, `NaN`, `[Infinity]`, `{"key":1e309}`, `{"key":-1e999}`,
		`"\ud800"`, `"\udfff"`, `"\ud800\ud800"`, `"\ud800x"`, `"\ud800\\udc00"`,
		`{"\ud800":0}`, string([]byte{'"', 0xff, '"'}), "\xef\xbb\xbf{}",
		strings.Repeat("9", 4301), "-" + strings.Repeat("9", 4301),
		strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65),
		"[" + strings.Repeat("0,", 200000) + "0]",
		`"` + strings.Repeat("a", contextJSONLimit) + `"`,
	} {
		_, problem := decodeOrderedContextJSON(ctx, []byte(raw))
		if problem == nil {
			t.Fatalf("accepted unsupported input %.70s", raw)
		}
	}
	bad := []cataloginput.Value{
		{Kind: "date", Text: "2026-09-26"}, {Kind: "bytes", Bytes: []byte("private")},
		{Kind: "string", Text: string([]byte{0xff})}, {Kind: "float", Text: "NaN"},
		{Kind: "float", Text: "1e999"}, {Kind: "int", Text: "1.0"}, {Kind: "int", Text: "01"},
		{Kind: "int", Text: "1 "}, {Kind: "float", Text: "1 "}, {Kind: "int", Text: "null"},
		{Kind: "mapping", Entries: []cataloginput.Entry{{Key: cataloginput.Value{Kind: "int", Text: "1"}, Value: cataloginput.Value{Kind: "null"}}}},
		{Kind: "mapping", Entries: []cataloginput.Entry{
			{Key: cataloginput.Value{Kind: "string", Text: "duplicate"}, Value: cataloginput.Value{Kind: "null"}},
			{Key: cataloginput.Value{Kind: "string", Text: "duplicate"}, Value: cataloginput.Value{Kind: "null"}},
		}},
	}
	cyclic := cataloginput.Value{Kind: "sequence", Items: make([]cataloginput.Value, 1)}
	cyclic.Items[0] = cyclic
	bad = append(bad, cyclic)
	for i, value := range bad {
		raw, problem := encodeOrderedContextJSON(ctx, value, false)
		if problem == nil || raw != nil {
			t.Fatalf("unsupported value %d: partial output or no error", i)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, problem := decodeOrderedContextJSON(canceled, []byte(`{}`)); problem == nil {
		t.Fatal("decode ignored cancellation")
	}
	if raw, problem := encodeOrderedContextJSON(canceled, cataloginput.Value{Kind: "null"}, false); problem == nil || raw != nil {
		t.Fatal("encode ignored cancellation")
	}
	// Exercise every append boundary, including punctuation and expanded C0
	// escapes, with a small internal budget instead of allocating huge outputs.
	value, problem := decodeOrderedContextJSON(ctx, []byte(`{"key":["\u0000",1.0,true,null]}`))
	if problem != nil {
		t.Fatal(problem)
	}
	full, problem := encodeOrderedContextJSON(ctx, value, false)
	if problem != nil {
		t.Fatal(problem)
	}
	for size := 0; size <= len(full); size++ {
		encoder := orderedContextEncoder{ctx: ctx, maximum: size}
		err := encoder.value(value, 0)
		if (err == nil) != (size == len(full)) || len(encoder.output) > size {
			t.Fatalf("incorrect output bound %d", size)
		}
	}
}

func TestOrderedContextJSONIdentityDistinguishesOrder(t *testing.T) {
	ctx := context.Background()
	first, err := decodeOrderedContextJSON(ctx, []byte(`{"z":{"b":1,"a":2},"a":true}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := decodeOrderedContextJSON(ctx, []byte(`{"a":true,"z":{"a":2,"b":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, sorted := range []bool{false, true} {
		a, err := encodeOrderedContextJSON(ctx, first, sorted)
		if err != nil {
			t.Fatal(err)
		}
		b, err := encodeOrderedContextJSON(ctx, second, sorted)
		if err != nil {
			t.Fatal(err)
		}
		if (sha256.Sum256(a) == sha256.Sum256(b)) != sorted {
			t.Fatalf("order identity policy failed, sorted=%v", sorted)
		}
	}
}
