package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"careerhub.local/backend/internal/skills/resolver"
)

func payloadString(value string) *string { return &value }

func payloadTerm(id, name, group string) Term {
	return Term{ID: id, Name: name, Category: "tool", Kind: "tool", PrimaryGroupID: payloadString(group)}
}

func payloadSnapshot(t *testing.T, groups []Group, skills []Term, coreGroups, coreSkills map[string]bool, notices []Notice) *Snapshot {
	t.Helper()
	snapshot, err := newSnapshot(groups, skills, coreGroups, coreSkills, notices, []byte("groups\r\n"), []byte("skills\n"))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func payloadJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func payloadError(t *testing.T, err error, class, message string) {
	t.Helper()
	var actual *Error
	if !errors.As(err, &actual) || actual.Class != class || actual.Message != message || actual.Code != "" {
		t.Fatalf("got %#v, want %s: %s", err, class, message)
	}
}

func payloadVector() ([]Group, []Term) {
	groups := []Group{
		{ID: "g", Name: "Group <>& \u2028 \u2029 \\u2028 \" \x01 \x7f 😀", CascadeArchivedSkillIDs: []string{"old"}},
		{ID: "empty", Name: "Empty"},
	}
	skill := payloadTerm("skill", "Straße\u2028\\u2028", "g")
	skill.Aliases = []string{"é", "e\u0301"}
	skill.Relations = []Relation{{Type: "related_to", TargetID: "old"}}
	old := payloadTerm("old", "Old", "g")
	old.Archived = true
	return groups, []Term{skill, old}
}

func TestRawAndCatalogRevisionPythonVectors(t *testing.T) {
	if actual := RawRevision([]byte("groups\r\n"), []byte("skills\n")); actual != "aea7c8c2eebbe99a780c5c12d095f7e71b9f47049fff1ac949f9949fd40fdf93" {
		t.Fatalf("raw revision %s", actual)
	}
	if actual := RawRevision(nil, nil); actual != "9386abcd75fd16f0a7941a20fd1e9c426728c726a885250838fdcce7d438bab5" {
		t.Fatalf("empty raw revision %s", actual)
	}
	empty, err := catalogRevision(nil, nil)
	if err != nil || empty != "d22b72a385f9f0438fd003f178e4157aa0ae30903a99cf2c40c227acb0bdd0b9" {
		t.Fatalf("empty catalog revision %s, %v", empty, err)
	}
	for _, tc := range []struct {
		name     string
		via, at  *string
		expected string
	}{
		{"legacy", nil, nil, "e2aca99ee544be2596a19207cc62a2a02bf2a164b81d7547303f23d7014c5f02"},
		{"via", payloadString("manual_registry"), nil, "0fbe1b5579d5e0db6f4d31762269f6218066974f9980818365f84ffeb83a8195"},
		{"timestamp", nil, payloadString("2026-09-07T23:59:00Z"), "18a54cad2e12408acfb8d066e09e9460b2b31a082a2266a46bd20c4216fa52bb"},
		{"both", payloadString("manual_registry"), payloadString("2026-09-07T23:59:00Z"), "358553eb7b6c138aac4fb6ab7e7a86969cd9729cb95258e36abc310078e7a785"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groups, skills := payloadVector()
			skills[0].CreatedVia, skills[0].CreatedAt = tc.via, tc.at
			revision, err := catalogRevision(groups, skills)
			if err != nil || revision != tc.expected {
				t.Fatalf("revision %s, want %s (%v)", revision, tc.expected, err)
			}
			skills[0].FieldsSet = []string{"created_via", "created_at", "primary_group_id"}
			repeated, err := catalogRevision(groups, skills)
			if err != nil || repeated != revision {
				t.Fatal("model field presence must not enter the catalog hash")
			}
		})
	}
	groups, skills := payloadVector()
	a := payloadSnapshot(t, groups, skills, nil, nil, nil)
	b := payloadSnapshot(t, groups, skills, map[string]bool{"g": true}, map[string]bool{"skill": true}, []Notice{{Kind: "fixture"}})
	if a.CatalogRevision() != b.CatalogRevision() {
		t.Fatal("origin/notices entered catalog revision")
	}
	groups[0], groups[1] = groups[1], groups[0]
	c := payloadSnapshot(t, groups, skills, nil, nil, nil)
	if a.CatalogRevision() == c.CatalogRevision() {
		t.Fatal("input group order lost from catalog revision")
	}
	if RawRevision([]byte("groups\n"), []byte("skills\n")) == a.Revision() {
		t.Fatal("raw revision normalized line endings")
	}
}

