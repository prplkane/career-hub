package cataloginput

import (
	"reflect"
	"testing"
)

func preparedString(text string) Value { return Value{Kind: "string", Text: text} }

func preparedRecord(name Value, aliases Value, extra ...Entry) Value {
	return Value{Kind: "mapping", Entries: append([]Entry{
		{Key: preparedString("name"), Value: name},
		{Key: preparedString("aliases"), Value: aliases},
	}, extra...)}
}

func TestPrepareCatalogFalseyRoot(t *testing.T) {
	empty := Value{Kind: "sequence", Items: []Value{}}
	for _, input := range []Value{{Kind: "null"}, {Kind: "bool"}, {Kind: "int", Text: "0"},
		{Kind: "float", Text: "0.0"}, {Kind: "float", Text: "-0.0"}, {Kind: "string"},
		{Kind: "bytes"}, {Kind: "sequence"}, {Kind: "mapping"}} {
		if actual := PrepareCatalog(input, true); !reflect.DeepEqual(actual, empty) {
			t.Fatalf("falsey %s was not empty sequence: %#v", input.Kind, actual)
		}
	}
	for _, input := range []Value{{Kind: "bool", Boolean: true}, {Kind: "int", Text: "1"},
		{Kind: "float", Text: "nan"}, {Kind: "float", Text: "inf"}, {Kind: "float", Text: "bad"},
		{Kind: "string", Text: " "}, {Kind: "bytes", Bytes: []byte{0}}, {Kind: "date", Text: "2026-01-01"},
		{Kind: "unknown"}} {
		if actual := PrepareCatalog(input, true); !reflect.DeepEqual(actual, input) {
			t.Fatalf("truthy/unsupported %s changed", input.Kind)
		}
	}
}

func TestPrepareLegacyAliasesPreservesOriginalTextAndFieldPresence(t *testing.T) {
	aliases := Value{Kind: "sequence", Items: []Value{preparedString(" STRASSE "), preparedString(" \u3000"),
		preparedString(" Alt \t"), preparedString("ALT")}}
	record := preparedRecord(preparedString("Straße"), aliases)
	input := Value{Kind: "sequence", Items: []Value{record}}
	result := PrepareCatalog(input, true)
	got := result.Items[0].Entries[1].Value.Items
	if len(got) != 1 || got[0].Text != " Alt \t" || len(result.Items[0].Entries) != 2 {
		t.Fatalf("legacy cleanup changed text or keys: %#v", result)
	}
	if !reflect.DeepEqual(input.Items[0], record) || len(input.Items[0].Entries[1].Value.Items) != 4 {
		t.Fatal("input aliases mutated")
	}
	if !reflect.DeepEqual(PrepareCatalog(input, false), input) {
		t.Fatal("group/nonlegacy preparation cleaned aliases")
	}
	for _, field := range []string{"kind", "primary_domain", "primary_group_id", "relations", "archived"} {
		input.Items[0] = preparedRecord(preparedString("Straße"), aliases, Entry{preparedString(field), Value{Kind: "null"}})
		if !reflect.DeepEqual(PrepareCatalog(input, true), input) {
			t.Fatalf("explicit %s did not disable compatibility cleanup", field)
		}
	}
	for _, field := range []string{"parent", "created_via", "created_at"} {
		input.Items[0] = preparedRecord(preparedString("Straße"), aliases, Entry{preparedString(field), Value{Kind: "null"}})
		if len(PrepareCatalog(input, true).Items[0].Entries[1].Value.Items) != 1 {
			t.Fatalf("%s incorrectly disabled cleanup", field)
		}
	}
}

func TestPrepareLegacyInvalidOrAlreadyCleanValuesAreNotRepaired(t *testing.T) {
	for _, record := range []Value{
		{Kind: "null"},
		{Kind: "mapping"},
		preparedRecord(Value{Kind: "bytes", Bytes: []byte("Name")}, Value{Kind: "sequence"}),
		preparedRecord(preparedString("Name"), Value{Kind: "null"}),
		preparedRecord(preparedString("Name"), Value{Kind: "sequence", Items: []Value{preparedString("NAME"), {Kind: "null"}}}),
		preparedRecord(preparedString("Name"), Value{Kind: "sequence", Items: []Value{preparedString(" Alt ")}},
			Entry{Key: Value{Kind: "int", Text: "1"}, Value: preparedString("extra")}),
	} {
		input := Value{Kind: "sequence", Items: []Value{record}}
		if !reflect.DeepEqual(PrepareCatalog(input, true), input) {
			t.Fatalf("invalid/clean input repaired: %#v", record)
		}
	}
}
