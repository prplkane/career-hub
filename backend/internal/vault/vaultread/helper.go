package vaultread

import (
	"context"

	"careerhub.local/backend/internal/skills/cataloginput"
)

// HelperSources returns only the requested canonical source records, not a
// browser payload. The caller owns opt-in, current read-set checks and filtering.
// Disabled sources do not cause a Vault read. Enabled Profile/Eligibility retain
// the complete source editor validation but do not JSON-encode unrelated fields.
func HelperSources(ctx context.Context, source reader, profile, eligibility bool) (map[string]cataloginput.Value, map[string]string, *Error) {
	records, revisions := map[string]cataloginput.Value{}, map[string]string{}
	if ctx == nil || ctx.Err() != nil {
		return nil, nil, canceled()
	}
	if !profile && !eligibility {
		return records, revisions, nil
	}
	if source == nil {
		return nil, nil, &Error{Stage: "read", Code: "VAULT_READ_FAILED"}
	}
	state, err := loadVault(ctx, source)
	if err != nil {
		return nil, nil, err
	}
	if profile {
		err = validateProfileState(ctx, state)
	} else {
		err = validateVault(ctx, state)
	}
	if err != nil {
		return nil, nil, err
	}
	if profile {
		records["profile"], revisions["profile"] = cloneEditorValue(state.documents[profileIndex]), state.revisions[profileIndex]
	}
	if eligibility {
		records["eligibility"], revisions["eligibility"] = cloneEditorValue(state.documents[eligibilityIndex]), state.revisions[eligibilityIndex]
	}
	return records, revisions, nil
}