func TestCanonicalUnicodeBytesAndEscapes(t *testing.T) {
	value := map[string]any{"z": nil, "é": "\u2028\u2029\\u2028\\u2029<>&/\x00\x01\b\f\n\r\t\"\\\x7f😀", "a": []any{true, false}, "\ue000": "pua", "😀": "astral"}
	encoded, err := canonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	expected := "{\"a\":[true,false],\"z\":null,\"é\":\"\u2028\u2029\\\\u2028\\\\u2029<>&/\\u0000\\u0001\\b\\f\\n\\r\\t\\\"\\\\\x7f😀\",\"\ue000\":\"pua\",\"😀\":\"astral\"}"
	if string(encoded) != expected {
		t.Fatalf("canonical got %q, want %q", encoded, expected)
	}
	if bytes.HasSuffix(encoded, []byte{'\n'}) {
		t.Fatal("revision JSON has a trailing LF")
	}
	for _, bad := range []any{map[string]any{"s": "\xff"}, map[string]any{"\xff": "s"}, 1} {
		if result, err := canonicalJSON(bad); err == nil || result != nil {
			t.Fatal("invalid internal projection accepted")
		}
	}
	if _, err := newSnapshot([]Group{{ID: "g", Name: "\xff"}}, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("invalid Unicode silently replaced in revision")
	}
	array, err := canonicalJSON([]string{"a", "é"})
	if err != nil || string(array) != `["a","é"]` {
		t.Fatal("string array encoding")
	}
}

