package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/vault/vaultbundle"
	"careerhub.local/backend/internal/vault/vaultsetup"
)

// exportVault returns the verified Career Vault export file for this Home.
// The owner holds the operation gate and validates the bundle exactly as an
// export-file import would; this adapter only adds size, digest and name.
func exportVault(ctx context.Context, w *readonly.Workspace, root, resources string, now func() time.Time) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	out := &httpapi.ApplicationOutcome{}
	failure := func(code string) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		return out, &httpapi.ApplicationError{Code: code, Category: "vault_export"}
	}
	home, err := w.StartupHome()
	if err != nil {
		return failure("VAULT_EXPORT_UNAVAILABLE")
	}
	content, problem := vaultsetup.New(root, home).WithResources(resources).Export(ctx)
	if problem != nil {
		return failure(problem.Code)
	}
	sum := sha256.Sum256(content)
	out.Result, err = json.Marshal(map[string]any{
		"file_name": vaultbundle.FileName(now()), "size_bytes": len(content), "sha256": hex.EncodeToString(sum[:]),
	})
	if err != nil {
		return failure("LOCAL_RESPONSE_INVALID")
	}
	out.Content = content
	return out, nil
}
