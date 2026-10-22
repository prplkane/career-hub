package matching

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/resolver"
	"careerhub.local/backend/internal/textcompat"
)

func build(t *testing.T, terms ...resolver.Term) *Matcher {
	t.Helper()
	m, err := New(context.Background(), terms)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCompleteSpansLongestAmbiguityAndArchive(t *testing.T) {
	m := build(t,
		resolver.Term{ID: "ml", Name: "Machine learning"},
		resolver.Term{ID: "learning", Name: "Learning"},
		resolver.Term{ID: "strasse", Name: "Straße"},
		resolver.Term{ID: "shared_a", Name: "Cloud A", Aliases: []string{"Shared"}},
		resolver.Term{ID: "shared_b", Name: "Cloud B", Aliases: []string{"Shared"}},
		resolver.Term{ID: "old", Name: "Shared", Archived: true},
		resolver.Term{ID: "legacy", Name: "Legacy", Archived: true},
	)
	source := "🚀 Machine\t  learning. STRASSE; Shared Legacy"
	got, err := m.Find(context.Background(), "source", "block", source)
	if err != nil {
		t.Fatal(err)
	}
	verifyQuotes(t, source, got)
	if len(got.Mentions) != 2 || got.Mentions[0].SourceRef.Start != 2 || got.Mentions[0].SourceText != "Machine\t  learning" || got.Mentions[1].TechnologyID != "strasse" {
		t.Fatalf("wrong complete spans: %+v", got)
	}
	if len(got.Issues) != 2 || got.Issues[0].Status != resolver.Ambiguous || len(got.Issues[0].ArchivedCandidateIDs) != 0 || got.Issues[1].ArchivedCandidateIDs[0] != "legacy" {
		t.Fatalf("wrong issues: %+v", got)
	}
}

func TestImmutableConcurrentReuse(t *testing.T) {
	terms := []resolver.Term{{ID: "a", Name: "Original", Aliases: []string{"Alias"}}, {ID: "b", Name: "Second", Aliases: []string{"Shared"}}, {ID: "c", Name: "Third", Aliases: []string{"Shared"}}}
	m := build(t, terms...)
	terms[0].Name = "Changed"
	terms[0].Aliases[0] = "Replaced"
	terms[1].Archived = true
	terms[2].ID = "mutated"
	want, err := m.Find(context.Background(), "source", "block", "Alias Shared")
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 20 {
				got, err := m.Find(context.Background(), "source", "block", "Alias Shared")
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Error("input/result aliasing or nondeterministic reuse")
					return
				}
				got.Mentions[0].CanonicalName = "mutated"
				got.Issues[0].CandidateIDs[0] = "mutated"
			}
		}()
	}
	wait.Wait()
}

func TestInvalidInputIsPrivateAndAtomic(t *testing.T) {
	for name, terms := range map[string][]resolver.Term{
		"duplicate":   {{ID: "secret", Name: "A"}, {ID: "secret", Name: "B"}},
		"empty-id":    {{Name: "A"}},
		"space-id":    {{ID: " \t", Name: "A"}},
		"bad-id":      {{ID: "\xff", Name: "A"}},
		"long-id":     {{ID: strings.Repeat("x", MaxIDBytes+1), Name: "A"}},
		"bad-name":    {{ID: "a", Name: "\xff"}},
		"blank-name":  {{ID: "a", Name: " \u0085\u001f"}},
		"bad-alias":   {{ID: "a", Name: "A", Aliases: []string{"\xff"}}},
		"blank-alias": {{ID: "a", Name: "A", Aliases: []string{""}}},
	} {
		t.Run(name, func(t *testing.T) {
			m, err := New(context.Background(), terms)
			if m != nil || !errors.Is(err, ErrInput) {
				t.Fatalf("expected safe invalid refusal: %v", err)
			}
		})
	}
	if m, err := New(nil, nil); m != nil || err != ErrInput {
		t.Fatal("nil constructor context accepted")
	}
	m := build(t, resolver.Term{ID: "a", Name: "A"})
	for _, input := range [][3]string{{"", "b", "A"}, {"s", "", "A"}, {"s", "b", "\xff"}, {"s", "\xff", "A"}} {
		if got, err := m.Find(context.Background(), input[0], input[1], input[2]); got != nil || err != ErrInput {
			t.Fatal("invalid find input accepted")
		}
	}
	for _, matcher := range []*Matcher{nil, {}} {
		if got, err := matcher.Find(context.Background(), "s", "b", "A"); got != nil || err != ErrInput {
			t.Fatal("uninitialized matcher accepted")
		}
	}
	if got, err := m.Find(nil, "s", "b", "A"); got != nil || err != ErrInput {
		t.Fatal("nil find context accepted")
	}
	for _, matcher := range []*Matcher{m, build(t)} {
		for _, source := range []string{"", "nothing"} {
			got, err := matcher.Find(context.Background(), "s", "b", source)
			if err != nil || got.Mentions == nil || got.Issues == nil {
				t.Fatal("empty result must encode as arrays")
			}
		}
	}
}

