package catalog

import (
	"reflect"
	"slices"
	"testing"
)

func mergeFixture(id, name string) Term {
	return Term{ID: id, Name: name, Category: "tool", Kind: "technology", Aliases: []string{}, Relations: []Relation{},
		FieldsSet: []string{"id", "name", "category"}}
}

func mergePointer(value string) *string { return &value }

func TestMergeTermPreservesAbsentFieldsAndHonorsExplicitClears(t *testing.T) {
	core := mergeFixture("tool", "Core Name")
	core.Aliases = []string{"Shared Alias"}
	core.Parent, core.PrimaryDomain, core.PrimaryGroupID = mergePointer("parent"), mergePointer("other"), mergePointer("unclassified")
	core.CreatedVia, core.CreatedAt = mergePointer("manual_registry"), mergePointer("2026-01-01T00:00:00Z")
	core.Kind, core.Archived = "tool", true
	core.Relations = []Relation{{Type: "uses", TargetID: "runtime"}}
	custom := mergeFixture("tool", "Private Name")
	custom.Aliases, custom.Category = []string{"shared alias", "Private Alias"}, "custom_skill"
	result := mergeTerm(core, custom)
	if result.Name != "Private Name" || result.Category != "custom_skill" || !result.Archived || result.Kind != "tool" {
		t.Fatalf("required/absent overrides: %#v", result)
	}
	if !reflect.DeepEqual(result.Aliases, []string{"Shared Alias", "Core Name", "Private Alias"}) || result.Parent == core.Parent {
		t.Fatalf("aliases or pointer ownership: %#v", result)
	}
	custom.FieldsSet = []string{"parent", "primary_domain", "primary_group_id", "relations", "kind", "archived", "created_via", "created_at"}
	cleared := mergeTerm(core, custom)
	if cleared.Parent != nil || cleared.PrimaryDomain != nil || cleared.PrimaryGroupID != nil || cleared.CreatedVia != nil || cleared.CreatedAt != nil || cleared.Archived || cleared.Kind != "technology" || len(cleared.Relations) != 0 {
		t.Fatalf("explicit clears lost: %#v", cleared)
	}
}

func TestGroupReconciliationPreservesPrivateIDsAndSuffixOrdering(t *testing.T) {
	core := []Group{{ID: "core", Name: "Data"}, {ID: "same", Name: "Old"}, {ID: "reserved", Name: "Data (built-in)"}}
	custom := []Group{{ID: "private", Name: "DATA"}, {ID: "same", Name: "Mine"}, {ID: "reserved_two", Name: "Data (built-in 2)"}}
	groups, origins, redirects, notices := reconcileGroups(core, custom)
	if groups[0].ID != "core" || groups[0].Name != "Data (built-in 3)" || origins["same"] || redirects["same"] != "same" {
		t.Fatalf("group reconciliation: %#v %#v %#v", groups, origins, redirects)
	}
	if len(notices) != 1 || notices[0].CustomID != "private" || notices[0].Value != "Data" {
		t.Fatalf("group notice: %#v", notices)
	}
	if core[0].Name != "Data" || custom[0].Name != "DATA" {
		t.Fatal("caller groups mutated")
	}
}

func TestSkillShadowingRedirectsReferencesAndPrunesOnlyCoreAliases(t *testing.T) {
	old, other, child := mergeFixture("old", "Future Tool"), mergeFixture("other", "Other"), mergeFixture("child", "Child")
	other.Aliases = []string{"Shared", "Kept"}
	child.Parent = mergePointer("old")
	child.Relations = []Relation{{Type: "uses", TargetID: "old"}, {Type: "uses", TargetID: "private"}}
	private := mergeFixture("private", "FUTURE TOOL")
	private.Aliases = []string{"Shared"}
	private.Parent, private.Relations = mergePointer("old"), []Relation{{Type: "uses", TargetID: "old"}}
	result, origins, notices := reconcileSkills([]Term{old, other, child}, []Term{private}, nil)
	if len(result) != 3 || origins["old"] || origins["private"] || !origins["child"] {
		t.Fatalf("identity: %#v %#v", result, origins)
	}
	if *result[0].Parent != "private" || !reflect.DeepEqual(result[0].Relations, []Relation{{Type: "uses", TargetID: "private"}}) {
		t.Fatalf("redirect/dedup: %#v", result[0])
	}
	if !reflect.DeepEqual(result[1].Aliases, []string{"Kept"}) || result[2].Parent != nil || len(result[2].Relations) != 0 {
		t.Fatalf("alias/self removal: %#v", result)
	}
	if len(notices) != 2 || notices[0].Kind != "core_skill_shadowed_by_name" || notices[1].Kind != "core_skill_alias_shadowed" {
		t.Fatalf("notice ordering: %#v", notices)
	}
	if *child.Parent != "old" || len(child.Relations) != 2 || len(other.Aliases) != 2 {
		t.Fatal("caller skills mutated")
	}
}

func TestBuildRespectsPrivateGroupArchiveWithoutRewritingInputs(t *testing.T) {
	core := mergeFixture("new_core", "New core skill")
	groups := []Group{{ID: "unclassified", Name: "My archive", Archived: true}}
	snapshot, err := Build(nil, groups, []Term{core}, nil, []byte("groups"), []byte("skills"))
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Skills()[0].Archived || core.Archived || len(groups[0].CascadeArchivedSkillIDs) != 0 {
		t.Fatal("archive must be applied in memory to effective core only")
	}
	if snapshot.GroupOrigin("unclassified") != "custom" || snapshot.SkillOrigin("new_core") != "core" || len(snapshot.Notices()) != 1 {
		t.Fatal("origin/notice changed")
	}
	if !slices.Contains(snapshot.Skills()[0].FieldsSet, "archived") || slices.Contains(core.FieldsSet, "archived") {
		t.Fatal("effective archive must become explicit without mutating source presence")
	}
	reused, err := Build(nil, nil, []Term{core}, snapshot.Skills(), nil, nil)
	if err != nil || !reused.Skills()[0].Archived {
		t.Fatalf("reusing effective overlay lost archive: %v", err)
	}
	if _, err = Build(nil, groups, nil, []Term{mergeFixture("private", "Private active")}, nil, nil); err == nil {
		t.Fatal("active private skill under archived group must fail")
	}
}

func TestRedirectFieldPresenceChangesOnlyWithValues(t *testing.T) {
	old, child := mergeFixture("old", "Old"), mergeFixture("child", "Child")
	child.Parent = mergePointer("old")
	child.FieldsSet = append(child.FieldsSet, "parent")
	private := mergeFixture("private", "OLD")
	result, _, _ := reconcileSkills([]Term{old, child}, []Term{private}, nil)
	if !slices.Contains(result[0].FieldsSet, "relations") || !slices.Contains(result[0].FieldsSet, "parent") {
		t.Fatal("parent redirect must mark both model_copy keys")
	}
	if !slices.Equal(result[1].FieldsSet, private.FieldsSet) || slices.Contains(child.FieldsSet, "relations") {
		t.Fatal("no-op/source field presence must remain unchanged")
	}
}

func TestBuildRejectsDuplicateInputSkillIDsEvenWhenShadowed(t *testing.T) {
	core := mergeFixture("duplicate", "Core")
	custom := mergeFixture("duplicate", "Private")
	for _, inputs := range [][2][]Term{{{core, core}, {custom}}, {{core}, {custom, custom}}} {
		if _, err := Build(nil, nil, inputs[0], inputs[1], nil, nil); err == nil {
			t.Fatal("duplicate file IDs must not disappear under overlay")
		}
	}
}
