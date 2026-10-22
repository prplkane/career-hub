package homebackup

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManifestInvalidFields(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		value       any
	}{
		{"format-bool", "format_version", true},
		{"future-format", "format_version", 3},
		{"home-float", "data_home_format_version", json.Number("2.0")},
		{"home-old", "data_home_format_version", 1},
		{"home-id", "data_home_id", "not-an-id"},
		{"channel", "data_home_channel", "unknown"},
		{"schema-bool", "sqlite_schema_version", true},
		{"schema-future", "sqlite_schema_version", 999999},
		{"files-null", "files", nil},
		{"files-object", "files", map[string]any{}},
		{"extra-field", "unexpected", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var fields map[string]any
			if err := json.Unmarshal(manifestJSON(t, testManifest()), &fields); err != nil {
				t.Fatal(err)
			}
			fields[tc.field] = tc.value
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if _, p := ParseManifest(raw); p == nil {
				t.Fatal("accepted invalid field")
			}
		})
	}
}

func TestManifestFileRules(t *testing.T) {
	for _, name := range []string{"", "../jobs/a", "jobs/../a", "jobs//a", "jobs/./a", "jobs/a/", "/jobs/a", `jobs\a`, "jobs/a:b", "jobs/a\x00b", "untracked/a"} {
		t.Run(name, func(t *testing.T) {
			if _, p := ParseManifest(manifestJSON(t, testManifest(testFile(name, "")))); p == nil {
				t.Fatal("accepted invalid path")
			}
		})
	}
	for _, edit := range []func(*File){
		func(f *File) { f.SizeBytes = -1 }, func(f *File) { f.SizeBytes = MaxEntryBytes + 1 },
		func(f *File) { f.SHA256 = strings.Repeat("A", 64) }, func(f *File) { f.SHA256 = "bad" },
	} {
		f := testFile("jobs/a", "")
		edit(&f)
		if _, p := ParseManifest(manifestJSON(t, testManifest(f))); p == nil {
			t.Fatal("accepted invalid file metadata")
		}
	}
	if _, p := ParseManifest(manifestJSON(t, testManifest(testFile("jobs/ß", ""), testFile("jobs/SS", "")))); p == nil {
		t.Fatal("accepted casefold collision")
	}
	files := make([]File, 17)
	for i := range files {
		files[i] = testFile("jobs/"+string(rune('a'+i)), "")
		files[i].SizeBytes = MaxEntryBytes
	}
	if _, p := ParseManifest(manifestJSON(t, testManifest(files...))); p == nil || p.Code != "BACKUP_SIZE_LIMIT" {
		t.Fatal(p)
	}
}

func TestJSONScalarAndGrammarBoundaries(t *testing.T) {
	base := string(manifestJSON(t, testManifest(testFile("jobs/PLACEHOLDER", ""))))
	for _, tc := range []struct {
		name, token string
		valid       bool
	}{
		{"surrogate-pair", `\ud83d\ude00`, true},
		{"lone-high", `\ud83d`, false},
		{"lone-low", `\ude00`, false},
		{"non-pair", `\ud83d\u1234`, false},
		{"replacement-character", "�", true},
		{"bad-hex", `\uZZZZ`, false},
		{"control", "\n", false},
		{"nul", `\u0000`, false},
		{"escaped-quote", `\"`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, p := ParseManifest([]byte(strings.Replace(base, "PLACEHOLDER", tc.token, 1)))
			if (p == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, p)
			}
		})
	}
	if !validJSONScalars([]byte(`"\\ud800"`)) {
		t.Fatal("escaped backslash mistaken for surrogate")
	}
	for _, raw := range []string{base + `{}`, base + "\xff", strings.Repeat("[", 9) + strings.Repeat("]", 9), `{`, `[]`, `null`} {
		if _, p := ParseManifest([]byte(raw)); p == nil {
			t.Fatal("accepted invalid grammar/root")
		}
	}
	for _, version := range []string{"2.0", "2e0"} {
		raw := strings.Replace(base, `"format_version":2,`, `"format_version":`+version+`,`, 1)
		if _, p := ParseManifest([]byte(raw)); p != nil {
			t.Fatal(p)
		}
	}
}
