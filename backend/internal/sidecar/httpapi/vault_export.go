package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"careerhub.local/backend/internal/vault/vaultbundle"
)

const VaultExportPath = "/api/vault/export"

var vaultExportName = regexp.MustCompile(`^career-vault-export-[0-9]{4}-[0-9]{2}-[0-9]{2}\.chvault$`)

// serveVaultExport returns one Career Vault export file. The body is exactly
// {"contract_version":1}; the owner returns verified bytes plus their size,
// digest and fixed download name, which are checked again before sending.
func (h *handler) serveVaultExport(w http.ResponseWriter, r *http.Request) {
	if h.application.VaultExport == nil || r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery {
		sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	fields, code, _ := materialJSONCheckedLimit(r, func(raw []byte) bool { return applicationScalarJSON(raw) && uniqueJobDetailsJSON(raw) }, 1024)
	if code != "" {
		applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
		return
	}
	if len(fields) != 1 || string(fields["contract_version"]) != "1" {
		applicationFailure(w, &ApplicationOutcome{}, "LOCAL_REQUEST_INVALID")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if !applicationResponseDeadline(w, ctx) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if ctx.Err() != nil {
		applicationFailure(w, &ApplicationOutcome{}, "LOCAL_OPERATION_CANCELED")
		return
	}
	var out *ApplicationOutcome
	defer func() {
		if recover() != nil {
			applicationFailure(w, nil, "LOCAL_OPERATION_FAILED")
		}
	}()
	out, problem := h.application.VaultExport(ctx)
	if problem != nil {
		applicationFailure(w, out, problem.Code, problem.Category)
		return
	}
	if ctx.Err() != nil {
		applicationFailure(w, out, "LOCAL_OPERATION_CANCELED")
		return
	}
	var meta struct {
		FileName  string `json:"file_name"`
		SHA256    string `json:"sha256"`
		SizeBytes int    `json:"size_bytes"`
	}
	if out == nil || !materialJSONObject(out.Result) || len(out.Result) > MaxResponseBytes || json.Unmarshal(out.Result, &meta) != nil ||
		!vaultExportName.MatchString(meta.FileName) || meta.SizeBytes < 1 || meta.SizeBytes > vaultbundle.MaxBundleBytes || len(out.Content) != meta.SizeBytes {
		applicationFailure(w, out, "LOCAL_RESPONSE_INVALID")
		return
	}
	sum := sha256.Sum256(out.Content)
	if meta.SHA256 != hex.EncodeToString(sum[:]) {
		applicationFailure(w, out, "LOCAL_RESPONSE_INVALID")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+meta.FileName+`"`)
	sendTypedBytes(w, http.StatusOK, out.Content, "application/octet-stream")
}
