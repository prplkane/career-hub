package fixedregex

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestReviewedCharacterClassesAndOriginalCoordinates(t *testing.T) {
	rules := MustCompile(map[string]Definition{
		"number":    {Pattern: `(?P<currency>[$€£])?([\d,]+)(?:[.](\d+))?[\s]*k?`, IgnoreCase: true},
		"literal":   {Pattern: `\\s[\s]+\\d`},
		"word":      {Pattern: `\b[Ii]+\b`, IgnoreCase: true},
		"separator": {Pattern: `[,;]+`},
	})
	matcher, err := rules.ForSource("  €١,٠٠٠.٥\u00a0K; $2\ue000")
	if err != nil {
		t.Fatal(err)
	}
	text, err := matcher.Text("  €١,٠٠٠.٥\u00a0K; $2\ue000")
	if err != nil {
		t.Fatal(err)
	}
	all, err := text.AllLimit("number", 2)
	if err != nil || len(all) != 2 {
		t.Fatalf("matches %d %v", len(all), err)
	}
	first := all[0]
	if first.Start() != 2 || first.Value() != "€١,٠٠٠.٥\u00a0K" || first.Group("currency") != "€" || first.Group("unknown") != "" {
		t.Fatal("capture/source-byte relationship")
	}
	for index, want := range []string{first.Value(), "€", "١,٠٠٠", "٥"} {
		if got, ok := first.GroupAt(index); !ok || got != want {
			t.Fatalf("ordinal %d %q %v", index, got, ok)
		}
	}
	for _, index := range []int{-1, math.MinInt, math.MaxInt, 4} {
		if _, ok := first.GroupAt(index); ok {
			t.Fatal("invalid capture index")
		}
	}
	copy := first.Indices()
	copy[0] = 0
	if first.Start() != 2 {
		t.Fatal("capture indices escaped")
	}
	if _, ok := text.Match("number", "start"); ok {
		t.Fatal("start mode searched")
	}
	if _, ok := text.Match("number", "full"); ok {
		t.Fatal("full mode searched")
	}
	only, _ := matcher.Text("€١,٠٠٠.٥\u00a0K")
	if _, ok := only.Match("number", "full"); !ok {
		t.Fatal("full match")
	}
	bare, _ := matcher.Text("12")
	item, ok := bare.Match("number", "")
	if !ok {
		t.Fatal("bare")
	}
	if _, ok := item.GroupAt(1); ok {
		t.Fatal("unmatched optional is not a capture")
	}
	escaped, _ := matcher.Text("\\s\u2003\\d")
	if !escaped.Has("literal") {
		t.Fatal("escaped slash rewritten as a property")
	}
	dotted, _ := matcher.Text("İıI")
	if !dotted.Has("word") {
		t.Fatal("Python ASCII-I case equivalents")
	}
	split, _ := matcher.Text("a,b;;c")
	if !reflect.DeepEqual(split.Split("separator", false), []string{"a", "b", "c"}) || !reflect.DeepEqual(split.Split("separator", true), []string{"a", "b;;c"}) {
		t.Fatal("split contract")
	}
}

func TestLimitsInputAndImmutableDefinitions(t *testing.T) {
	definitions := map[string]Definition{"word": {Pattern: `\bPython\b`}}
	rules := MustCompile(definitions)
	definitions["word"] = Definition{Pattern: "Java"}
	matcher, err := rules.ForSource("Python Python")
	if err != nil {
		t.Fatal(err)
	}
	text, err := matcher.Text("Python Python")
	if err != nil {
		t.Fatal(err)
	}
	if !text.Has("word") {
		t.Fatal("caller mutated compiled rules")
	}
	for _, limit := range []int{-1, MaxSourceBytes + 2} {
		if out, err := text.AllLimit("word", limit); out != nil || !errors.Is(err, ErrInput) {
			t.Fatal("limit input")
		}
	}
	for _, limit := range []int{0, 1} {
		if out, err := text.AllLimit("word", limit); out != nil || !errors.Is(err, ErrLimit) {
			t.Fatal("partial match publication")
		}
	}
	empty, _ := matcher.Text("")
	if out, err := empty.AllLimit("word", 0); err != nil || out == nil || len(out) != 0 {
		t.Fatal("empty permitted zero budget")
	}
	for _, source := range []string{string([]byte{0xff}), strings.Repeat("a", MaxSourceBytes+1)} {
		if _, err := rules.ForSource(source); !errors.Is(err, ErrInput) {
			t.Fatal("invalid source")
		}
		if _, err := matcher.Text(source); !errors.Is(err, ErrInput) {
			t.Fatal("invalid text")
		}
	}
	if _, err := (*Rules)(nil).ForSource(""); !errors.Is(err, ErrInput) {
		t.Fatal("nil rules")
	}
	if _, err := (&Rules{}).ForSource(""); !errors.Is(err, ErrInput) {
		t.Fatal("zero rules")
	}
	if _, err := (*Matcher)(nil).Text(""); !errors.Is(err, ErrInput) {
		t.Fatal("nil matcher")
	}
	if _, err := (&Matcher{}).Text(""); !errors.Is(err, ErrInput) {
		t.Fatal("zero matcher")
	}
	marks := matcher.Markers()
	if _, err := matcher.Text(string(marks[:])); err == nil {
		t.Fatal("introduced marker accepted")
	}
}

func TestMarkerFallbackAndConcurrentReuse(t *testing.T) {
	rules := MustCompile(map[string]Definition{"word": {Pattern: `\bPython\b`}})
	source := "\ue000Python\ue001\ue002\ue003"
	matcher, err := rules.ForSource(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range matcher.Markers() {
		if strings.ContainsRune(source, point) {
			t.Fatal("colliding marker")
		}
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			text, err := matcher.Text(source)
			if err != nil {
				t.Error(err)
				return
			}
			found, ok := text.Match("word", "")
			if !ok || found.Value() != "Python" || found.Start() != 3 {
				t.Error("concurrent match")
			}
		}()
	}
	workers.Wait()
}

func TestInvalidStaticDefinitionsFailDuringConstruction(t *testing.T) {
	invalid := []map[string]Definition{nil, {"": {Pattern: "x"}}, {"a": {Pattern: string([]byte{0xff})}}, {"a": {Pattern: "("}}, {"a": {Pattern: strings.Repeat("x", 16385)}}}
	for _, definitions := range invalid {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("invalid static rules accepted")
				}
			}()
			MustCompile(definitions)
		}()
	}
}