func TestGroupedPayloadFieldsOriginsAndStableOrder(t *testing.T) {
	groups := []Group{{ID: "g", Name: "Z group"}, {ID: "empty", Name: "A group"}}
	terms := []Term{payloadTerm("second", "STRASSE", "g"), payloadTerm("first", "Straße", "g"), payloadTerm("spaced", "A  B", "g"), payloadTerm("plain", "A B", "g")}
	terms[0].Aliases = []string{"alternate"}
	terms[0].Parent = payloadString("first")
	terms[0].PrimaryDomain = payloadString("other")
	terms[0].Relations = []Relation{{Type: "uses", TargetID: "first"}}
	terms[0].CreatedVia = payloadString("manual_registry")
	terms[0].CreatedAt = payloadString("2026-09-07T23:59:00Z")
	terms[0].FieldsSet = []string{"name"}
	notices := []Notice{{Kind: "fixture", CoreID: "core", CustomID: "custom", Value: "value", Resolution: "kept"}}
	snapshot := payloadSnapshot(t, groups, terms, map[string]bool{"g": true}, map[string]bool{"second": true}, notices)
	payload := snapshot.Payload()
	if payload["skill_count"] != 4 || payload["default_custom_group_id"] != MyCustomGroupID || payload["revision"] != snapshot.Revision() || payload["catalog_revision"] != snapshot.CatalogRevision() {
		t.Fatal("payload header")
	}
	gotGroups := payload["groups"].([]any)
	first, empty := gotGroups[0].(map[string]any), gotGroups[1].(map[string]any)
	if first["id"] != "g" || empty["id"] != "empty" || first["origin"] != "core" || empty["origin"] != "custom" {
		t.Fatal("group order/origin")
	}
	ordered := first["skills"].([]any)
	ids := make([]string, 0, len(ordered))
	for _, value := range ordered {
		ids = append(ids, value.(map[string]any)["id"].(string))
	}
	if !reflect.DeepEqual(ids, []string{"spaced", "plain", "second", "first"}) {
		t.Fatalf("stable casefold-only order %v", ids)
	}
	skill := ordered[2].(map[string]any)
	for key, expected := range map[string]any{"id": "second", "name": "STRASSE", "category": "tool", "kind": "tool", "primary_group_id": "g", "parent": "first", "primary_domain": "other", "archived": false, "created_via": "manual_registry", "created_at": "2026-09-07T23:59:00Z", "origin": "core"} {
		if skill[key] != expected {
			t.Fatalf("%s=%v, want %v", key, skill[key], expected)
		}
	}
	if len(skill) != 13 || !reflect.DeepEqual(skill["aliases"], []any{"alternate"}) || !reflect.DeepEqual(skill["relations"], []any{map[string]any{"type": "uses", "target_id": "first"}}) {
		t.Fatal("model projection fields")
	}
	legacy := ordered[3].(map[string]any)
	for _, key := range []string{"primary_domain", "parent", "created_via", "created_at"} {
		value, present := legacy[key]
		if !present || value != nil {
			t.Fatalf("%s null missing", key)
		}
	}
	if !reflect.DeepEqual(legacy["aliases"], []any{}) || !reflect.DeepEqual(legacy["relations"], []any{}) || !reflect.DeepEqual(empty["skills"], []any{}) || !reflect.DeepEqual(first["cascade_archived_skill_ids"], []any{}) {
		t.Fatal("empty arrays serialized as null")
	}
	if !reflect.DeepEqual(payload["compatibility_notices"], []any{map[string]any{"kind": "fixture", "core_id": "core", "custom_id": "custom", "value": "value", "resolution": "kept"}}) {
		t.Fatal("notice projection")
	}
	if snapshot.GroupOrigin("unknown") != "custom" || snapshot.SkillOrigin("unknown") != "custom" {
		t.Fatal("unknown ID origin")
	}
	// Fallback is a payload projection, not a mutation or a canonical-hash rewrite.
	fallback := Term{ID: "fallback", Name: "Fallback", Category: "programming_language", Kind: "language"}
	f := payloadSnapshot(t, []Group{{ID: "programming_languages", Name: "Languages"}}, []Term{fallback}, nil, nil, nil)
	fskill := f.Payload()["groups"].([]any)[0].(map[string]any)["skills"].([]any)[0].(map[string]any)
	if fskill["primary_group_id"] != "programming_languages" || f.Skills()[0].PrimaryGroupID != nil {
		t.Fatal("effective group ID mutated source field")
	}
}

