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

type approvalCase struct {
	Name   string                     `json:"name"`
	Valid  bool                       `json:"valid"`
	Set    map[string]json.RawMessage `json:"set"`
	Delete []string                   `json:"delete"`
	Raw    *string                    `json:"raw"`
	Want   map[string]any             `json:"want"`
}

func TestApprovalCasesMatchSourceOracle(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "tests", "fixtures", "migration_intake_approval_cases.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Base  map[string]json.RawMessage `json:"base"`
		Cases []approvalCase             `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 25 {
		t.Fatal("source-built approval corpus is unexpectedly incomplete")
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
			got, failure := ParseApproval(input)
			if !tc.Valid {
				if failure == nil || failure.Code == "" || !reflect.DeepEqual(got, Approval{}) {
					t.Fatalf("invalid source case accepted or returned partial result: %+v / %+v", got, failure)
				}
				return
			}
			if failure != nil {
				t.Fatalf("source-valid case rejected: %s", failure.Code)
			}
			sha := "sha256:" + strings.Repeat("a", 64)
			if got.ReviewRevision != sha || got.PreviousVaultRevision != sha || got.VaultRevision != sha {
				t.Fatalf("revision projection differs: %+v", got)
			}
			actual := map[string]any{
				"schema_version":             got.SchemaVersion,
				"status":                     got.Status,
				"source_id":                  got.SourceID,
				"applied_at":                 got.AppliedAt,
				"review_revision":            got.ReviewRevision,
				"previous_vault_revision":    got.PreviousVaultRevision,
				"vault_revision":             got.VaultRevision,
				"previous_registry_revision": optionalText(got.PreviousRegistryRevision),
				"registry_revision":          optionalText(got.RegistryRevision),
				"change_count":               float64(got.ChangeCount),
			}
			wantSourceID := "src_fictional_1234567890"
			if specified, ok := tc.Want["source_id"]; ok {
				var stringValue bool
				wantSourceID, stringValue = specified.(string)
				if !stringValue {
					t.Fatal("source fixture ID is not a string")
				}
			}
			if got.SourceID != wantSourceID {
				t.Fatalf("source ID: got %q, want %q", got.SourceID, wantSourceID)
			}
			for key, want := range tc.Want {
				if actual[key] != want {
					t.Fatalf("%s: got %v, want %v", key, actual[key], want)
				}
			}
		})
	}
}

func optionalText(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func TestApprovalInputLimitAndNoPartialProjection(t *testing.T) {
	tooLarge := []byte(strings.Repeat(" ", MaxApprovalBytes+1))
	got, failure := ParseApproval(tooLarge)
	if failure == nil || failure.Code != "APPROVAL_INPUT_LIMIT" || !reflect.DeepEqual(got, Approval{}) {
		t.Fatalf("over-limit approval was not a refusal: %+v / %+v", got, failure)
	}
}

func FuzzParseApproval(f *testing.F) {
	for _, seed := range []string{"", "[broken", "source_id: src_fictional\nchanges: []\n"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > MaxApprovalBytes {
			return
		}
		got, failure := ParseApproval(raw)
		if failure != nil && !reflect.DeepEqual(got, Approval{}) {
			t.Fatalf("invalid input returned partial approval: %s", failure.Code)
		}
	})
}
