package vaultread

import (
	"context"
	"encoding/json"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/runtimepaths"
)

// ReadSetup reports only the fixed Vault's presence/validation state. It is not
// a claim that the whole Home is empty, a write preflight or permission to seed
// anything. The host must retain admission and writer exclusion around it.
// Missing files never make malformed or unreadable present files disappear.
func ReadSetup(ctx context.Context, root string) (payload json.RawMessage, failure *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, canceled()
	}
	data, pathErr := runtimepaths.Open(root)
	if pathErr != nil {
		return nil, &Error{Stage: "path", Source: "data_root", Code: "VAULT_PATH_INVALID"}
	}
	defer func() {
		if err := data.Close(); err != nil && failure == nil {
			payload, failure = nil, &Error{Stage: "read", Source: "data_root", Code: "VAULT_CLOSE_FAILED"}
		}
	}()
	return setupPayload(ctx, data)
}

func setupPayload(ctx context.Context, source reader) (json.RawMessage, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, canceled()
	}
	state := &vaultState{}
	missing := []string{}
	for index, spec := range schemas {
		if ctx.Err() != nil {
			return nil, canceled()
		}
		raw, present, pathErr := source.ReadFile(spec.path, cataloginput.YAMLMaxInputBytes)
		if pathErr != nil {
			code := "VAULT_READ_FAILED"
			if pathErr.Code == "FILE_SIZE_LIMIT" {
				code = "VAULT_INPUT_LIMIT"
			}
			return nil, &Error{Stage: "read", Source: spec.source, Code: code}
		}
		if !present {
			missing = append(missing, spec.source)
			continue
		}
		value, err := parseVaultDocument(spec, raw)
		if err != nil {
			return nil, err
		}
		state.documents[index] = value
	}
	if ctx.Err() != nil {
		return nil, canceled()
	}
	status := "incomplete"
	if len(missing) == vaultFileCount {
		status = "not_configured"
	} else if len(missing) == 0 {
		if err := validateProfileState(ctx, state); err != nil {
			return nil, err
		}
		status = "configured"
	}
	// Only fixed enums and schema labels are emitted: no paths, profile values,
	// file bytes, revision tokens or inferred candidate facts.
	payload, _ := json.Marshal(struct {
		ContractVersion int      `json:"contract_version"`
		State           string   `json:"state"`
		Missing         []string `json:"missing"`
	}{1, status, missing})
	return payload, nil
}
