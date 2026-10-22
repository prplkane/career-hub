package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func validationPtr(value string) *string { return &value }

func validationTerm(id string) Term {
	return Term{ID: id, Name: "Name " + id, Category: "custom_skill", Kind: "technology", Aliases: []string{}, Relations: []Relation{}, FieldsSet: []string{}}
}

func requireCatalogError(t *testing.T, err error, class, code, message string) {
	t.Helper()
	var got *Error
	if !errors.As(err, &got) {
		t.Fatalf("expected catalog error, got %T: %v", err, err)
	}
	if got.Class != class || got.Code != code || got.Message != message || got.Error() != message {
		t.Fatalf("got %#v; want class=%q code=%q message=%q", got, class, code, message)
	}
}

func TestGraphValidationOrderedErrors(t *testing.T) {
	a, b, c := validationTerm("a"), validationTerm("b"), validationTerm("c")
	for _, relationType := range []string{"member_of", "uses", "implements", "replaces", "related_to"} {
		t.Run("duplicate-"+relationType, func(t *testing.T) {
			changed := cloneTerm(a)
			changed.Relations = []Relation{{Type: relationType, TargetID: "b"}, {Type: relationType, TargetID: "b"}}
			requireCatalogError(t, ValidateTerms([]Term{changed, b}), "ValueError", "", "Duplicate "+relationType+" relation from a to b")
		})
		t.Run("self-"+relationType, func(t *testing.T) {
			changed := cloneTerm(a)
			changed.Relations = []Relation{{Type: relationType, TargetID: "a"}}
			requireCatalogError(t, ValidateTerms([]Term{changed}), "ValueError", "", "Technology relation may not target itself: a")
		})
	}
	a.Relations = []Relation{{Type: "uses", TargetID: "missing"}}
	requireCatalogError(t, ValidateTerms([]Term{a, b, b}), "ValueError", "", "Duplicate technology id: b")
	requireCatalogError(t, ValidateTerms([]Term{a, b}), "ValueError", "", "Technology relation from a targets unknown id: missing")
	b.Relations = []Relation{{Type: "uses", TargetID: "other_missing"}}
	requireCatalogError(t, ValidateTerms([]Term{b, a}), "ValueError", "", "Technology relation from b targets unknown id: other_missing")
	a.Parent, a.Relations = validationPtr("c"), []Relation{{Type: "member_of", TargetID: "c"}, {Type: "uses", TargetID: "missing"}}
	requireCatalogError(t, ValidateTerms([]Term{a, c}), "ValueError", "", "Technology relation from a targets unknown id: missing")
	a.Relations = a.Relations[:1]
	requireCatalogError(t, ValidateTerms([]Term{a, c}), "ValueError", "", "Duplicate member_of relation from a to c")
	a.Relations, a.Parent = nil, validationPtr("missing_parent")
	requireCatalogError(t, ValidateTerms([]Term{a}), "ValueError", "", "Technology relation from a targets unknown id: missing_parent")
	a.Parent = validationPtr("a")
	requireCatalogError(t, ValidateTerms([]Term{a}), "ValueError", "", "Technology relation may not target itself: a")
	a.Parent = nil
	a.Relations = []Relation{{Type: "related_to", TargetID: "b"}}
	b.Relations = []Relation{{Type: "related_to", TargetID: "a"}}
	requireCatalogError(t, ValidateTerms([]Term{b, a}), "ValueError", "", "Duplicate related_to relation between a and b")
}

