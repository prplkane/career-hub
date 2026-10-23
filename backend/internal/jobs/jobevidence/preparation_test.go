package jobevidence

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/cataloginput"
)

func object(fields ...cataloginput.Entry) cataloginput.Value {
	return cataloginput.Value{Kind: "mapping", Entries: fields}
}
func member(name string, value cataloginput.Value) cataloginput.Entry {
	return cataloginput.Entry{Key: str(name), Value: value}
}
func sequence(values ...cataloginput.Value) cataloginput.Value {
	return cataloginput.Value{Kind: "sequence", Items: values}
}
func overlay(terms ...cataloginput.Value) cataloginput.Value {
	return sequence(object(member("id", str("reviewed")), member("aliases", sequence(str("Match"))), member("evidence_terms", sequence(terms...))))
}

type cancelAfter struct {
	context.Context
	calls, after int
}

func (c *cancelAfter) Err() error {
	c.calls++
	if c.calls >= c.after {
		return context.Canceled
	}
	return nil
}

func TestPreparationLimitsAndAtomicCancellation(t *testing.T) {
	ctx, today := context.Background(), time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	if got, err := LoadConcepts(nil, overlay(str("SQL"))); err == nil || got != nil {
		t.Fatal("nil context accepted")
	}
	for _, value := range []cataloginput.Value{sequence(make([]cataloginput.Value, MaxConcepts+1)...), overlay(make([]cataloginput.Value, MaxConceptTerms+1)...), overlay(str(strings.Repeat("x", MaxTextBytes+1))), overlay(str("\xff"))} {
		if got, err := LoadConcepts(ctx, value); err == nil || got != nil {
			t.Fatal("concept bound returned partial overlay")
		}
	}
	terms := make([]cataloginput.Value, MaxConceptTerms)
	for i := range terms {
		terms[i] = str("SQL")
	}
	concepts, err := LoadConcepts(ctx, overlay(terms...))
	if err != nil {
		t.Fatal(err)
	}
	index := Index{"direct": {Text: strings.Repeat("x", 1<<20), Kind: "direct", EvidenceIDs: []string{"private"}}}
	if got, err := FindConcepts(ctx, "Match", index, concepts); err == nil || err.Code != "EVIDENCE_QUERY_LIMIT" || !reflect.DeepEqual(got, ConceptMatches{}) {
		t.Fatal("cumulative query work escaped bound")
	}
	if got, err := FindConcepts(nil, "Match", index, concepts); err == nil || !reflect.DeepEqual(got, ConceptMatches{}) {
		t.Fatal("canceled concept query returned matches")
	}
	if got, err := FindConcepts(ctx, "SQL", Index{"hint": {Text: "sql", Kind: "taxonomy", EvidenceIDs: []string{"s"}}}, nil); err != nil || len(got.Direct) != 0 || len(got.Taxonomy) != 1 {
		t.Fatal("nil overlay lost hint separation")
	}
	for _, text := range []string{"\xff", strings.Repeat("x", MaxTextBytes+1)} {
		if got, err := DateRangeMonths(ctx, text, today); err == nil || got != nil {
			t.Fatal("legacy text bounds lost")
		}
	}
	if got, err := DateRangeMonths(ctx, "2020 - 2021", time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)); err == nil || got != nil {
		t.Fatal("out-of-source calendar accepted")
	}
	good := object(member("id", str("private")), member("dates", str("2020 - 2021")))
	bad := object(member("id", str("bad")), member("dates", cataloginput.Value{Kind: "bool", Boolean: true}))
	if got, err := ExperienceDurations(ctx, sequence(good, bad), today); err == nil || got != nil {
		t.Fatal("partial durations escaped late unsupported input")
	}
	if got, err := ExperienceDurations(ctx, sequence(make([]cataloginput.Value, MaxSources+1)...), today); err == nil || got != nil {
		t.Fatal("duration count limit lost")
	}
	for _, call := range []func(context.Context) bool{
		func(ctx context.Context) bool {
			got, err := LoadConcepts(ctx, overlay(str("SQL")))
			return err != nil && got == nil
		},
		func(ctx context.Context) bool {
			got, err := ExperienceDurations(ctx, sequence(good), today)
			return err != nil && got == nil
		},
		func(ctx context.Context) bool {
			got, err := DateRangeMonths(ctx, "2020 - 2021", today)
			return err != nil && got == nil
		},
	} {
		if !call(&cancelAfter{Context: context.Background(), after: 3}) {
			t.Fatal("late cancellation published evidence")
		}
	}
	for _, text := range []string{"\xff", strings.Repeat("x", MaxTextBytes+1)} {
		if got, err := TechnologyGroupLabel(ctx, []string{text}); err == nil || got != "" {
			t.Fatal("invalid label escaped")
		}
	}
}

func TestExpansionBudgetCyclesAndNoInputMutation(t *testing.T) {
	ctx := context.Background()
	if got, err := BuildExpansions(nil, nil); err == nil || got != nil {
		t.Fatal("nil context accepted")
	}
	terms := []catalog.Term{{ID: "a", Name: "A", Aliases: []string{"alias"}}, {ID: "b", Name: "B", Parent: pointer("a")}}
	before, _ := json.Marshal(terms)
	got, err := BuildExpansions(ctx, terms)
	after, _ := json.Marshal(terms)
	if err != nil || string(before) != string(after) || !reflect.DeepEqual(got["a"], []string{"B"}) {
		t.Fatal("expansion changed input/order")
	}
	for _, values := range [][]catalog.Term{make([]catalog.Term, MaxSources+1), {{ID: "a", Name: "\xff"}}, {{ID: "a", Name: strings.Repeat("x", MaxIndexBytes+1)}}} {
		if got, err := BuildExpansions(ctx, values); err == nil || got != nil {
			t.Fatal("invalid catalog returned partial expansion")
		}
	}
	chain := make([]catalog.Term, 1100)
	for i := range chain {
		chain[i] = catalog.Term{ID: fmt.Sprint(i), Name: ""}
		if i > 0 {
			chain[i].Parent = pointer(fmt.Sprint(i - 1))
		}
	}
	if got, err := BuildExpansions(ctx, chain); err == nil || err.Code != "EVIDENCE_EXPANSION_LIMIT" || got != nil {
		t.Fatal("aggregate zero-byte graph work was unbounded")
	}
}
func pointer(value string) *string { return &value }