func TestSnapshotInputOutputIsolation(t *testing.T) {
	groups, skills := payloadVector()
	skills[0].CreatedVia = payloadString("manual_registry")
	skills[0].CreatedAt = payloadString("2026-09-07T23:59:00Z")
	skills[0].PrimaryDomain = payloadString("other")
	skills[0].Parent = payloadString("old")
	skills[0].FieldsSet = []string{"name"}
	coreGroups, coreSkills := map[string]bool{"g": true}, map[string]bool{"skill": true}
	notices := []Notice{{Kind: "original"}}
	rawGroups, rawSkills := []byte("groups"), []byte("skills")
	snapshot, err := newSnapshot(groups, skills, coreGroups, coreSkills, notices, rawGroups, rawSkills)
	if err != nil {
		t.Fatal(err)
	}
	before := payloadJSON(t, snapshot.Payload())
	mutate := func(group *Group, skill *Term) {
		group.Name = "mutated"
		group.CascadeArchivedSkillIDs[0] = "changed"
		skill.Name = "mutated"
		skill.Aliases[0] = "changed"
		skill.Relations[0].TargetID = "changed"
		skill.FieldsSet[0] = "changed"
		*skill.PrimaryGroupID = "changed"
		*skill.Parent = "changed"
		*skill.PrimaryDomain = "changed"
		*skill.CreatedVia = "changed"
		*skill.CreatedAt = "changed"
	}
	mutate(&groups[0], &skills[0])
	coreGroups["g"] = false
	delete(coreSkills, "skill")
	notices[0].Kind = "changed"
	rawGroups[0] = 'X'
	rawSkills[0] = 'X'
	returnedGroups, returnedSkills := snapshot.Groups(), snapshot.Skills()
	mutate(&returnedGroups[0], &returnedSkills[0])
	returnedNotices := snapshot.Notices()
	returnedNotices[0].Kind = "changed"
	groupIDs, skillIDs := snapshot.CoreGroupIDs(), snapshot.CoreSkillIDs()
	groupIDs[0] = "changed"
	skillIDs[0] = "changed"
	payload := snapshot.Payload()
	first := payload["groups"].([]any)[0].(map[string]any)
	first["name"] = "changed"
	first["cascade_archived_skill_ids"].([]any)[0] = "changed"
	payloadSkills := first["skills"].([]any)
	for _, value := range payloadSkills {
		item := value.(map[string]any)
		if item["id"] == "skill" {
			item["aliases"].([]any)[0] = "changed"
			item["relations"].([]any)[0].(map[string]any)["target_id"] = "changed"
		}
	}
	payload["compatibility_notices"].([]any)[0].(map[string]any)["kind"] = "changed"
	if after := payloadJSON(t, snapshot.Payload()); !bytes.Equal(before, after) {
		t.Fatalf("snapshot mutated: %s", after)
	}
	if !reflect.DeepEqual(snapshot.CoreGroupIDs(), []string{"g"}) || !reflect.DeepEqual(snapshot.CoreSkillIDs(), []string{"skill"}) || snapshot.Skills()[0].FieldsSet[0] != "name" {
		t.Fatal("accessor aliasing")
	}
	empty := payloadSnapshot(t, nil, nil, nil, nil, nil)
	if empty.Groups() == nil || empty.Skills() == nil || empty.Notices() == nil || empty.CoreGroupIDs() == nil || empty.CoreSkillIDs() == nil {
		t.Fatal("empty accessor arrays are nil")
	}
	sets := payloadSnapshot(t, nil, nil, map[string]bool{"z": true, "a": true, "excluded": false}, map[string]bool{"z": true, "a": true}, nil)
	if !reflect.DeepEqual(sets.CoreGroupIDs(), []string{"a", "z"}) || !reflect.DeepEqual(sets.CoreSkillIDs(), []string{"a", "z"}) {
		t.Fatal("ID sets are not sorted")
	}
}

