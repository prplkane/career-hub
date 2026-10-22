package readonly

import (
	"context"
	"encoding/json"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/vaultread"
)

// Profile returns fresh, unnormalized profile JSON only while the originally
// admitted Data Home remains initialized and unchanged. The eight-file reader
// validates all evidence; this method adds no defaults, writes, cache or lease.
// Cancellation is observed between synchronous operations, not inside OS reads.
func (w *Workspace) Profile(ctx context.Context) (json.RawMessage, error) {
	return w.Vault(ctx, vaultread.ProfileView)
}

// Vault applies the same fresh resource/home admission to every fixed read
// projection. Invalid selectors do not access either root. There are no writes
// or global writer leases here, including for the aggregate Snapshot view.
func (w *Workspace) Vault(ctx context.Context, view vaultread.View) (json.RawMessage, error) {
	if !view.Valid() {
		return nil, &vaultread.Error{Stage: "view", Code: "VAULT_VIEW_INVALID"}
	}
	if failure := profileCanceled(ctx); failure != nil {
		return nil, failure
	}
	if w == nil || w.dataHomeID == "" {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	if _, err := validateRoots(w.start); err != nil {
		return nil, err
	}
	return admittedVault(ctx, w.dataHomeID,
		func() (*datahome.Info, *datahome.Error) {
			return datahome.Inspect(w.start.DataRoot, &w.start.Channel)
		},
		func() (json.RawMessage, *vaultread.Error) {
			return vaultread.Read(ctx, w.start.DataRoot, view)
		})
}

func profileCanceled(ctx context.Context) *vaultread.Error {
	if ctx == nil || ctx.Err() != nil {
		return &vaultread.Error{Stage: "context", Code: "VAULT_READ_CANCELED"}
	}
	return nil
}

// The private seam tests admission drift without races or replacing product
// dependencies. Post-read admission applies to diagnostics as well as payloads;
// an invalidated home cannot return a stale successful or validation response.
func admittedVault(ctx context.Context, expectedID string,
	inspect func() (*datahome.Info, *datahome.Error),
	load func() (json.RawMessage, *vaultread.Error),
) (json.RawMessage, error) {
	if failure := profileCanceled(ctx); failure != nil {
		return nil, failure
	}
	if expectedID == "" {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	before, admissionErr := inspect()
	if failure := profileCanceled(ctx); failure != nil {
		return nil, failure
	}
	if admissionErr != nil {
		return nil, admissionError(admissionErr)
	}
	if before == nil || !before.Initialized {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	if before.DataHomeID != expectedID {
		return nil, fail("DATA_HOME_IDENTITY_CHANGED")
	}
	identity, channel, format := before.DataHomeID, before.Channel, before.FormatVersion
	payload, loadErr := load()
	if failure := profileCanceled(ctx); failure != nil {
		return nil, failure
	}
	after, admissionErr := inspect()
	if failure := profileCanceled(ctx); failure != nil {
		return nil, failure
	}
	if admissionErr != nil {
		return nil, admissionError(admissionErr)
	}
	if after == nil || !after.Initialized {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	if identity != after.DataHomeID || channel != after.Channel || format != after.FormatVersion {
		return nil, fail("DATA_HOME_IDENTITY_CHANGED")
	}
	if loadErr != nil {
		return nil, loadErr
	}
	if len(payload) == 0 {
		return nil, fail("SIDECAR_SCHEMA_INITIALIZATION_FAILED")
	}
	return payload, nil
}
