package cataloginput

import (
	"reflect"
	"strings"
	"testing"
)

func TestYAMLScalarCharacterization(t *testing.T) {
	cases := []struct {
		raw  string
		want Value
	}{
		{"", Value{Kind: "null"}}, {"# empty", Value{Kind: "null"}}, {"\ufeffyes", Value{Kind: "bool", Boolean: true}},
		{"yes", Value{Kind: "bool", Boolean: true}}, {"OFF", Value{Kind: "bool"}}, {"y", Value{Kind: "string", Text: "y"}},
		{"08", Value{Kind: "string", Text: "08"}}, {"0o10", Value{Kind: "string", Text: "0o10"}}, {"1e3", Value{Kind: "string", Text: "1e3"}},
		{"012", Value{Kind: "int", Text: "10"}}, {"1:20", Value{Kind: "int", Text: "80"}}, {"0xFF", Value{Kind: "int", Text: "255"}},
		{"1.0e+3", Value{Kind: "float", Text: "1000.0"}}, {".nan", Value{Kind: "float", Text: "nan"}}, {"-.inf", Value{Kind: "float", Text: "-inf"}},
		{"-0.0", Value{Kind: "float", Text: "-0.0"}}, {"1:20.5", Value{Kind: "float", Text: "80.5"}},
		{"2026-09-07", Value{Kind: "date", Text: "2026-09-07"}}, {"2026-09-07T00:00:00Z", Value{Kind: "datetime", Text: "2026-09-07T00:00:00+00:00"}},
		{"2001-1-2 3:04:05.123456789-5:30", Value{Kind: "datetime", Text: "2001-01-02T03:04:05.123456-05:30"}},
		{"'yes'", Value{Kind: "string", Text: "yes"}}, {"!!str yes", Value{Kind: "string", Text: "yes"}}, {"! yes", Value{Kind: "bool", Boolean: true}},
		{"!!null whatever", Value{Kind: "null"}}, {"!!int 0o10", Value{Kind: "int", Text: "8"}}, {"!!int 1:99", Value{Kind: "int", Text: "159"}},
		{"!!binary SGk=", Value{Kind: "bytes", Bytes: []byte("Hi")}}, {"!!binary SG!k=", Value{Kind: "bytes", Bytes: []byte("Hi")}},
		{"!!binary AA===BBBB", Value{Kind: "bytes", Bytes: []byte{0}}}, {"!!binary ====", Value{Kind: "bytes", Bytes: []byte{}}},
		{"!!float 1e3", Value{Kind: "float", Text: "1000.0"}},
	}
	for _, tt := range cases {
		t.Run(tt.raw, func(t *testing.T) {
			got, e := ParseYAML([]byte(tt.raw))
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v; want %#v", got, tt.want)
			}
		})
	}
}

func TestYAMLDictionaryOrderAndEquality(t *testing.T) {
	cases := []struct {
		raw          string
		keys, values []Value
	}{
		{"{a: first, b: second, a: last}", []Value{{Kind: "string", Text: "a"}, {Kind: "string", Text: "b"}}, []Value{{Kind: "string", Text: "last"}, {Kind: "string", Text: "second"}}},
		{"{true: a, 1: b, 1.0: c, false: d, 0.0: e}", []Value{{Kind: "bool", Boolean: true}, {Kind: "bool"}}, []Value{{Kind: "string", Text: "c"}, {Kind: "string", Text: "e"}}},
		{"{=: yes}", []Value{{Kind: "string", Text: "="}}, []Value{{Kind: "bool", Boolean: true}}},
		{"{<<: [{x: first, a: one}, {x: second, b: two}], x: local}", []Value{{Kind: "string", Text: "x"}, {Kind: "string", Text: "b"}, {Kind: "string", Text: "a"}}, []Value{{Kind: "string", Text: "local"}, {Kind: "string", Text: "two"}, {Kind: "string", Text: "one"}}},
		{"{x: local, <<: {x: base, z: tail}}", []Value{{Kind: "string", Text: "x"}, {Kind: "string", Text: "z"}}, []Value{{Kind: "string", Text: "local"}, {Kind: "string", Text: "tail"}}},
	}
	for _, tt := range cases {
		t.Run(tt.raw, func(t *testing.T) {
			v, e := ParseYAML([]byte(tt.raw))
			if e != nil {
				t.Fatal(e)
			}
			if len(v.Entries) != len(tt.keys) {
				t.Fatalf("entry count %d", len(v.Entries))
			}
			for i, entry := range v.Entries {
				if !reflect.DeepEqual(entry.Key, tt.keys[i]) || !reflect.DeepEqual(entry.Value, tt.values[i]) {
					t.Fatalf("entry %d = %#v", i, entry)
				}
			}
		})
	}
}

