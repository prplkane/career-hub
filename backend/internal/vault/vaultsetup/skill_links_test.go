package vaultsetup

import (
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
)

// These cases are the specification for unresolvedSkillLinks and its per-record
// helper unresolvedInRecord.
func TestUnresolvedSkillLinks(t *testing.T) {
	known := map[string]bool{"core_tool": true, "user_synthetic_skill": true, "user_retired_skill": true}
	parse := func(t *testing.T, text string) cataloginput.Value {
		t.Helper()
		value, problem := cataloginput.ParseYAML([]byte(text))
		if problem != nil {
			t.Fatal("fixture YAML", problem)
		}
		return value
	}
	cases := []struct {
		name                             string
		experiences, projects, education string
		want                             int
	}{
		{"all links known", "[{id: e, competency_ids: [core_tool, user_synthetic_skill]}]", "[{id: p, competency_ids: [user_retired_skill]}]", "{certifications: [{id: c, competency_ids: [core_tool]}]}", 0},
		{"no records", "[]", "[]", "{schools: [], certifications: []}", 0},
		{"unknown experience link", "[{id: e, competency_ids: [core_tool, missing]}]", "[]", "{certifications: []}", 1},
		{"unknown project link", "[]", "[{id: p, competency_ids: [missing]}]", "{certifications: []}", 1},
		{"unknown certification link", "[]", "[]", "{certifications: [{id: c, competency_ids: [missing]}]}", 1},
		{"schools are not checked", "[]", "[]", "{schools: [{id: s, competency_ids: [missing]}], certifications: []}", 0},
		{"archived records count", "[{id: e, archived: true, competency_ids: [missing]}]", "[{id: p, archived: true, competency_ids: [missing]}]", "{certifications: [{id: c, archived: true, competency_ids: [missing]}]}", 3},
		{"duplicates count each time", "[{id: e, competency_ids: [missing, missing]}]", "[]", "{certifications: []}", 2},
		{"blank entries are ignored", "[{id: e, competency_ids: ['', '   ']}]", "[]", "{certifications: []}", 0},
		{"entries are stripped", "[{id: e, competency_ids: ['  core_tool  ']}]", "[]", "{certifications: []}", 0},
		{"ids are case sensitive", "[{id: e, competency_ids: [Core_Tool]}]", "[]", "{certifications: []}", 1},
		{"missing competency_ids", "[{id: e}]", "[{id: p}]", "{certifications: [{id: c}]}", 0},
		{"null competency_ids", "[{id: e, competency_ids: null}]", "[{id: p, competency_ids: ~}]", "{certifications: [{id: c, competency_ids: null}]}", 0},
		{"scalar competency_ids", "[{id: e, competency_ids: core_tool}]", "[]", "{certifications: []}", 1},
		{"mapping competency_ids", "[]", "[{id: p, competency_ids: {core_tool: true}}]", "{certifications: []}", 1},
		{"non-string items", "[{id: e, competency_ids: [core_tool, 1, null, true]}]", "[]", "{certifications: []}", 3},
		{"nested sequence item", "[{id: e, competency_ids: [[core_tool]]}]", "[]", "{certifications: []}", 1},
		{"across all three collections", "[{id: e, competency_ids: [missing]}]", "[{id: p, competency_ids: [gone, lost]}]", "{certifications: [{id: c, competency_ids: [absent]}]}", 4},
		{"non-record items count zero", "[plain text, null, 3]", "[[nested]]", "{certifications: [text]}", 0},
		{"wrong root kinds count zero", "{competency_ids: [missing]}", "missing", "[{competency_ids: [missing]}]", 0},
		{"certifications not a sequence", "[]", "[]", "{certifications: {id: c, competency_ids: [missing]}}", 0},
		{"empty known catalog", "[{id: e, competency_ids: [core_tool]}]", "[]", "{certifications: []}", 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog := known
			if test.name == "empty known catalog" {
				catalog = map[string]bool{}
			}
			got := unresolvedSkillLinks(parse(t, test.experiences), parse(t, test.projects), parse(t, test.education), catalog)
			if got != test.want {
				t.Fatalf("got %d unresolved links, want %d", got, test.want)
			}
		})
	}
	t.Run("zero inputs count zero", func(t *testing.T) {
		if got := unresolvedSkillLinks(cataloginput.Value{}, cataloginput.Value{}, cataloginput.Value{}, nil); got != 0 {
			t.Fatalf("got %d", got)
		}
	})
}
