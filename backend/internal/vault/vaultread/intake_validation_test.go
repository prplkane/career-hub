package vaultread

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
)

func intakeDocuments(t *testing.T, f *fixtureReader) map[string]cataloginput.Value {
	t.Helper()
	state := fixtureState(t, f)
	documents := map[string]cataloginput.Value{}
	for _, index := range []int{profileIndex, educationIndex, experienceIndex, projectIndex, skillIndex} {
		documents[schemas[index].path] = state.documents[index]
	}
	return documents
}

func TestIntakeValidationRetainsSchemaCalendarAndReferences(t *testing.T) {
	cases := map[string]bool{"valid": true, "unknown_profile": true, "missing_required": true,
		"education_multiple_errors": true, "cert_duplicates": true, "date_errors": true,
		"archived_still_referenced": true, "archived_still_validated": true,
		"duplicate_experience": true, "reference_unknown": true, "reference_unhashable": true}
	for _, tc := range validationCases() {
		if !cases[tc.name] {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			f := validFixture()
			tc.edit(f)
			documents := intakeDocuments(t, f)
			before, _ := json.Marshal(documents)
			err := ValidateIntakeVault(context.Background(), documents)
			code := ""
			if err != nil {
				code = err.Code
			}
			if code != tc.code {
				t.Fatalf("code=%s want=%s", code, tc.code)
			}
			after, _ := json.Marshal(documents)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("validation edited captured documents")
			}
		})
	}
}

func TestIntakeValidationRequiresClosedScopeAndHonorsCancellation(t *testing.T) {
	documents := intakeDocuments(t, validFixture())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		if err := ValidateIntakeVault(ctx, documents); err == nil || err.Code != "VAULT_READ_CANCELED" {
			t.Fatal("cancellation ignored")
		}
	}
	documents["career_vault/proof_blocks.yaml"] = cataloginput.Value{Kind: "sequence"}
	if err := ValidateIntakeVault(context.Background(), documents); err == nil || err.Code != "VAULT_INTAKE_SCOPE_INVALID" {
		t.Fatal("extra source accepted")
	}
	delete(documents, "career_vault/profile.yaml")
	if err := ValidateIntakeVault(context.Background(), documents); err == nil {
		t.Fatal("wrong five sources accepted")
	}
}
