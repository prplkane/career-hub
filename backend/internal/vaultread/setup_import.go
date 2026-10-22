package vaultread

import (
	"context"

	"careerhub.local/backend/internal/cataloginput"
)

// SetupPaths returns a fresh closed list; callers cannot change the schema.
func SetupPaths() []string {
	paths := make([]string, len(schemas))
	for index, spec := range schemas {
		paths[index] = spec.path
	}
	return paths
}

// ValidateSetupFiles validates a complete user-supplied Vault without reading
// disk or inserting defaults. Import is not a way to weaken Profile validation.
func ValidateSetupFiles(ctx context.Context, files map[string][]byte) *Error {
	if ctx == nil || ctx.Err() != nil {
		return canceled()
	}
	if len(files) != len(schemas) {
		return &Error{Stage: "input", Code: "VAULT_SETUP_SCOPE_INVALID"}
	}
	state := &vaultState{}
	for index, spec := range schemas {
		if ctx.Err() != nil {
			return canceled()
		}
		raw, present := files[spec.path]
		if !present {
			return &Error{Stage: "input", Code: "VAULT_SETUP_SCOPE_INVALID"}
		}
		if len(raw) > cataloginput.YAMLMaxInputBytes {
			return &Error{Stage: "input", Source: spec.source, Code: "VAULT_INPUT_LIMIT"}
		}
		value, problem := parseVaultDocument(spec, raw)
		if problem != nil {
			return problem
		}
		state.documents[index] = value
	}
	return validateProfileState(ctx, state)
}
