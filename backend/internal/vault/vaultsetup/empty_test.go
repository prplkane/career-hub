package vaultsetup

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/vault/vaultread"
)

func TestPrepareEmptyDeterministicLiteralNameAndNoCandidateDefaults(t *testing.T) {
	ctx := context.Background()
	name := "Synthetic Ω\nrequires_future_sponsorship: false"
	one, problem := PrepareEmpty(ctx, "  "+name+"  ")
	if problem != nil {
		t.Fatal(problem.Code)
	}
	two, problem := PrepareEmpty(ctx, name)
	if problem != nil || len(one.files) != 8 {
		t.Fatal("bundle", problem)
	}
	for path, raw := range one.files {
		if !bytes.Equal(raw, two.files[path]) {
			t.Fatal("nondeterministic", path)
		}
	}
	if err := vaultread.ValidateSetupFiles(ctx, one.files); err != nil {
		t.Fatal(err.Code)
	}
	profile, err := cataloginput.ParseYAML(one.files["career_vault/profile.yaml"])
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range profile.Entries {
		if entry.Key.Text == "name" && entry.Value.Text != name {
			t.Fatal("name changed or injected")
		}
		if entry.Key.Text == "requires_future_sponsorship" {
			t.Fatal("YAML injection")
		}
	}
	for _, path := range []string{"experience_blocks", "project_blocks", "skill_blocks", "proof_blocks"} {
		if string(one.files["career_vault/"+path+".yaml"]) != "[]\n" {
			t.Fatal("invented record", path)
		}
	}
	visa, err := cataloginput.ParseYAML(one.files["career_vault/visa_profile.yaml"])
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range visa.Entries {
		if entry.Value.Kind != "null" && (entry.Value.Kind != "string" || entry.Value.Text != "") {
			t.Fatal("invented eligibility")
		}
	}
	for _, name := range []string{"", " \n\t ", "\xff", strings.Repeat("Ω", 501), strings.Repeat(" ", 4097)} {
		if bundle, err := PrepareEmpty(ctx, name); err == nil || err.Code != "VAULT_SETUP_NAME_INVALID" || bundle != nil {
			t.Fatal("invalid name accepted")
		}
	}
	if _, err := PrepareEmpty(nil, "Synthetic"); err == nil || err.Code != "VAULT_SETUP_CANCELED" {
		t.Fatal("nil context")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := PrepareEmpty(ctx, "Synthetic"); err == nil || err.Code != "VAULT_SETUP_CANCELED" {
		t.Fatal("canceled context")
	}
}
