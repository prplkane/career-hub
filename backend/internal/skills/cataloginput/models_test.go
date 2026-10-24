package cataloginput

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func str(text string) Value               { return Value{Kind: "string", Text: text} }
func seq(items ...Value) Value            { return Value{Kind: "sequence", Items: items} }
func entry(key string, value Value) Entry { return Entry{Key: str(key), Value: value} }
func obj(entries ...Entry) Value          { return Value{Kind: "mapping", Entries: entries} }
func raw(kind, text string) Value         { return Value{Kind: kind, Text: text} }
func blob(text string) Value              { return Value{Kind: "bytes", Bytes: []byte(text)} }
func baseTerm(overrides ...Entry) Value {
	entries := []Entry{entry("id", str("one")), entry("name", str("One")), entry("category", str("tool"))}
	for _, override := range overrides {
		replaced := false
		for i := range entries {
			if entries[i].Key.Kind == override.Key.Kind && entries[i].Key.Text == override.Key.Text {
				entries[i] = override
				replaced = true
				break
			}
		}
		if !replaced {
			entries = append(entries, override)
		}
	}
	return obj(entries...)
}
func baseGroup(overrides ...Entry) Value {
	term := baseTerm(overrides...)
	entries := []Entry{}
	for _, item := range term.Entries {
		if item.Key.Text != "category" {
			entries = append(entries, item)
		}
	}
	return obj(entries...)
}
func assertIssues(t *testing.T, err *ValidationError, expected string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected validation failure")
	}
	if err.Unsupported != "" {
		t.Fatalf("unexpected unsupported boundary: %s", err.Unsupported)
	}
	actual, marshalErr := json.Marshal(err.Issues)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	var want, got any
	if e := json.Unmarshal([]byte(expected), &want); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal(actual, &got); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("issues = %s\nwant = %s", actual, expected)
	}
}

func TestMinimalDefaultsAndExplicitPresence(t *testing.T) {
	terms, err := ValidateTerms(seq(baseTerm()))
	if err != nil {
		t.Fatal(err)
	}
	term := terms[0]
	if term.ID != "one" || term.Name != "One" || term.Category != "tool" || term.Kind != "technology" || term.Archived {
		t.Fatalf("wrong defaults: %+v", term)
	}
	if term.Aliases == nil || term.Relations == nil || term.FieldsSet == nil {
		t.Fatal("nil default arrays")
	}
	if term.Parent != nil || term.PrimaryGroupID != nil || term.PrimaryDomain != nil || term.CreatedVia != nil || term.CreatedAt != nil {
		t.Fatal("non-null optional default")
	}
	if !reflect.DeepEqual(term.FieldsSet, []string{"category", "id", "name"}) {
		t.Fatal(term.FieldsSet)
	}
	fields := []Entry{entry("aliases", seq()), entry("relations", seq()), entry("kind", str("technology")), entry("archived", Value{Kind: "bool"})}
	for _, name := range []string{"parent", "primary_group_id", "primary_domain", "created_via", "created_at"} {
		fields = append(fields, entry(name, Value{Kind: "null"}))
	}
	explicit, err := ValidateTerms(seq(baseTerm(fields...)))
	if err != nil || len(explicit[0].FieldsSet) != 12 {
		t.Fatalf("explicit defaults: %+v %v", explicit, err)
	}
	groups, groupErr := ValidateGroups(seq(baseGroup()))
	if groupErr != nil || groups[0].CascadeArchivedSkillIDs == nil || groups[0].Archived {
		t.Fatalf("group defaults: %+v %v", groups, groupErr)
	}
	emptyTerms, termErr := ValidateTerms(seq())
	emptyGroups, groupErr := ValidateGroups(seq())
	if termErr != nil || groupErr != nil || emptyTerms == nil || emptyGroups == nil {
		t.Fatal("empty list defaults")
	}
}