func TestLookupEnrichmentAndCleanInput(t *testing.T) {
	groups := []Group{{ID: "g", Name: "Core Group"}, {ID: "custom", Name: "Custom Group"}}
	active := payloadTerm("python", "Python", "g")
	active.Aliases = []string{"Py"}
	active.PrimaryDomain = payloadString("software_engineering")
	custom := payloadTerm("custom", "Object-Oriented Programming", "custom")
	custom.Aliases = []string{"OOP"}
	old := payloadTerm("old", "Old Tool", "g")
	old.Archived = true
	snapshot := payloadSnapshot(t, groups, []Term{active, custom, old}, map[string]bool{"g": true}, map[string]bool{"python": true, "old": true}, []Notice{{Kind: "fixture"}})
	requested := []string{"\x1c Python \u3000", "Py", " OOP ", "Old Tool", "unknown", "Python developer"}
	report, err := snapshot.Lookup(requested)
	if err != nil {
		t.Fatal(err)
	}
	if requested[0] != "\x1c Python \u3000" {
		t.Fatal("lookup mutated request")
	}
	if report["schema_version"] != 1 || report["read_only"] != true {
		t.Fatal("lookup metadata")
	}
	if !reflect.DeepEqual(report["registry"], map[string]any{"group_count": 2, "skill_count": 3, "revision": snapshot.Revision(), "catalog_revision": snapshot.CatalogRevision(), "compatibility_notice_count": 1}) {
		t.Fatal("registry metadata")
	}
	if !reflect.DeepEqual(report["summary"], map[string]any{"input_count": 6, "status_counts": map[string]any{"exact_match": 1, "alias_match": 2, "ambiguous": 0, "unmatched": 3, "invalid_reference": 0}}) {
		t.Fatal("lookup summary")
	}
	results := report["results"].([]any)
	statuses := []string{"exact_match", "alias_match", "alias_match", "unmatched", "unmatched", "unmatched"}
	actions := []string{"reuse_existing", "reuse_existing", "reuse_existing", "review_archived_before_adding", "review_before_adding", "review_before_adding"}
	for i, value := range results {
		item := value.(map[string]any)
		if item["status"] != statuses[i] || item["recommended_action"] != actions[i] {
			t.Fatalf("result %d", i)
		}
		if i >= 3 && item["match"] != nil {
			t.Fatal("unmatched result has match")
		}
	}
	first := results[0].(map[string]any)
	if first["source_text"] != "Python" || first["normalized"] != "python" {
		t.Fatal("cleaned source not preserved")
	}
	expectedMatch := map[string]any{"id": "python", "canonical_name": "Python", "origin": "core", "category": "tool", "kind": "tool", "primary_domain": "software_engineering", "primary_group": map[string]any{"id": "g", "name": "Core Group", "origin": "core"}, "archived": false}
	if !reflect.DeepEqual(first["match"], expectedMatch) || !reflect.DeepEqual(first["candidates"], []any{expectedMatch}) || !reflect.DeepEqual(first["archived_candidates"], []any{}) {
		t.Fatal("match enrichment")
	}
	customMatch := results[2].(map[string]any)["match"].(map[string]any)
	if customMatch["origin"] != "custom" || customMatch["primary_group"].(map[string]any)["origin"] != "custom" || customMatch["primary_domain"] != nil {
		t.Fatal("custom origin/null")
	}
	archived := results[3].(map[string]any)["archived_candidates"].([]any)
	if len(archived) != 1 || archived[0].(map[string]any)["id"] != "old" || archived[0].(map[string]any)["archived"] != true {
		t.Fatal("archived candidates")
	}
	first["match"].(map[string]any)["primary_group"].(map[string]any)["name"] = "changed"
	if first["candidates"].([]any)[0].(map[string]any)["primary_group"].(map[string]any)["name"] != "Core Group" {
		t.Fatal("match and candidate maps alias")
	}
	repeated, err := snapshot.Lookup([]string{"Python"})
	if err != nil || !reflect.DeepEqual(repeated["results"].([]any)[0].(map[string]any)["match"], expectedMatch) {
		t.Fatal("lookup result mutated snapshot")
	}
}

