package vaultread

import (
	"context"
	"strings"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
)

func TestSetupImportRequiresExactValidatedVault(t *testing.T) {
	valid := validFixture().files
	if err := ValidateSetupFiles(context.Background(), valid); err != nil {
		t.Fatal(err.Code)
	}
	paths := SetupPaths()
	paths[0] = "arbitrary.yaml"
	if SetupPaths()[0] == paths[0] {
		t.Fatal("caller changed schema")
	}
	for _, test := range []struct {
		name, code string
		change     func(map[string][]byte)
	}{
		{"missing", "VAULT_SETUP_SCOPE_INVALID", func(m map[string][]byte) { delete(m, schemas[0].path) }},
		{"extra", "VAULT_SETUP_SCOPE_INVALID", func(m map[string][]byte) { m["secrets/key"] = []byte("private") }},
		{"wrong_path", "VAULT_SETUP_SCOPE_INVALID", func(m map[string][]byte) { delete(m, schemas[0].path); m["../profile.yaml"] = []byte("private") }},
		{"invalid_yaml", "VAULT_YAML_INVALID", func(m map[string][]byte) { m[schemas[0].path] = []byte("name: [broken") }},
		{"empty_evidence", "VAULT_RECORD_VALIDATION_FAILED", func(m map[string][]byte) { m[schemas[experienceIndex].path] = []byte("[]") }},
		{"foreign_reference", "VAULT_RECORD_VALIDATION_FAILED", func(m map[string][]byte) {
			m[schemas[proofIndex].path] = []byte("- {id: p, claim: Test, evidence_type: experience, source_block: missing}")
		}},
		{"limit", "VAULT_INPUT_LIMIT", func(m map[string][]byte) {
			m[schemas[0].path] = []byte(strings.Repeat("x", cataloginput.YAMLMaxInputBytes+1))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := validFixture().files
			test.change(files)
			if err := ValidateSetupFiles(context.Background(), files); err == nil || err.Code != test.code {
				t.Fatalf("got %v, want %s", err, test.code)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ValidateSetupFiles(ctx, valid); err == nil || err.Code != "VAULT_READ_CANCELED" {
		t.Fatal(err)
	}
}
