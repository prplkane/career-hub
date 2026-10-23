package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/vault/vaultread"
)

type VaultSetupRequest struct {
	Files          map[string][]byte
	NewProfileName *string
	Apply          bool
	ConfirmedPlan  string
}

func (h *handler) serveVaultSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery {
		sendError(w, 404, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	fields, code, _ := materialJSONCheckedLimit(r, func(raw []byte) bool { return applicationScalarJSON(raw) && uniqueJobDetailsJSON(raw) }, 24<<20)
	if code != "" {
		applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
		return
	}
	invalid := func() { applicationFailure(w, &ApplicationOutcome{}, "VAULT_SETUP_INVALID") }
	request := VaultSetupRequest{Apply: r.URL.Path == "/api/vault/setup/import", Files: map[string][]byte{}}
	keys := []string{"contract_version", "files"}
	newProfile := string(fields["contract_version"]) == "2"
	if newProfile {
		keys = []string{"contract_version", "profile_name"}
	}
	if request.Apply {
		keys = append(keys, "confirmed_plan")
	}
	if len(fields) != len(keys) || !materialKeys(fields, keys...) || (!newProfile && string(fields["contract_version"]) != "1") {
		invalid()
		return
	}
	if request.Apply && (json.Unmarshal(fields["confirmed_plan"], &request.ConfirmedPlan) != nil || len(request.ConfirmedPlan) != 71 || !strings.HasPrefix(request.ConfirmedPlan, "sha256:")) {
		invalid()
		return
	}
	if newProfile {
		var name string
		if string(fields["profile_name"]) == "null" || json.Unmarshal(fields["profile_name"], &name) != nil || len(name) > 4096 {
			invalid()
			return
		}
		request.NewProfileName = &name
	} else {
		var encoded map[string]string
		paths := vaultread.SetupPaths()
		if json.Unmarshal(fields["files"], &encoded) != nil || len(encoded) != len(paths) {
			invalid()
			return
		}
		for _, path := range paths {
			// The UI submits fixed base filenames, never a path into its filesystem.
			name := strings.TrimPrefix(path, "career_vault/")
			value, present := encoded[name]
			if !present || value == "" || len(value) > ((cataloginput.YAMLMaxInputBytes+2)/3)*4 || strings.ContainsAny(value, "\r\n") {
				invalid()
				return
			}
			raw, err := base64.StdEncoding.DecodeString(value)
			if err != nil || len(raw) > cataloginput.YAMLMaxInputBytes {
				invalid()
				return
			}
			request.Files[path] = raw
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) { return h.application.VaultSetup(ctx, request) })
}