func TestAggregateLimitsRejectInsteadOfTruncate(t *testing.T) {
	m := build(t, resolver.Term{ID: "a", Name: "A"})
	for name, source := range map[string]string{
		"byte-limit":      strings.Repeat("x", MaxSourceBytes+1),
		"scalar-limit":    strings.Repeat("x", MaxSourceRunes+1),
		"candidate-limit": strings.Repeat("A ", MaxCandidates+1),
		"result-limit":    strings.Repeat("A ", MaxResults+1),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := m.Find(context.Background(), "s", "b", source); got != nil || err != ErrLimit {
				t.Fatalf("expected atomic limit: %v", err)
			}
		})
	}
	for name, terms := range map[string][]resolver.Term{
		"terms":            make([]resolver.Term, MaxTerms+1),
		"alias-count":      {{ID: "a", Name: "A", Aliases: make([]string, MaxValues)}},
		"value":            {{ID: "a", Name: strings.Repeat("A", MaxValueBytes+1)}},
		"normalized-value": {{ID: "a", Name: strings.Repeat("\u0390", MaxValueBytes/2)}},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := New(context.Background(), terms); got != nil || err != ErrLimit {
				t.Fatalf("constructor limit not atomic: %v", err)
			}
		})
	}
	// Identical aliases cannot bypass aggregate input accounting through dedup.
	aliases := make([]string, MaxCatalogBytes/MaxValueBytes+1)
	for i := range aliases {
		aliases[i] = strings.Repeat("x", MaxValueBytes)
	}
	if got, err := New(context.Background(), []resolver.Term{{ID: "a", Name: "A", Aliases: aliases}}); got != nil || err != ErrLimit {
		t.Fatal("dedup bypassed catalog budget")
	}
	// Distinct long phrases stay within catalog bytes but exceed automaton nodes.
	aliases = make([]string, 70)
	for i := range aliases {
		aliases[i] = fmt.Sprintf("%03d", i) + strings.Repeat("x", MaxValueBytes-3)
	}
	if got, err := New(context.Background(), []resolver.Term{{ID: "a", Name: "A", Aliases: aliases}}); got != nil || err != ErrLimit {
		t.Fatal("node limit not enforced")
	}
	// Count whole retained ambiguity lists, not just matching source text.
	terms := make([]resolver.Term, 2000)
	for i := range terms {
		terms[i] = resolver.Term{ID: fmt.Sprintf("%04d", i) + strings.Repeat("i", MaxIDBytes-4), Name: fmt.Sprintf("Term%d", i), Aliases: []string{"Shared"}}
	}
	large := build(t, terms...)
	if got, err := large.Find(context.Background(), "s", "b", strings.Repeat("Shared ", 34)); got != nil || err != ErrLimit {
		t.Fatal("resolution lists bypassed work budget")
	}
	if got, err := large.Find(context.Background(), "s", "b", strings.Repeat("Shared ", 17)); got != nil || err != ErrLimit {
		t.Fatal("ambiguity lists bypassed output budget")
	}
	// Source whitespace can be much larger than its normalized alias.
	aliases = make([]string, 128)
	for i := range aliases {
		aliases[i] = strings.Repeat("A ", i+1) + "B"
	}
	long := build(t, resolver.Term{ID: "a", Name: "A B", Aliases: aliases})
	source := strings.Repeat("A ", 128) + strings.Repeat(" ", 300000) + "B"
	if got, err := long.Find(context.Background(), "s", "b", source); got != nil || err != ErrLimit {
		t.Fatal("normalization bypassed repeated source-work budget")
	}
}