func TestTypedValuesPreservedAndOutputsIndependent(t *testing.T) {
	input := seq(baseTerm(entry("id", blob("one")), entry("name", blob(" \x1c Straße \u3000")), entry("category", str("\u0085tool\t")),
		entry("aliases", seq(blob(" Alias "), str("é"), str("e\u0301"))), entry("parent", blob("parent")), entry("primary_group_id", str("group")),
		entry("kind", blob("tool")), entry("primary_domain", blob("other")), entry("created_via", blob("resume_digest")),
		entry("created_at", blob(" 2024-02-29T23:59:59.123456789123Z\n")), entry("archived", blob("YeS")),
		entry("relations", seq(obj(entry("type", blob("uses")), entry("target_id", blob("parent")))))))
	terms, err := ValidateTerms(input)
	if err != nil {
		t.Fatal(err, err.Issues)
	}
	a := terms[0]
	if a.Name != "Straße" || a.Category != "tool" || !a.Archived || a.Kind != "tool" || *a.Parent != "parent" || *a.PrimaryGroupID != "group" || *a.PrimaryDomain != "other" || *a.CreatedVia != "resume_digest" {
		t.Fatalf("projection %+v", a)
	}
	if *a.CreatedAt != "2024-02-29T23:59:59.123456789123Z" || !reflect.DeepEqual(a.Aliases, []string{"Alias", "é", "e\u0301"}) {
		t.Fatal("text changed", a)
	}
	terms[0].Aliases[0] = "Changed"
	terms[0].Relations[0].Type = "replaces"
	terms[0].FieldsSet[0] = "Changed"
	*terms[0].Parent = "changed"
	again, err := ValidateTerms(input)
	if err != nil || again[0].Aliases[0] != "Alias" || again[0].Relations[0].Type != "uses" || *again[0].Parent != "parent" || again[0].FieldsSet[0] == "Changed" {
		t.Fatal("output mutation leaked to input")
	}
	input.Items[0].Entries[0].Value.Bytes[0] = 'x'
	if again[0].ID != "one" {
		t.Fatal("byte storage aliased")
	}
}

func TestRootAndRecordShapes(t *testing.T) {
	for _, input := range []Value{{Kind: "null"}, str(""), obj(), raw("int", "0"), Value{Kind: "bool"}, blob("x"), raw("date", "2024-01-01")} {
		_, err := ValidateTerms(input)
		assertIssues(t, err, `[{"type":"list_type","loc":[],"msg":"Input should be a valid list"}]`)
		_, err = ValidateGroups(input)
		assertIssues(t, err, `[{"type":"list_type","loc":[],"msg":"Input should be a valid list"}]`)
	}
	_, err := ValidateTerms(seq(Value{Kind: "null"}, obj()))
	assertIssues(t, err, `[{"type":"model_type","loc":[0],"msg":"Input should be a valid dictionary or instance of TechnologyTerm"},{"type":"missing","loc":[1,"id"],"msg":"Field required"},{"type":"missing","loc":[1,"name"],"msg":"Field required"},{"type":"missing","loc":[1,"category"],"msg":"Field required"}]`)
	_, err = ValidateGroups(seq(str("x"), obj()))
	assertIssues(t, err, `[{"type":"model_type","loc":[0],"msg":"Input should be a valid dictionary or instance of SkillGroup"},{"type":"missing","loc":[1,"id"],"msg":"Field required"},{"type":"missing","loc":[1,"name"],"msg":"Field required"}]`)
}

