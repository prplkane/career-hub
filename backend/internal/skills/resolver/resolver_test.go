package resolver

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func newResolver(t *testing.T, terms []Term) *Resolver {
	t.Helper()
	r, err := New(terms)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func wantResolution(source, normalized string, status Status, ids, archived []string, id string) Resolution {
	result := Resolution{
		SourceText: source, Normalized: normalized, Status: status,
		CandidateIDs: copyIDs(ids), ArchivedCandidateIDs: copyIDs(archived),
	}
	if id != "" {
		result.CompetencyID = &id
	}
	return result
}

func TestResolutionOutcomes(t *testing.T) {
	r := newResolver(t, []Term{
		{ID: "python", Name: "Python", Aliases: []string{"Py"}},
		{ID: "mobile_device_management", Name: "Mobile Device Management", Aliases: []string{"MDM"}},
		{ID: "management_data", Name: "MDM"},
		{ID: "legacy_tool", Name: "Old Tool", Aliases: []string{"OldTool"}, Archived: true},
	})
	tests := []struct {
		name      string
		source    string
		reference bool
		want      Resolution
	}{
		{"canonical", "  Python  ", false, wantResolution("  Python  ", "python", ExactMatch, []string{"python"}, nil, "python")},
		{"alias", "PY", false, wantResolution("PY", "py", AliasMatch, []string{"python"}, nil, "python")},
		{"canonical-versus-alias-collision", "MDM", false, wantResolution("MDM", "mdm", Ambiguous, []string{"management_data", "mobile_device_management"}, nil, "")},
		{"archived-only-name", "Old Tool", false, wantResolution("Old Tool", "old tool", Unmatched, nil, []string{"legacy_tool"}, "")},
		{"archived-only-alias", "OLDTool", false, wantResolution("OLDTool", "oldtool", Unmatched, nil, []string{"legacy_tool"}, "")},
		{"unknown", "Go", false, wantResolution("Go", "go", Unmatched, nil, nil, "")},
		{"complete-phrase-not-substring", "Strong Python skills", false, wantResolution("Strong Python skills", "strong python skills", Unmatched, nil, nil, "")},
		{"empty", "", false, wantResolution("", "", Unmatched, nil, nil, "")},
		{"whitespace-only", " \t\n", false, wantResolution(" \t\n", "", Unmatched, nil, nil, "")},
		{"active-reference", "python", true, wantResolution("python", "python", ExactMatch, []string{"python"}, nil, "python")},
		{"archived-reference", "legacy_tool", true, wantResolution("legacy_tool", "legacy_tool", ExactMatch, []string{"legacy_tool"}, []string{"legacy_tool"}, "legacy_tool")},
		{"missing-reference", "missing", true, wantResolution("missing", "missing", InvalidReference, nil, nil, "")},
		{"reference-case-sensitive", "Python", true, wantResolution("Python", "Python", InvalidReference, nil, nil, "")},
		{"reference-does-not-trim", " python ", true, wantResolution(" python ", " python ", InvalidReference, nil, nil, "")},
		{"reference-does-not-match-alias", "Py", true, wantResolution("Py", "Py", InvalidReference, nil, nil, "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Resolve(tt.source)
			if tt.reference {
				got = r.ResolveReference(tt.source)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v; want %#v", got, tt.want)
			}
		})
	}
}

func TestArchiveSeparationAndOrdering(t *testing.T) {
	terms := []Term{
		{ID: "z_active", Name: "Zebra", Aliases: []string{"shared"}},
		{ID: "z_archived", Name: "Old Zebra", Aliases: []string{"shared"}, Archived: true},
		{ID: "a_archived", Name: "Old Ant", Aliases: []string{"shared"}, Archived: true},
		{ID: "a_active", Name: "Shared"},
	}
	r := newResolver(t, terms)
	want := wantResolution("SHARED", "shared", Ambiguous,
		[]string{"a_active", "z_active"}, []string{"a_archived", "z_archived"}, "")
	if got := r.Resolve("SHARED"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	// Input order cannot determine a winner or alter either candidate list.
	for left, right := 0, len(terms)-1; left < right; left, right = left+1, right-1 {
		terms[left], terms[right] = terms[right], terms[left]
	}
	if got := newResolver(t, terms).Resolve("SHARED"); !reflect.DeepEqual(got, want) {
		t.Fatalf("reversed catalog changed resolution: %#v", got)
	}
	terms[0].Archived = true // a_active; leaves only z_active in the active index.
	got := newResolver(t, terms).Resolve("SHARED")
	want = wantResolution("SHARED", "shared", AliasMatch, []string{"z_active"},
		[]string{"a_active", "a_archived", "z_archived"}, "z_active")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("archived collision changed active match: got %#v; want %#v", got, want)
	}
}

func TestDuplicateIDsFailWithoutPartialResolver(t *testing.T) {
	for _, archived := range []bool{false, true} {
		r, err := New([]Term{
			{ID: "python", Name: "Python"},
			{ID: "python", Name: "Another name", Archived: archived},
		})
		if r != nil || !errors.Is(err, ErrDuplicateID) || err.Error() != "Duplicate technology id" {
			t.Fatalf("got resolver=%v error=%v", r, err)
		}
	}
}

func TestRepeatedIndexedNamesDoNotRepeatCandidates(t *testing.T) {
	// Catalog-model validation normally rejects these aliases. The resolver still
	// has set semantics, as does Python's resolver on a constructed model.
	r := newResolver(t, []Term{{ID: "python", Name: "Python", Aliases: []string{"python", " PYTHON ", "Py", "Py"}}})
	for _, source := range []string{"Python", "Py"} {
		got := r.Resolve(source)
		if !reflect.DeepEqual(got.CandidateIDs, []string{"python"}) || got.Status == Ambiguous {
			t.Fatalf("repeated index entry created ambiguity: %#v", got)
		}
	}
}

func TestUnicodeNormalizationAndSourcePreservation(t *testing.T) {
	tests := []struct{ name, source, normalized string }{
		{"Straße", "\tSTRASSE\n", "strasse"},
		{"İ", "i\u0307", "i\u0307"},
		{"Σςσ", "σσσ", "σσσ"},
		{"ﬃ", "FFI", "ffi"},
		{"Data Science", "\u001cData\u00a0\u2003Science\u001f", "data science"},
		{"C++", "C++", "c++"},
		{"C#", "c#", "c#"},
		{".NET", " .nEt ", ".net"},
		{"Node.js", "NODE.JS", "node.js"},
		{"技能 🚀", "技能\t🚀", "技能 🚀"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newResolver(t, []Term{{ID: "term", Name: tt.name}})
			want := wantResolution(tt.source, tt.normalized, ExactMatch, []string{"term"}, nil, "term")
			if got := r.Resolve(tt.source); !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v; want %#v", got, want)
			}
		})
	}
	// Casefold and whitespace collapse do not apply NFC/NFKC, erase zero-width
	// characters, remove punctuation, or infer a phrase from nearby prose.
	r := newResolver(t, []Term{{ID: "accent", Name: "é"}, {ID: "data", Name: "Data Science"}, {ID: "cpp", Name: "C++"}})
	for _, source := range []string{"e\u0301", "Data\u200bScience", "Data-Science", "C"} {
		if got := r.Resolve(source); got.Status != Unmatched || got.SourceText != source {
			t.Fatalf("unapproved normalization for %q: %#v", source, got)
		}
	}
}