func TestMemberGraphTraversalAndArchiveBehavior(t *testing.T) {
	a, b, c, d := validationTerm("a"), validationTerm("b"), validationTerm("c"), validationTerm("d")
	a.Relations = []Relation{{Type: "member_of", TargetID: "c"}, {Type: "member_of", TargetID: "b"}}
	b.Parent, c.Parent, d.Parent = validationPtr("a"), validationPtr("d"), validationPtr("c")
	requireCatalogError(t, ValidateTerms([]Term{d, c, b, a}), "ValueError", "", "member_of relation cycle includes: a")
	// Sorted roots and child traversal determine the first cycle, not input order.
	a.Relations = nil
	a.Parent = validationPtr("b")
	requireCatalogError(t, ValidateTerms([]Term{d, c, b, a}), "ValueError", "", "member_of relation cycle includes: a")
	a.Archived, b.Archived = true, true
	requireCatalogError(t, ValidateTerms([]Term{a, b}), "ValueError", "", "member_of relation cycle includes: a")
	// Only member_of is acyclic: other relation types may form valid cycles.
	a.Parent, b.Parent = nil, nil
	a.Relations = []Relation{{Type: "uses", TargetID: "b"}}
	b.Relations = []Relation{{Type: "implements", TargetID: "a"}}
	if err := ValidateTerms([]Term{a, b}); err != nil {
		t.Fatal(err)
	}
	// Diamond edges revisit a completed node without reporting a cycle.
	a.Relations = []Relation{{Type: "member_of", TargetID: "b"}, {Type: "member_of", TargetID: "c"}}
	b.Parent, c.Parent, d.Parent = validationPtr("d"), validationPtr("d"), nil
	b.Relations = nil
	if err := ValidateTerms([]Term{d, c, b, a}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTerms(nil); err != nil {
		t.Fatal(err)
	}
}

func TestIterativeTraversalDoesNotMutateDeepGraph(t *testing.T) {
	terms := make([]Term, 4096)
	for index := range terms {
		terms[index] = validationTerm(fmt.Sprintf("term_%04d", index))
		if index+1 < len(terms) {
			terms[index].Parent = validationPtr(fmt.Sprintf("term_%04d", index+1))
		}
	}
	before, _ := json.Marshal(terms)
	if err := ValidateTerms(terms); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(terms)
	if !bytes.Equal(before, after) {
		t.Fatal("graph validation mutated input")
	}
	// This proves bounded stack usage, not parity with Python's recursion limit.
}

func TestRegistryValidationOrder(t *testing.T) {
	groups := []Group{{ID: "one", Name: "One"}, {ID: "two", Name: "Two"}, {ID: UnclassifiedGroupID, Name: "Unclassified"}}
	a, b := validationTerm("a"), validationTerm("b")
	requireCatalogError(t, Validate([]Group{groups[0], groups[0]}, []Term{a, a}), "SkillRegistryError", "SKILL_GROUP_ID_CONFLICT", "Duplicate skill group id: one")
	conflict := cloneGroup(groups[1])
	conflict.Name = "  ONE  "
	conflict.Archived = true
	requireCatalogError(t, Validate([]Group{groups[0], conflict}, []Term{a, a}), "SkillRegistryError", "SKILL_GROUP_NAME_CONFLICT", "Skill group name '  ONE  ' conflicts with group 'one'.")
	a.PrimaryGroupID = validationPtr("missing")
	requireCatalogError(t, Validate(groups, []Term{a, b, b}), "ValueError", "", "Duplicate technology id: b")
	b.Relations = []Relation{{Type: "uses", TargetID: "missing_target"}}
	requireCatalogError(t, Validate(groups, []Term{a, b}), "ValueError", "", "Technology relation from b targets unknown id: missing_target")
	b.Relations = nil
	requireCatalogError(t, Validate(groups, []Term{a, b}), "SkillRegistryError", "SKILL_GROUP_NOT_FOUND", "Skill 'Name a' references unknown primary group 'missing'.")
	a.PrimaryGroupID = validationPtr("one")
	groups[0].Archived = true
	a.Aliases = []string{a.Name}
	requireCatalogError(t, Validate(groups, []Term{a}), "SkillRegistryError", "SKILL_GROUP_ARCHIVED", "Active skill 'Name a' cannot use archived group 'One'.")
	a.Archived = true
	requireCatalogError(t, Validate(groups, []Term{a}), "SkillRegistryError", "SKILL_NAME_CONFLICT", "Skill 'Name a' repeats a name or alias after normalization.")
	a.Aliases = nil
	b.Name = "NAME A"
	requireCatalogError(t, Validate(groups, []Term{a, b}), "SkillRegistryError", "SKILL_NAME_CONFLICT", "Name or alias 'NAME A' conflicts with skill 'Name a'.")
	b.Name, b.Aliases = "Other", []string{" \tNAME A "}
	requireCatalogError(t, Validate(groups, []Term{a, b}), "SkillRegistryError", "SKILL_NAME_CONFLICT", "Name or alias ' \tNAME A ' conflicts with skill 'Name a'.")
	// A later skill's bad group cannot mask an earlier name conflict.
	c := validationTerm("c")
	c.PrimaryGroupID = validationPtr("missing")
	requireCatalogError(t, Validate(groups, []Term{a, b, c}), "SkillRegistryError", "SKILL_NAME_CONFLICT", "Name or alias ' \tNAME A ' conflicts with skill 'Name a'.")
	if err := Validate(nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryUnicodeAndCascadeState(t *testing.T) {
	groups := []Group{{ID: "first", Name: "Straße"}, {ID: "second", Name: "STRASSE"}}
	requireCatalogError(t, Validate(groups, nil), "SkillRegistryError", "SKILL_GROUP_NAME_CONFLICT", "Skill group name 'STRASSE' conflicts with group 'first'.")
	groups = []Group{{ID: "group", Name: "Group"}, {ID: "other", Name: "Other"}}
	a := validationTerm("a")
	a.PrimaryGroupID = validationPtr("group")
	a.Name, a.Aliases = "Data Science", []string{"\u001cDATA\u00a0SCIENCE\u001f"}
	requireCatalogError(t, Validate(groups, []Term{a}), "SkillRegistryError", "SKILL_NAME_CONFLICT", "Skill 'Data Science' repeats a name or alias after normalization.")
	a.Aliases = nil
	groups[0].CascadeArchivedSkillIDs = []string{"a"}
	requireCatalogError(t, Validate(groups, []Term{a}), "SkillRegistryError", "SKILL_GROUP_CASCADE_INVALID", "Active skill group 'Group' retains invalid cascade state.")
	groups[0].Archived = true
	requireCatalogError(t, Validate(groups, []Term{a}), "SkillRegistryError", "SKILL_GROUP_ARCHIVED", "Active skill 'Data Science' cannot use archived group 'Group'.")
	a.Archived = true
	if err := Validate(groups, []Term{a}); err != nil {
		t.Fatal(err)
	}
	groups[0].CascadeArchivedSkillIDs = []string{"missing", "a"}
	requireCatalogError(t, Validate(groups, []Term{a}), "SkillRegistryError", "SKILL_GROUP_CASCADE_INVALID", "Skill group 'Group' has invalid cascade state for 'missing'.")
	groups[0].CascadeArchivedSkillIDs = []string{"a"}
	a.PrimaryGroupID = validationPtr("other")
	requireCatalogError(t, Validate(groups, []Term{a}), "SkillRegistryError", "SKILL_GROUP_CASCADE_INVALID", "Skill group 'Group' has invalid cascade state for 'a'.")
	// Cascade checks run after every skill's group/name check.
	a.PrimaryGroupID = validationPtr("missing")
	requireCatalogError(t, Validate(groups, []Term{a}), "SkillRegistryError", "SKILL_GROUP_NOT_FOUND", "Skill 'Data Science' references unknown primary group 'missing'.")
}

func TestDeepClonesAndJSONProjection(t *testing.T) {
	shared := "shared"
	original := Term{ID: "term", Name: "Term", Aliases: []string{"Alias"}, Category: "custom_skill", Kind: "technology", PrimaryGroupID: &shared, Parent: &shared, PrimaryDomain: &shared, CreatedVia: &shared, CreatedAt: &shared, Relations: []Relation{{Type: "uses", TargetID: "other"}}, FieldsSet: []string{"name", "aliases"}}
	cloned := cloneTerm(original)
	cloned.Aliases[0], cloned.Relations[0].TargetID, cloned.FieldsSet[0] = "changed", "changed", "changed"
	for _, pointer := range []*string{cloned.PrimaryGroupID, cloned.Parent, cloned.PrimaryDomain, cloned.CreatedVia, cloned.CreatedAt} {
		*pointer = "changed"
	}
	if original.Aliases[0] != "Alias" || original.Relations[0].TargetID != "other" || original.FieldsSet[0] != "name" || shared != "shared" {
		t.Fatal("clone retained caller-owned memory")
	}
	*cloned.Parent = "parent only"
	if *cloned.PrimaryGroupID != "changed" {
		t.Fatal("independent pointer fields alias one another")
	}
	empty := cloneTerm(Term{})
	if empty.Aliases == nil || empty.Relations == nil || empty.FieldsSet == nil || empty.PrimaryGroupID != nil || empty.Parent != nil || empty.PrimaryDomain != nil || empty.CreatedVia != nil || empty.CreatedAt != nil {
		t.Fatal("clone lost empty/null distinctions")
	}
	raw, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if len(fields) != 12 || string(fields["aliases"]) != "[]" || string(fields["relations"]) != "[]" || string(fields["parent"]) != "null" {
		t.Fatalf("invalid term field projection: %s", raw)
	}
	if _, exists := fields["FieldsSet"]; exists {
		t.Fatal("field-presence metadata leaked into JSON")
	}
	group := Group{ID: "group", Name: "Group", CascadeArchivedSkillIDs: []string{"a"}}
	clonedGroup := cloneGroup(group)
	clonedGroup.CascadeArchivedSkillIDs[0] = "changed"
	if group.CascadeArchivedSkillIDs[0] != "a" || cloneGroup(Group{}).CascadeArchivedSkillIDs == nil {
		t.Fatal("group clone shared slice or returned null collection")
	}
	raw, _ = json.Marshal(Notice{Kind: "notice", CoreID: "core", CustomID: "custom", Value: "value", Resolution: "resolution"})
	if string(raw) != `{"kind":"notice","core_id":"core","custom_id":"custom","value":"value","resolution":"resolution"}` {
		t.Fatalf("notice field projection changed: %s", raw)
	}
}

func TestDefaultGroupsAndEffectiveMapping(t *testing.T) {
	groups := DefaultGroups()
	if len(groups) != 14 || groups[0].ID != "programming_languages" || groups[13].ID != UnclassifiedGroupID {
		t.Fatalf("wrong fallback order: %#v", groups)
	}
	if err := Validate(groups, nil); err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group.Archived || group.CascadeArchivedSkillIDs == nil || len(group.CascadeArchivedSkillIDs) != 0 || group.ID == MyCustomGroupID {
			t.Fatalf("invalid core fallback: %#v", group)
		}
	}
	groups[0].Name, groups[0].CascadeArchivedSkillIDs = "changed", []string{"changed"}
	if !reflect.DeepEqual(DefaultGroups()[0], Group{ID: "programming_languages", Name: "Programming Languages", CascadeArchivedSkillIDs: []string{}}) {
		t.Fatal("caller mutated future fallback groups")
	}
	legacy := map[string]string{
		"ai ai_capability ai_developer_tool ai_integration_protocol ai_service_management machine_learning machine_learning_framework":                                                     "ai_machine_learning",
		"automation developer_tool methodology software_architecture":                                                                                                                      "architecture_delivery",
		"backend backend_framework object_relational_mapper":                                                                                                                               "backend_development",
		"business_intelligence data data_architecture data_format data_modeling data_processing_framework data_tool data_transformation_tool database_connectivity workflow_orchestration": "data_analytics",
		"business_system content_management_platform domain_skill platform":                                                                                                                "business_systems",
		"certification custom_skill": "unclassified",
		"ci_cd cloud cloud_platform container_orchestration deployment devops infrastructure_as_code observability_tool operating_system operational_practice": "cloud_infrastructure",
		"collaboration_suite collaboration_tool productivity_tool video_conferencing":                                                                          "collaboration_productivity",
		"compliance_standard endpoint_management identity_provider identity_security networking password_manager security":                                     "security_identity",
		"database database_platform search_engine":                  "databases_storage",
		"database_language programming_language scripting_language": "programming_languages",
		"frontend frontend_framework":                               "frontend_development",
		"integration messaging_integration":                         "api_integrations",
		"security_testing testing testing_methodology":              "testing_quality",
	}
	legacyPairs := make([][2]string, 0, 64)
	for categories, want := range legacy {
		for _, category := range strings.Fields(categories) {
			legacyPairs = append(legacyPairs, [2]string{category, EffectiveGroupID(Term{Category: category})})
			if got := EffectiveGroupID(Term{Category: category}); got != want {
				t.Errorf("category %q: got %q want %q", category, got, want)
			}
		}
	}
	domainPairs := make([][2]string, 0, 9)
	for domain, want := range map[string]string{"ai_systems": "ai_machine_learning", "software_engineering": "architecture_delivery", "data_and_analytics": "data_analytics", "cloud_and_infrastructure": "cloud_infrastructure", "security_and_identity": "security_identity", "web_and_api": "api_integrations", "business_systems": "business_systems", "product_and_delivery": "architecture_delivery", "other": UnclassifiedGroupID} {
		term := Term{Category: "programming_language", PrimaryDomain: validationPtr(domain)}
		domainPairs = append(domainPairs, [2]string{domain, EffectiveGroupID(term)})
		if got := EffectiveGroupID(term); got != want {
			t.Errorf("domain %q: got %q want %q", domain, got, want)
		}
		term.PrimaryGroupID = validationPtr("explicit_group")
		if got := EffectiveGroupID(term); got != "explicit_group" {
			t.Errorf("explicit group did not win: %q", got)
		}
	}
	for _, category := range []string{"", "unknown", "PROGRAMMING_LANGUAGE", " programming_language "} {
		if got := EffectiveGroupID(Term{Category: category}); got != UnclassifiedGroupID {
			t.Errorf("unexpected category normalization: %q => %q", category, got)
		}
	}
	// Independent Python source-constant oracle: defaults in source order, domain
	// and legacy pairs sorted by key, ASCII compact JSON with sorted object keys.
	// This also checks every fallback name, not merely IDs and group count.
	defaults := make([][2]string, 0, 14)
	for _, group := range DefaultGroups() {
		defaults = append(defaults, [2]string{group.ID, group.Name})
	}
	sort.Slice(legacyPairs, func(i, j int) bool { return legacyPairs[i][0] < legacyPairs[j][0] })
	sort.Slice(domainPairs, func(i, j int) bool { return domainPairs[i][0] < domainPairs[j][0] })
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(map[string][][2]string{"defaults": defaults, "domains": domainPairs, "legacy": legacyPairs}); err != nil {
		t.Fatal(err)
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))))
	if len(legacyPairs) != 64 || len(domainPairs) != 9 || fingerprint != "68381ef59c58eff1b0c64975009dd19ca258e5c8ecdfbb0a86d25a07bc7b19c8" {
		t.Fatalf("Python mapping oracle mismatch: legacy=%d domains=%d hash=%s", len(legacyPairs), len(domainPairs), fingerprint)
	}
}
