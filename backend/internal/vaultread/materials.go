package vaultread

import (
	"context"

	"careerhub.local/backend/internal/cataloginput"
)

// MaterialSources returns only the profile/education inputs needed by the
// typed material editor, after full Vault and profile-contact validation. It
// retains YAML scalar types (notably GPA numbers and legacy calendar dates).
// The owner must retain/recheck the complete observed eight-file read set.
func MaterialSources(ctx context.Context, source reader) (profile, education cataloginput.Value, profileRevision string, problem *Error) {
	if source == nil {
		return profile, education, "", &Error{Stage: "read", Code: "VAULT_READ_FAILED"}
	}
	state, e := loadVault(ctx, source)
	if e != nil {
		return profile, education, "", e
	}
	if e = validateProfileState(ctx, state); e != nil {
		return profile, education, "", e
	}
	return state.documents[profileIndex], state.documents[educationIndex], state.revisions[profileIndex], nil
}
