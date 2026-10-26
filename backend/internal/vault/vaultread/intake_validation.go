package vaultread

import (
	"context"

	"careerhub.local/backend/internal/skills/cataloginput"
)

// ValidateIntakeVault uses the existing manual/calendar/reference validators
// for exactly the five canonical files changed by Resume approval. Unlike a
// whole-Vault editor this operation does not read eligibility, proof or style.
// Values must come from the bounded YAML decoder, not arbitrary caller trees.
func ValidateIntakeVault(ctx context.Context, documents map[string]cataloginput.Value) *Error {
	indexes := []int{profileIndex, educationIndex, experienceIndex, projectIndex, skillIndex}
	state := &vaultState{}
	if len(documents) != len(indexes) {
		return &Error{Stage: "configuration", Code: "VAULT_INTAKE_SCOPE_INVALID"}
	}
	for _, index := range indexes {
		value, present := documents[schemas[index].path]
		if !present || value.Kind != schemas[index].kind {
			return &Error{Stage: "shape", Code: "VAULT_ROOT_TYPE"}
		}
		state.documents[index] = value
	}
	return validateVaultSubset(ctx, state, indexes)
}
