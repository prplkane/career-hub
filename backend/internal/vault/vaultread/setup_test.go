package vaultread

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSetupDistinguishesAbsentPartialAndValidatedVault(t *testing.T) {
	for _, tc := range []struct {
		name, expected string
		absent         int
	}{
		{"new", "not_configured", vaultFileCount},
		{"partial", "incomplete", 3},
		{"configured", "configured", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := validFixture()
			missing := []string{}
			for _, spec := range schemas[:tc.absent] {
				delete(f.files, spec.path)
				missing = append(missing, spec.source)
			}
			raw, err := setupPayload(context.Background(), f)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				ContractVersion int      `json:"contract_version"`
				State           string   `json:"state"`
				Missing         []string `json:"missing"`
			}
			if json.Unmarshal(raw, &got) != nil || got.ContractVersion != 1 || got.State != tc.expected || !reflect.DeepEqual(got.Missing, missing) {
				t.Fatalf("wrong setup projection: %s", raw)
			}
			if len(f.reads) != vaultFileCount || len(f.files) != vaultFileCount-tc.absent || strings.Contains(string(raw), "Synthetic") || strings.Contains(string(raw), "career_vault") {
				t.Fatal("setup reread, created or exposed candidate data")
			}
		})
	}
}

func TestSetupNeverTreatsPresentInvalidInputAsAnEmptyVault(t *testing.T) {
	for _, tc := range []struct{ raw, code string }{
		{"[private", "VAULT_YAML_INVALID"},
		{"[]", "VAULT_ROOT_TYPE"},
	} {
		f := &fixtureReader{files: map[string][]byte{schemas[styleIndex].path: []byte(tc.raw)}}
		raw, err := setupPayload(context.Background(), f)
		if raw != nil || err == nil || err.Code != tc.code || err.Source != "style" {
			t.Fatal("partial corrupt Vault was hidden behind onboarding")
		}
	}
	f := validFixture()
	f.files[schemas[profileIndex].path] = []byte("name: Synthetic\n")
	if raw, err := setupPayload(context.Background(), f); raw != nil || err == nil || err.Code != "VAULT_RECORD_VALIDATION_FAILED" {
		t.Fatal("complete but invalid Vault was labeled configured")
	}
	for _, tc := range []struct{ native, expected string }{
		{"FILE_READ", "VAULT_READ_FAILED"},
		{"FILE_SIZE_LIMIT", "VAULT_INPUT_LIMIT"},
	} {
		f := &fixtureReader{failure: tc.native}
		if raw, err := setupPayload(context.Background(), f); raw != nil || err == nil || err.Code != tc.expected {
			t.Fatal("unreadable input became onboarding state")
		}
	}
}

func TestSetupCancellationBeforeDuringAndAfterReads(t *testing.T) {
	f := validFixture()
	if raw, err := setupPayload(nil, f); raw != nil || err == nil || err.Code != "VAULT_READ_CANCELED" || len(f.reads) != 0 {
		t.Fatal("nil context accessed the Vault")
	}
	for _, at := range []string{schemas[0].path, schemas[vaultFileCount-1].path} {
		ctx, cancel := context.WithCancel(context.Background())
		f := validFixture()
		f.before = func(path string) {
			if path == at {
				cancel()
			}
		}
		raw, err := setupPayload(ctx, f)
		cancel()
		if raw != nil || err == nil || err.Code != "VAULT_READ_CANCELED" {
			t.Fatal("canceled setup supplied a state")
		}
	}
}