func TestInputAndResultMutationCannotChangeResolver(t *testing.T) {
	terms := []Term{
		{ID: "python", Name: "Python", Aliases: []string{"Py"}},
		{ID: "old_python", Name: "Old Python", Aliases: []string{"Py"}, Archived: true},
	}
	r := newResolver(t, terms)
	want := r.Resolve("Py")
	terms[0].Aliases[0] = "Changed alias"
	terms[0].Name = "Changed name"
	terms[0].ID = "changed_id"
	terms[0].Archived = true
	terms[1].Aliases = nil
	for iteration := 0; iteration < 2; iteration++ {
		got := r.Resolve("Py")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("caller mutation changed resolution: %#v", got)
		}
		got.CandidateIDs[0] = "changed"
		got.ArchivedCandidateIDs[0] = "changed"
		*got.CompetencyID = "changed"
	}
	reference := r.ResolveReference("old_python")
	reference.CandidateIDs[0] = "changed"
	reference.ArchivedCandidateIDs[0] = "changed"
	*reference.CompetencyID = "changed"
	if got := r.ResolveReference("old_python"); got.CandidateIDs[0] != "old_python" || got.ArchivedCandidateIDs[0] != "old_python" || *got.CompetencyID != "old_python" {
		t.Fatalf("reference result mutated catalog: %#v", got)
	}
	if got := r.Resolve("Changed alias"); got.Status != Unmatched {
		t.Fatalf("caller alias mutation was retained: %#v", got)
	}
	if got := r.Resolve("Python"); got.Status != ExactMatch || got.CompetencyID == nil || *got.CompetencyID != "python" {
		t.Fatalf("caller name mutation changed canonical match: %#v", got)
	}
}