type steppedContext struct {
	context.Context
	calls, stop int
}

func (ctx *steppedContext) Err() error {
	ctx.calls++
	if ctx.stop > 0 && ctx.calls >= ctx.stop {
		return context.Canceled
	}
	return nil
}

func TestEveryObservedCancellationIsAtomic(t *testing.T) {
	terms := []resolver.Term{{ID: "a", Name: "A B", Aliases: []string{"Straße", "A"}}, {ID: "b", Name: "B C"}, {ID: "c", Name: "Cloud C", Aliases: []string{"Shared"}}, {ID: "d", Name: "Cloud D", Aliases: []string{"Shared"}}, {ID: "old", Name: "Legacy", Archived: true}}
	ctx := &steppedContext{Context: context.Background()}
	m, err := New(ctx, terms)
	if err != nil {
		t.Fatal(err)
	}
	construction := ctx.calls
	for stop := 1; stop <= construction; stop++ {
		got, err := New(&steppedContext{Context: context.Background(), stop: stop}, terms)
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("constructor cancellation %d published state", stop)
		}
	}
	for _, source := range []string{"", "A B C; Shared Legacy. STRASSE A B"} {
		ctx = &steppedContext{Context: context.Background()}
		if _, err = m.Find(ctx, "s", "b", source); err != nil {
			t.Fatal(err)
		}
		checks := ctx.calls
		for stop := 1; stop <= checks; stop++ {
			got, err := m.Find(&steppedContext{Context: context.Background(), stop: stop}, "s", "b", source)
			if got != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("find cancellation %d/%d published partial output", stop, checks)
			}
		}
		t.Logf("%d construction / %d find cancellation checkpoints", construction, checks)
	}
}

// Slow test-only original-substring oracle. It shares the separately qualified
// whole-string resolver, but no automaton, normalized map, precomputed boundary
// flags or overlap bitmap with the implementation under test.
func brute(source string, terms []resolver.Term) *Result {
	r, _ := resolver.New(terms)
	names := map[string]string{}
	for _, term := range terms {
		names[term.ID] = term.Name
	}
	points := []rune(source)
	result := &Result{Mentions: []Mention{}, Issues: []Issue{}}
	accepted := []Mention{}
	isWord := func(p rune) bool { return textcompat.IsWord(p) || textcompat.IsMark(p) }
	isTech := func(p rune) bool { return strings.ContainsRune(".+#&", p) }
	for start := range points {
		if textcompat.IsSpace(points[start]) || start > 0 && isWord(points[start-1]) {
			continue
		}
		left := start - 1
		for left >= 0 && isTech(points[left]) {
			left--
		}
		if left != start-1 && left >= 0 && isWord(points[left]) {
			continue
		}
		for end := start + 1; end <= len(points); end++ {
			if textcompat.IsSpace(points[end-1]) || end < len(points) && (isWord(points[end]) || points[end] == '+' || points[end] == '#') {
				continue
			}
			right := end
			for right < len(points) && isTech(points[right]) {
				right++
			}
			if right != end && right < len(points) && isWord(points[right]) {
				continue
			}
			quote := string(points[start:end])
			found := r.Resolve(quote)
			ref := SourceRef{"s", "b", start, end}
			if found.CompetencyID != nil {
				accepted = append(accepted, Mention{*found.CompetencyID, names[*found.CompetencyID], quote, ref, Extractor})
			} else if found.Status == resolver.Ambiguous || len(found.ArchivedCandidateIDs) > 0 {
				if found.Status == resolver.Ambiguous {
					found.ArchivedCandidateIDs = []string{}
				}
				result.Issues = append(result.Issues, Issue{found.Status, quote, ref, found.CandidateIDs, found.ArchivedCandidateIDs})
			}
		}
	}
	sort.SliceStable(accepted, func(i, j int) bool {
		return accepted[i].SourceRef.End-accepted[i].SourceRef.Start > accepted[j].SourceRef.End-accepted[j].SourceRef.Start
	})
	overlaps := func(ref SourceRef) bool {
		for _, kept := range result.Mentions {
			if ref.Start < kept.SourceRef.End && kept.SourceRef.Start < ref.End {
				return true
			}
		}
		return false
	}
	for _, item := range accepted {
		if !overlaps(item.SourceRef) {
			result.Mentions = append(result.Mentions, item)
		}
	}
	sort.Slice(result.Mentions, func(i, j int) bool { return result.Mentions[i].SourceRef.Start < result.Mentions[j].SourceRef.Start })
	issues := []Issue{}
	for _, item := range result.Issues {
		if !overlaps(item.SourceRef) {
			issues = append(issues, item)
		}
	}
	result.Issues = issues
	return result
}

