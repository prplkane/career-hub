package applicationcontacts

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 2<<20 {
		return 0, os.ErrInvalid
	}
	return b.Buffer.Write(p)
}

func TestPrivateFormattingAndBounds(t *testing.T) {
	private := "synthetic-private-contact@example.invalid"
	for _, v := range []any{Request{Fields: map[string]any{"name": private}}, FactChange{Field: "name", Value: private}, channel{Value: private}, invalid(private)} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			if strings.Contains(fmt.Sprintf(format, v), private) {
				t.Fatal("private data formatted")
			}
		}
	}
	for _, r := range []Request{{Operation: "create", Fields: map[string]any{"name": string([]byte{0xff})}}, {Operation: "create", Fields: map[string]any{"name": strings.Repeat("x", maxRequestBytes+1)}}, {Operation: "create", Fields: map[string]any{"sql": "not accepted"}}} {
		if r.validate() == nil {
			t.Fatal("unsafe request accepted")
		}
	}
	work := 0
	if _, p := similar(context.Background(), "abcdefg", "abcdefx", .92, &work); p == nil {
		t.Fatal("work bound")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	work = 128 << 20
	if _, p := similar(ctx, "abcdefg", "abcdefx", .92, &work); p == nil {
		t.Fatal("cancellation")
	}
}
func TestExactStatedJSONAndMalformedStorage(t *testing.T) {
	f := map[string]string{"name": "Zoë 😀", "role": "R&D", "company": "<>&", "context": "\u007f\u2028\n"}
	m := stated(f, []channel{{Kind: "phone", Value: "+١٢٣٤٥٦٧"}})
	raw := statedJSON(m)
	want := `{"channels":[{"kind":"phone","value":"+\u0661\u0662\u0663\u0664\u0665\u0666\u0667"}],"company":"<>&","context":"\u007f\u2028\n","name":"Zo\u00eb \ud83d\ude00","role":"R&D"}`
	if raw != want {
		t.Fatal("ASCII provenance JSON differs", raw)
	}
	// Use normalized facts when round-tripping persisted source data.
	m["context"] = "Line"
	raw = statedJSON(m)
	if got, p := readStated(raw); p != nil || !reflect.DeepEqual(got, m) {
		t.Fatal("valid provenance changed")
	}
	for _, raw := range []string{`null`, `{}`, `{"name":"x","name":"y"}`, `{"name":"\ud800"}`, `{"name":"\udc00"}`, `{"name":"\ud800a"}`, strings.Repeat(" ", maxCellBytes+1), string([]byte{0xff})} {
		if _, p := readStated(raw); p == nil {
			t.Fatal("malformed provenance accepted")
		}
	}
}