func TestErrorOrderAndAfterValidators(t *testing.T) {
	input := obj(entry("z_extra", str("secret")), entry("created_at", str("bad")), entry("archived", str("bad")),
		entry("category", str(" ")), entry("aliases", seq(raw("int", "3"), blob("\xff"))), entry("name", str("\x1c")), entry("id", str("BAD")), entry("a_extra", str("hidden")))
	terms, err := ValidateTerms(seq(input))
	if terms != nil {
		t.Fatal("partial output on error")
	}
	assertIssues(t, err, `[{"type":"string_pattern_mismatch","loc":[0,"id"],"msg":"String should match pattern '^[a-z][a-z0-9_]*$'"},{"type":"value_error","loc":[0,"name"],"msg":"Value error, must not be blank"},{"type":"string_type","loc":[0,"aliases",0],"msg":"Input should be a valid string"},{"type":"string_unicode","loc":[0,"aliases",1],"msg":"Input should be a valid string, unable to parse raw data as a unicode string"},{"type":"value_error","loc":[0,"category"],"msg":"Value error, must not be blank"},{"type":"bool_parsing","loc":[0,"archived"],"msg":"Input should be a valid boolean, unable to interpret input"},{"type":"value_error","loc":[0,"created_at"],"msg":"Value error, created_at must be an ISO 8601 UTC timestamp"},{"type":"extra_forbidden","loc":[0,"z_extra"],"msg":"Extra inputs are not permitted"},{"type":"extra_forbidden","loc":[0,"a_extra"],"msg":"Extra inputs are not permitted"}]`)
	encoded, _ := json.Marshal(err)
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "hidden") || strings.Contains(string(encoded), "\"input\"") {
		t.Fatal("raw input in errors")
	}
	_, err = ValidateTerms(seq(baseTerm(entry("aliases", seq(str("ONE"))), entry("extra", str("x")))))
	assertIssues(t, err, `[{"type":"extra_forbidden","loc":[0,"extra"],"msg":"Extra inputs are not permitted"}]`)
	_, err = ValidateTerms(seq(baseTerm(entry("aliases", seq(str("ONE"))), entry("archived", str("bad")))))
	assertIssues(t, err, `[{"type":"bool_parsing","loc":[0,"archived"],"msg":"Input should be a valid boolean, unable to interpret input"}]`)
	_, err = ValidateTerms(seq(baseTerm(entry("aliases", seq(str("a"), str("A"))))))
	assertIssues(t, err, `[{"type":"value_error","loc":[0],"msg":"Value error, name and aliases must be unique within one competency"}]`)
}

func TestAliasesAndCascadeValidation(t *testing.T) {
	for _, aliases := range []Value{seq(str("ONE")), seq(str("Straße"), str("STRASSE")), seq(str("Data tool"), str("DATA\tTOOL"))} {
		_, err := ValidateTerms(seq(baseTerm(entry("aliases", aliases))))
		assertIssues(t, err, `[{"type":"value_error","loc":[0],"msg":"Value error, name and aliases must be unique within one competency"}]`)
	}
	_, err := ValidateTerms(seq(baseTerm(entry("aliases", seq(str(" "), str("\u0085"))))))
	assertIssues(t, err, `[{"type":"value_error","loc":[0,"aliases"],"msg":"Value error, aliases must not contain blank values"}]`)
	_, err = ValidateTerms(seq(baseTerm(entry("aliases", seq(str(" "), raw("int", "1"))))))
	assertIssues(t, err, `[{"type":"string_type","loc":[0,"aliases",1],"msg":"Input should be a valid string"}]`)
	for _, field := range []string{"aliases", "relations"} {
		_, err = ValidateTerms(seq(baseTerm(entry(field, Value{Kind: "null"}))))
		if len(err.Issues) != 1 || err.Issues[0].Type != "list_type" {
			t.Fatal(field, err)
		}
	}
	for _, tc := range []struct {
		input   Value
		message string
	}{
		{seq(str("BAD"), str("BAD")), "Value error, must not contain duplicate skill ids"},
		{seq(str("BAD"), str("valid")), "Value error, must contain lower-case snake_case skill ids"},
		{seq(str("valid\n")), "Value error, must contain lower-case snake_case skill ids"},
		{seq(str(" "), raw("int", "1")), "Input should be a valid string"},
		{Value{Kind: "null"}, "Input should be a valid list"},
	} {
		_, err = ValidateGroups(seq(baseGroup(entry("cascade_archived_skill_ids", tc.input))))
		if err == nil || len(err.Issues) != 1 || err.Issues[0].Msg != tc.message {
			t.Fatalf("cascade %+v: %+v", tc.input, err)
		}
	}
	groups, err := ValidateGroups(seq(baseGroup(entry("cascade_archived_skill_ids", seq(blob("a"), str("b_2"))))))
	if err != nil || !reflect.DeepEqual(groups[0].CascadeArchivedSkillIDs, []string{"a", "b_2"}) {
		t.Fatal(groups, err)
	}
}