func TestGeneratedOriginalSubstringParity(t *testing.T) {
	random := rand.New(rand.NewSource(508001))
	parts := []string{"A", "B", "C", "ß", "ss", "İ", "i\u0307", "C#", "C++", ".NET", "Node.js", "A B", "B C", "Shared", "Old", "日本語", "🚀", "\ue000", "\u0301", "&", "."}
	separators := []string{"", " ", "  ", "\t", "\n", "/", "-", ",", ".", "+", "#", "&", "_", "\u001f", "\u1e5e"}
	for n := range 750 {
		terms := []resolver.Term{{ID: "ambiguous1", Name: "Shared"}, {ID: "ambiguous2", Name: "Shared"}, {ID: "old", Name: "Old", Archived: true}}
		for i := range 4 {
			terms = append(terms, resolver.Term{ID: fmt.Sprint(i), Name: parts[random.Intn(len(parts))], Aliases: []string{parts[random.Intn(len(parts))]}, Archived: random.Intn(5) == 0})
		}
		var source strings.Builder
		for range 1 + random.Intn(7) {
			source.WriteString(parts[random.Intn(len(parts))])
			source.WriteString(separators[random.Intn(len(separators))])
		}
		m := build(t, terms...)
		got, err := m.Find(context.Background(), "s", "b", source.String())
		if err != nil {
			t.Fatal(err)
		}
		want := brute(source.String(), terms)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("generated case %d for %q\nwant %+v\ngot %+v", n, source.String(), want, got)
		}
		verifyQuotes(t, source.String(), got)
	}
}

func TestOccupancyWordEdges(t *testing.T) {
	for start := 0; start < 140; start++ {
		for end := start + 1; end <= 140; end++ {
			used := make(occupancy, 3)
			used.mark(start, end)
			for point := 0; point < 140; point++ {
				if used.overlaps(point, point+1) != (point >= start && point < end) {
					t.Fatal("bitmap interval boundary mismatch")
				}
			}
		}
	}
}

func FuzzOriginalSpanRecognition(f *testing.F) {
	terms := []resolver.Term{{ID: "a", Name: "A", Aliases: []string{"Python", "Straße"}},
		{ID: "c", Name: "C++", Aliases: []string{"C#", "A B"}},
		{ID: "b", Name: "B C", Aliases: []string{"Shared"}},
		{ID: "d", Name: "D", Aliases: []string{"Shared"}},
		{ID: "ss", Name: "ss"}, {ID: "old", Name: "Old", Archived: true}}
	m, err := New(context.Background(), terms)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range []string{"", "A B C", "foo.Python Python", "STRASSE ß ss", "C++/C#", "Shared Old", "\ue000Python\ue001", "\xff", "A\u0301", "A\tB"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > 160 {
			t.Skip()
		}
		got, err := m.Find(context.Background(), "s", "b", source)
		if !utf8.ValidString(source) {
			if got != nil || err != ErrInput {
				t.Fatal("invalid UTF8 published a result")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if want := brute(source, terms); !reflect.DeepEqual(got, want) {
			t.Fatalf("literal mismatch for %q", source)
		}
		verifyQuotes(t, source, got)
	})
}
