package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// The native mailbox route is a maintainer-preview seam. The installed email
// UI remains disabled until credential ownership and diagnostics are qualified.
func (h *handler) serveEmailSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery {
		sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	const limit = 64 * 1024
	if r.ContentLength > limit {
		applicationFailure(w, &ApplicationOutcome{}, "LOCAL_REQUEST_TOO_LARGE")
		return
	}
	fields, code, _ := materialJSONChecked(r, func(raw []byte) bool {
		return len(raw) <= limit && applicationScalarJSON(raw)
	})
	if code != "" {
		applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
		return
	}
	if !materialKeys(fields, "contract_version", "trigger") || len(fields) != 2 ||
		string(fields["contract_version"]) != "2" || h.application.EmailSync == nil {
		applicationFailure(w, &ApplicationOutcome{}, "EMAIL_CONNECTION_INVALID", "email_sync")
		return
	}
	trigger := ""
	switch string(fields["trigger"]) {
	case `"manual"`:
		trigger = "manual"
	case `"scheduled"`:
		trigger = "scheduled"
	default:
		applicationFailure(w, &ApplicationOutcome{}, "EMAIL_CONNECTION_INVALID", "email_sync")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()
	requestedRunID := strings.TrimSpace(r.Header.Get("X-Career-Hub-Diagnostic-Run"))
	h.applicationCallUnlocked(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.EmailSync(ctx, trigger, requestedRunID)
	})
}
