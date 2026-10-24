package catalogwrite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/cataloginput"
)

func encodingGroups() []catalog.Group {
	names := []string{
		"Ordinary group", `quotes: "double" and 'single'`, `C:\synthetic\path`,
		"true", "FALSE", "yes", "NO", "on", "off", "null", "~", "012", "0xFF",
		"1:20", "1e3", ".nan", ".inf", "2026-09-08", "[]", "{}", "# comment",
		"&anchor", "*alias", "!tag", "---", "...", "a: b", "a # b",
		"first\nsecond", "first\r\nsecond", "first\rsecond", "first\tsecond",
		"first\x00second", "first\x01second", "first\x7fsecond", "first\u0085second",
		"first\u2028second", "first\u2029second", "Résumé 数据 日本語", "astral 😀 𝄞",
		"e\u0301 is not normalized", "zero\u200bwidth", "leading interior\n  spaces\nlast",
		strings.Repeat("existing long name ", 20) + "preserved",
	}
	groups := make([]catalog.Group, len(names))
	for i, name := range names {
		groups[i] = catalog.Group{ID: fmt.Sprintf("user_case_%d", i), Name: name}
	}
	groups = append(groups, catalog.Group{
		ID: "user_archived", Name: "Archived group", Archived: true,
		CascadeArchivedSkillIDs: []string{"user_second", "user_first"},
	})
	return groups
}

func assertEncodedGroupsEqual(t *testing.T, want, got []catalog.Group) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatal("group count changed")
	}
	for i := range want {
		if want[i].ID != got[i].ID || want[i].Name != got[i].Name || want[i].Archived != got[i].Archived || !slices.Equal(want[i].CascadeArchivedSkillIDs, got[i].CascadeArchivedSkillIDs) {
			t.Fatalf("group %d model changed", i)
		}
	}
}

func TestEncodeGroupsOrderOmissionsAndDeterminism(t *testing.T) {
	groups := []catalog.Group{{ID: "user_one", Name: "yes"}, {ID: "user_two", Name: "Second", Archived: true, CascadeArchivedSkillIDs: []string{"user_b", "user_a"}}}
	const want = "- id: \"user_one\"\n  name: \"yes\"\n  archived: false\n- id: \"user_two\"\n  name: \"Second\"\n  archived: true\n  cascade_archived_skill_ids:\n    - \"user_b\"\n    - \"user_a\"\n"
	before, _ := json.Marshal(groups)
	first, err := encodeGroups(groups)
	if err != nil || string(first) != want {
		t.Fatalf("ordered block YAML mismatch: error=%v output=%q", err, first)
	}
	second, err := encodeGroups(groups)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("encoding not deterministic")
	}
	after, _ := json.Marshal(groups)
	if !bytes.Equal(before, after) {
		t.Fatal("encoding mutated input models")
	}
	first[0] = '!'
	if second[0] != '-' {
		t.Fatal("separate encodings share output storage")
	}
}

func TestEncodeGroupsEmpty(t *testing.T) {
	for _, groups := range [][]catalog.Group{nil, {}} {
		raw, err := encodeGroups(groups)
		if err != nil || string(raw) != "[]\n" {
			t.Fatal("empty groups must encode as an empty YAML sequence")
		}
	}
}

func TestEncodeGroupsScalarRoundTrip(t *testing.T) {
	want := encodingGroups()
	raw, err := encodeGroups(want)
	if err != nil || !utf8.Valid(raw) || bytes.ContainsRune(raw, '\r') {
		t.Fatal("encoding did not produce UTF-8 LF YAML")
	}
	got, decodeErr := cataloginput.DecodeGroups(raw, true)
	if decodeErr != nil {
		t.Fatalf("encoded groups did not reload: %v", decodeErr)
	}
	assertEncodedGroupsEqual(t, want, got)
}

func TestEncodeGroupsRejectsInvalidUTF8(t *testing.T) {
	for _, group := range []catalog.Group{
		{ID: "bad\xff", Name: "valid"},
		{ID: "user_valid", Name: "bad\xff"},
		{ID: "user_valid", Name: "valid", CascadeArchivedSkillIDs: []string{"bad\xff"}},
	} {
		raw, err := encodeGroups([]catalog.Group{group})
		if raw != nil || err != errGroupEncoding || strings.Contains(err.Error(), "bad") {
			t.Fatal("invalid UTF-8 was not safely refused")
		}
	}
}

func TestEncodeGroupsBounds(t *testing.T) {
	base := catalog.Group{ID: "user_boundary", Name: "x"}
	raw, err := encodeGroups([]catalog.Group{base})
	if err != nil {
		t.Fatal(err)
	}
	base.Name = strings.Repeat("x", cataloginput.YAMLMaxInputBytes-len(raw)+1)
	raw, err = encodeGroups([]catalog.Group{base})
	if err != nil || len(raw) != cataloginput.YAMLMaxInputBytes {
		t.Fatal("exact output byte boundary failed")
	}
	base.Name += "x"
	if raw, err = encodeGroups([]catalog.Group{base}); raw != nil || err != errGroupEncoding {
		t.Fatal("oversized emitted bytes were not refused")
	}
	base.Name = strings.Repeat("x", cataloginput.YAMLMaxInputBytes+1)
	if raw, err = encodeGroups([]catalog.Group{base}); raw != nil || err != errGroupEncoding {
		t.Fatal("oversized scalar input was not refused")
	}
	groups := make([]catalog.Group, cataloginput.YAMLMaxNodes/7+1)
	if raw, err = encodeGroups(groups); raw != nil || err != errGroupEncoding {
		t.Fatal("oversized group AST was not refused")
	}
	base.Name = "small"
	base.CascadeArchivedSkillIDs = make([]string, cataloginput.YAMLMaxNodes)
	if raw, err = encodeGroups([]catalog.Group{base}); raw != nil || err != errGroupEncoding {
		t.Fatal("oversized cascade AST was not refused")
	}
}

func TestGroupYAMLOutputCannotBypassCap(t *testing.T) {
	output := &groupYAMLOutput{}
	if _, err := output.Write(bytes.Repeat([]byte{'x'}, cataloginput.YAMLMaxInputBytes)); err != nil {
		t.Fatal(err)
	}
	// Hide bytes.Reader.WriteTo so io.Copy considers the destination ReaderFrom.
	readerOnly := struct{ io.Reader }{bytes.NewReader([]byte("extra"))}
	if _, err := io.Copy(output, readerOnly); err == nil || output.buffer.Len() != cataloginput.YAMLMaxInputBytes {
		t.Fatal("io.Copy bypassed the byte cap")
	}
}
