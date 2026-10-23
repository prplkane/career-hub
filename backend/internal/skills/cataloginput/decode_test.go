package cataloginput

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/catalog"
)

func TestDecodeTermsBytesPreservesLegacyFieldsAndInput(t *testing.T) {
	raw := []byte("# old catalog\r\n- id: one\r\n  name: One\r\n  category: tool\r\n  aliases: [ONE, ' Alt ', ALT]\r\n")
	before := bytes.Clone(raw)
	terms, err := DecodeTerms(raw, false)
	if err != nil || len(terms) != 1 || !reflect.DeepEqual(terms[0].Aliases, []string{"Alt"}) ||
		!reflect.DeepEqual(terms[0].FieldsSet, []string{"aliases", "category", "id", "name"}) {
		t.Fatalf("legacy byte decode mismatch: %#v, %v", terms, err)
	}
	if !bytes.Equal(raw, before) {
		t.Fatal("original bytes rewritten")
	}
	// The model owns outputs, not storage shared with the source byte buffer.
	for i := range raw {
		raw[i] = 'x'
	}
	if terms[0].Name != "One" || terms[0].Aliases[0] != "Alt" {
		t.Fatal("decoded values alias original bytes")
	}
}

func TestDecodeFalseyRootsAndRequiredCatalogs(t *testing.T) {
	for _, raw := range []string{"", "# comment\n", "---\n", "null", "false", "0", "-0.0", "''", "[]", "{}", "!!binary ''"} {
		terms, err := DecodeTerms([]byte(raw), true)
		if err != nil || terms == nil || len(terms) != 0 {
			t.Fatalf("optional term root %q: %#v %v", raw, terms, err)
		}
		if terms, err = DecodeTerms([]byte(raw), false); terms != nil || err == nil || err.Stage != "catalog" || err.Code != "CATALOG_EMPTY" {
			t.Fatalf("required term root %q: %#v %v", raw, terms, err)
		}
		groups, groupErr := DecodeGroups([]byte(raw), true)
		if groupErr != nil || groups == nil || len(groups) != 0 {
			t.Fatalf("optional group root %q", raw)
		}
		if groups, groupErr = DecodeGroups([]byte(raw), false); groups != nil || groupErr == nil || groupErr.Code != "CATALOG_EMPTY" {
			t.Fatalf("required group root %q", raw)
		}
	}
}

func TestDecodeFailuresPreserveStageAndNeverReturnPartialRecords(t *testing.T) {
	for _, test := range []struct{ raw, stage string }{
		{"[", "yaml"}, {"- id: one\n  name: One\n", "model"},
		{"- {id: one, name: One, category: tool}\n- null\n", "model"},
		{"- {id: one, name: One, category: tool, aliases: [ONE], archived: false}", "model"},
		{"- {id: one, name: One, category: tool}\n- {id: one, name: Two, category: tool}", "catalog"},
	} {
		terms, err := DecodeTerms([]byte(test.raw), true)
		if terms != nil || err == nil || err.Stage != test.stage {
			t.Fatalf("stage %s: %#v %v", test.stage, terms, err)
		}
		if strings.Contains(err.Error(), "One") || strings.Contains(err.Error(), "one") {
			t.Fatal("default error text leaked source labels")
		}
	}
	_, err := DecodeTerms([]byte("- {id: one, name: One, category: tool}\n- {id: one, name: Two, category: tool}"), true)
	if err.Code != "CATALOG_DUPLICATE_ID" || err.DuplicateID != "one" {
		t.Fatal("duplicate identity was lost")
	}
	// Whole-list model validation precedes the duplicate-ID pass.
	_, err = DecodeTerms([]byte("- {id: one, name: One, category: tool}\n- {id: one, name: Two, category: tool}\n- {}"), true)
	if err == nil || err.Stage != "model" {
		t.Fatal("duplicate ID hid later record validation errors")
	}
	groups, groupErr := DecodeGroups([]byte("- {id: one, name: One}\n- {id: one, name: Two}"), false)
	if groupErr != nil || len(groups) != 2 {
		t.Fatal("group loader invented an early duplicate guard")
	}
	if groups, groupErr = DecodeGroups([]byte("- {id: BAD, name: ''}"), true); groups != nil || groupErr == nil || groupErr.Stage != "model" {
		t.Fatal("invalid groups did not fail validation")
	}
	if groups, groupErr = DecodeGroups([]byte("["), true); groups != nil || groupErr == nil || groupErr.Stage != "yaml" {
		t.Fatal("malformed groups did not fail parsing")
	}
}

func TestDecodedCatalogBuildUsesOriginalBytesAndOverlayPresence(t *testing.T) {
	core := []byte("- {id: one, name: Core, category: tool, archived: true}\n")
	custom := []byte("- {id: one, name: Private, category: custom_skill}\n")
	coreTerms, coreErr := DecodeTerms(core, false)
	customTerms, customErr := DecodeTerms(custom, true)
	if coreErr != nil || customErr != nil {
		t.Fatalf("byte decode failed: %v %v", coreErr, customErr)
	}
	snapshot, err := catalog.Build(nil, nil, coreTerms, customTerms, nil, custom)
	if err != nil {
		t.Fatal(err)
	}
	terms := snapshot.Skills()
	if len(terms) != 1 || terms[0].Name != "Private" || !terms[0].Archived || !reflect.DeepEqual(terms[0].Aliases, []string{"Core"}) {
		t.Fatalf("decoded absent archive override was lost: %#v", terms)
	}
	if snapshot.Revision() != catalog.RawRevision(nil, custom) {
		t.Fatal("raw revision did not use exact input bytes")
	}
	cleared := []byte("- {id: one, name: Private, category: custom_skill, archived: false}\n")
	customTerms, customErr = DecodeTerms(cleared, true)
	if customErr != nil {
		t.Fatal(customErr)
	}
	snapshot, err = catalog.Build(nil, nil, coreTerms, customTerms, nil, cleared)
	if err != nil || snapshot.Skills()[0].Archived {
		t.Fatal("explicit false did not override core archive")
	}
}
