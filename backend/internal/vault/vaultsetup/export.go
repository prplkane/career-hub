package vaultsetup

import (
	"bytes"
	"context"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/vault/vaultbundle"
)

// Export reads this Home's Career Vault, both custom catalogs and the saved
// short answers (when present) in one guarded pass, confirms nothing changed
// during the pass, and returns a bundle only after it passes the same checks
// an export-file import runs. It never writes. Failures carry codes only.
func (s *Service) Export(ctx context.Context) (out []byte, failure *Error) {
	if s == nil || ctx == nil || !s.home.Initialized || s.home.DataHomeID == "" || s.resources == "" || s.read == nil || s.close == nil {
		return nil, &Error{"VAULT_EXPORT_UNAVAILABLE"}
	}
	if ctx.Err() != nil {
		return nil, &Error{"VAULT_EXPORT_CANCELED"}
	}
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		return nil, &Error{"VAULT_EXPORT_CANCELED"}
	}
	defer release()
	root, pathErr := runtimepaths.Open(s.root)
	if pathErr != nil {
		return nil, &Error{"VAULT_EXPORT_UNAVAILABLE"}
	}
	defer func() {
		if s.close(root) != nil && failure == nil {
			out, failure = nil, &Error{"VAULT_EXPORT_UNAVAILABLE"}
		}
	}()
	if s.admit(root) != nil {
		return nil, &Error{"VAULT_EXPORT_UNAVAILABLE"}
	}
	files := map[string][]byte{}
	for _, path := range vaultbundle.Paths() {
		raw, present, problem := s.read(root, path, cataloginput.YAMLMaxInputBytes)
		if problem != nil {
			if problem.Code == "FILE_SIZE_LIMIT" {
				return nil, &Error{"VAULT_EXPORT_TOO_LARGE"}
			}
			return nil, &Error{"VAULT_EXPORT_UNAVAILABLE"}
		}
		if !present {
			if vaultbundle.Optional(path) {
				continue
			}
			return nil, &Error{"VAULT_EXPORT_NOT_CONFIGURED"}
		}
		files[path] = raw
	}
	if ctx.Err() != nil {
		return nil, &Error{"VAULT_EXPORT_CANCELED"}
	}
	// Second pass: a cooperating writer is excluded by the gate, but an outside
	// edit during the first pass must not produce a mixed export.
	for _, path := range vaultbundle.Paths() {
		raw, present, problem := s.read(root, path, cataloginput.YAMLMaxInputBytes)
		want, had := files[path]
		if problem != nil || present != had || !bytes.Equal(raw, want) {
			return nil, &Error{"VAULT_EXPORT_CHANGED"}
		}
	}
	if s.admit(root) != nil {
		return nil, &Error{"VAULT_EXPORT_UNAVAILABLE"}
	}
	encoded, encodeErr := vaultbundle.Encode(files)
	if encodeErr != nil {
		if encodeErr.Code == vaultbundle.CodeLimit {
			return nil, &Error{"VAULT_EXPORT_TOO_LARGE"}
		}
		return nil, &Error{"VAULT_EXPORT_INVALID"}
	}
	// Round-trip through the import path so export refuses whatever import would.
	bundle, problem := PrepareBundle(ctx, encoded)
	if problem == nil {
		problem = checkBundleSkills(ctx, s.resources, bundle.files)
	}
	if problem != nil {
		if ctx.Err() != nil {
			return nil, &Error{"VAULT_EXPORT_CANCELED"}
		}
		switch problem.Code {
		case "VAULT_SETUP_CANCELED":
			return nil, &Error{"VAULT_EXPORT_CANCELED"}
		case "VAULT_SETUP_DESTINATION_UNAVAILABLE":
			return nil, &Error{"VAULT_EXPORT_UNAVAILABLE"}
		case vaultbundle.CodeLimit:
			return nil, &Error{"VAULT_EXPORT_TOO_LARGE"}
		case "VAULT_SETUP_SKILL_UNKNOWN":
			// Its own code: the owner can fix this one by re-saving records.
			return nil, &Error{"VAULT_EXPORT_SKILL_UNKNOWN"}
		}
		return nil, &Error{"VAULT_EXPORT_INVALID"}
	}
	if ctx.Err() != nil {
		return nil, &Error{"VAULT_EXPORT_CANCELED"}
	}
	return encoded, nil
}