func TestIdentifierAndStringRules(t *testing.T) {
	for _, field := range []string{"id", "parent", "primary_group_id"} {
		for _, text := range []string{"", "A", "1a", "a\n", "a\r\n", "a ", "é", "a-b", "a\x00"} {
			_, err := ValidateTerms(seq(baseTerm(entry(field, str(text)))))
			if err == nil || len(err.Issues) != 1 || err.Issues[0].Type != "string_pattern_mismatch" {
				t.Fatalf("%s %q: %+v", field, text, err)
			}
		}
	}
	for _, field := range []string{"id", "name", "category", "parent", "primary_group_id", "created_at"} {
		for _, input := range []Value{Value{Kind: "bool"}, raw("int", "1"), raw("float", "1.0"), seq(), obj(), raw("date", "2024-01-01"), raw("datetime", "2024-01-01T00:00:00")} {
			_, err := ValidateTerms(seq(baseTerm(entry(field, input))))
			if err == nil || len(err.Issues) != 1 || err.Issues[0].Type != "string_type" {
				t.Fatalf("%s %s: %+v", field, input.Kind, err)
			}
		}
		_, err := ValidateTerms(seq(baseTerm(entry(field, blob("\xff")))))
		if err == nil || len(err.Issues) != 1 || err.Issues[0].Type != "string_unicode" {
			t.Fatalf("%s bytes: %+v", field, err)
		}
	}
}

