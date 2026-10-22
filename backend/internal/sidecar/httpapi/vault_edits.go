package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// VaultEditRequest is composed only from the fixed routes below. The client
// cannot select a filename, recovery operation, callback or Data Home.
type VaultEditRequest struct {
	Kind, Operation, ID, Revision string
	Record                        map[string]any
}

var vaultEditCollections = []struct{ path, kind string }{
	{"/api/vault/experiences", "experience"},
	{"/api/vault/projects", "project"},
	{"/api/vault/education/schools", "school"},
	{"/api/vault/education/certifications", "certification"},
	{"/api/vault/skills", "legacy_skill"},
	{"/api/skills-registry/groups", "registry_group"},
	{"/api/skills-registry/skills", "registry_skill"},
}

func vaultEditRoute(r *http.Request) (VaultEditRequest, bool) {
	if r.URL == nil || len(r.RequestURI) > MaxRequestTargetBytes || len(r.URL.RequestURI()) > MaxRequestTargetBytes {
		return VaultEditRequest{}, false
	}
	path := r.URL.EscapedPath()
	if path == "/api/skills-registry/skills/batch" && r.Method == "POST" {
		return VaultEditRequest{Kind: "registry_skill", Operation: "batch"}, true
	}
	for _, collection := range vaultEditCollections {
		if path == collection.path && r.Method == "POST" {
			return VaultEditRequest{Kind: collection.kind, Operation: "create"}, true
		}
		if !strings.HasPrefix(path, collection.path+"/") {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(path, collection.path+"/"), "/")
		op := "update"
		if len(parts) == 2 && r.Method == "POST" && (parts[1] == "archive" || parts[1] == "restore") {
			op = parts[1]
		} else if len(parts) != 1 || r.Method != "PUT" {
			return VaultEditRequest{}, false
		}
		id, err := url.PathUnescape(parts[0])
		// Decode only the ID, exactly once. Percent signs remain literal record
		// data; the native owner never interprets an ID as an encoded file path.
		if err != nil || id == "" || len(id) > 2048 || !utf8.ValidString(id) || id == "." || id == ".." || strings.ContainsAny(id, "/\\") || strings.ContainsFunc(id, func(c rune) bool { return c < 32 || c == 127 }) {
			return VaultEditRequest{}, false
		}
		return VaultEditRequest{Kind: collection.kind, Operation: op, ID: id}, true
	}
	return VaultEditRequest{}, false
}

func (h *handler) serveVaultEdit(w http.ResponseWriter, r *http.Request, edit VaultEditRequest) {
	fields, code, _ := materialJSONChecked(r, applicationScalarJSON)
	if code != "" {
		applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
		return
	}
	invalid := "VAULT_RECORD_VALIDATION_FAILED"
	if strings.HasPrefix(edit.Kind, "registry_") {
		invalid = "SKILL_REGISTRY_INVALID"
	}
	keys := []string{"expected_revision"}
	if edit.Operation == "create" || edit.Operation == "update" || edit.Operation == "batch" {
		keys = append(keys, "record")
		if json.Unmarshal(fields["record"], &edit.Record) != nil || edit.Record == nil {
			applicationFailure(w, &ApplicationOutcome{}, invalid)
			return
		}
	}
	if !materialKeys(fields, keys...) || json.Unmarshal(fields["expected_revision"], &edit.Revision) != nil || string(fields["expected_revision"]) == "null" {
		applicationFailure(w, &ApplicationOutcome{}, invalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	status := http.StatusOK
	if edit.Operation == "create" || edit.Operation == "batch" {
		status = http.StatusCreated
	}
	h.applicationCall(w, ctx, status, func() (*ApplicationOutcome, *ApplicationError) { return h.application.VaultEdit(ctx, edit) })
}
