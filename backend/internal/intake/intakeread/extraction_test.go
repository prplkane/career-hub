package intakeread

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type extractionCase struct {
	Name   string                     `json:"name"`
	Valid  bool                       `json:"valid"`
	Set    map[string]json.RawMessage `json:"set"`
	Delete []string                   `json:"delete"`
	Raw    *string                    `json:"raw"`
	Want   map[string]any             `json:"want"`
}

func TestExtractionCasesMatchSourceOracle(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "tests", "fixtures", "migration_intake_extraction_cases.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Base  map[string]json.RawMessage `json:"base"`
		Cases []extractionCase           `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 20 {
		t.Fatal("source-built extraction corpus is unexpectedly incomplete")
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			var input []byte
			if tc.Raw != nil {
				input = []byte(*tc.Raw)
			} else {
				payload := maps.Clone(fixture.Base)
				for _, key := range tc.Delete {
					delete(payload, key)
				}
				maps.Copy(payload, tc.Set)
				var marshalErr error
				input, marshalErr = json.Marshal(payload) // JSON is valid YAML input.
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
			}
			got, failure := ParseExtraction(input)
			if !tc.Valid {
				if failure == nil || failure.Code == "" || !reflect.DeepEqual(got, Extraction{}) {
					t.Fatalf("invalid source case accepted or returned partial result: %+v / %+v", got, failure)
				}
				return
			}
			if failure != nil {
				t.Fatalf("source-valid case rejected: %s", failure.Code)
			}
			if got.Source.SHA256 != strings.Repeat("a", 64) {
				t.Fatalf("source projection differs: %+v", got.Source)
			}
			actual := map[string]any{
				"source_id":         got.Source.ID,
				"schema_version":    got.SchemaVersion,
				"extractor_version": got.ExtractorVersion,
				"registry_revision": got.RegistryRevision,
				"blocks":            float64(len(got.Blocks)),
				"section_hints":     float64(len(got.SectionHints)),
			}
			wantSourceID := "src_fictional_1234567890"
			if specified, ok := tc.Want["source_id"]; ok {
				var stringValue bool
				wantSourceID, stringValue = specified.(string)
				if !stringValue {
					t.Fatal("source fixture ID is not a string")
				}
			}
			if got.Source.ID != wantSourceID {
				t.Fatalf("source ID: got %q, want %q", got.Source.ID, wantSourceID)
			}
			if len(got.Blocks) > 0 {
				actual["first_block_id"] = got.Blocks[0].ID
				actual["first_block_text"] = got.Blocks[0].Text
			}
			if len(got.SectionHints) > 0 {
				actual["first_hint_type"] = got.SectionHints[0].SectionType
				actual["first_hint_source_id"] = got.SectionHints[0].SourceID
				actual["first_hint_block_id"] = got.SectionHints[0].BlockID
			}
			for key, want := range tc.Want {
				if actual[key] != want {
					t.Fatalf("%s: got %v, want %v", key, actual[key], want)
				}
			}
		})
	}
}

func TestExtractionInputLimitAndNoPartialProjection(t *testing.T) {
	tooLarge := []byte(strings.Repeat(" ", MaxExtractionBytes+1))
	got, failure := ParseExtraction(tooLarge)
	if failure == nil || failure.Code != "EXTRACTION_INPUT_LIMIT" || !reflect.DeepEqual(got, Extraction{}) {
		t.Fatalf("over-limit extraction was not a refusal: %+v / %+v", got, failure)
	}
}

func FuzzParseExtraction(f *testing.F) {
	for _, seed := range []string{"", "[broken", "source: {id: src_fictional}\nblocks: []\n"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > MaxExtractionBytes {
			return
		}
		got, failure := ParseExtraction(raw)
		if failure != nil && !reflect.DeepEqual(got, Extraction{}) {
			t.Fatalf("invalid input returned partial extraction: %s", failure.Code)
		}
	})
}