func TestEnumsAndRelations(t *testing.T) {
	for _, tc := range []struct {
		field   string
		choices []string
	}{{"kind", kinds}, {"primary_domain", domains}, {"created_via", creationSources}} {
		for _, choice := range tc.choices {
			_, err := ValidateTerms(seq(baseTerm(entry(tc.field, str(choice)))))
			if err != nil {
				t.Fatalf("%s %s: %v", tc.field, choice, err)
			}
		}
		for _, input := range []Value{str("UNKNOWN"), blob("\xff"), raw("int", "1"), obj(), seq()} {
			_, err := ValidateTerms(seq(baseTerm(entry(tc.field, input))))
			if err == nil || len(err.Issues) != 1 || err.Issues[0].Type != "enum" || err.Issues[0].Msg != enumMessage(tc.choices) {
				t.Fatalf("enum %s: %+v", tc.field, err)
			}
		}
	}
	for _, kind := range relationTypes {
		_, err := ValidateTerms(seq(baseTerm(entry("relations", seq(obj(entry("type", str(kind)), entry("target_id", str("other"))))))))
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := ValidateTerms(seq(baseTerm(entry("relations", seq(obj(), Value{Kind: "null"}, obj(entry("extra", str("x")), entry("target_id", str("BAD")), entry("type", str("NO"))))))))
	if err == nil {
		t.Fatal("missing relation errors")
	}
	gotTypes := []string{}
	gotLocs := [][]any{}
	for _, issue := range err.Issues {
		gotTypes = append(gotTypes, issue.Type)
		gotLocs = append(gotLocs, issue.Loc)
	}
	if !reflect.DeepEqual(gotTypes, []string{"missing", "missing", "model_type", "enum", "string_pattern_mismatch", "extra_forbidden"}) {
		t.Fatal(gotTypes)
	}
	if !reflect.DeepEqual(gotLocs, [][]any{{0, "relations", 0, "type"}, {0, "relations", 0, "target_id"}, {0, "relations", 1}, {0, "relations", 2, "type"}, {0, "relations", 2, "target_id"}, {0, "relations", 2, "extra"}}) {
		t.Fatal(gotLocs)
	}
}

func TestBooleanCoercionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		input     Value
		want      bool
		errorType string
	}{
		{Value{Kind: "bool", Boolean: true}, true, ""}, {Value{Kind: "bool"}, false, ""},
		{raw("int", "0"), false, ""}, {raw("int", "1"), true, ""}, {raw("int", "2"), false, "bool_parsing"}, {raw("int", "-1"), false, "bool_parsing"},
		{raw("int", "9223372036854775807"), false, "bool_parsing"}, {raw("int", "-9223372036854775808"), false, "bool_parsing"},
		{raw("int", "9223372036854775808"), false, "bool_type"}, {raw("int", "-9223372036854775809"), false, "bool_type"},
		{raw("float", "0.0"), false, ""}, {raw("float", "-0.0"), false, ""}, {raw("float", "1.0"), true, ""}, {raw("float", "2.0"), false, "bool_parsing"},
		{raw("float", "-9.223372036854776e+18"), false, "bool_parsing"}, {raw("float", "9.223372036854776e+18"), false, "bool_type"},
		{raw("float", "1.5"), false, "bool_type"}, {raw("float", "nan"), false, "bool_type"}, {raw("float", "inf"), false, "bool_type"}, {raw("float", "-inf"), false, "bool_type"},
		{raw("float", "1e+100"), false, "bool_type"}, {Value{Kind: "null"}, false, "bool_type"}, {seq(), false, "bool_type"}, {obj(), false, "bool_type"},
		{raw("date", "2024-01-01"), false, "bool_type"}, {str(" yes "), false, "bool_parsing"}, {str("ＴＲＵＥ"), false, "bool_parsing"}, {blob("\xff"), false, "bool_parsing"},
	} {
		terms, err := ValidateTerms(seq(baseTerm(entry("archived", tc.input))))
		if tc.errorType == "" {
			if err != nil || terms[0].Archived != tc.want {
				t.Fatalf("%+v: %+v %v", tc, terms, err)
			}
		} else if err == nil || len(err.Issues) != 1 || err.Issues[0].Type != tc.errorType {
			t.Fatalf("%+v: %+v", tc, err)
		}
	}
	for _, word := range []string{"1", "TRUE", "t", "YES", "Y", "on", "0", "FALSE", "F", "no", "N", "OFF"} {
		for _, input := range []Value{str(word), blob(word)} {
			_, err := ValidateTerms(seq(baseTerm(entry("archived", input))))
			if err != nil {
				t.Fatalf("bool %q: %v", word, err)
			}
		}
	}
}

func TestTimestamps(t *testing.T) {
	for _, text := range []string{"0001-01-01T00:00:00Z", "9999-12-31T23:59:59.123456789123456789Z", "2024-02-29T23:59:59Z", "\x1c2026-01-02T03:04:05.1Z\u3000"} {
		terms, err := ValidateTerms(seq(baseTerm(entry("created_at", str(text)))))
		if err != nil || terms[0].CreatedAt == nil {
			t.Fatal(text, err)
		}
	}
	for _, text := range []string{"", "2024-01-01", "0000-01-01T00:00:00Z", "2023-02-29T00:00:00Z", "2024-00-01T00:00:00Z", "2024-13-01T00:00:00Z", "2024-01-00T00:00:00Z", "2024-01-32T00:00:00Z", "2024-01-01T24:00:00Z", "2024-01-01T00:60:00Z", "2024-01-01T00:00:60Z", "2024-01-01T00:00:00.Z", "2024-01-01T00:00:00.١Z", "２０２４-01-01T00:00:00Z", "2024-1-01T00:00:00Z", "2024-01-01T00:00:00+00:00", "2024-01-01T00:00:00z", "2024-01-01 00:00:00Z", "2024-01-01T00:00:00abcZ", "2024-01-01T00:00:00.Z"} {
		_, err := ValidateTerms(seq(baseTerm(entry("created_at", str(text)))))
		if err == nil || len(err.Issues) != 1 || err.Issues[0].Type != "value_error" {
			t.Fatalf("timestamp %q: %+v", text, err)
		}
	}
}