func TestLookupErrorOrderAndDefensiveSnapshotCases(t *testing.T) {
	duplicate := payloadSnapshot(t, nil, []Term{payloadTerm("same", "One", "missing"), payloadTerm("same", "Two", "missing")}, nil, nil, nil)
	for _, tc := range []struct {
		terms   []string
		message string
	}{
		{nil, "at least one --term is required"}, {[]string{}, "at least one --term is required"}, {[]string{"\x1c\u3000"}, "term 1 must not be blank"}, {[]string{"One", "\t", ""}, "term 2 must not be blank"},
	} {
		result, err := duplicate.Lookup(tc.terms)
		payloadError(t, err, "SkillLookupError", tc.message)
		if result != nil {
			t.Fatal("partial lookup result")
		}
	}
	_, err := duplicate.Lookup([]string{"One"})
	payloadError(t, err, "ValueError", "Duplicate technology id")
	missing := payloadSnapshot(t, nil, []Term{payloadTerm("one", "One", "missing")}, nil, nil, nil)
	_, err = missing.Lookup([]string{"One"})
	payloadError(t, err, "SkillLookupError", "Skill 'One' references an unknown group 'missing'")
	empty := payloadSnapshot(t, nil, nil, nil, nil, nil)
	result, err := empty.Lookup([]string{"unknown"})
	if err != nil || result["results"].([]any)[0].(map[string]any)["recommended_action"] != "review_before_adding" {
		t.Fatal("empty snapshot lookup")
	}
	// Ambiguity cannot pass current Build validation, but Snapshot mirrors the
	// Python dataclass's defensive behavior for this manually supplied projection.
	a, b := payloadTerm("zeta", "One", "g"), payloadTerm("alpha", "Two", "g")
	a.Aliases = []string{"same"}
	b.Aliases = []string{"same"}
	ambiguous := payloadSnapshot(t, []Group{{ID: "g", Name: "Group"}}, []Term{a, b}, nil, nil, nil)
	result, err = ambiguous.Lookup([]string{"same"})
	if err != nil {
		t.Fatal(err)
	}
	row := result["results"].([]any)[0].(map[string]any)
	if row["status"] != "ambiguous" || row["recommended_action"] != "review_ambiguous" || row["match"] != nil || row["candidates"].([]any)[0].(map[string]any)["id"] != "alpha" {
		t.Fatal("ambiguous lookup order/action")
	}
	if recommendedAction(resolver.ExactMatch, []string{"old"}) != "reuse_existing" || recommendedAction(resolver.Ambiguous, []string{"old"}) != "review_ambiguous" {
		t.Fatal("recommendation precedence")
	}
}

func TestCleanLookupTermsBeforeExternalBuild(t *testing.T) {
	input := []string{"\x1c Python \u3000", " Data  Engineering ", "\u200b"}
	cleaned, err := CleanLookupTerms(input)
	if err != nil || !reflect.DeepEqual(cleaned, []string{"Python", "Data  Engineering", "\u200b"}) {
		t.Fatalf("cleaned %v, error %v", cleaned, err)
	}
	cleaned[0] = "changed"
	if input[0] != "\x1c Python \u3000" {
		t.Fatal("clean helper aliases input")
	}
	for _, tc := range []struct {
		texts   []string
		message string
	}{
		{nil, "at least one --term is required"}, {[]string{"one", "\x1c\u3000", ""}, "term 2 must not be blank"},
	} {
		result, err := CleanLookupTerms(tc.texts)
		payloadError(t, err, "SkillLookupError", tc.message)
		if result != nil {
			t.Fatal("partial cleaned terms returned")
		}
	}
}

func TestConcurrentSnapshotPayloadAndLookup(t *testing.T) {
	snapshot := payloadSnapshot(t, []Group{{ID: "g", Name: "Group"}}, []Term{payloadTerm("s", "Straße", "g")}, map[string]bool{"g": true}, map[string]bool{"s": true}, nil)
	before := string(payloadJSON(t, snapshot.Payload()))
	var tasks sync.WaitGroup
	for i := 0; i < 8; i++ {
		tasks.Go(func() {
			for n := 0; n < 30; n++ {
				got, err := snapshot.Lookup([]string{"STRASSE"})
				if err != nil || got["results"].([]any)[0].(map[string]any)["status"] != "exact_match" {
					t.Error("lookup unstable")
				}
				if string(payloadJSON(t, snapshot.Payload())) != before {
					t.Error("payload unstable")
				}
				snapshot.Skills()[0].Name = strings.Repeat("changed", 2)
			}
		})
	}
	tasks.Wait()
}
