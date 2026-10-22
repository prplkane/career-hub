package catalogwrite

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/skills/catalog"
)

func TestSkillEncodingPreservesAllOptionalValuesAndPresence(t *testing.T) {
	cases := []string{
		`- {id: absent_aliases, name: Absent aliases, category: tool}`,
		`- {id: minimal, name: Minimal, aliases: [], category: tool}`,
		`- {id: nulls, name: Nulls, aliases: [], category: tool, primary_group_id: null, parent: null, primary_domain: null, created_via: null, created_at: null}`,
		`- id: full
  name: "Literal \x00\nline"
  aliases: ["ON", "~", "Résumé 数据 😀", "q: \"value\""]
  category: tool
  primary_group_id: user_tools
  parent: ancestor
  kind: tool
  primary_domain: other
  relations: [{type: related_to, target_id: other}]
  archived: true
  created_via: manual_registry
  created_at: "2026-09-09T00:00:00Z"
`,
	}
	for _, raw := range cases {
		terms, e := cataloginput.DecodeTerms([]byte(raw), true)
		if e != nil {
			t.Fatal(e)
		}
		encoded, err := encodeSkills(terms)
		if err != nil {
			t.Fatal(err)
		}
		decoded, e := cataloginput.DecodeTerms(encoded, true)
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(terms, decoded) {
			t.Fatalf("model or field presence changed: %#v / %#v", terms, decoded)
		}
		again, e2 := encodeSkills(terms)
		if e2 != nil || !bytes.Equal(encoded, again) {
			t.Fatal("nondeterministic encoding")
		}
	}
	raw, err := encodeSkills(nil)
	if err != nil || string(raw) != "[]\n" {
		t.Fatal("empty catalog changed")
	}
}
func TestSkillEncodingBoundsAndFieldPresence(t *testing.T) {
	terms, e := cataloginput.DecodeTerms([]byte(`- {id: one, name: One, category: tool, parent: null}`), true)
	if e != nil {
		t.Fatal(e)
	}
	raw, err := encodeSkills(terms)
	if err != nil {
		t.Fatal(err)
	}
	decoded, e := cataloginput.DecodeTerms(raw, true)
	if e != nil {
		t.Fatal(e)
	}
	for _, field := range []string{"kind", "relations", "archived", "primary_domain", "created_via", "created_at", "primary_group_id"} {
		if slices.Contains(decoded[0].FieldsSet, field) {
			t.Fatalf("absent overlay field materialized: %s", field)
		}
	}
	if decoded[0].Parent != nil || !slices.Contains(decoded[0].FieldsSet, "parent") {
		t.Fatal("explicit null omitted")
	}
	term := terms[0]
	term.Name = "\xff"
	if raw, err := encodeSkills([]catalog.Term{term}); raw != nil || err == nil {
		t.Fatal("invalid UTF-8 emitted")
	}
	term = terms[0]
	term.Name = strings.Repeat("x", cataloginput.YAMLMaxInputBytes+1)
	if raw, err := encodeSkills([]catalog.Term{term}); raw != nil || err == nil {
		t.Fatal("oversized scalar emitted")
	}
	term = terms[0]
	term.Aliases = make([]string, cataloginput.YAMLMaxNodes)
	if raw, err := encodeSkills([]catalog.Term{term}); raw != nil || err == nil {
		t.Fatal("node budget bypassed")
	}
	term = terms[0]
	term.Relations = make([]catalog.Relation, cataloginput.YAMLMaxNodes)
	term.FieldsSet = append(slices.Clone(term.FieldsSet), "relations")
	if raw, err := encodeSkills([]catalog.Term{term}); raw != nil || err == nil {
		t.Fatal("relation node budget bypassed")
	}
	term = terms[0]
	term.Name = strings.Repeat("\x01", cataloginput.YAMLMaxInputBytes/3)
	if raw, err := encodeSkills([]catalog.Term{term}); raw != nil || err == nil {
		t.Fatal("escaped output budget bypassed")
	}
}