func TestResolutionJSONShapeAndEmptyCollections(t *testing.T) {
	r := newResolver(t, []Term{{ID: "python", Name: "Python"}})
	for _, got := range []Resolution{r.Resolve("unknown"), r.ResolveReference("missing"), r.Resolve("Python")} {
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 6 {
			t.Fatalf("unexpected fields: %s", encoded)
		}
		for _, key := range []string{"source_text", "normalized", "status", "candidate_ids", "archived_candidate_ids", "competency_id"} {
			if _, exists := fields[key]; !exists {
				t.Fatalf("missing %q: %s", key, encoded)
			}
		}
		if got.CandidateIDs == nil || got.ArchivedCandidateIDs == nil || string(fields["archived_candidate_ids"]) != "[]" {
			t.Fatalf("null candidate collection: %s", encoded)
		}
		if got.CompetencyID == nil && string(fields["competency_id"]) != "null" {
			t.Fatalf("missing ID must remain JSON null: %s", encoded)
		}
	}
}

func TestEmptyCatalogAndZeroValue(t *testing.T) {
	for _, r := range []*Resolver{newResolver(t, nil), newResolver(t, []Term{}), {}} {
		if got := r.Resolve("Anything"); !reflect.DeepEqual(got, wantResolution("Anything", "anything", Unmatched, nil, nil, "")) {
			t.Fatalf("empty catalog produced match: %#v", got)
		}
		if got := r.ResolveReference("anything"); !reflect.DeepEqual(got, wantResolution("anything", "anything", InvalidReference, nil, nil, "")) {
			t.Fatalf("empty catalog produced reference: %#v", got)
		}
	}
}

func TestConcurrentReadsOwnTheirResults(t *testing.T) {
	r := newResolver(t, []Term{{ID: "python", Name: "Python", Aliases: []string{"Py"}}})
	var workers sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for iteration := 0; iteration < 100; iteration++ {
				got := r.Resolve("Py")
				if got.Status != AliasMatch || len(got.CandidateIDs) != 1 || got.CandidateIDs[0] != "python" || got.CompetencyID == nil || *got.CompetencyID != "python" {
					t.Errorf("shared mutable result: %#v", got)
					return
				}
				got.CandidateIDs[0] = "local only"
				*got.CompetencyID = "local only"
				if got := r.ResolveReference("python"); got.Status != ExactMatch {
					t.Errorf("reference changed: %#v", got)
					return
				}
			}
		}()
	}
	workers.Wait()
}

func FuzzResolutionInvariants(f *testing.F) {
	for _, seed := range []string{"", "Python", "PY", "MDM", "Old Tool", "Straße", "\u001fData\u00a0Science", "技能 🚀"} {
		f.Add(seed)
	}
	r, err := New([]Term{
		{ID: "python", Name: "Python", Aliases: []string{"Py"}},
		{ID: "mobile", Name: "Mobile Device Management", Aliases: []string{"MDM"}},
		{ID: "master", Name: "Master Data Management", Aliases: []string{"MDM"}},
		{ID: "legacy", Name: "Old Tool", Archived: true},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, source string) {
		// Arbitrary invalid bytes are outside the Unicode scalar input contract.
		source = strings.ToValidUTF8(source, "\ufffd")
		if len(source) > 8192 {
			t.Skip()
		}
		got := r.Resolve(source)
		if got.SourceText != source || got.CandidateIDs == nil || got.ArchivedCandidateIDs == nil {
			t.Fatalf("source or collection invariant failed: %#v", got)
		}
		if got.Status == ExactMatch || got.Status == AliasMatch {
			if len(got.CandidateIDs) != 1 || got.CompetencyID == nil || *got.CompetencyID != got.CandidateIDs[0] {
				t.Fatalf("single-match invariant failed: %#v", got)
			}
		} else if got.CompetencyID != nil {
			t.Fatalf("unresolved ID selected: %#v", got)
		}
		if again := r.Resolve(source); !reflect.DeepEqual(got, again) {
			t.Fatalf("nondeterministic result: %#v and %#v", got, again)
		}
	})
}
