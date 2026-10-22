package jobevidence

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
)

type referenceValue struct {
	Kind, Text string
	Boolean    bool `json:"value"`
	Items      []referenceValue
	Entries    [][]referenceValue
}

func (value referenceValue) decoded(t *testing.T) cataloginput.Value {
	t.Helper()
	result := cataloginput.Value{Kind: value.Kind, Text: value.Text, Boolean: value.Boolean}
	for _, item := range value.Items {
		result.Items = append(result.Items, item.decoded(t))
	}
	for _, entry := range value.Entries {
		if len(entry) != 2 {
			t.Fatal("malformed source fixture entry")
		}
		result.Entries = append(result.Entries, cataloginput.Entry{Key: entry[0].decoded(t), Value: entry[1].decoded(t)})
	}
	return result
}

func str(text string) cataloginput.Value { return cataloginput.Value{Kind: "string", Text: text} }
func record(id string) cataloginput.Value {
	return cataloginput.Value{Kind: "mapping", Entries: []cataloginput.Entry{{Key: str("id"), Value: str(id)}, {Key: str("role"), Value: str("Python")}}}
}
func TestEvidenceBoundsCancelAndAtomicErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Normalize(ctx, "Python"); err == nil || err.Code != "EVIDENCE_CANCELED" {
		t.Fatal("canceled normalization")
	}
	if _, err := Flatten(nil, nil); err == nil {
		t.Fatal("nil context accepted")
	}
	for _, text := range []string{"\xff", strings.Repeat("x", MaxTextBytes+1)} {
		if got, err := Normalize(context.Background(), text); err == nil || got != "" {
			t.Fatal("invalid normalization returned text")
		}
	}
	cycle := cataloginput.Value{Kind: "sequence"}
	cycle.Items = []cataloginput.Value{cycle}
	cycle.Items[0].Items = cycle.Items
	if got, err := Flatten(context.Background(), []cataloginput.Value{cycle}); err == nil || got != "" {
		t.Fatal("recursive input escaped bound")
	}
	for _, budget := range []*walkBudget{{ctx: context.Background(), nodes: maxNodes}, {ctx: context.Background(), inputBytes: 32 << 20}} {
		if got, err := flatten(budget, []cataloginput.Value{str("x")}); err == nil || got != "" {
			t.Fatal("aggregate walk budget lost")
		}
	}
	input := Documents{Education: cataloginput.Value{Kind: "mapping"}, Experience: cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{record("private-id"), {Kind: "mapping"}}}}
	got, err := BuildIndex(context.Background(), input)
	if err == nil || got != nil {
		t.Fatal("partial index escaped missing ID")
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, err), "private") {
			t.Fatal("diagnostic leaked evidence")
		}
	}
	if got, err := BuildIndex(nil, input); err == nil || got != nil {
		t.Fatal("nil context built index")
	}
	input.Experience.Items = make([]cataloginput.Value, MaxSources+1)
	if got, err := BuildIndex(context.Background(), input); err == nil || err.Code != "EVIDENCE_INDEX_LIMIT" || got != nil {
		t.Fatal("index source count unbounded")
	}
}

func TestEvidenceQueriesFailAtomicallyAndNeverPromoteHints(t *testing.T) {
	index := Index{"hint": {Text: "python", Kind: "taxonomy", EvidenceIDs: []string{"skill"}}}
	got, err := Find(context.Background(), "Python", index)
	if err != nil || len(got.Direct) != 0 || !reflect.DeepEqual(got.Taxonomy, []string{"skill"}) {
		t.Fatal("taxonomy became direct evidence")
	}
	for _, source := range []Source{
		{Text: "python", Kind: "unknown"}, {Text: "\xff", Kind: "direct"},
		{Text: "python", Kind: "direct", EvidenceIDs: []string{"\xff"}},
		{Text: strings.Repeat("x", MaxIndexBytes+1), Kind: "direct"},
	} {
		bad := Index{"good": {Text: "python", Kind: "direct", EvidenceIDs: []string{"real"}}, "bad": source}
		if got, err := Find(context.Background(), "Python", bad); err == nil || !reflect.DeepEqual(got, Matches{}) {
			t.Fatal("invalid index returned partial matches")
		}
	}
	if got, err := Find(nil, "Python", index); err == nil || !reflect.DeepEqual(got, Matches{}) {
		t.Fatal("canceled query returned matches")
	}
}
