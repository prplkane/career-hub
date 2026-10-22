package matching

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/resolver"
)

func TestDocumentBlocksRetainBoundariesAndOrder(t *testing.T) {
	m := build(t, resolver.Term{ID: "ml", Name: "Machine learning"}, resolver.Term{ID: "py", Name: "Python"})
	blocks := []Block{{"one", "Machine"}, {"two", "learning"}, {"three", ""}, {"four", "Python Machine\tlearning"}}
	got, err := m.FindBlocks(context.Background(), "source", blocks)
	if err != nil || len(got) != len(blocks) {
		t.Fatalf("invalid document result: %v", err)
	}
	for i, block := range blocks {
		expected, err := m.Find(context.Background(), "source", block.ID, block.Text)
		if err != nil || !reflect.DeepEqual(got[i], *expected) {
			t.Fatal("separate blocks were joined or reordered")
		}
		verifyQuotes(t, block.Text, &got[i])
	}
	if len(got[0].Mentions) != 0 || len(got[1].Mentions) != 0 || len(got[3].Mentions) != 2 {
		t.Fatal("cross-block phrase was invented")
	}
	if got, err := m.FindBlocks(context.Background(), "s", nil); err != nil || got == nil || len(got) != 0 {
		t.Fatal("empty block list must encode as []")
	}
}

func TestDocumentLateFailuresPublishNothing(t *testing.T) {
	m := build(t, resolver.Term{ID: "a", Name: "A"})
	for name, blocks := range map[string][]Block{
		"duplicate":    {{"b", "A"}, {"b", "A"}},
		"invalid-id":   {{"b", "A"}, {"", "A"}},
		"invalid-text": {{"b", "A"}, {"c", "\xff"}},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := m.FindBlocks(context.Background(), "s", blocks); got != nil || err != ErrInput {
				t.Fatalf("invalid document published output: %v", err)
			}
		})
	}
	for name, blocks := range map[string][]Block{
		"blocks":               make([]Block, MaxBlocks+1),
		"aggregate-bytes":      {{"b", strings.Repeat("A", MaxSourceBytes/2)}, {"c", strings.Repeat("A", MaxSourceBytes/2+1)}},
		"aggregate-scalars":    {{"b", strings.Repeat("🚀", MaxSourceRunes/2)}, {"c", strings.Repeat("🚀", MaxSourceRunes/2+1)}},
		"aggregate-results":    {{"b", strings.Repeat("A ", MaxResults/2)}, {"c", strings.Repeat("A ", MaxResults/2+1)}},
		"aggregate-candidates": {{"b", strings.Repeat("Ax ", MaxCandidates/2)}, {"c", strings.Repeat("Ax ", MaxCandidates/2+1)}},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := m.FindBlocks(context.Background(), "s", blocks); got != nil || err != ErrLimit {
				t.Fatalf("document limit not atomic: %v", err)
			}
		})
	}
	for _, args := range []struct {
		ctx      context.Context
		matcher  *Matcher
		sourceID string
	}{{nil, m, "s"}, {context.Background(), nil, "s"}, {context.Background(), m, ""}} {
		if got, err := args.matcher.FindBlocks(args.ctx, args.sourceID, nil); got != nil || err != ErrInput {
			t.Fatal("invalid batch context accepted")
		}
	}
	ctx := &steppedContext{Context: context.Background()}
	blocks := []Block{{"b", "A"}, {"c", "A"}}
	if _, err := m.FindBlocks(ctx, "s", blocks); err != nil {
		t.Fatal(err)
	}
	for stop := 1; stop <= ctx.calls; stop++ {
		if got, err := m.FindBlocks(&steppedContext{Context: context.Background(), stop: stop}, "s", blocks); got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("batch cancellation %d published output", stop)
		}
	}
	t.Logf("%d document cancellation checkpoints", ctx.calls)
}

func TestDocumentSharedResolutionWorkAndOutputBudgets(t *testing.T) {
	terms := make([]resolver.Term, 2000)
	for i := range terms {
		terms[i] = resolver.Term{ID: fmt.Sprintf("%04d", i) + strings.Repeat("i", MaxIDBytes-4), Name: fmt.Sprintf("Term%d", i), Aliases: []string{"Shared"}}
	}
	m := build(t, terms...)
	blocks := make([]Block, 17)
	for i := range blocks {
		blocks[i] = Block{fmt.Sprint(i), "Shared"}
	}
	if got, err := m.FindBlocks(context.Background(), "s", blocks); got != nil || err != ErrLimit {
		t.Fatal("per-block output counters lost document limit")
	}
	// The archive lists are discarded for active ambiguity, but construction and
	// raw resolution work must still count them before hiding overlapping issues.
	for i := range terms {
		terms[i].Archived = i%2 == 0
	}
	terms = append(terms, resolver.Term{ID: "long", Name: "Shared Suite"})
	m = build(t, terms...)
	blocks = make([]Block, 34)
	for i := range blocks {
		blocks[i] = Block{fmt.Sprint(i), "Shared Suite"}
	}
	if got, err := m.FindBlocks(context.Background(), "s", blocks); got != nil || err != ErrLimit {
		t.Fatal("hidden issues bypassed document work budget")
	}
}

func benchmarkFixture() ([]resolver.Term, string) {
	terms := make([]resolver.Term, 2000)
	for i := range terms {
		terms[i] = resolver.Term{ID: fmt.Sprintf("skill_%04d", i), Name: fmt.Sprintf("Skill-%04d", i), Aliases: []string{fmt.Sprintf("Alias %04d", i)}}
	}
	source := strings.Repeat("Build and review reliable tools with Skill-0042 and Alias 0915; preserve exact sources.\n", 100)
	return terms, source
}

func BenchmarkCompileSyntheticCatalog2000(b *testing.B) {
	terms, _ := benchmarkFixture()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := New(context.Background(), terms); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScanSyntheticJD(b *testing.B) {
	terms, source := benchmarkFixture()
	m, err := New(context.Background(), terms)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(source)))
	b.ResetTimer()
	for b.Loop() {
		if result, err := m.Find(context.Background(), "s", "b", source); err != nil || len(result.Mentions) != 200 {
			b.Fatal("synthetic scan failed", err)
		}
	}
}