func TestInvalidKeyLocationsAndOrdering(t *testing.T) {
	entries := []Entry{entry("z_extra", str("x"))}
	keys := []Value{blob("id"), raw("int", "9"), Value{Kind: "null"}, Value{Kind: "bool", Boolean: true}, Value{Kind: "bool"}, raw("int", "9223372036854775808"), raw("float", "-0.0"), blob("a'b"), blob("a\"b"), blob("a\xff\n\t\\")}
	for _, key := range keys {
		entries = append(entries, Entry{Key: key, Value: str("private")})
	}
	_, err := ValidateTerms(seq(obj(entries...)))
	if err == nil || len(err.Issues) != 14 {
		t.Fatalf("invalid key count %+v", err)
	}
	wantKeys := []any{"b'id'", int64(9), "None", int64(1), int64(0), "9223372036854775808", "-0.0", `b"a'b"`, `b'a"b'`, `b'a\xff\n\t\\'`}
	for i, issue := range err.Issues[4:] {
		if issue.Type != "invalid_key" || !reflect.DeepEqual(issue.Loc, []any{0, wantKeys[i]}) {
			t.Fatalf("key%d %+v", i, issue)
		}
	}
	if err.Issues[3].Type != "extra_forbidden" {
		t.Fatal("extras insertion order")
	}
	if bytesRepr([]byte{'\'', '"', '\r', 0, 127}) != `b'\'"\r\x00\x7f'` {
		t.Fatal(bytesRepr([]byte{'\'', '"', '\r', 0, 127}))
	}
}

func TestUnsupportedBoundariesAndErrorBudgets(t *testing.T) {
	for _, input := range []Value{raw("object", "hidden"), str(string([]byte{0xff})), seq(baseTerm(entry("extra", raw("unknown", "x")))), seq(obj(entry("id", str("one")), entry("id", str("two")))), seq(baseTerm(Entry{Key: raw("date", "2024-01-01"), Value: str("x")})), seq(baseTerm(Entry{Key: seq(), Value: str("x")}))} {
		terms, err := ValidateTerms(input)
		if terms != nil || err == nil || err.Unsupported == "" || len(err.Issues) != 0 {
			t.Fatalf("unsupported %+v: %+v %v", input, terms, err)
		}
		if err.Error() != "catalog model input unsupported" {
			t.Fatal(err.Error())
		}
	}
	v := newValidation()
	for i := 0; i < MaxValidationIssues; i++ {
		v.add("missing", []any{i}, "Field required")
	}
	if len(v.issues) != MaxValidationIssues || v.unsupported != "" {
		t.Fatal("issue limit too strict")
	}
	v.add("missing", nil, "Field required")
	if v.unsupported == "" || len(v.issues) != 0 {
		t.Fatal("did not reject, or retained truncated issues")
	}
	v.add("missing", nil, "Field required")
	if len(v.issues) != 0 {
		t.Fatal("continued collecting after unsupported")
	}
	v = newValidation()
	v.add("extra_forbidden", []any{strings.Repeat("<", MaxValidationIssueBytes/6)}, "Extra inputs are not permitted")
	if v.unsupported == "" || len(v.issues) != 0 {
		t.Fatal("encoded-byte limit not enforced")
	}
	_, err := ValidateTerms(seq(obj()))
	if err.Error() != "catalog model validation failed" {
		t.Fatal(err.Error())
	}
}