func TestYAMLErrorCategories(t *testing.T) {
	cases := []struct{ raw, kind, class, code string }{
		{"[]\n---\n[]", "invalid", "ComposerError", "YAML_MULTIPLE_DOCUMENTS"},
		{"{a: &same one, b: &same two}", "invalid", "ComposerError", "YAML_DUPLICATE_ANCHOR"},
		{"!!python/tuple [one]", "invalid", "ConstructorError", "YAML_UNSUPPORTED_TAG"},
		{"!!str [a]", "invalid", "ConstructorError", "YAML_SCALAR_SHAPE"},
		{"!!bool wat", "invalid", "KeyError", "YAML_BOOL_VALUE"},
		{"!!int 08", "invalid", "ValueError", "YAML_INT_VALUE"},
		{"!!binary a", "invalid", "ConstructorError", "YAML_BINARY_PADDING"},
		{"2026-99-40", "invalid", "ValueError", "YAML_TIMESTAMP_VALUE"},
		{"!!timestamp wat", "invalid", "AttributeError", "YAML_TIMESTAMP_SYNTAX"},
		{"{<<: nope}", "invalid", "ConstructorError", "YAML_MERGE_SHAPE"},
		{"{[a]: b}", "invalid", "ConstructorError", "YAML_UNHASHABLE_KEY"},
		{"&a [*a]", "unsupported", "UnsupportedYAMLValue", "YAML_RECURSIVE_VALUE"},
		{"&a {<<: *a}", "unsupported", "UnsupportedYAMLValue", "YAML_RECURSIVE_MERGE"},
		{"!!set {}", "unsupported", "UnsupportedYAMLValue", "YAML_SAFE_COLLECTION_TYPE"},
		{"{.nan: one}", "unsupported", "UnsupportedYAMLValue", "YAML_NAN_MAPPING_KEY"},
		{"{&a =: x, *a: y}", "unsupported", "UnsupportedYAMLValue", "YAML_SPECIAL_KEY_ALIAS"},
		{`"\uD800"`, "unsupported", "UnsupportedYAMLValue", "YAML_NONSCALAR_UNICODE"},
		{`"é\U0000DFFF"`, "unsupported", "UnsupportedYAMLValue", "YAML_NONSCALAR_UNICODE"},
		{"{<<: !!python/object:anything {a: one}}", "unsupported", "UnsupportedYAMLValue", "YAML_TAGGED_MERGE"},
		{"!!str {=: ten}", "unsupported", "UnsupportedYAMLValue", "YAML_SCALAR_MAPPING_VALUE"},
	}
	for _, tt := range cases {
		t.Run(tt.raw, func(t *testing.T) {
			_, e := ParseYAML([]byte(tt.raw))
			if e == nil || e.Kind != tt.kind || e.Class != tt.class || e.Code != tt.code {
				t.Fatalf("error = %#v", e)
			}
			if strings.Contains(e.Error(), tt.raw) {
				t.Fatal("source text leaked")
			}
		})
	}
}

func TestYAMLResourceLimits(t *testing.T) {
	cases := []struct{ raw, code string }{
		{strings.Repeat(" ", YAMLMaxInputBytes+1), "YAML_INPUT_BYTES"},
		{strings.Repeat("[", YAMLMaxDepth+1) + "x" + strings.Repeat("]", YAMLMaxDepth+1), "YAML_DEPTH"},
		{"[&a x," + strings.Repeat("*a,", YAMLMaxAliasExpansions+1) + "]", "YAML_ALIAS_EXPANSIONS"},
		{"[" + strings.Repeat("0,", YAMLMaxNodes+1) + "]", "YAML_NODES"},
		{"[&a " + strings.Repeat("x", 1024) + "," + strings.Repeat("*a,", YAMLMaxExpandedTextBytes/1024+1) + "]", "YAML_EXPANDED_TEXT"},
		{strings.Repeat("1", YAMLMaxIntegerDigits+1), "YAML_INTEGER_DIGITS"},
	}
	for _, tt := range cases {
		t.Run(tt.code, func(t *testing.T) {
			_, e := ParseYAML([]byte(tt.raw))
			if e == nil || e.Kind != "limit" || e.Code != tt.code {
				t.Fatalf("error = %#v", e)
			}
		})
	}
	_, e := ParseYAML([]byte{0xff})
	if e == nil || e.Class != "UnicodeDecodeError" {
		t.Fatalf("UTF8 error = %#v", e)
	}
	_, e = ParseYAML([]byte{0xff, 0xfe, '[', 0, ']', 0})
	if e == nil || e.Class != "UnicodeDecodeError" {
		t.Fatalf("UTF16 byte input error = %#v", e)
	}
}

func TestYAMLAliasesProduceIndependentValues(t *testing.T) {
	v, e := ParseYAML([]byte("[&a {name: Python}, *a]"))
	if e != nil {
		t.Fatal(e)
	}
	v.Items[0].Entries[0].Value.Text = "changed"
	if v.Items[1].Entries[0].Value.Text != "Python" {
		t.Fatal("mutable Value sharing")
	}
}

func FuzzParseYAML(f *testing.F) {
	for _, s := range []string{"[]", "{a: yes}", "[&a {x: one}, *a]", "{<<: [{x: a},{x: b}]}", "!!binary SGk=", "---\n[]\n---\n{}"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		v, e := ParseYAML(raw)
		if e != nil {
			if v.Kind != "" {
				t.Fatal("partial value on failure")
			}
			if e.Kind != "invalid" && e.Kind != "unsupported" && e.Kind != "limit" {
				t.Fatal("invalid error category")
			}
		}
	})
}
