package readonly

import (
	"context"
	"encoding/json"
	"runtime"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

// StaticAsset receives a fixed compile-time filename from the HTTP allowlist,
// never a user-selected path. Managed reads still reject unsafe components.
func (w *Workspace) StaticAsset(ctx context.Context, name string) (raw []byte, failure error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, fail("STATIC_RESOURCE_UNAVAILABLE")
	}
	if err := w.CheckAdmission(); err != nil {
		return nil, err
	}
	root, e := runtimepaths.Open(w.start.ResourceRoot)
	if e != nil {
		return nil, fail("STATIC_RESOURCE_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			raw, failure = nil, fail("STATIC_RESOURCE_UNAVAILABLE")
		}
	}()
	raw, present, e := root.ReadSingleLinkFile("src/intake_ui_static/"+name, 4<<20)
	if e != nil || !present {
		return nil, fail("STATIC_RESOURCE_UNAVAILABLE")
	}
	if err := w.CheckAdmission(); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, fail("STATIC_RESOURCE_UNAVAILABLE")
	}
	return raw, nil
}

func platformSecurity(platform string) map[string]any {
	if platform == "windows" {
		return map[string]any{"platform": "windows", "credential_store": map[string]any{"available": true, "storage_copy": "Stored in Windows Credential Manager, never in Career Hub data or backups.", "help_copy": "The secret stays in Windows Credential Manager. Career Hub does not mark messages read, move, delete, reply, or send."}, "document_encryption": map[string]any{"available": true, "storage_copy": "Files and metadata are protected by your Windows account. Recovery backups remain device/account-bound."}}
	}
	if platform == "darwin" {
		return map[string]any{"platform": "macos", "credential_store": map[string]any{"available": false, "storage_copy": "macOS Keychain storage is not available yet; use manual .eml import.", "help_copy": "Gmail sync remains unavailable until macOS Keychain support passes security review. Manual .eml import remains available."}, "document_encryption": map[string]any{"available": false, "storage_copy": "Encrypted document storage is not available on macOS yet. Existing data is not rewritten."}}
	}
	return map[string]any{"platform": "development_unsupported", "credential_store": map[string]any{"available": false, "storage_copy": "Protected credential storage is unavailable on this development platform; use manual .eml import.", "help_copy": "Gmail sync is unavailable on this development platform. Manual .eml import remains available."}, "document_encryption": map[string]any{"available": false, "storage_copy": "Encrypted document storage is unavailable on this development platform. Existing data is not rewritten."}}
}

// RuntimeInfo describes this explicitly selected workspace. Platform storage
// policy is not a claim of a connected mailbox or complete product integration.
func (w *Workspace) RuntimeInfo(ctx context.Context) (json.RawMessage, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	if err := w.CheckAdmission(); err != nil {
		return nil, err
	}
	raw, e := json.Marshal(map[string]any{"data_profile": nil, "data_root": w.start.DataRoot, "channel": w.start.Channel, "app_version": w.AppVersion(), "data_home_format_version": 2, "platform_security": platformSecurity(runtime.GOOS), "storage_mode": "external_data_home"})
	if e != nil {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	if ctx.Err() != nil {
		return nil, fail("DATA_HOME_UNAVAILABLE")
	}
	return raw, nil
}
